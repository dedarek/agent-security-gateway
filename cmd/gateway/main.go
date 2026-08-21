// Command gateway is the Agent Security Gateway data-plane entrypoint.
//
// MVP scope: wire the three-axis Risk Decision Engine (permission axis only for
// now) to a passthrough proxy, and run a self-contained demo of the security
// loop described in docs/MVP.md. Data/Network and Behavior axes land in later
// phases (see docs/PLAN.md).
package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/config"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
)

// autoApprover is an MVP stand-in for human-in-the-loop approval. Here it always
// approves; a real Approver pushes to a Web/Slack/飞书 approval queue and blocks.
type autoApprover struct{ approve bool }

func (a autoApprover) Confirm(_ context.Context, c *api.ToolCall, _ api.Decision) (bool, error) {
	log.Printf("CONFIRM required for %s -> auto-%v (demo)", c.ToolID, a.approve)
	return a.approve, nil
}

// echoForwarder is an MVP stand-in for the real upstream MCP server.
type echoForwarder struct{}

func (echoForwarder) Forward(_ context.Context, c *api.ToolCall) (*api.ToolResult, error) {
	out, _ := json.Marshal(map[string]string{"status": "ok", "tool": c.ToolID})
	return &api.ToolResult{CallID: c.CallID, Output: out}, nil
}

func main() {
	cfg := config.Default()
	log.Printf("Agent Security Gateway starting (listen=%s upstream=%s)", cfg.Listen, cfg.Upstream)

	reg := engine.NewRegistry()
	reg.Register(engine.NewPermissionEngine())

	gw := &proxy.Gateway{
		Registry:  reg,
		Approver:  autoApprover{approve: true},
		Forwarder: echoForwarder{},
		Audit:     audit.StdoutSink{},
	}

	// --- Self-contained demo (docs/MVP.md Demo 剧本) ---
	demo(gw)
}

func demo(gw *proxy.Gateway) {
	ctx := context.Background()
	employee := api.Principal{UserID: "u1", AgentID: "a1", SessionID: "s1", Role: "employee"}

	calls := []api.ToolCall{
		{CallID: "c1", Principal: employee, ToolID: "filesystem.read", Resource: "file:/tmp/x", Action: "read"},
		{CallID: "c2", Principal: employee, ToolID: "database.delete_user", Resource: "database.users", Action: "delete"},
		{CallID: "c3", Principal: employee, ToolID: "database.export_all_users", Resource: "database.users", Action: "read"},
	}

	for i := range calls {
		calls[i].Timestamp = time.Now()
		_, d, err := gw.Handle(ctx, &calls[i])
		if err != nil {
			log.Printf("call %s error: %v", calls[i].CallID, err)
			continue
		}
		log.Printf("call %-28s => %s (%s)", calls[i].ToolID, d.Final, d.Rationale)
	}
}

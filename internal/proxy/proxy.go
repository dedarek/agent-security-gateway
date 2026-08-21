// Package proxy is the Gateway ingress. In the MVP it intercepts MCP tool calls,
// runs them through the Risk Decision Engine, and forwards ALLOW / denies BLOCK /
// suspends CONFIRM. This file is a passthrough skeleton illustrating the flow.
package proxy

import (
	"context"
	"log"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/engine"
)

// Approver decides a CONFIRM verdict via human-in-the-loop (CLI / web / Slack /
// 飞书). Returns true to approve.
type Approver interface {
	Confirm(ctx context.Context, c *api.ToolCall, d api.Decision) (bool, error)
}

// Forwarder sends an allowed call to the real upstream MCP server / tool.
type Forwarder interface {
	Forward(ctx context.Context, c *api.ToolCall) (*api.ToolResult, error)
}

// Gateway wires the ingress to the decision engine, approver, forwarder and audit sink.
type Gateway struct {
	Registry  *engine.Registry
	Approver  Approver
	Forwarder Forwarder
	Audit     audit.Sink
}

// Handle runs one tool call through the full Pre -> (approve) -> Runtime -> Post pipeline.
func (g *Gateway) Handle(ctx context.Context, c *api.ToolCall) (*api.ToolResult, api.Decision, error) {
	// ---- PRE ----
	pre := g.Registry.EvaluatePre(ctx, c)
	switch pre.Final {
	case api.VerdictBlock:
		g.emit(c, nil, pre)
		return nil, pre, nil
	case api.VerdictConfirm:
		ok, err := g.Approver.Confirm(ctx, c, pre)
		if err != nil {
			return nil, pre, err
		}
		if !ok {
			pre.Final = api.VerdictBlock
			pre.Rationale = "human denied confirmation"
			g.emit(c, nil, pre)
			return nil, pre, nil
		}
	case api.VerdictRedact:
		// MVP: redaction of arguments handled by the engine's redactions; omitted here.
	}

	// ---- RUNTIME: forward to the real tool ----
	res, err := g.Forwarder.Forward(ctx, c)
	if err != nil {
		return nil, pre, err
	}

	// ---- POST ----
	post := g.Registry.EvaluatePost(ctx, c, res)
	if post.Final == api.VerdictBlock {
		g.emit(c, res, post)
		return nil, post, nil // suppress result on post-block
	}
	g.emit(c, res, post)
	return res, post, nil
}

func (g *Gateway) emit(c *api.ToolCall, r *api.ToolResult, d api.Decision) {
	ev := api.Event{
		SessionID: c.Principal.SessionID,
		Call:      *c,
		Result:    r,
		Decision:  d,
		Timestamp: time.Now(),
	}
	if err := g.Audit.Write(ev); err != nil {
		log.Printf("audit write failed: %v", err)
	}
}

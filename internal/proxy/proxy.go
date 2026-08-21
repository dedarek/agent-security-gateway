// Package proxy is the Gateway ingress. It intercepts tool calls, runs them
// through the three-axis Risk Decision Engine, forwards ALLOW / denies BLOCK /
// suspends CONFIRM / scrubs REDACT, records the trajectory, and emits a signed
// action-receipt per decision.
package proxy

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/audit"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/receipt"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

// Approver decides a CONFIRM verdict via human-in-the-loop (CLI / web / Slack / 飞书).
type Approver interface {
	Confirm(ctx context.Context, c *api.ToolCall, d api.Decision) (bool, error)
}

// Forwarder sends an allowed call to the real upstream MCP server / tool.
type Forwarder interface {
	Forward(ctx context.Context, c *api.ToolCall) (*api.ToolResult, error)
}

// ResultObserver is notified of every tool result, so axes like the taint engine
// can record untrusted-source output for later data-flow checks.
type ResultObserver interface {
	ObserveResult(sessionID, toolID string, output []byte)
}

// Gateway wires ingress -> decision engine -> approver -> forwarder -> audit + receipts.
type Gateway struct {
	Registry   *engine.Registry
	Approver   Approver
	Forwarder  Forwarder
	Audit      audit.Sink
	Sessions   *session.Store
	Receipts   *receipt.Emitter
	Observers  []ResultObserver
	PolicyHash string
}

// Handle runs one tool call through Pre -> (approve) -> Runtime -> Post and
// returns the most-severe decision across phases. Exactly one signed receipt is
// emitted per call, carrying the effective verdict.
func (g *Gateway) Handle(ctx context.Context, c *api.ToolCall) (*api.ToolResult, api.Decision, error) {
	// ---- PRE ----
	pre := g.Registry.EvaluatePre(ctx, c)
	if pre.Final == api.VerdictBlock {
		g.record(c, nil, pre)
		return nil, pre, nil
	}
	if pre.Final == api.VerdictConfirm {
		ok, err := g.Approver.Confirm(ctx, c, pre)
		if err != nil {
			return nil, pre, err
		}
		if !ok {
			pre.Final = api.VerdictBlock
			pre.Rationale = "human denied confirmation"
			g.record(c, nil, pre)
			return nil, pre, nil
		}
	}
	// REDACT before forwarding: scrub sensitive values out of the arguments so
	// they never reach the upstream tool (e.g. a local secret flowing into an
	// external HTTP tool). The decision keeps the original rationale; the call
	// proceeds with sanitized bytes.
	if pre.Final == api.VerdictRedact {
		c.Arguments = applyRedactions(c.Arguments, collectRedactions(pre.Signals))
	}

	// Record the (approved) call into the session trajectory for the behavior axis.
	if g.Sessions != nil {
		g.Sessions.AppendToolCall(c.Principal.SessionID, c.CallID, lastSeg(c.ToolID), string(c.Arguments))
	}

	// ---- RUNTIME: forward to the real tool ----
	res, err := g.Forwarder.Forward(ctx, c)
	if err != nil {
		return nil, pre, err
	}
	if g.Sessions != nil && res != nil {
		g.Sessions.AppendToolResult(c.Principal.SessionID, c.CallID, string(res.Output))
	}
	// Feed the result to observers (taint engine records untrusted-source output).
	if res != nil {
		for _, obs := range g.Observers {
			obs.ObserveResult(c.Principal.SessionID, c.ToolID, res.Output)
		}
	}

	// ---- POST ----
	post := g.Registry.EvaluatePost(ctx, c, res)

	// REDACT after execution: scrub sensitive values out of the result BEFORE
	// it is returned to the agent or written to the audit sink. (The session
	// trajectory and taint observers above intentionally saw the raw bytes:
	// detection engines need true content; the agent-facing boundary does not.)
	if post.Final == api.VerdictRedact && res != nil {
		res.Output = applyRedactions(res.Output, collectRedactions(post.Signals))
	}

	// Effective decision = most severe across pre and post.
	eff := moreSevere(pre, post)
	if eff.Final == api.VerdictBlock {
		g.record(c, res, eff)
		return nil, eff, nil
	}
	g.record(c, res, eff)
	return res, eff, nil
}

// collectRedactions gathers the concrete scrub operations from all REDACT
// signals of a decision.
func collectRedactions(signals []api.Signal) []api.Redaction {
	var out []api.Redaction
	for _, s := range signals {
		out = append(out, s.Redactions...)
	}
	return out
}

// applyRedactions rewrites bytes: every literal Match is replaced with its
// Replace value. This is what makes a REDACT verdict real — the caller gets
// sanitized data, not a promise. Longest matches are applied first so that
// overlapping hits (e.g. a full URL and a host inside it) scrub completely.
func applyRedactions(data []byte, redactions []api.Redaction) []byte {
	if len(redactions) == 0 || len(data) == 0 {
		return data
	}
	out := string(data)
	rs := make([]api.Redaction, len(redactions))
	copy(rs, redactions)
	sort.Slice(rs, func(i, j int) bool { return len(rs[i].Match) > len(rs[j].Match) })
	for _, r := range rs {
		if r.Match == "" {
			continue // Path-based field scrubbing is future work; never no-op the whole payload
		}
		out = strings.ReplaceAll(out, r.Match, r.Replace)
	}
	return []byte(out)
}

// moreSevere returns the decision with the higher verdict (Allow<Redact<Confirm<Block),
// merging signals so the receipt/audit keeps full evidence.
func moreSevere(a, b api.Decision) api.Decision {
	winner := a
	if b.Final > a.Final {
		winner = b
	}
	winner.Signals = append(append([]api.Signal{}, a.Signals...), b.Signals...)
	if a.Risk > winner.Risk {
		winner.Risk = a.Risk
	}
	if b.Risk > winner.Risk {
		winner.Risk = b.Risk
	}
	return winner
}

// record emits both the audit event and a signed action-receipt.
func (g *Gateway) record(c *api.ToolCall, r *api.ToolResult, d api.Decision) {
	ev := api.Event{
		SessionID: c.Principal.SessionID,
		Call:      *c,
		Result:    r,
		Decision:  d,
		Timestamp: time.Now(),
	}
	if g.Audit != nil {
		if err := g.Audit.Write(ev); err != nil {
			log.Printf("audit write failed: %v", err)
		}
	}
	if g.Receipts != nil {
		if _, err := g.Receipts.Emit(g.toActionRecord(c, d)); err != nil {
			log.Printf("receipt emit failed: %v", err)
		}
	}
}

// toActionRecord maps a decision into Pipelock's ActionRecord taxonomy.
func (g *Gateway) toActionRecord(c *api.ToolCall, d api.Decision) receipt.ActionRecord {
	return receipt.ActionRecord{
		ActionID:        c.CallID,
		ActionType:      actionType(c.Action),
		Timestamp:       time.Now().UTC(),
		Principal:       c.Principal.UserID,
		Actor:           c.Principal.AgentID,
		DelegationChain: []string{c.Principal.UserID, c.Principal.AgentID},
		Target:          c.ToolID,
		SideEffectClass: sideEffect(c.Action),
		Reversibility:   reversibility(c.Action),
		PolicyHash:      g.PolicyHash,
		Verdict:         d.Final.String(),
		SessionID:       c.Principal.SessionID,
		Transport:       "mcp",
		Method:          c.ToolID,
	}
}

func actionType(a string) string {
	switch a {
	case "read":
		return "read"
	case "write":
		return "write"
	case "delete":
		return "actuate"
	case "network":
		return "delegate"
	default:
		return "read"
	}
}

func sideEffect(a string) string {
	switch a {
	case "delete", "write", "network":
		return "external"
	default:
		return "local"
	}
}

func reversibility(a string) string {
	switch a {
	case "delete":
		return "irreversible"
	case "write", "network":
		return "partial"
	default:
		return "reversible"
	}
}

func lastSeg(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' {
			return id[i+1:]
		}
	}
	return id
}

var _ = json.Marshal // keep json import for future streaming redaction

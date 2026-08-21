// Package engine defines the pluggable Engine interface and the Risk Decision
// Engine that aggregates multi-axis signals into a single verdict.
//
// Engines are how ToolHive / Pipelock / Invariant capabilities plug in without
// the Gateway core depending on any of them. An Engine may be in-process (Go)
// or an external gRPC sidecar (e.g. a Python behavior analyzer).
package engine

import (
	"context"

	"github.com/dedarek/agent-security-gateway/api"
)

// Stream is passed to EvaluateRuntime for streaming/incremental inspection.
// Kept minimal here; real impl wraps the upstream tool response stream.
type Stream interface {
	Peek() ([]byte, bool)
}

// Engine is the single interface every detection capability implements. An
// engine belongs to one Axis but may participate in any subset of phases; return
// a nil *Signal (with nil error) for phases it does not care about.
//
// Design borrows Bifrost's capability-split plugin contract (core/schemas/plugin.go)
// — but INVERTS its "plugin error is a non-blocking warning" default. Here an
// engine declares FailMode(); on error the aggregator honors it, and security-
// sensitive engines default to FailClosed (error => BLOCK). See
// docs/BASE-PROJECTS-ANALYSIS.md §3.3.
type Engine interface {
	Name() string
	Axis() api.Axis
	// FailMode is how the aggregator treats an error from this engine.
	FailMode() api.FailMode

	EvaluatePre(ctx context.Context, c *api.ToolCall) (*api.Signal, error)
	EvaluateRuntime(ctx context.Context, c *api.ToolCall, s Stream) (*api.Signal, error)
	EvaluatePost(ctx context.Context, c *api.ToolCall, r *api.ToolResult) (*api.Signal, error)
}

// Registry holds the active engines, grouped so we can enable/disable per axis
// via config.
type Registry struct {
	engines []Engine
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(e Engine) { r.engines = append(r.engines, e) }

func (r *Registry) Engines() []Engine { return r.engines }

// EvaluatePre runs every engine's pre hook (in production: in parallel with a
// deadline) and aggregates the signals into a Decision.
func (r *Registry) EvaluatePre(ctx context.Context, c *api.ToolCall) api.Decision {
	var signals []api.Signal
	for _, e := range r.engines {
		sig, err := e.EvaluatePre(ctx, c)
		if s := normalize(e, sig, err); s != nil {
			signals = append(signals, *s)
		}
	}
	return Aggregate(c.CallID, api.PhasePre, signals)
}

// EvaluatePost runs every engine's post hook and aggregates.
func (r *Registry) EvaluatePost(ctx context.Context, c *api.ToolCall, res *api.ToolResult) api.Decision {
	var signals []api.Signal
	for _, e := range r.engines {
		sig, err := e.EvaluatePost(ctx, c, res)
		if s := normalize(e, sig, err); s != nil {
			signals = append(signals, *s)
		}
	}
	return Aggregate(c.CallID, api.PhasePost, signals)
}

// normalize turns an engine error into a signal that honors the engine's
// declared FailMode, so a broken engine never silently disappears from the
// decision (the mistake Bifrost makes by swallowing plugin errors as warnings).
func normalize(e Engine, sig *api.Signal, err error) *api.Signal {
	if err == nil {
		return sig
	}
	verdict := api.VerdictBlock // FailClosed default: error => BLOCK
	if e.FailMode() == api.FailOpen {
		verdict = api.VerdictAllow // low-sensitivity path: degrade + alert
	}
	return &api.Signal{
		Axis:     e.Axis(),
		Engine:   e.Name(),
		Score:    100,
		Verdict:  verdict,
		Reasons:  []string{"engine error: " + err.Error()},
		FailMode: e.FailMode(),
	}
}

// Aggregate implements the multi-axis merge described in docs/ARCHITECTURE.md §5:
//
//	any BLOCK        -> BLOCK        (one-vote veto: safety first)
//	else any CONFIRM -> CONFIRM      (human in the loop)
//	else any REDACT  -> REDACT       (preserve usability)
//	else                ALLOW
//
// Risk = max(score) for alerting/sorting.
func Aggregate(callID string, phase api.Phase, signals []api.Signal) api.Decision {
	final := api.VerdictAllow
	risk := 0
	for _, s := range signals {
		if s.Score > risk {
			risk = s.Score
		}
		if s.Verdict > final { // Verdict is ordered Allow<Redact<Confirm<Block
			final = s.Verdict
		}
	}
	return api.Decision{
		CallID:    callID,
		Phase:     phase,
		Final:     final,
		Signals:   signals,
		Risk:      risk,
		Rationale: rationale(final, signals),
	}
}

func rationale(final api.Verdict, signals []api.Signal) string {
	for _, s := range signals {
		if s.Verdict == final && len(s.Reasons) > 0 {
			return s.Axis.String() + ": " + s.Reasons[0]
		}
	}
	return final.String()
}

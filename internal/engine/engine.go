// Package engine defines the pluggable Engine interface and the Risk Decision
// Engine that aggregates multi-axis signals into a single verdict.
//
// Engines are how ToolHive / Pipelock / Invariant capabilities plug in without
// the Gateway core depending on any of them. An Engine may be in-process (Go)
// or an external gRPC sidecar (e.g. a Python behavior analyzer).
package engine

import (
	"context"
	"sync"
	"time"

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

// HookObserver is implemented by engines that can learn from a raw harness
// hook payload (PreToolUse/PostToolUse) when there is no proxy in the data
// path. This is what gives config-only hook deployments the same causal/taint
// capability the MCP proxy path gets via ResultObserver.
type HookObserver interface {
	ObserveHook(sessionID, toolID string, payload []byte)
}

// ObserveHook feeds a raw hook payload to every hook-aware engine. Call this
// BEFORE EvaluatePre so the current call's own provenance is available to the
// decision.
func (r *Registry) ObserveHook(sessionID, toolID string, payload []byte) {
	if sessionID == "" || len(payload) == 0 {
		return
	}
	for _, e := range r.engines {
		if h, ok := e.(HookObserver); ok {
			h.ObserveHook(sessionID, toolID, payload)
		}
	}
}

// EvaluatePre runs every engine's pre hook in PARALLEL with a deadline and
// aggregates the signals into a Decision. A per-call timeout keeps the gateway
// latency bounded even when a third-party engine misbehaves.
func (r *Registry) EvaluatePre(ctx context.Context, c *api.ToolCall) api.Decision {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	sigs := make([]api.Signal, len(r.engines))
	for i, e := range r.engines {
		wg.Add(1)
		go func(i int, e Engine) {
			defer wg.Done()
			sig, err := e.EvaluatePre(ctx, c)
			if s := normalize(e, sig, err); s != nil {
				sigs[i] = *s
			}
		}(i, e)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		// timed out: any engine that hasn't reported is treated as fail per its mode
		for i, e := range r.engines {
			if sigs[i].Engine == "" {
				if s := normalize(e, nil, ctx.Err()); s != nil {
					sigs[i] = *s
				}
			}
		}
	}
	var signals []api.Signal
	for _, s := range sigs {
		if s.Engine != "" {
			signals = append(signals, s)
		}
	}
	return Aggregate(c.CallID, api.PhasePre, signals)
}

// EvaluatePost runs every engine's post hook in parallel and aggregates.
func (r *Registry) EvaluatePost(ctx context.Context, c *api.ToolCall, res *api.ToolResult) api.Decision {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	sigs := make([]api.Signal, len(r.engines))
	for i, e := range r.engines {
		wg.Add(1)
		go func(i int, e Engine) {
			defer wg.Done()
			sig, err := e.EvaluatePost(ctx, c, res)
			if s := normalize(e, sig, err); s != nil {
				sigs[i] = *s
			}
		}(i, e)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		for i, e := range r.engines {
			if sigs[i].Engine == "" {
				if s := normalize(e, nil, ctx.Err()); s != nil {
					sigs[i] = *s
				}
			}
		}
	}
	var signals []api.Signal
	for _, s := range sigs {
		if s.Engine != "" {
			signals = append(signals, s)
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

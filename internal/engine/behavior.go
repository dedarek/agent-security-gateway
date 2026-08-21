package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/session"
)

// BehaviorEngine is the behavior/causal axis (Invariant class). It delegates to
// a Python sidecar that runs Invariant's LocalPolicy over the session trajectory
// (intelligence/analyzer/sidecar.py). The sidecar loads a real Invariant DSL
// policy expressing causal rules like "inbox data -> external send_email".
//
// The Go side owns the trajectory (session.Store) and, on each pre-check, sends
// past events + the pending tool call to the sidecar; any returned violation
// attributable to the pending call => BLOCK. See docs/BASE-PROJECTS-ANALYSIS.md §4.
type BehaviorEngine struct {
	sidecarURL string
	store      *session.Store
	client     *http.Client
	failMode   api.FailMode
}

func NewBehaviorEngine(sidecarURL string, store *session.Store, failMode api.FailMode) *BehaviorEngine {
	return &BehaviorEngine{
		sidecarURL: sidecarURL,
		store:      store,
		client:     &http.Client{Timeout: 5 * time.Second},
		failMode:   failMode,
	}
}

func (b *BehaviorEngine) Name() string           { return "behavior.invariant" }
func (b *BehaviorEngine) Axis() api.Axis         { return api.AxisBehavior }
func (b *BehaviorEngine) FailMode() api.FailMode { return b.failMode }

type checkRequest struct {
	Messages []session.Message `json:"messages"`
	Pending  []session.Message `json:"pending"`
}

type checkResponse struct {
	Violations []struct {
		Message string `json:"message"`
	} `json:"violations"`
	Error string `json:"error,omitempty"`
}

// EvaluatePre sends past trajectory + the pending call to the analyzer.
func (b *BehaviorEngine) EvaluatePre(ctx context.Context, c *api.ToolCall) (*api.Signal, error) {
	past := b.store.Trace(c.Principal.SessionID)
	pending := []session.Message{{
		Role: "assistant",
		ToolCalls: []session.ToolCall{{
			ID: c.CallID, Type: "function",
			Function: session.Function{Name: lastSegment(c.ToolID), Arguments: string(c.Arguments)},
		}},
	}}

	resp, err := b.check(ctx, checkRequest{Messages: past, Pending: pending})
	if err != nil {
		return nil, err // aggregator applies FailMode
	}
	if len(resp.Violations) > 0 {
		reasons := make([]string, 0, len(resp.Violations))
		for _, v := range resp.Violations {
			reasons = append(reasons, v.Message)
		}
		return &api.Signal{
			Axis:     api.AxisBehavior,
			Engine:   b.Name(),
			Score:    92,
			Verdict:  api.VerdictBlock,
			Reasons:  reasons,
			Evidence: []api.Evidence{{Kind: "trajectory", Detail: "invariant DSL violation over session trajectory"}},
			FailMode: b.failMode,
		}, nil
	}
	return &api.Signal{Axis: api.AxisBehavior, Engine: b.Name(), Verdict: api.VerdictAllow}, nil
}

func (b *BehaviorEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (b *BehaviorEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

func (b *BehaviorEngine) check(ctx context.Context, req checkRequest) (*checkResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.sidecarURL+"/check", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("behavior sidecar unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("behavior sidecar status %d", res.StatusCode)
	}
	var out checkResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("behavior sidecar error: %s", out.Error)
	}
	return &out, nil
}

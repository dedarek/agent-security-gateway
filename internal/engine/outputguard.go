// Package engine — output safety via llm-guard (protectai/llm-guard).
// Scans prompt and output for prompt injection, secrets, PII, toxicity.
// Delegates to a Python sidecar on :8903 that wraps llm-guard scanners.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

type OutputGuardEngine struct {
	url      string
	client   *http.Client
	failMode api.FailMode
}

func NewOutputGuardEngine(url string, failMode api.FailMode) *OutputGuardEngine {
	return &OutputGuardEngine{
		url:      url,
		client:   &http.Client{Timeout: 4 * time.Second},
		failMode: failMode,
	}
}

func (e *OutputGuardEngine) Name() string           { return "outputguard.llm-guard" }
func (e *OutputGuardEngine) Axis() api.Axis         { return api.AxisDataNetwork }
func (e *OutputGuardEngine) FailMode() api.FailMode { return e.failMode }

type guardRequest struct {
	Text string `json:"text"`
	Kind string `json:"kind"` // input | output
}

type guardResponse struct {
	Blocked bool     `json:"blocked"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Redact  []api.Redaction `json:"redactions,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func (e *OutputGuardEngine) EvaluatePre(ctx context.Context, c *api.ToolCall) (*api.Signal, error) {
	if e.url == "" {
		return nil, nil
	}
	text := string(c.Arguments)
	if len(text) == 0 {
		return nil, nil
	}
	// Only scan llm and sensitive tools
	if c.ToolID != "llm.chat" && c.ToolID != "llm.messages" && !isSensitiveForGuard(c.ToolID) {
		return nil, nil
	}
	resp, err := e.scan(ctx, guardRequest{Text: text, Kind: "input"})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("guard error: %s", resp.Error)
	}
	if resp.Blocked {
		return &api.Signal{
			Axis:    api.AxisDataNetwork,
			Engine:  e.Name(),
			Score:   resp.Score,
			Verdict: api.VerdictBlock,
			Reasons: resp.Reasons,
		}, nil
	}
	if len(resp.Redact) > 0 {
		return &api.Signal{
			Axis:       api.AxisDataNetwork,
			Engine:     e.Name(),
			Score:      resp.Score,
			Verdict:    api.VerdictRedact,
			Reasons:    resp.Reasons,
			Redactions: resp.Redact,
		}, nil
	}
	return &api.Signal{Axis: api.AxisDataNetwork, Engine: e.Name(), Verdict: api.VerdictAllow}, nil
}

func (e *OutputGuardEngine) EvaluatePost(ctx context.Context, c *api.ToolCall, r *api.ToolResult) (*api.Signal, error) {
	if e.url == "" || r == nil || len(r.Output) == 0 {
		return nil, nil
	}
	resp, err := e.scan(ctx, guardRequest{Text: string(r.Output), Kind: "output"})
	if err != nil {
		return nil, err
	}
	if resp.Blocked {
		return &api.Signal{
			Axis:    api.AxisDataNetwork,
			Engine:  e.Name(),
			Score:   resp.Score,
			Verdict: api.VerdictBlock,
			Reasons: resp.Reasons,
		}, nil
	}
	return &api.Signal{Axis: api.AxisDataNetwork, Engine: e.Name(), Verdict: api.VerdictAllow}, nil
}

func (e *OutputGuardEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (e *OutputGuardEngine) scan(ctx context.Context, req guardRequest) (*guardResponse, error) {
	b, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/scan", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("outputguard unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("outputguard status %d", res.StatusCode)
	}
	var out guardResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func isSensitiveForGuard(toolID string) bool {
	t := lastTool(toolID)
	switch t {
	case "Bash", "Write", "WebFetch", "SendEmail", "HttpPost":
		return true
	default:
		return false
	}
}

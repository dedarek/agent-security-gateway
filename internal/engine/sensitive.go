// Package engine — sensitive operation detection.
// Flags high-risk tools (Bash, Write, Edit, WebFetch, etc) for extra review.
// This is the "敏感操作要额外把关" requirement from the 40Q answers.
package engine

import (
	"context"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

var defaultSensitiveTools = map[string]bool{
	"Bash": true, "Write": true, "Edit": true, "MultiEdit": true,
	"WebFetch": true, "WebSearch": true, "NotebookEdit": true,
	"ComputerUse": true, "Task": true,
}

// SensitiveEngine flags sensitive tools. Action is configurable: log | alert | confirm | block.
type SensitiveEngine struct {
	tools  map[string]bool
	action api.Verdict // CONFIRM or BLOCK
}

func NewSensitiveEngine(extra []string, action string) *SensitiveEngine {
	m := make(map[string]bool, len(defaultSensitiveTools)+len(extra))
	for k, v := range defaultSensitiveTools {
		m[k] = v
	}
	for _, t := range extra {
		m[strings.TrimSpace(t)] = true
	}
	var v api.Verdict
	switch strings.ToLower(action) {
	case "block":
		v = api.VerdictBlock
	case "confirm":
		v = api.VerdictConfirm
	case "redact":
		v = api.VerdictRedact
	default:
		v = api.VerdictConfirm
	}
	return &SensitiveEngine{tools: m, action: v}
}

func (e *SensitiveEngine) Name() string     { return "sensitive" }
func (e *SensitiveEngine) Axis() api.Axis   { return api.AxisPermission }
func (e *SensitiveEngine) FailMode() api.FailMode { return api.FailOpen }

func (e *SensitiveEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	tool := lastTool(c.ToolID)
	if !e.tools[tool] && !e.tools[c.ToolID] {
		return nil, nil
	}
	return &api.Signal{
		Axis:    api.AxisPermission,
		Engine:  e.Name(),
		Score:   60,
		Verdict: e.action,
		Reasons: []string{"sensitive operation: " + tool + " requires review"},
	}, nil
}

func (e *SensitiveEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (e *SensitiveEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

func lastTool(id string) string {
	if idx := strings.LastIndex(id, "."); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

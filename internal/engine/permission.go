package engine

import (
	"context"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// PermissionEngine is a stub implementation of the permission axis (ToolHive
// class). In production, EvaluatePre delegates to a Cedar/OPA policy decision
// point; this stub hard-codes a couple of rules so the MVP demo runs end-to-end.
//
// See docs/PLAN.md Phase 1 and docs/MVP.md for the target behavior.
type PermissionEngine struct {
	// deny lists a tool_id that is always blocked for a given role.
	denyForRole map[string][]string
	// confirm lists sensitive tools that require human approval.
	confirm map[string]bool
}

func NewPermissionEngine() *PermissionEngine {
	return &PermissionEngine{
		denyForRole: map[string][]string{
			"employee": {"database.delete_user"},
		},
		confirm: map[string]bool{
			"database.export_all_users": true,
		},
	}
}

func (p *PermissionEngine) Name() string   { return "permission.cedar-stub" }
func (p *PermissionEngine) Axis() api.Axis { return api.AxisPermission }

func (p *PermissionEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	// Rule 1: role-based forbid.
	for _, denied := range p.denyForRole[c.Principal.Role] {
		if strings.EqualFold(denied, c.ToolID) {
			return &api.Signal{
				Axis:     api.AxisPermission,
				Engine:   p.Name(),
				Score:    90,
				Verdict:  api.VerdictBlock,
				Reasons:  []string{"role '" + c.Principal.Role + "' forbidden to call " + c.ToolID},
				Evidence: []api.Evidence{{Kind: "policy_match", Detail: "forbid(" + c.Principal.Role + ", " + c.ToolID + ")"}},
				FailMode: api.FailClosed,
			}, nil
		}
	}
	// Rule 2: sensitive operation requires confirmation.
	if p.confirm[c.ToolID] {
		return &api.Signal{
			Axis:     api.AxisPermission,
			Engine:   p.Name(),
			Score:    60,
			Verdict:  api.VerdictConfirm,
			Reasons:  []string{c.ToolID + " is sensitive and requires human approval"},
			FailMode: api.FailClosed,
		}, nil
	}
	// Default allow (this engine has no opinion).
	return &api.Signal{
		Axis:    api.AxisPermission,
		Engine:  p.Name(),
		Score:   0,
		Verdict: api.VerdictAllow,
	}, nil
}

func (p *PermissionEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil // permission axis has no runtime hook in the MVP
}

func (p *PermissionEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	// Phase 1 target: verify result did not exceed authorized scope.
	return nil, nil
}

package engine

import (
	"context"
	"fmt"
	"os"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"

	"github.com/dedarek/agent-security-gateway/api"
)

// PermissionEngine is the permission axis (ToolHive class). It uses cedar-go
// v1.8.0 — the exact engine ToolHive wraps in pkg/authz/authorizers/cedar — and
// mirrors ToolHive's entity/request model:
//
//	principal  Client::"<agent-id>"   with a `role` attribute for RBAC
//	action     Action::"call_tool"    (may it run at all?)  deny => BLOCK
//	action     Action::"auto_execute" (run without approval?) deny => CONFIRM
//	resource   Tool::"<tool-id>"
//
// The two-action check implements Bifrost's execute-vs-auto-execute split as the
// human-in-the-loop primitive. See docs/BASE-PROJECTS-ANALYSIS.md §1 & §3.4.
type PermissionEngine struct {
	mu        sync.RWMutex
	policySet *cedar.PolicySet
}

// NewPermissionEngineFromFile loads Cedar policies from a .cedar file.
func NewPermissionEngineFromFile(path string) (*PermissionEngine, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cedar policy: %w", err)
	}
	return NewPermissionEngineFromString(string(src))
}

// NewPermissionEngineFromString loads Cedar policies from a policy string.
func NewPermissionEngineFromString(policyText string) (*PermissionEngine, error) {
	ps, err := cedar.NewPolicySetFromBytes("permission.cedar", []byte(policyText))
	if err != nil {
		return nil, fmt.Errorf("parse cedar policies: %w", err)
	}
	return &PermissionEngine{policySet: ps}, nil
}

func (p *PermissionEngine) Name() string           { return "permission.cedar" }
func (p *PermissionEngine) Axis() api.Axis         { return api.AxisPermission }
func (p *PermissionEngine) FailMode() api.FailMode { return api.FailClosed }

// authorize runs one Cedar decision for the given action verb.
func (p *PermissionEngine) authorize(action string, c *api.ToolCall) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	principalUID := cedar.NewEntityUID("Client", cedar.String(c.Principal.AgentID))
	resourceUID := cedar.NewEntityUID("Tool", cedar.String(c.ToolID))

	// Principal entity carries the role attribute so `principal.role` works.
	principal := cedar.Entity{
		UID:        principalUID,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: cedar.NewRecord(cedar.RecordMap{"role": cedar.String(c.Principal.Role)}),
		Tags:       cedar.NewRecord(cedar.RecordMap{}),
	}
	entities := cedar.EntityMap{principalUID: principal}

	req := cedar.Request{
		Principal: principalUID,
		Action:    cedar.NewEntityUID("Action", cedar.String(action)),
		Resource:  resourceUID,
		Context:   cedar.NewRecord(cedar.RecordMap{}),
	}

	decision, diag := cedar.Authorize(p.policySet, entities, req)
	if len(diag.Errors) > 0 {
		return false, fmt.Errorf("cedar diagnostic: %v", diag.Errors)
	}
	return decision == cedar.Allow, nil
}

func (p *PermissionEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	// 1) May the tool run at all?
	allowed, err := p.authorize("call_tool", c)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return &api.Signal{
			Axis:     api.AxisPermission,
			Engine:   p.Name(),
			Score:    90,
			Verdict:  api.VerdictBlock,
			Reasons:  []string{fmt.Sprintf("cedar denied call_tool on %s for role=%s", c.ToolID, c.Principal.Role)},
			Evidence: []api.Evidence{{Kind: "policy_match", Detail: "forbid call_tool " + c.ToolID}},
			FailMode: api.FailClosed,
		}, nil
	}

	// 2) May it run WITHOUT human approval?
	auto, err := p.authorize("auto_execute", c)
	if err != nil {
		return nil, err
	}
	if !auto {
		return &api.Signal{
			Axis:     api.AxisPermission,
			Engine:   p.Name(),
			Score:    60,
			Verdict:  api.VerdictConfirm,
			Reasons:  []string{fmt.Sprintf("%s is executable but not auto-executable — human approval required", c.ToolID)},
			Evidence: []api.Evidence{{Kind: "policy_match", Detail: "forbid auto_execute " + c.ToolID}},
			FailMode: api.FailClosed,
		}, nil
	}

	return &api.Signal{Axis: api.AxisPermission, Engine: p.Name(), Score: 0, Verdict: api.VerdictAllow}, nil
}

func (p *PermissionEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}

func (p *PermissionEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

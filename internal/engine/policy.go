// Package engine — per-agent policy engine.
// Reads policies from DB (via *sql.DB) and enforces per-agent overrides.
// Global policies (agent_id IS NULL) are fallback; agent-specific wins.
package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

type PolicyEngine struct {
	db       *sql.DB
	failMode api.FailMode
}

func NewPolicyEngine(db *sql.DB) *PolicyEngine {
	return &PolicyEngine{db: db, failMode: api.FailOpen}
}

func (e *PolicyEngine) Name() string           { return "policy.per_agent" }
func (e *PolicyEngine) Axis() api.Axis         { return api.AxisPermission }
func (e *PolicyEngine) FailMode() api.FailMode { return e.failMode }

func (e *PolicyEngine) EvaluatePre(_ context.Context, c *api.ToolCall) (*api.Signal, error) {
	if e.db == nil || c == nil {
		return nil, nil
	}
	agentID := strings.TrimSpace(c.Principal.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(c.Principal.SessionID)
	}
	rows, err := e.db.Query(`SELECT action, enabled, rule_id, selector_json, agent_id
		FROM policies WHERE enabled=1 AND (agent_id=? OR agent_id IS NULL)`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var best *matchedPolicy
	for rows.Next() {
		var row matchedPolicy
		var enabled int
		var selectorJSON string
		var rowAgent sql.NullString
		if err := rows.Scan(&row.Action, &enabled, &row.RuleID, &selectorJSON, &rowAgent); err != nil {
			return nil, err
		}
		if enabled == 0 {
			continue
		}
		row.AgentSpecific = rowAgent.Valid

		var selector policySelector
		if strings.TrimSpace(selectorJSON) != "" && selectorJSON != "{}" {
			if err := json.Unmarshal([]byte(selectorJSON), &selector); err != nil {
				// A malformed selector must not silently broaden its scope.
				continue
			}
		}
		var ok bool
		if selector.empty() {
			ok = legacyRuleMatches(row.RuleID, c)
			row.Specificity = legacySpecificity(row.RuleID, c)
		} else {
			ok, row.Specificity = selector.matches(c)
			// A structured selector is an intentional v2 override of a
			// legacy tool-wide rule, even when it has only one condition.
			row.Specificity += 10
		}
		if !ok {
			continue
		}
		if row.AgentSpecific {
			row.Specificity += 1000
		}
		if best == nil || row.Specificity > best.Specificity ||
			(row.Specificity == best.Specificity && actionRank(row.Action) > actionRank(best.Action)) {
			best = &row
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if best != nil {
		return policyToSignal(best.Action, best.RuleID), nil
	}
	return nil, nil
}

func (e *PolicyEngine) EvaluateRuntime(_ context.Context, _ *api.ToolCall, _ Stream) (*api.Signal, error) {
	return nil, nil
}
func (e *PolicyEngine) EvaluatePost(_ context.Context, _ *api.ToolCall, _ *api.ToolResult) (*api.Signal, error) {
	return nil, nil
}

type matchedPolicy struct {
	Action        string
	RuleID        string
	Specificity   int
	AgentSpecific bool
}

// policySelector is the small common denominator shared by ToolHive's
// feature/operation/resource model and Bifrost's MCP execute/auto-execute
// split. Specialized data-flow engines remain separate.
type policySelector struct {
	Kind         string `json:"kind,omitempty"`       // capability | mcp | skill
	Feature      string `json:"feature,omitempty"`    // server | tool | resource | prompt | skill
	Capability   string `json:"capability,omitempty"` // shell | filesystem | network
	Server       string `json:"server,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Operation    string `json:"operation,omitempty"`     // list | get | call | read | write | delete
	Target       string `json:"target,omitempty"`        // URL, host, path or command glob
	PathClass    string `json:"path_class,omitempty"`    // workspace | sensitive
	CommandClass string `json:"command_class,omitempty"` // readonly | network | privileged | destructive
	DataClass    string `json:"data_class,omitempty"`    // public | sensitive
}

func (s policySelector) empty() bool { return s == (policySelector{}) }

func (s policySelector) matches(c *api.ToolCall) (bool, int) {
	tool := lastTool(c.ToolID)
	server := c.Resource
	operation := normalizeOperation(c.Action)
	target := string(c.Arguments)
	score := 0

	if s.Kind != "" {
		if s.Kind == "mcp" && !strings.Contains(c.ToolID, ".") && server == "" {
			return false, 0
		}
		if s.Kind == "skill" && !strings.Contains(strings.ToLower(c.ToolID), "skill") && operation != "load" {
			return false, 0
		}
		score++
	}
	if s.Feature != "" {
		valid := (s.Feature == "tool" && tool != "") ||
			(s.Feature == "server" && server != "") ||
			(s.Feature == "resource" && operation == "read") ||
			(s.Feature == "prompt" && operation == "get") ||
			(s.Feature == "skill" && strings.Contains(strings.ToLower(c.ToolID), "skill"))
		if !valid {
			return false, 0
		}
		score++
	}
	for _, f := range []struct{ pattern, value string }{
		{s.Server, server}, {s.Tool, tool},
	} {
		if f.pattern != "" {
			if !globMatch(f.pattern, f.value) {
				return false, 0
			}
			score++
		}
	}
	if s.Operation != "" {
		if !operationMatches(s.Operation, operation, s) {
			return false, 0
		}
		score++
	}
	if s.Target != "" {
		if !globMatch(s.Target, target) {
			return false, 0
		}
		score++
	}
	if s.Capability != "" {
		if !capabilityMatches(s.Capability, c) {
			return false, 0
		}
		score++
	}
	if s.PathClass != "" {
		if !globMatch(s.PathClass, pathClass(c)) {
			return false, 0
		}
		score++
	}
	if s.CommandClass != "" {
		if !globMatch(s.CommandClass, commandClass(c)) {
			return false, 0
		}
		score++
	}
	if s.DataClass != "" {
		if !globMatch(s.DataClass, dataClass(c)) {
			return false, 0
		}
		score++
	}
	return true, score
}

func policyToSignal(action, ruleID string) *api.Signal {
	var verdict api.Verdict
	switch strings.ToLower(action) {
	case "block":
		verdict = api.VerdictBlock
	case "confirm", "alert":
		verdict = api.VerdictConfirm
	case "redact":
		verdict = api.VerdictRedact
	default: // log/allow
		return &api.Signal{Axis: api.AxisPermission, Engine: "policy.per_agent", Verdict: api.VerdictAllow, Reasons: []string{"policy log: " + ruleID}}
	}
	return &api.Signal{
		Axis:    api.AxisPermission,
		Engine:  "policy.per_agent",
		Score:   80,
		Verdict: verdict,
		Reasons: []string{"per-agent policy: " + ruleID + " -> " + action},
	}
}

func legacyRuleMatches(ruleID string, c *api.ToolCall) bool {
	return ruleID == "*" || ruleID == c.ToolID || ruleID == lastTool(c.ToolID)
}

func legacySpecificity(ruleID string, c *api.ToolCall) int {
	if ruleID == c.ToolID {
		return 3
	}
	if ruleID == lastTool(c.ToolID) {
		return 2
	}
	if ruleID == "*" {
		return 1
	}
	return 0
}

func actionRank(action string) int {
	switch strings.ToLower(action) {
	case "block":
		return 3
	case "confirm", "alert":
		return 2
	default:
		return 1
	}
}

func globMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	ok, err := path.Match(pattern, value)
	return (err == nil && ok) || pattern == value
}

func normalizeOperation(action string) string {
	a := strings.ToLower(strings.TrimSpace(action))
	switch a {
	case "exec", "execute", "bash":
		return "execute"
	case "network", "fetch":
		return "call"
	default:
		return a
	}
}

func operationMatches(pattern, actual string, s policySelector) bool {
	if globMatch(pattern, actual) {
		return true
	}
	// The ingress currently records generic MCP tool calls as `write` for
	// backwards-compatible receipts. In the MCP policy vocabulary that is a
	// tools/call operation, not a filesystem write.
	return s.Kind == "mcp" && s.Feature == "tool" && pattern == "call" && actual == "write"
}

func capabilityMatches(cap string, c *api.ToolCall) bool {
	tool := strings.ToLower(lastTool(c.ToolID))
	switch strings.ToLower(cap) {
	case "shell":
		return tool == "bash" || tool == "shell" || tool == "exec"
	case "filesystem":
		return tool == "read" || tool == "write" || tool == "edit" || strings.Contains(tool, "file")
	case "network":
		return tool == "webfetch" || tool == "websearch" || normalizeOperation(c.Action) == "call"
	default:
		return false
	}
}

func commandClass(c *api.ToolCall) string {
	a := strings.ToLower(string(c.Arguments))
	for _, x := range []string{"rm ", "rm\"", "dd ", "mkfs", "kill ", "shutdown"} {
		if strings.Contains(a, x) {
			return "destructive"
		}
	}
	for _, x := range []string{"sudo", "chmod", "chown", "setfacl"} {
		if strings.Contains(a, x) {
			return "privileged"
		}
	}
	for _, x := range []string{"curl", "wget", "ssh ", "scp ", "nc ", "netcat"} {
		if strings.Contains(a, x) {
			return "network"
		}
	}
	return "readonly"
}

func pathClass(c *api.ToolCall) string {
	a := strings.ToLower(string(c.Arguments))
	for _, x := range []string{".env", ".ssh", "shadow", "passwd", "secret", "token", "credential", "private_key"} {
		if strings.Contains(a, x) {
			return "sensitive"
		}
	}
	return "workspace"
}

func dataClass(c *api.ToolCall) string {
	a := strings.ToLower(string(c.Arguments))
	for _, x := range []string{"token", "secret", "password", "private_key", "api_key"} {
		if strings.Contains(a, x) {
			return "sensitive"
		}
	}
	return "public"
}

// EvaluateSelectorJSON evaluates ONE candidate policy selector (raw JSON)
// against a tool call, returning a BLOCK signal if it matches and its action
// is block. Used by the policy simulator for historical replay — evaluates
// the candidate in isolation, not the DB policy set.
func EvaluateSelectorJSON(selectorJSON string, action, ruleID string, c *api.ToolCall) (*api.Signal, error) {
	if c == nil {
		return nil, nil
	}
	var sel policySelector
	if err := json.Unmarshal([]byte(selectorJSON), &sel); err != nil {
		return nil, err
	}
	matched, _ := sel.matches(c)
	if !matched {
		return nil, nil
	}
	if strings.EqualFold(action, "block") {
		return &api.Signal{
			Axis:    api.AxisPermission,
			Engine:  "policy.simulator",
			Score:   85,
			Verdict: api.VerdictBlock,
			Reasons: []string{"candidate policy " + ruleID + " matches"},
		}, nil
	}
	return &api.Signal{Axis: api.AxisPermission, Engine: "policy.simulator", Verdict: api.VerdictAllow}, nil
}

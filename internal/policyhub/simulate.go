package policyhub

import (
	"encoding/json"
	"fmt"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/engine"
)

// SimulationResult is the outcome of replaying a candidate policy against
// historical events.
type SimulationResult struct {
	Total       int            `json:"total"`
	WouldAllow  int            `json:"would_allow"`
	WouldBlock  int            `json:"would_block"`
	Changed     int            `json:"changed"`
	ChangedToB  int            `json:"changed_to_block"`
	ChangedToA  int            `json:"changed_to_allow"`
	Policy      map[string]any `json:"policy"`
	ChangedList []ChangedEvent `json:"changed_samples"`
}

// ChangedEvent is one event whose verdict differs under the candidate policy.
type ChangedEvent struct {
	SessionID    string `json:"session_id"`
	ToolID       string `json:"tool_id"`
	OldVerdict   string `json:"old_verdict"`
	NewVerdict   string `json:"new_verdict"`
	Reason       string `json:"reason"`
	OldRationale string `json:"old_rationale,omitempty"`
}

// Simulate replays a candidate policy (raw JSON body: selector + action +
// rule_id) against historical events and reports what would change.
// Only the candidate selector is evaluated — the diff is attributable to
// this policy alone, independent of the DB policy set.
func Simulate(policyJSON []byte, events []api.Event) (*SimulationResult, error) {
	var cand struct {
		Selector json.RawMessage `json:"selector"`
		Action   string          `json:"action"`
		RuleID   string          `json:"rule_id"`
	}
	if err := json.Unmarshal(policyJSON, &cand); err != nil {
		return nil, fmt.Errorf("bad policy json: %w", err)
	}
	selRaw := cand.Selector
	if len(selRaw) == 0 {
		// accept a flat selector body {tool:..., operation:...} too
		selRaw = policyJSON
	}
	action := cand.Action
	if action == "" {
		action = "block"
	}
	ruleID := cand.RuleID
	if ruleID == "" {
		ruleID = "candidate"
	}

	res := &SimulationResult{
		Policy: map[string]any{"selector": json.RawMessage(selRaw), "action": action, "rule_id": ruleID},
	}
	for _, ev := range events {
		call := &api.ToolCall{
			CallID:    ev.Call.CallID,
			ToolID:    ev.Call.ToolID,
			Arguments: ev.Call.Arguments,
			Action:    ev.Call.Action,
			Resource:  ev.Call.Resource,
			Principal: ev.Call.Principal,
		}
		old := verdictStr(ev.Decision.Final)
		sig, err := engine.EvaluateSelectorJSON(string(selRaw), action, ruleID, call)
		newV := "ALLOW"
		reason := ""
		if err == nil && sig != nil && sig.Verdict == api.VerdictBlock {
			newV = "BLOCK"
			if len(sig.Reasons) > 0 {
				reason = sig.Reasons[0]
			}
		}
		res.Total++
		if newV == "BLOCK" {
			res.WouldBlock++
		} else {
			res.WouldAllow++
		}
		if newV != old {
			res.Changed++
			if newV == "BLOCK" {
				res.ChangedToB++
			} else {
				res.ChangedToA++
			}
			if len(res.ChangedList) < 10 {
				res.ChangedList = append(res.ChangedList, ChangedEvent{
					SessionID:    ev.SessionID,
					ToolID:       ev.Call.ToolID,
					OldVerdict:   old,
					NewVerdict:   newV,
					Reason:       reason,
					OldRationale: ev.Decision.Rationale,
				})
			}
		}
	}
	return res, nil
}

func verdictStr(v api.Verdict) string {
	switch v {
	case api.VerdictBlock:
		return "BLOCK"
	case api.VerdictConfirm:
		return "CONFIRM"
	case api.VerdictRedact:
		return "REDACT"
	default:
		return "ALLOW"
	}
}

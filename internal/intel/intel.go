// Package intel is the Intelligence plane: it replays stored trajectories,
// finds the causal chain behind a BLOCK (root cause), and drafts a Cedar
// policy suggestion an operator can accept with one click.
package intel

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

type Suggestion struct {
	ID          string   `json:"id"`
	SessionID   string   `json:"session_id"`
	BlockedTool string   `json:"blocked_tool"`
	RootCause   string   `json:"root_cause"`
	Chain       []string `json:"chain"` // human-readable causal chain
	CedarPolicy string   `json:"cedar_policy"`
	Status      string   `json:"status"` // open | accepted | dismissed
}

// Analyze walks one session's trajectory backwards from the blocked call and
// reconstructs how untrusted content reached the sink.
func Analyze(ev store.SessionSummary, events []api.Event) *Suggestion {
	var blocked *api.Event
	for i := range events {
		if events[i].Decision.Final == api.VerdictBlock {
			blocked = &events[i] // last block in session
		}
	}
	if blocked == nil {
		return nil
	}

	sug := &Suggestion{
		ID:          fmt.Sprintf("sug-%s-%s", ev.SessionID, blocked.Call.CallID),
		SessionID:   ev.SessionID,
		BlockedTool: blocked.Call.ToolID,
		Status:      "open",
	}

	// 1) Which untrusted source fed this call? Find the earliest event whose
	//    result content contains any argument value of the blocked call.
	values := argValues(blocked.Call.Arguments)
	for i := range events {
		e := &events[i]
		if e.Result == nil || e.Decision.Final == api.VerdictBlock {
			continue
		}
		content := strings.ToLower(string(e.Result.Output))
		for _, v := range values {
			lv := strings.ToLower(strings.TrimSpace(v))
			if len(lv) >= 6 && strings.Contains(content, lv) {
				sug.Chain = append(sug.Chain,
					fmt.Sprintf("%s returned untrusted content containing %q", e.Call.ToolID, truncate(v, 40)))
				sug.RootCause = fmt.Sprintf(
					"indirect prompt injection / data-flow: value %q first appeared in %s output, then flowed into blocked call %s",
					truncate(v, 40), e.Call.ToolID, blocked.Call.ToolID)
				goto found
			}
		}
	}
found:

	// 2) Fill intermediate steps (chronological tool sequence).
	for i := range events {
		e := &events[i]
		if e.Result == nil || e.Call.CallID == blocked.Call.CallID {
			continue
		}
		sug.Chain = append(sug.Chain, fmt.Sprintf("%s => %s", e.Call.ToolID, e.Decision.Final.String()))
	}
	sug.Chain = append(sug.Chain, fmt.Sprintf("%s => BLOCKED", blocked.Call.ToolID))

	if sug.RootCause == "" {
		sug.RootCause = "policy violation without detected data-flow: " + blocked.Decision.Rationale
	}

	// 3) Draft Cedar policy. If the chain starts at an untrusted-source tool,
	//    forbid auto_execute on the sink for non-admin roles — the operator-
	//    friendly generalization of "email content must not authorize egress".
	//    Use the FULL tool id (e.g. "gw.send_email") so the rule matches the
	//    resource exactly as the gateway namespaces it.
	source := firstSource(sug.Chain)
	sink := blocked.Call.ToolID
	if source != "" {
		sug.CedarPolicy = fmt.Sprintf(`// Suggested by Intelligence after incident in session %s
// Root cause: %s
forbid (
  principal,
  action == Action::"auto_execute",
  resource == Tool::"%s"
)
when { principal.role != "admin" };`, sug.SessionID, strings.ReplaceAll(sug.RootCause, "\n", " "), sink)
	} else {
		sug.CedarPolicy = fmt.Sprintf(`// Suggested by Intelligence after incident in session %s
forbid (
  principal,
  action == Action::"call_tool",
  resource == Tool::"%s"
)
when { principal.role == "employee" };`, sug.SessionID, sink)
	}
	return sug
}

func firstSource(chain []string) string {
	if len(chain) == 0 {
		return ""
	}
	head := chain[0]
	if strings.Contains(head, "get_inbox") || strings.Contains(head, "read_secret") ||
		strings.Contains(head, "fetch") || strings.Contains(head, "read_file") {
		return head
	}
	return ""
}

func argValues(raw []byte) []string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	out := []string{}
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func lastSegment(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// SortSuggestions newest-first helper for the UI list.
func SortSuggestions(all []*Suggestion) {
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
}

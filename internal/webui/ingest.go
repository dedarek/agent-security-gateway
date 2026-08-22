// Hub-side ingest: receives probe event batches (NDJSON) and forwards them
// into the central event store; also serves remote CONFIRM checks from probes.
package webui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// RegisterIngest adds the probe-facing endpoints to the UI mux. Events arrive
// as NDJSON with kind=llm_call|tool_call and are normalized into api.Event so
// the existing trajectory/explorer UIs work unchanged.
func (s *Server) RegisterIngest(mux *http.ServeMux) {
	mux.HandleFunc("/api/ingest", s.apiIngest)
}

func (s *Server) apiIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		s.Store.Write(normalizeProbeEvent(raw))
		n++
	}
	writeJSON(w, map[string]int{"accepted": n})
}

// normalizeProbeEvent maps a probe record into the shared Event schema.
// llm_call events are recorded as a synthetic session entry so the explorer
// can replay agent reasoning alongside tool calls.
func normalizeProbeEvent(raw map[string]any) api.Event {
	session, _ := raw["session"].(string)
	if session == "" {
		session = "probe-unknown"
	}
	kind, _ := raw["kind"].(string)
	ev := api.Event{
		SessionID: session,
		Timestamp: timeNow(),
	}
	if t, ok := raw["trace_id"].(string); ok {
		ev.TraceID = t
	}
	if p, ok := raw["parent"].(string); ok {
		ev.ParentID = p
	}
	switch kind {
	case "llm_call":
		model, _ := raw["model"].(string)
		ev.Call = api.ToolCall{
			CallID:   idFor(session, "llm"),
			ToolID:   "llm." + model,
			Action:   "read",
			Arguments: mustJSONField(raw["request"]),
		}
		if resp := mustJSONField(raw["response"]); len(resp) > 0 {
			trunc := resp
			if len(trunc) > 16*1024 {
				trunc = trunc[:16*1024]
			}
			ev.Result = &api.ToolResult{Output: trunc}
		}
	case "tool_call":
		tool, _ := raw["tool"].(string)
		verdictStr, _ := raw["verdict"].(string)
		reason, _ := raw["reason"].(string)
		ev.Call = api.ToolCall{
			CallID:    idFor(session, tool),
			ToolID:    tool,
			Action:    "write",
			Arguments: mustJSONField(raw["args"]),
		}
		ev.Decision = decisionFromVerdict(verdictStr, reason)
	default:
		ev.Call = api.ToolCall{CallID: idFor(session, kind), ToolID: "probe." + kind}
	}
	return ev
}

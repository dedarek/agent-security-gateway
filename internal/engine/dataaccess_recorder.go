package engine

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/db"
)

// DataAccessRecorder turns tool-call observations into first-class DataAccess
// events in the trace store. It runs alongside the TaintEngine: the taint
// engine decides, the recorder writes the data-flow hop (source/operation/
// destination/class/taint/decision) so the lineage graph can be reconstructed
// later.
//
// This is the bridge between the existing trace + taint machinery and the
// context-aware DLP layer: every read/write/transform/transmit becomes a node
// in the data lineage graph.
type DataAccessRecorder struct {
	database *sql.DB
	taint    *TaintEngine
}

func NewDataAccessRecorder(database *sql.DB, taint *TaintEngine) *DataAccessRecorder {
	return &DataAccessRecorder{database: database, taint: taint}
}

// ObserveHook mirrors TaintEngine.ObserveHook's input shape (hook payload with
// tool_input/tool_response) and records a DataAccess hop. verdict is the final
// decision for this call.
func (r *DataAccessRecorder) ObserveHook(sessionID, traceID, toolID string, payload []byte, verdict string) {
	if r == nil || r.database == nil || traceID == "" {
		return
	}
	da := r.build(sessionID, toolID, payload, verdict)
	da.TraceID = traceID
	if err := db.UpsertDataAccess(r.database, da); err != nil {
		return // non-fatal: lineage is best-effort
	}
}

// ObserveProxy is the proxy-path counterpart: a real api.ToolCall with its
// result. The recorder extracts data-flow from the tool id + arguments.
func (r *DataAccessRecorder) ObserveProxy(c *api.ToolCall, verdict string) {
	if r == nil || r.database == nil || c == nil {
		return
	}
	da := api.DataAccess{
		TraceID:      c.CallID, // proxy path: call id anchors the span
		SpanID:       c.CallID,
		AgentID:      c.Principal.AgentID,
		ToolID:       c.ToolID,
		Decision:     verdict,
		At:           time.Now().UTC(),
		TrustZoneSrc: "local",
	}
	da.Operation, da.Source, da.Destination = classifyTool(c.ToolID, c.Arguments)
	_ = db.UpsertDataAccess(r.database, da)
}

func (r *DataAccessRecorder) build(sessionID, toolID string, payload []byte, verdict string) api.DataAccess {
	name := lastSegment(toolID)
	da := api.DataAccess{
		SpanID:       toolID + "-" + shortHash(sessionID),
		AgentID:      agentFromHook(payload),
		ToolID:       name,
		Decision:     verdict,
		At:           time.Now().UTC(),
		TrustZoneSrc: "local",
	}
	input, _ := hookParts(payload)
	// classifyTool wants the raw arguments JSON (it parses keys like
	// file_path/url). tool_input is the hook's arguments envelope.
	argsJSON := mustJSONRaw(input)
	da.Operation, da.Source, da.Destination = classifyTool(toolID, argsJSON)
	// taint tags: if this tool is a sink and session has taints, tag it
	if r.taint != nil {
		if marks := r.taint.store.Taints(sessionID); len(marks) > 0 {
			for _, m := range marks {
				da.TaintTags = append(da.TaintTags, m.Tokens...)
			}
		}
	}
	return da
}

func mustJSONRaw(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func agentFromHook(payload []byte) string {
	var p struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.AgentID
}

func shortHash(s string) string {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return itoa(int64(h & 0xffff))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// classifyTool maps a tool id + args to (operation, source, destination).
// read: Read/Grep/Glob/Get with file_path/source
// transmit: http_post/fetch/send_email/curl with url/to/destination
// write: Write/Edit/Bash redirect producing artifact
// transform: everything else (analysis/summarize)
func classifyTool(toolID string, args []byte) (op, source, dest string) {
	name := strings.ToLower(lastSegment(toolID))
	var a map[string]any
	_ = json.Unmarshal(args, &a)

	// extract path-ish values
	pathVal := firstString(a, "file_path", "path", "source", "src", "filename")
	urlVal := firstString(a, "url", "to", "destination", "dst", "target", "endpoint", "recipient")
	cmdVal := firstString(a, "command", "cmd")

	switch {
	case contains(name, "read", "grep", "glob", "get", "cat", "ls", "view", "inspect", "open"):
		op = "read"
		source = pathVal
		if source == "" {
			source = urlVal
		}
	case contains(name, "http", "fetch", "curl", "post", "send", "email", "upload", "web", "transmit", "notify"):
		op = "transmit"
		dest = urlVal
		if dest == "" && cmdVal != "" {
			dest = cmdVal
		}
	case contains(name, "write", "edit", "create", "append", "save", "patch"):
		op = "write"
		dest = pathVal
	case contains(name, "bash", "exec", "shell", "run", "command", "terminal"):
		op = "transform"
		source = cmdVal
		if strings.Contains(strings.ToLower(cmdVal), "curl") || strings.Contains(strings.ToLower(cmdVal), "wget") {
			op = "transmit"
			dest = cmdVal
		} else if strings.Contains(strings.ToLower(cmdVal), ">") || strings.Contains(strings.ToLower(cmdVal), "tee") {
			op = "write"
			dest = cmdVal
		}
	default:
		op = "transform"
		source = pathVal
		dest = urlVal
	}
	return op, source, dest
}

func firstString(a map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := a[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

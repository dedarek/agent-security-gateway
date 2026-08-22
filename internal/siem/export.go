// SIEM export: converts stored events into CEF (ArcSight common format) and
// JSON lines compatible with Splunk HEC / Elastic ingest.
package siem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dedarek/agent-security-gateway/api"
)

// CEF renders one event in Common Event Format for ArcSight/QRadar style
// collectors: CEF:0|vendor|product|version|signature|name|severity|extension
func CEF(ev api.Event) string {
	sev := 0
	switch ev.Decision.Final {
	case api.VerdictBlock:
		sev = 9
	case api.VerdictRedact:
		sev = 6
	case api.VerdictConfirm:
		sev = 4
	}
	name := ev.Call.ToolID + " " + ev.Decision.Final.String()
	ext := fmt.Sprintf(
		"src=%s duser=%s fname=%s msg=%q cs1=%s cs1Label=traceId",
		ev.SessionID, ev.Call.Principal.UserID, ev.Call.ToolID,
		strings.ReplaceAll(ev.Decision.Rationale, "\"", "'"), ev.TraceID)
	return fmt.Sprintf("CEF:0|dedarek|Agent Security Gateway|0.2|%s|%s|%d|%s",
		ev.Call.Action, name, sev, ext)
}

// SplunkJSON renders one event as a Splunk HEC-compatible JSON line.
func SplunkJSON(ev api.Event) string {
	out := map[string]any{
		"time":       ev.Timestamp.Unix(),
		"sourcetype": "asg:event",
		"event": map[string]any{
			"session":   ev.SessionID,
			"trace_id":  ev.TraceID,
			"tool":      ev.Call.ToolID,
			"action":    ev.Call.Action,
			"principal": ev.Call.Principal.UserID,
			"verdict":   ev.Decision.Final.String(),
			"risk":      ev.Decision.Risk,
			"reason":    ev.Decision.Rationale,
			"timestamp": ev.Timestamp.Format(time.RFC3339),
		},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// Export converts events in the given format ("cef"|"splunk").
func Export(events []api.Event, format string) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if format == "cef" {
			out = append(out, CEF(ev))
		} else {
			out = append(out, SplunkJSON(ev))
		}
	}
	return out
}

// Rampart bridge: consumes Rampart audit SSE/API and normalizes into ASEF
// events for the ASG control plane (8090 /api/ingest NDJSON).
//
// Architecture (per product review):
//
//	Rampart (Action Plane) --SSE/JSON--> rampart-bridge --ASEF--> 8090 --> Semantica/Console
//	8181    (Model Plane)  --ingest-------> rampart-bridge ----------------------^
//
// Event mapping (Rampart audit.v1 -> ASEF probe event):
//
//	agent            -> agent_id        (normalized: claude-code -> local-claude-code)
//	session          -> session
//	run_id           -> trace_id
//	tool             -> kind/tool       (exec/file/mcp -> tool_call)
//	request.command  -> request
//	decision.action  -> verdict         (allow/deny/ask)
//	decision.matched_policies -> policy_ids
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type rampEvent struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Agent         string `json:"agent"`
	Session       string `json:"session"`
	RunID         string `json:"run_id"`
	Tool          string `json:"tool"`
	Request       struct {
		Command string `json:"command"`
	} `json:"request"`
	Decision struct {
		Action          string   `json:"action"`
		MatchedPolicies []string `json:"matched_policies"`
	} `json:"decision"`
}

type asefEvent struct {
	Kind     string   `json:"kind"`
	AgentID  string   `json:"agent_id"`
	Session  string   `json:"session"`
	TraceID  string   `json:"trace_id"`
	SpanID   string   `json:"span_id,omitempty"`
	ParentID string   `json:"parent_id,omitempty"`
	Tool     string   `json:"tool"`
	Request  any      `json:"request,omitempty"`
	Verdict  string   `json:"verdict"`
	Policies []string `json:"policies,omitempty"`
}

func main() {
	rampartURL := flag.String("rampart", "http://127.0.0.1:9090", "Rampart serve base URL")
	tokenPath := flag.String("token", "", "Path to Rampart token file (default ~/.rampart/token)")
	hubURL := flag.String("hub", "http://127.0.0.1:8090", "ASG control plane base URL")
	pollInterval := flag.Duration("interval", 3*time.Second, "Audit poll interval (SSE fallback)")
	flag.Parse()

	token := ""
	if *tokenPath != "" {
		b, _ := os.ReadFile(*tokenPath)
		token = strings.TrimSpace(string(b))
	} else {
		home, _ := os.UserHomeDir()
		b, _ := os.ReadFile(home + "/.rampart/token")
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		log.Fatal("no Rampart token found")
	}

	// Poll audit events (SSE stream also available at /v1/events/stream; poll is
	// simpler and idempotent for the bridge). Track last-seen event id.
	seen := map[string]bool{}
	lastTS := ""
	tc := newTraceCorrelator()
	go heartbeatLoop(*hubURL, tc, 30*time.Second)

	log.Printf("rampart-bridge: %s -> %s (poll %s)", *rampartURL, *hubURL, *pollInterval)
	for {
		time.Sleep(*pollInterval)
		events, err := fetchAudit(*rampartURL, token, lastTS)
		if err != nil {
			log.Printf("fetch audit: %v", err)
			continue
		}
		for _, ev := range events {
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			if ev.Timestamp > lastTS {
				lastTS = ev.Timestamp
			}
			asef := toASEF(ev)
			if asef.AgentID == "" {
				continue // nothing to attribute (e.g. agent-less parse failure)
			}
			// Trace correlation: mint/attach trace_id + span per session
			traceID := tc.traceFor(asef.AgentID, asef.Session)
			span, parent := tc.nextSpan(traceID)
			asef.TraceID = traceID
			asef.SpanID = span
			asef.ParentID = parent
			if err := sendIngest(*hubURL, asef); err != nil {
				log.Printf("ingest %s: %v", ev.ID, err)
				continue
			}
			log.Printf("→ %s agent=%s tool=%s verdict=%s trace=%s", ev.ID[:12], ev.Agent, ev.Tool, ev.Decision.Action, traceID)
		}
	}
}

func fetchAudit(base, token, since string) ([]rampEvent, error) {
	url := base + "/v1/audit/events?limit=100"
	if since != "" {
		url += "&since=" + since
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Events []rampEvent `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Events, nil
}

func toASEF(ev rampEvent) asefEvent {
	// agent_id: qualify with machine-local prefix to match ASG convention
	agentID := ev.Agent
	if agentID == "" {
		return asefEvent{} // no agent identity — nothing to attribute
	}
	if !strings.Contains(agentID, "-") {
		agentID = "local-" + agentID
	}
	kind := "tool_call"
	if ev.Tool == "exec" || ev.Tool == "shell" {
		kind = "tool_call"
	}
	req := map[string]any{}
	if ev.Request.Command != "" {
		req["command"] = ev.Request.Command
	}
	session := ev.Session
	if session == "" {
		session = ev.RunID // fallback: trace ID as session anchor
	}
	if session == "" {
		session = "rampart-" + ev.Agent
	}
	// Map Rampart verdict (allow/deny/ask) -> ASG verdict (ALLOW/BLOCK/CONFIRM)
	verdict := "ALLOW"
	switch strings.ToLower(ev.Decision.Action) {
	case "deny", "block":
		verdict = "BLOCK"
	case "ask", "approval", "confirm":
		verdict = "CONFIRM"
	}
	return asefEvent{
		Kind:     kind,
		AgentID:  agentID,
		Session:  session,
		TraceID:  ev.RunID,
		Tool:     ev.Tool,
		Request:  req,
		Verdict:  verdict,
		Policies: ev.Decision.MatchedPolicies,
	}
}

func sendIngest(hub string, ev asefEvent) error {
	b, _ := json.Marshal(ev)
	resp, err := http.Post(hub+"/api/ingest", "application/x-ndjson", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return fmt.Errorf("hub %d: %v", resp.StatusCode, out)
	}
	return nil
}

// keep bufio import (future SSE mode)
var _ = bufio.NewReader

// heartbeatLoop keeps event-driven agents "active" in the console: every
// interval it sends a heartbeat for every agent seen (with accumulated
// model/provider), so an agent card does not flip to offline between events.
func heartbeatLoop(hub string, tc *traceCorrelator, interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		machineName, machineIP := localMachineMetadata()
		for _, e := range tc.snapshot() {
			at := agentTypeFromName(e.AgentID)
			body, _ := json.Marshal(map[string]string{
				string([]byte{'a', 'g', 'e', 'n', 't', '_', 'i', 'd'}):                     e.AgentID,
				string([]byte{'a', 'g', 'e', 'n', 't', '_', 't', 'y', 'p', 'e'}):           at,
				string([]byte{'a', 'l', 'i', 'a', 's'}):                                    string([]byte{}),
				string([]byte{'m', 'a', 'c', 'h', 'i', 'n', 'e', '_', 'n', 'a', 'm', 'e'}): machineName,
				string([]byte{'i', 'p'}):                                                   machineIP,
			})
			resp, err := http.Post(hub+"/api/agents/heartbeat", "application/json", bytes.NewReader(body))
			if err != nil {
				continue
			}
			resp.Body.Close()
		}
	}
}

// localMachineMetadata is bridge-side evidence: it reports the host and the
// first active non-loopback IPv4 address visible to the bridge process.
func localMachineMetadata() (string, string) {
	host, _ := os.Hostname()
	interfaces, _ := net.Interfaces()
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Index < interfaces[j].Index })
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil {
				return host, ip.String()
			}
		}
	}
	return host, ""
}

// agentTypeFromName maps a Rampart agent id to the ASG agent_type used for
// console logos and filtering (claude-code -> claude_code, codex -> codex, ...).
func agentTypeFromName(agent string) string {
	switch {
	case strings.Contains(agent, "claude"):
		return "claude_code"
	case strings.Contains(agent, "codex"):
		return "codex"
	case strings.Contains(agent, "cursor"):
		return "cursor"
	case strings.Contains(agent, "copilot"):
		return "copilot"
	case strings.Contains(agent, "opencode"):
		return "opencode"
	default:
		return "custom"
	}
}

// snapshot returns all known (agent, session) pairs seen so far.
func (tc *traceCorrelator) snapshot() []traceEntry {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	out := make([]traceEntry, 0, len(tc.m))
	for _, e := range tc.m {
		out = append(out, e)
	}
	return out
}

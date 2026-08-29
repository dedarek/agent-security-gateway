// Package kg — ontology.go builds the unified B/D/E entity model from events.
//
// Design rule (MVP, no debt): nodes are deduplicated real-world things
// (agents, tools, origins, sinks, stories). Actions live on edges as
// properties, so node count grows with entity variety, not action count.
package kg

import (
	"encoding/base64"
	"regexp"
	"sort"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
)

// ── Unified node/edge shapes (JSON-serializable, shared by all three layers) ──

type OntoNode struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"` // story|origin|sink|agent|tool|verdict
	Label string         `json:"label"`
	Props map[string]any `json:"props,omitempty"`
}

type OntoEdge struct {
	Source string         `json:"source"`
	Target string         `json:"target"`
	Type   string         `json:"type"` // narrates|reads|flows_to|exfiltrates|resulted_in
	Props  map[string]any `json:"props,omitempty"`
}

type OntoGraph struct {
	Nodes []OntoNode `json:"nodes"`
	Edges []OntoEdge `json:"edges"`
}

// sensitivePath marks credentials/keys so they surface as first-class origins.
var sensitivePath = regexp.MustCompile(`(?i)(\.ssh|\.aws|\.kube|id_rsa|id_ed25519|\.env|\.pem$|credentials|secret|token)`)

var (
	hostRe = regexp.MustCompile(`https?://([a-zA-Z0-9.-]+)`)
	pathRe = regexp.MustCompile(`(?:file_path["']?\s*[:=]\s*["']([^"']+))|(/[\w./\-]{3,})`)
	// taint rationale: "value '/tmp/x' in Bash originated from untrusted source derived(Read:/home/u/.aws/credentials)"
	derivedRe = regexp.MustCompile(`derived\((\w+):([^)]+)\)`)
	valueRe   = regexp.MustCompile(`value '([^']+)'`)
	hostInCmd = regexp.MustCompile(`([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}`)
)

// normHost lowercases and strips a default port — conservative merge only.
func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ":443")
	h = strings.TrimSuffix(h, ":80")
	return h
}

// normPath trims trailing slash — conservative merge only.
func normPath(p string) string {
	return strings.TrimSuffix(strings.TrimSpace(p), "/")
}

// BuildOntology folds events into the unified B/D/E graph.
func BuildOntology(events []api.Event) *OntoGraph {
	nodes := map[string]*OntoNode{}
	var edges []OntoEdge
	addNode := func(n OntoNode) *OntoNode {
		if ex, ok := nodes[n.ID]; ok {
			return ex
		}
		if n.Props == nil {
			n.Props = map[string]any{}
		}
		nodes[n.ID] = &n
		return nodes[n.ID]
	}
	addEdge := func(src, typ, dst string, props map[string]any) {
		edges = append(edges, OntoEdge{Source: src, Target: dst, Type: typ, Props: props})
	}

	for _, ev := range events {
		actor := ev.Call.Principal.AgentID
		if actor == "" {
			actor = ev.Call.Principal.UserID
		}
		if actor == "" {
			actor = "unknown"
		}
		agentID := "agent:" + actor
		tool := lastSeg(ev.Call.ToolID)
		toolID := "tool:" + tool
		final := ev.Decision.Final.String()
		risk := ev.Decision.Risk
		at := ev.Timestamp
		if at.IsZero() {
			at = ev.Call.Timestamp
		}
		atStr := at.UTC().Format("2006-01-02 15:04:05")
		sid := ev.SessionID
		if sid == "" {
			sid = ev.Call.Principal.SessionID
		}

		addNode(OntoNode{ID: agentID, Type: "agent", Label: actor})
		addNode(OntoNode{ID: toolID, Type: "tool", Label: tool, Props: map[string]any{"danger": toolDanger(tool)}})

		// story node per session
		if sid != "" {
			storyID := "story:" + sid
			st := addNode(OntoNode{ID: storyID, Type: "story", Label: sid, Props: map[string]any{
				"agent": actor, "steps": 0, "peak_risk": 0, "outcome": "clean", "last": atStr,
			}})
			st.Props["steps"] = st.Props["steps"].(int) + 1
			if risk > st.Props["peak_risk"].(int) {
				st.Props["peak_risk"] = risk
			}
			if final == "BLOCK" {
				st.Props["outcome"] = "blocked"
			}
			st.Props["last"] = atStr
			addEdge(storyID, "narrates", agentID, nil)
		}

		// extract origin (what was read) + sink (where it went) from arguments
		origins, sinks := extractEndpoints(ev)

		for _, og := range origins {
			on := addNode(OntoNode{ID: og, Type: "origin", Label: shortID(og), Props: map[string]any{
				"sensitive": isSensitive(og), "reads": 0,
			}})
			on.Props["reads"] = on.Props["reads"].(int) + 1
			addEdge(agentID, "reads", og, map[string]any{"at": atStr, "tool": tool, "verdict": final, "risk": risk})
		}
		for _, sk := range sinks {
			addNode(OntoNode{ID: sk, Type: "sink", Label: shortID(sk)})
			addEdge(agentID, "exfiltrates", sk, map[string]any{"at": atStr, "tool": tool, "verdict": final, "risk": risk})
		}

		// taint lineage: parse derived(...) from taint signal reasons to link origin→sink
		taintSrc := taintOrigin(ev)
		if taintSrc != "" {
			for _, sk := range sinks {
				addNode(OntoNode{ID: sk, Type: "sink", Label: shortID(sk)})
				addEdge(taintSrc, "flows_to", sk, map[string]any{
					"tainted": true, "at": atStr, "verdict": final, "risk": risk, "tool": tool,
				})
			}
		}

		// verdict detail node only for BLOCK/CONFIRM (D layer pulls these on demand)
		if final == "BLOCK" || final == "CONFIRM" {
			vID := "verdict:" + ev.Call.CallID
			addNode(OntoNode{ID: vID, Type: "verdict", Label: final, Props: map[string]any{
				"risk": risk, "at": atStr, "session": sid, "agent": actor, "tool": tool,
			}})
			if sid != "" {
				addEdge("story:"+sid, "resulted_in", vID, nil)
			}
		}
	}

	out := &OntoGraph{Nodes: []OntoNode{}, Edges: edges}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, *n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })
	return out
}

// extractEndpoints pulls origins (read targets) and sinks (network/file write
// targets) out of a tool call. Deterministic regexes, conservative.
func extractEndpoints(ev api.Event) (origins []string, sinks []string) {
	var raw string
	if len(ev.Call.Arguments) > 0 {
		raw = string(ev.Call.Arguments)
		// Arguments may be base64 in some paths; try JSON-ish as-is first.
		if !strings.Contains(raw, "/") && !strings.Contains(raw, "http") {
			if dec, err := decodeB64(raw); err == nil {
				raw = dec
			}
		}
	}
	rationale := ev.Decision.Rationale
	tool := lastSeg(ev.Call.ToolID)

	seen := map[string]bool{}
	addO := func(s string) { if s != "" && !seen[s] { seen[s] = true; origins = append(origins, s) } }
	addS := func(s string) { if s != "" && !seen[s] { seen[s] = true; sinks = append(sinks, s) } }

	switch tool {
	case "Read", "Grep", "Glob":
		for _, m := range pathRe.FindAllStringSubmatch(raw, -1) {
			p := m[1]
			if p == "" {
				p = m[2]
			}
			if p != "" {
				addO("org:file:" + normPath(p))
			}
		}
	case "Write", "Edit":
		for _, m := range pathRe.FindAllStringSubmatch(raw, -1) {
			p := m[1]
			if p == "" {
				p = m[2]
			}
			if p != "" {
				addS("snk:file:" + normPath(p))
			}
		}
	case "WebFetch":
		if h := hostRe.FindStringSubmatch(raw); len(h) > 1 {
			addO("org:host:" + normHost(h[1]))
		}
	case "Bash":
		// sensitive path reads inside shell
		for _, m := range pathRe.FindAllStringSubmatch(raw, -1) {
			p := m[1]
			if p == "" {
				p = m[2]
			}
			if p != "" && sensitivePath.MatchString(p) {
				addO("org:file:" + normPath(p))
			}
		}
		// network egress hosts
		for _, h := range hostRe.FindAllStringSubmatch(raw, -1) {
			addS("snk:host:" + normHost(h[1]))
		}
		// curl without scheme
		if !strings.Contains(raw, "http") {
			for _, h := range hostInCmd.FindAllString(raw, -1) {
				if !strings.HasSuffix(h, ".sh") && strings.Contains(h, ".") {
					addS("snk:host:" + normHost(h))
				}
			}
		}
	}
	// taint rationale also reveals the origin even when args are cropped
	if t := taintOrigin(ev); t != "" {
		addO(t)
	}
	_ = rationale
	return origins, sinks
}

// taintOrigin parses "derived(Read:/path)" from a behavior.taint signal reason
// into a normalized origin id. Returns "" when absent.
func taintOrigin(ev api.Event) string {
	for _, sig := range ev.Decision.Signals {
		if !strings.Contains(sig.Engine, "taint") {
			continue
		}
		for _, r := range sig.Reasons {
			if m := derivedRe.FindStringSubmatch(r); len(m) == 3 {
				src := strings.TrimSpace(m[2])
				if strings.Contains(src, "/") {
					return "org:file:" + normPath(src)
				}
				return "org:" + src
			}
		}
	}
	return ""
}

func isSensitive(orgID string) bool {
	return sensitivePath.MatchString(orgID)
}

func toolDanger(tool string) string {
	switch tool {
	case "Read", "Grep", "Glob", "LS":
		return "L0"
	case "Write", "Edit":
		return "L1"
	case "Bash", "WebFetch", "WebSearch":
		return "L2"
	}
	return "L1"
}

func shortID(id string) string {
	// strip type prefix for a readable label
	if i := strings.Index(id, ":"); i >= 0 {
		rest := id[i+1:]
		if j := strings.Index(rest, ":"); j >= 0 {
			return rest[j+1:]
		}
		return rest
	}
	return id
}

func decodeB64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	return string(b), err
}

// OntoEvidence renders one decision's per-engine votes (D layer).
type EngineVote struct {
	Engine  string   `json:"engine"`
	Axis    int      `json:"axis"`
	Score   int      `json:"score"`
	Vote    string   `json:"vote"`
	Reasons []string `json:"reasons,omitempty"`
}

type Evidence struct {
	Final     string       `json:"final"`
	Risk      int          `json:"risk"`
	Rationale string       `json:"rationale"`
	Votes     []EngineVote `json:"votes"`
	SoleAxis  bool         `json:"sole_axis"` // true when exactly one engine drove the verdict
	TaintFrom string       `json:"taint_from,omitempty"`
}

func BuildEvidence(ev api.Event) *Evidence {
	e := &Evidence{
		Final:     ev.Decision.Final.String(),
		Risk:      ev.Decision.Risk,
		Rationale: ev.Decision.Rationale,
		TaintFrom: taintOrigin(ev),
	}
	blockers := 0
	for _, s := range ev.Decision.Signals {
		if s.Engine == "" {
			continue
		}
		e.Votes = append(e.Votes, EngineVote{
			Engine: s.Engine, Axis: int(s.Axis), Score: s.Score,
			Vote: s.Verdict.String(), Reasons: s.Reasons,
		})
		if s.Verdict == api.VerdictBlock {
			blockers++
		}
	}
	e.SoleAxis = blockers == 1 && e.Final == "BLOCK"
	// stable order: highest score first
	sort.Slice(e.Votes, func(i, j int) bool { return e.Votes[i].Score > e.Votes[j].Score })
	return e
}

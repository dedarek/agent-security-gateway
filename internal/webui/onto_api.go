package webui

import (
	"net/http"
	"sort"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/kg"
)

// RegisterOntoAPI serves the unified B/D/E ontology:
//
//	GET /api/onto/stories            → session story cards (E layer)
//	GET /api/onto/lineage?focus=<id> → focused taint-lineage subgraph (B layer)
//	GET /api/onto/evidence?event=<callid> → one decision's engine votes (D layer)
//	GET /api/onto/graph              → whole unified graph (bounded)
func (s *Server) RegisterOntoAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/onto/stories", s.Auth.middleware(s.apiOntoStories))
	mux.HandleFunc("/api/onto/lineage", s.Auth.middleware(s.apiOntoLineage))
	mux.HandleFunc("/api/onto/evidence", s.Auth.middleware(s.apiOntoEvidence))
	mux.HandleFunc("/api/onto/graph", s.Auth.middleware(s.apiOntoGraph))
}

func (s *Server) ontoEvents(n int) []api.Event {
	if s.Store == nil {
		return nil
	}
	return s.Store.Recent(n)
}

// apiOntoStories returns one card per session: agent, steps, peak risk,
// outcome, phase sequence (recon→collect→exfil→done), compact step list.
func (s *Server) apiOntoStories(w http.ResponseWriter, _ *http.Request) {
	events := s.ontoEvents(2000)
	g := kg.BuildOntology(events)

	// per-session step list (chronological) for the phase strip
	bySess := map[string][]stepRow{}
	for _, ev := range events {
		sid := ev.SessionID
		if sid == "" {
			sid = ev.Call.Principal.SessionID
		}
		if sid == "" {
			continue
		}
		at := ev.Timestamp
		if at.IsZero() {
			at = ev.Call.Timestamp
		}
		sum := ev.Decision.Rationale
		if sum == "" {
			sum = string(ev.Call.Arguments)
		}
		if len(sum) > 60 {
			sum = sum[:60] + "…"
		}
		bySess[sid] = append(bySess[sid], stepRow{
			At:      at.UTC().Format("15:04:05"),
			Tool:    lastOf(ev.Call.ToolID),
			Verdict: ev.Decision.Final.String(),
			Summary: sum,
		})
	}

	cards := []map[string]any{}
	for _, n := range g.Nodes {
		if n.Type != "story" {
			continue
		}
		sessionID := n.ID[len("story:"):]
		steps := bySess[sessionID]
		// chronological (events are newest-first from Store.Recent)
		for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
			steps[i], steps[j] = steps[j], steps[i]
		}
		phases := phasesOf(steps)
		cards = append(cards, map[string]any{
			"session_id": sessionID,
			"agent":      n.Props["agent"],
			"steps":      n.Props["steps"],
			"peak_risk":  n.Props["peak_risk"],
			"outcome":    n.Props["outcome"],
			"last":       n.Props["last"],
			"phases":     phases,
			"timeline":   steps,
		})
	}
	// BLOCK first, then by peak risk desc
	sort.Slice(cards, func(i, j int) bool {
		bi := cards[i]["outcome"] == "blocked"
		bj := cards[j]["outcome"] == "blocked"
		if bi != bj {
			return bi
		}
		return cards[i]["peak_risk"].(int) > cards[j]["peak_risk"].(int)
	})
	writeJSON(w, map[string]any{"stories": cards, "total": len(cards)})
}

// apiOntoLineage returns the focused subgraph around a node id (origin / sink /
// agent / story). Default: focus + 1-hop + all tainted flows_to edges among them.
func (s *Server) apiOntoLineage(w http.ResponseWriter, r *http.Request) {
	focus := r.URL.Query().Get("focus")
	g := kg.BuildOntology(s.ontoEvents(2000))

	if focus == "" {
		// global risk-first view: only nodes touched by a BLOCK / tainted edge
		writeJSON(w, riskSubgraph(g, 40))
		return
	}

	keep := map[string]bool{focus: true}
	// 1-hop
	for _, e := range g.Edges {
		if e.Source == focus {
			keep[e.Target] = true
		}
		if e.Target == focus {
			keep[e.Source] = true
		}
	}
	// extend along tainted flows_to one more hop (the heart of lineage)
	for _, e := range g.Edges {
		if e.Type == "flows_to" && (keep[e.Source] || keep[e.Target]) {
			keep[e.Source] = true
			keep[e.Target] = true
		}
	}
	var nodes []kg.OntoNode
	var edges []kg.OntoEdge
	for _, n := range g.Nodes {
		if keep[n.ID] {
			nodes = append(nodes, n)
		}
	}
	for _, e := range g.Edges {
		if keep[e.Source] && keep[e.Target] {
			edges = append(edges, e)
		}
	}
	writeJSON(w, map[string]any{"nodes": nodes, "edges": edges, "focus": focus})
}

// apiOntoEvidence returns the engine-vote matrix for one event (by CallID).
func (s *Server) apiOntoEvidence(w http.ResponseWriter, r *http.Request) {
	callID := r.URL.Query().Get("event")
	if callID == "" {
		http.Error(w, "missing event", 400)
		return
	}
	for _, ev := range s.ontoEvents(2000) {
		if ev.Call.CallID == callID {
			writeJSON(w, kg.BuildEvidence(ev))
			return
		}
	}
	http.Error(w, "event not found", 404)
}

// apiOntoGraph returns the whole unified graph, capped.
func (s *Server) apiOntoGraph(w http.ResponseWriter, _ *http.Request) {
	g := kg.BuildOntology(s.ontoEvents(2000))
	writeJSON(w, riskSubgraph(g, 60))
}

// riskSubgraph keeps risk-bearing structure: stories with outcome=blocked,
// sensitive origins, tainted sinks, and their connecting agents/tools.
// Capped at `cap` nodes, BLOCK-bearing first.
func riskSubgraph(g *kg.OntoGraph, cap int) map[string]any {
	score := func(n kg.OntoNode) int {
		switch n.Type {
		case "origin":
			if n.Props["sensitive"] == true {
				return 100
			}
			return 40
		case "sink":
			return 60
		case "story":
			if n.Props["outcome"] == "blocked" {
				return 90
			}
			return 10
		case "verdict":
			return 80
		case "agent":
			return 50
		case "tool":
			return 30
		}
		return 0
	}
	nodes := append([]kg.OntoNode{}, g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return score(nodes[i]) > score(nodes[j]) })
	if len(nodes) > cap {
		nodes = nodes[:cap]
	}
	keep := map[string]bool{}
	for _, n := range nodes {
		keep[n.ID] = true
	}
	var edges []kg.OntoEdge
	for _, e := range g.Edges {
		if keep[e.Source] && keep[e.Target] {
			edges = append(edges, e)
		}
	}
	return map[string]any{"nodes": nodes, "edges": edges, "total_raw": len(g.Nodes)}
}

// stepRow is one chronological step in a session story.
type stepRow struct {
	At      string `json:"at"`
	Tool    string `json:"tool"`
	Verdict string `json:"verdict"`
	Summary string `json:"summary"`
}

// phasesOf maps a session's steps to recon/collect/exfil/blocked stages.
func phasesOf(steps []stepRow) []map[string]string {
	var out []map[string]string
	for _, st := range steps {
		phase := "recon"
		switch {
		case st.Verdict == "BLOCK":
			phase = "blocked"
		case st.Tool == "Bash" || st.Tool == "WebFetch":
			phase = "exfil"
		case st.Tool == "Write" || st.Tool == "Edit":
			phase = "collect"
		case st.Tool == "Read" || st.Tool == "Grep" || st.Tool == "Glob":
			phase = "recon"
		}
		out = append(out, map[string]string{"phase": phase, "tool": st.Tool, "verdict": st.Verdict})
	}
	return out
}

func lastOf(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' || id[i] == '/' {
			return id[i+1:]
		}
	}
	return id
}

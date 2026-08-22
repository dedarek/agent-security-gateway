// Package webui serves the operator console: trajectory explorer, root-cause
// suggestions with one-click policy apply, and the live approval queue.
// Single embedded HTML page (no npm, no external assets) + JSON API.
package webui

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/approval"
	"github.com/dedarek/agent-security-gateway/internal/intel"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

//go:embed index.html
var page []byte

type Server struct {
	Store     *store.Store
	Approvals *approval.Manager
	Hub       *policyhub.Hub
	suggs     map[string]*intel.Suggestion
}

func New(st *store.Store, am *approval.Manager, hub *policyhub.Hub) *Server {
	return &Server{Store: st, Approvals: am, Hub: hub, suggs: map[string]*intel.Suggestion{}}
}

func (s *Server) Register(mux *http.ServeMux) {
	s.RegisterIngest(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/api/events", s.apiEvents)
	mux.HandleFunc("/api/sessions", s.apiSessions)
	mux.HandleFunc("/api/trajectory", s.apiTrajectory)
	mux.HandleFunc("/api/approvals", s.apiApprovals)
	mux.HandleFunc("/api/suggestions", s.apiSuggestions)
	mux.HandleFunc("/api/suggestion/decide", s.apiSuggestionDecide)
	mux.HandleFunc("/api/clusters", s.apiClusters)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) apiEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.Store.Recent(100))
}

func (s *Server) apiSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.Store.Sessions())
}

func (s *Server) apiTrajectory(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if id == "" {
		http.Error(w, "missing session", 400)
		return
	}
	events := s.Store.Trajectory(id)
	// Analyze on demand; cache by suggestion id.
	var sug *intel.Suggestion
	sum := store.SessionSummary{SessionID: id}
	for _, e := range events {
		if e.Decision.Final == api.VerdictBlock {
			sum.LastVerdict = "BLOCK"
		}
	}
	sum.Events = len(events)
	if cached, ok := s.suggs[sugKey(id)]; ok {
		sug = cached
	} else if sug = intel.Analyze(sum, events); sug != nil {
		s.suggs[sugKey(id)] = sug
		s.suggs[sug.ID] = sug // also indexed by suggestion id for /decide
	}
	writeJSON(w, map[string]any{"events": events, "suggestion": sug})
}

func (s *Server) apiApprovals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.Approvals.Pending())
	case http.MethodPost:
		var req struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ok := s.Approvals.Decide(req.ID, req.Approve)
		writeJSON(w, map[string]bool{"ok": ok})
	}
}

func (s *Server) apiSuggestions(w http.ResponseWriter, _ *http.Request) {
	out := []*intel.Suggestion{}
	for _, sg := range s.suggs {
		if strings.HasPrefix(sg.ID, "sug-") { // skip session-cache alias keys
			out = append(out, sg)
		}
	}
	intel.SortSuggestions(out)
	writeJSON(w, out)
}

func (s *Server) apiClusters(w http.ResponseWriter, _ *http.Request) {
	open := []*intel.Suggestion{}
	for _, sg := range s.suggs {
		if strings.HasPrefix(sg.ID, "sug-") && sg.Status == "open" {
			open = append(open, sg)
		}
	}
	writeJSON(w, intel.BuildClusters(open))
}

func (s *Server) apiSuggestionDecide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Accept bool   `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	sg, ok := s.suggs[req.ID]
	if !ok {
		http.Error(w, "unknown suggestion", 404)
		return
	}
	if req.Accept {
		if err := s.Hub.Accept(sg.ID, sg.CedarPolicy); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		sg.Status = "accepted"
	} else {
		s.Hub.Dismiss(sg.ID)
		sg.Status = "dismissed"
	}
	writeJSON(w, sg)
}

// sugKey namespaces the per-session cache index.
func sugKey(session string) string { return "sess:" + session }

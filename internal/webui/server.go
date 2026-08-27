// Package webui serves the operator console: trajectory explorer, root-cause
// suggestions with one-click policy apply, and the live approval queue.
// Single embedded HTML page (no npm, no external assets) + JSON API.
package webui

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/approval"
	"github.com/dedarek/agent-security-gateway/internal/intel"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

//go:embed index.html
var page []byte

type Server struct {
	Store      *store.Store
	Approvals  *approval.Manager
	Hub        *policyhub.Hub
	Auth       *uiAuth
	Agents     *agentregistry.Registry
	mu         sync.RWMutex
	suggs      map[string]*intel.Suggestion
	ingestAuth func(header string) bool // nil = open (dev)
	// Agent onboarding/telemetry can run without a tenant key. This is
	// deliberately separate from operator-console and central-MCP auth.
	agentIngressOpen  bool
	publicLLMUpstream string
}

func New(st *store.Store, am *approval.Manager, hub *policyhub.Hub) *Server {
	return &Server{Store: st, Approvals: am, Hub: hub, Auth: newUIAuth(), suggs: map[string]*intel.Suggestion{}}
}

// SetIngestAuth enforces tenant-key auth on POST /api/ingest.
func (s *Server) SetAgentRegistry(r *agentregistry.Registry) { s.Agents = r }

func (s *Server) SetIngestAuth(f func(header string) bool) { s.ingestAuth = f }

// SetAgentIngressOpen enables no-key registration, heartbeat, and telemetry.
// Public telemetry identity is treated as untrusted by normalizeIngressEvent.
func (s *Server) SetAgentIngressOpen(open bool) { s.agentIngressOpen = open }

func (s *Server) effectiveIngestAuth(auth func(string) bool) func(string) bool {
	if s.agentIngressOpen {
		return nil
	}
	return auth
}

func (s *Server) Register(mux *http.ServeMux) {
	// ingest auth: open in dev (nil), tenant-key enforced when SetIngestAuth is called
	s.RegisterIngestWithAuth(mux, s.effectiveIngestAuth(s.ingestAuth))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// If not authenticated, serve login page instead of console
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		_, _ = w.Write(page)
	}))
	// Login page endpoint (GET returns HTML, POST processes login)
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/api/events", s.Auth.middleware(s.apiEvents))
	mux.HandleFunc("/api/sessions", s.Auth.middleware(s.apiSessions))
	mux.HandleFunc("/api/trajectory", s.Auth.middleware(s.apiTrajectory))
	mux.HandleFunc("/api/approvals", s.Auth.middleware(s.apiApprovals))
	mux.HandleFunc("/api/suggestions", s.Auth.middleware(s.apiSuggestions))
	mux.HandleFunc("/api/suggestion/decide", s.Auth.middleware(s.apiSuggestionDecide))
	mux.HandleFunc("/api/clusters", s.Auth.middleware(s.apiClusters))
	mux.HandleFunc("/api/siem", s.Auth.middleware(s.apiSIEM))
	mux.HandleFunc("/api/query", s.Auth.middleware(s.apiQuery))
	mux.HandleFunc("/api/agents", s.Auth.middleware(s.apiAgents))
	mux.HandleFunc("/api/agents/detail", s.Auth.middleware(s.apiAgentDetail))
	mux.HandleFunc("/api/agents/action", s.Auth.middleware(s.apiAgentAction))
	mux.HandleFunc("/api/agents/register", s.apiAgentRegister)
	mux.HandleFunc("/api/agents/heartbeat", s.apiAgentHeartbeat)
	mux.HandleFunc("/api/ui-login", s.uiLogin)
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
	var sug *intel.Suggestion
	sum := store.SessionSummary{SessionID: id}
	for _, e := range events {
		if e.Decision.Final == api.VerdictBlock {
			sum.LastVerdict = "BLOCK"
		}
	}
	sum.Events = len(events)
	s.mu.RLock()
	cached, ok := s.suggs[sugKey(id)]
	s.mu.RUnlock()
	if ok {
		sug = cached
	} else if sug = intel.Analyze(sum, events); sug != nil {
		s.mu.Lock()
		// double-check under write lock
		if existing, ok := s.suggs[sugKey(id)]; ok {
			sug = existing
		} else {
			s.suggs[sugKey(id)] = sug
			s.suggs[sug.ID] = sug
		}
		s.mu.Unlock()
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
	s.mu.RLock()
	out := make([]*intel.Suggestion, 0, len(s.suggs))
	for _, sg := range s.suggs {
		if strings.HasPrefix(sg.ID, "sug-") {
			out = append(out, sg)
		}
	}
	s.mu.RUnlock()
	intel.SortSuggestions(out)
	writeJSON(w, out)
}

func (s *Server) apiClusters(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	open := make([]*intel.Suggestion, 0)
	for _, sg := range s.suggs {
		if strings.HasPrefix(sg.ID, "sug-") && sg.Status == "open" {
			open = append(open, sg)
		}
	}
	s.mu.RUnlock()
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
	s.mu.Lock()
	sg, ok := s.suggs[req.ID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "unknown suggestion", 404)
		return
	}
	if req.Accept {
		if err := s.Hub.Accept(sg.ID, sg.CedarPolicy); err != nil {
			s.mu.Unlock()
			http.Error(w, err.Error(), 500)
			return
		}
		sg.Status = "accepted"
	} else {
		s.Hub.Dismiss(sg.ID)
		sg.Status = "dismissed"
	}
	s.mu.Unlock()
	writeJSON(w, sg)
}

// sugKey namespaces the per-session cache index.
func sugKey(session string) string { return "sess:" + session }

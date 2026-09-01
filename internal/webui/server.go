// Package webui serves the operator console: trajectory explorer, root-cause
// suggestions with one-click policy apply, and the live approval queue.
// Single embedded HTML page (no npm, no external assets) + JSON API.
package webui

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/activity"
	"github.com/dedarek/agent-security-gateway/internal/session"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/approval"
	"github.com/dedarek/agent-security-gateway/internal/engine"
	"github.com/dedarek/agent-security-gateway/internal/intel"
	"github.com/dedarek/agent-security-gateway/internal/policyhub"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

//go:embed index.html
var page []byte

//go:embed all:dist
var webDist embed.FS

type Server struct {
	Store       *store.Store
	Approvals   *approval.Manager
	Hub         *policyhub.Hub
	Auth        *uiAuth
	Agents      *agentregistry.Registry
	Activity    *activity.Store
	InventoryDB *sql.DB
	Engine      *engine.Registry
	mu          sync.RWMutex
	suggs       map[string]*intel.Suggestion
	ingestAuth  func(header string) bool // nil = open (dev)
	// Agent onboarding/telemetry can run without a tenant key. This is
	// deliberately separate from operator-console and central-MCP auth.
	agentIngressOpen  bool
	publicLLMUpstream string
	hub               *sseHub
	streamClose       chan struct{}
	behaviorURL       string
	outputGuardURL    string
	// kgLive feeds new events into the KG builder+bridge so the lineage
	// graph grows in real time (previously only the MCP proxy path ingested;
	// hook-path events never reached the graph until a 30s self-heal replay).
	kgLive func(ev api.Event)
	// DataAccess records data-flow hops for ingested tool calls (hook path).
	DataAccess DataAccessObserver
	// Taints returns accumulated session taints (V0 DLP). Wired to the
	// taint engine by the gateway.
	Taints func(sessionID string) []session.TaintMark
}

// DataAccessObserver is implemented by engine.DataAccessRecorder.
type DataAccessObserver interface {
	ObserveHook(sessionID, traceID, toolID string, payload []byte, verdict string)
}

// SetKGLive wires the live KG ingest callback (builder.Ingest + bridge push).
func (s *Server) SetKGLive(fn func(ev api.Event)) { s.kgLive = fn }

// SetSidecarURLs records the behavior/outputguard sidecar base URLs so the
// status API can probe them server-side.
func (s *Server) SetSidecarURLs(behavior, outputGuard string) {
	s.behaviorURL = behavior
	s.outputGuardURL = outputGuard
}

func New(st *store.Store, am *approval.Manager, hub *policyhub.Hub) *Server {
	return &Server{Store: st, Approvals: am, Hub: hub, Auth: newUIAuth(), suggs: map[string]*intel.Suggestion{}}
}

// SetIngestAuth enforces tenant-key auth on POST /api/ingest.
func (s *Server) SetAgentRegistry(r *agentregistry.Registry) { s.Agents = r }

// SetInventoryDB wires the shared SQLite inventory catalog. A nil database
// leaves inventory endpoints unavailable without affecting agent telemetry.
func (s *Server) SetInventoryDB(database *sql.DB) { s.InventoryDB = database }

func (s *Server) SetActivityStore(a *activity.Store) { s.Activity = a }

func (s *Server) SetEngine(e *engine.Registry) { s.Engine = e }

// SetDataAccess wires the data-lineage recorder (hook/ingest path).
func (s *Server) SetDataAccess(r DataAccessObserver) { s.DataAccess = r }

// SetTaints wires the session-taint query (V0 DLP) to the taint engine.
func (s *Server) SetTaints(f func(sessionID string) []session.TaintMark) { s.Taints = f }

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
	// Public one-line installer — harness-agnostic, no key required.
	// Must be registered before the SPA catch-all "/" handler.
	mux.HandleFunc("/install.sh", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join("scripts", "asg-universal-install.sh"))
		if err != nil {
			// Fallback: try relative to executable dir when running as service
			if ex, e2 := os.Executable(); e2 == nil {
				b, err = os.ReadFile(filepath.Join(filepath.Dir(ex), "..", "scripts", "asg-universal-install.sh"))
			}
		}
		if err != nil {
			http.Error(w, "install script not found", 500)
			return
		}
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/asg-connect", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect")
	})
	mux.HandleFunc("/asg-connect.gz", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect.gz")
	})
	mux.HandleFunc("/asg-connect-darwin-arm64", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect-darwin-arm64")
	})
	mux.HandleFunc("/asg-connect-darwin-arm64.gz", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect-darwin-arm64.gz")
	})
	mux.HandleFunc("/asg-connect-darwin-amd64", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect-darwin-amd64")
	})
	mux.HandleFunc("/asg-connect-darwin-amd64.gz", func(w http.ResponseWriter, r *http.Request) {
		serveBin(w, r, "asg-connect-darwin-amd64.gz")
	})
	// ingest auth: open in dev (nil), tenant-key enforced when SetIngestAuth is called
	s.RegisterIngestWithAuth(mux, s.effectiveIngestAuth(s.ingestAuth))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// New React console (web/dist) — serve static files, fallback to index.html for SPA routing
	sub, err := fs.Sub(webDist, "dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(sub))
		mux.HandleFunc("/", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
			// API and other registered handlers take precedence (they are matched first via exact paths)
			// For SPA, serve index.html for any non-file path
			if r.URL.Path != "/" && !strings.Contains(r.URL.Path, ".") {
				// Try to serve as static file first, else SPA fallback
				if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
					fileServer.ServeHTTP(w, r)
					return
				}
				// SPA fallback
				data, _ := fs.ReadFile(sub, "index.html")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
				_, _ = w.Write(data)
				return
			}
			// Try static file
			if r.URL.Path == "/" {
				data, _ := fs.ReadFile(sub, "index.html")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
				_, _ = w.Write(data)
				return
			}
			fileServer.ServeHTTP(w, r)
		}))
	} else {
		mux.HandleFunc("/", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			_, _ = w.Write(page)
		}))
	}
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
	mux.HandleFunc("/api/agents/history", s.Auth.middleware(s.apiAgentHistory))
	mux.HandleFunc("/api/agents/delete", s.Auth.middleware(s.apiAgentDelete))
	mux.HandleFunc("/api/agents/action", s.Auth.middleware(s.apiAgentAction))
	mux.HandleFunc("/api/agents/register", s.apiAgentRegister)
	mux.HandleFunc("/api/agents/heartbeat", s.apiAgentHeartbeat)
	// Probe observations are keyless in single-tenant onboarding; they are
	// still untrusted and can only create pending inventory records.
	mux.HandleFunc("/api/inventory/ingest", s.apiInventoryIngest)
	mux.HandleFunc("/api/inventory", s.Auth.middleware(s.apiInventory))
	mux.HandleFunc("/api/data-access", s.Auth.middleware(s.apiDataAccess))
	mux.HandleFunc("/api/hub-check", s.apiHubCheck)
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

func serveBin(w http.ResponseWriter, r *http.Request, name string) {
	// Support gzipped variant for slow public tunnels (cpolar).
	if strings.HasSuffix(name, ".gz") {
		b, err := os.ReadFile(filepath.Join("bin", name))
		if err != nil {
			if ex, e2 := os.Executable(); e2 == nil {
				b, err = os.ReadFile(filepath.Join(filepath.Dir(ex), "..", "bin", name))
			}
		}
		if err != nil {
			http.Error(w, "binary not found: "+name, 404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
		return
	}
	b, err := os.ReadFile(filepath.Join("bin", name))
	if err != nil {
		if ex, e2 := os.Executable(); e2 == nil {
			b, err = os.ReadFile(filepath.Join(filepath.Dir(ex), "..", "bin", name))
		}
	}
	if err != nil {
		// Try repo root bin
		b, err = os.ReadFile(filepath.Join("..", "bin", name))
	}
	if err != nil {
		http.Error(w, "binary not found: "+name, 404)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

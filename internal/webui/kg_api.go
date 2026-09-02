package webui

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
)

// kgCache TTL-caches the expensive graph reads so lineage tracing is fast
// (worker is a Python process; repeated pulls on every click were slow).
type kgCache struct {
	mu    sync.Mutex
	nodes []byte
	edges []byte
	at    time.Time
	ttl   time.Duration
}

var gKG = &kgCache{ttl: 5 * time.Second}

func (c *kgCache) get(bridge *kgbridge.Bridge) (nodes, edges []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodes != nil && time.Since(c.at) < c.ttl {
		return c.nodes, c.edges, nil
	}
	n, err := bridge.GraphNodes()
	if err != nil {
		return nil, nil, err
	}
	e, err := bridge.GraphEdges()
	if err != nil {
		return nil, nil, err
	}
	c.nodes, c.edges, c.at = n, e, time.Now()
	return c.nodes, c.edges, nil
}

// RegisterKGAPI adds the knowledge-graph endpoints backed by the Semantica
// worker: /api/kg/search, /ask, /graph/nodes, /graph/edges, /graph/path.
func (s *Server) RegisterKGAPI(mux *http.ServeMux, bridge *kgbridge.Bridge) {
	mux.HandleFunc("/api/kg/search", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q == "" {
			http.Error(w, "missing query", 400)
			return
		}
		hits, err := bridge.Search(q, 5)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, hits)
	}))
	mux.HandleFunc("/api/kg/ask", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Question string `json:"question"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ans, err := bridge.Ask(req.Question)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, map[string]string{"answer": ans})
	}))
	// Semantica graph lineage: nodes / edges / path (链路追溯) — cached 5s
	mux.HandleFunc("/api/kg/graph/nodes", s.Auth.middleware(func(w http.ResponseWriter, _ *http.Request) {
		n, _, err := gKG.get(bridge)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(n)
	}))
	mux.HandleFunc("/api/kg/graph/edges", s.Auth.middleware(func(w http.ResponseWriter, _ *http.Request) {
		_, e, err := gKG.get(bridge)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(e)
	}))
	mux.HandleFunc("/api/kg/graph/path", s.Auth.middleware(func(w http.ResponseWriter, r *http.Request) {
		src := r.URL.Query().Get("source")
		tgt := r.URL.Query().Get("target")
		if src == "" || tgt == "" {
			http.Error(w, "missing source/target", 400)
			return
		}
		data, err := bridge.GraphPath(src, tgt)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
}

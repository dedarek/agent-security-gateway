package webui

import (
	"encoding/json"
	"net/http"

	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
)

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
	// Semantica graph lineage: nodes / edges / path (链路追溯)
	mux.HandleFunc("/api/kg/graph/nodes", s.Auth.middleware(func(w http.ResponseWriter, _ *http.Request) {
		data, err := bridge.GraphNodes()
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	mux.HandleFunc("/api/kg/graph/edges", s.Auth.middleware(func(w http.ResponseWriter, _ *http.Request) {
		data, err := bridge.GraphEdges()
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
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

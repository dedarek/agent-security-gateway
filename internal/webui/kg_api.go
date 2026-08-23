package webui

import (
	"encoding/json"
	"net/http"

	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
)

// RegisterKGAPI adds the knowledge-graph endpoints backed by the Semantica
// worker: /api/kg/stats, /search (semantic), /ask (KG-grounded Q&A).
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
}

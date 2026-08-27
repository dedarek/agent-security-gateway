package webui

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/dedarek/agent-security-gateway/internal/store"
)

// apiQuery handles filtered event queries: /api/query?session=&tool=&verdict=&limit=&offset=
func (s *Server) apiQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	events, total := s.Store.Query(store.QueryFilter{
		SessionID: q.Get("session"),
		ToolID:    q.Get("tool"),
		Verdict:   q.Get("verdict"),
		Offset:    offset,
		Limit:     limit,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"total":  total,
		"events": events,
	})
}

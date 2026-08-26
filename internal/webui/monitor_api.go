package webui

import (
	"net/http"
	"strconv"

	"github.com/dedarek/agent-security-gateway/internal/monitor"
)

var monInst monitorProvider

type monitorProvider interface {
	Recent(n int) []monitor.Finding
}

func (s *Server) SetMonitor(m monitorProvider) { monInst = m }

func (s *Server) RegisterMonitorAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/monitor/findings", s.Auth.middleware(s.apiMonitorFindings))
}

func (s *Server) apiMonitorFindings(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if monInst == nil {
		writeJSON(w, []monitor.Finding{})
		return
	}
	writeJSON(w, monInst.Recent(limit))
}

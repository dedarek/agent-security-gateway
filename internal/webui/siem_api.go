package webui

import (
	"net/http"

	"github.com/dedarek/agent-security-gateway/internal/siem"
)

// apiSIEM exports events in SIEM-friendly formats: /api/siem?format=cef|splunk
func (s *Server) apiSIEM(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "splunk"
	}
	events := s.Store.Recent(1000)
	lines := siem.Export(events, format)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, l := range lines {
		w.Write([]byte(l + "\n"))
	}
}

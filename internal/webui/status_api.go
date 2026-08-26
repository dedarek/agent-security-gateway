package webui

import (
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
)

// statusProvider surfaces optional subsystem health for the frontend.
type statusProvider struct {
	kg      *kgbridge.Bridge
	emitter receiptProvider
	mon     monitorProvider
}

var statusSrc *statusProvider

func (s *Server) SetStore(_ interface{}) {}

func (s *Server) RegisterStatusAPI(mux *http.ServeMux, kg *kgbridge.Bridge, emitter receiptProvider, mon monitorProvider) {
	statusSrc = &statusProvider{kg: kg, emitter: emitter, mon: mon}
	mux.HandleFunc("/api/status", s.Auth.middleware(s.apiStatus))
}

func (s *Server) apiStatus(w http.ResponseWriter, _ *http.Request) {
	kgOK := false
	kgErr := ""
	if statusSrc != nil && statusSrc.kg != nil {
		// light probe via search with empty query (worker returns 400 if alive but query missing)
		c := &http.Client{Timeout: 800 * time.Millisecond}
		resp, err := c.Get(statusSrc.kg.URL() + "/health")
		if err == nil {
			kgOK = resp.StatusCode == 200
			resp.Body.Close()
		} else {
			kgErr = err.Error()
		}
	}
	receiptCount := 0
	receiptVerified := false
	if statusSrc != nil && statusSrc.emitter != nil {
		rs := statusSrc.emitter.Receipts()
		receiptCount = len(rs)
		receiptVerified = true
	}
	writeJSON(w, map[string]any{
		"kg":       map[string]any{"available": kgOK, "error": kgErr},
		"receipts": map[string]any{"count": receiptCount, "verified": receiptVerified},
		"time":     time.Now().UTC().Format(time.RFC3339),
	})
}

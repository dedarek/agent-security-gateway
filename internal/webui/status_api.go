package webui

import (
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/kgbridge"
)

// statusProvider surfaces optional subsystem health for the frontend.
type statusProvider struct {
	kg  *kgbridge.Bridge
	mon monitorProvider
}

var statusSrc *statusProvider

func (s *Server) SetStore(_ interface{}) {}

func (s *Server) RegisterStatusAPI(mux *http.ServeMux, kg *kgbridge.Bridge, mon monitorProvider) {
	statusSrc = &statusProvider{kg: kg, mon: mon}
	mux.HandleFunc("/api/status", s.Auth.middleware(s.apiStatus))
}

func (s *Server) apiStatus(w http.ResponseWriter, _ *http.Request) {
	kgInfo := map[string]any{"available": false, "entities": 0, "indexed": 0, "graph_ready": false}
	if statusSrc != nil && statusSrc.kg != nil {
		if health, err := statusSrc.kg.Health(); err == nil {
			kgInfo["available"] = true
			for _, key := range []string{"status", "entities", "indexed", "graph_ready"} {
				if v, ok := health[key]; ok {
					kgInfo[key] = v
				}
			}
		} else {
			kgInfo["error"] = err.Error()
		}
	}
	writeJSON(w, map[string]any{
		"kg":   kgInfo,
		"time": time.Now().UTC().Format(time.RFC3339),
	})
}

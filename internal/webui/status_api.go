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

// probeGet fetches url with a short timeout and reports liveness.
func probeGet(url string) bool {
	return probeGetTimeout(url, 1500*time.Millisecond)
}

func probeGetTimeout(url string, d time.Duration) bool {
	cli := &http.Client{Timeout: d}
	r, err := cli.Get(url)
	if err != nil {
		return false
	}
	defer r.Body.Close()
	return r.StatusCode >= 200 && r.StatusCode < 500
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	// Aggregate sidecar liveness server-side so the console doesn't have to
	// cross-origin fetch :8901/:8902/:8903 or the public tunnel from the
	// browser (which CORS/mixed-content would mark as down even when alive).
	services := map[string]bool{"gateway": true}
	kgURL := ""
	if statusSrc != nil && statusSrc.kg != nil {
		kgURL = statusSrc.kg.URL()
	}
	if kgURL != "" {
		services["kg-worker"] = probeGet(kgURL + "/health")
	}
	if u := s.behaviorURL; u != "" {
		services["behavior"] = probeGet(u + "/health")
	}
	if u := s.outputGuardURL; u != "" {
		services["outputguard"] = probeGet(u + "/health")
	}
	services["cpolar"] = probeGetTimeout("https://asg-gateway.vip.cpolar.cn/healthz", 4*time.Second)

	kgInfo := map[string]any{"available": false, "entities": 0, "indexed": 0, "graph_ready": false,
		"kg_node_count": 0, "kg_edge_count": 0, "kg_last_ingest": nil}
	if statusSrc != nil && statusSrc.kg != nil {
		if health, err := statusSrc.kg.Health(); err == nil {
			kgInfo["available"] = true
			for _, key := range []string{"status", "entities", "indexed", "graph_ready",
				"node_count", "edge_count", "ingested_at", "worker_version"} {
				if v, ok := health[key]; ok {
					kgInfo[key] = v
				}
			}
			// Explicit, unambiguous names for the console: whether the graph
			// really holds data, not merely whether the process is alive.
			if v, ok := health["node_count"]; ok {
				kgInfo["kg_node_count"] = v
			}
			if v, ok := health["edge_count"]; ok {
				kgInfo["kg_edge_count"] = v
			}
			kgInfo["kg_last_ingest"] = health["ingested_at"]
		} else {
			kgInfo["error"] = err.Error()
		}
	}
	writeJSON(w, map[string]any{
		"kg":       kgInfo,
		"services": services,
		"time":     time.Now().UTC().Format(time.RFC3339),
	})
}

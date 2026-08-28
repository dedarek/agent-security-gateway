package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// streamData is one SSE payload.
type streamData struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// RegisterStreamAPI adds SSE endpoint. For now it sends periodic agent list
// and is a placeholder for full push (M4 will enhance with fan-out).
func (s *Server) RegisterStreamAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/stream", s.Auth.middleware(s.apiStream))
}

func (s *Server) apiStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial snapshot
	if s.Agents != nil {
		agents := s.Agents.List()
		sendSSE(w, "agents", agents)
		flusher.Flush()
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			if s.Agents != nil {
				agents := s.Agents.List()
				if err := sendSSE(w, "agents", agents); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func sendSSE(w http.ResponseWriter, event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
	return err
}

package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/activity"
)

// safeStep is the SSE-facing activity step projection (Raw hook payload excluded).
type safeStep struct {
	At        time.Time `json:"at"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	ToolName  string    `json:"tool_name"`
	Summary   string    `json:"summary"`
	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reason,omitempty"`
}

// sseHub fans out events to all connected /api/stream clients.
// A single-goroutine actor owns the client set; senders never block (slow
// clients drop events rather than stall the pipeline).
type sseHub struct {
	mu   chan func(*hubState)
	done chan struct{}
}

type hubState struct {
	clients map[chan sseEvent]struct{}
}

type sseEvent struct {
	event string
	data  any
}

func newSSEHub() *sseHub {
	h := &sseHub{mu: make(chan func(*hubState)), done: make(chan struct{})}
	st := &hubState{clients: make(map[chan sseEvent]struct{})}
	go func() {
		for {
			select {
			case f := <-h.mu:
				f(st)
			case <-h.done:
				return
			}
		}
	}()
	return h
}

func (h *sseHub) add() chan sseEvent {
	ch := make(chan sseEvent, 64)
	h.mu <- func(st *hubState) { st.clients[ch] = struct{}{} }
	return ch
}

func (h *sseHub) remove(ch chan sseEvent) {
	done := make(chan struct{})
	h.mu <- func(st *hubState) {
		delete(st.clients, ch)
		close(ch)
		close(done)
	}
	<-done
}

// Broadcast sends an event to all clients (non-blocking; slow clients drop).
func (h *sseHub) Broadcast(event string, data any) {
	h.mu <- func(st *hubState) {
		for ch := range st.clients {
			select {
			case ch <- sseEvent{event: event, data: data}:
			default:
			}
		}
	}
}

// NotifyActivity fans one activity step out to all SSE subscribers in real
// time. Called by the /api/activity handler after the step is persisted.
func (s *Server) NotifyActivity(st activity.Step) {
	if s.hub == nil {
		return
	}
	s.hub.Broadcast("activity", safeStep{
		At:        st.At,
		AgentID:   st.AgentID,
		SessionID: st.SessionID,
		Kind:      st.Kind,
		ToolName:  st.ToolName,
		Summary:   st.Summary,
		Verdict:   st.Verdict,
		Reason:    st.Reason,
	})
}

// RegisterStreamAPI adds the SSE endpoint. Clients receive:
//   - event "agents":   full agent list (on connect + every 5s)
//   - event "activity": one safeStep per hook activity (real-time fan-out)
func (s *Server) RegisterStreamAPI(mux *http.ServeMux) {
	if s.hub == nil {
		s.hub = newSSEHub()
	}
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

	ch := s.hub.add()
	defer s.hub.remove(ch)

	// Initial snapshot so the client doesn't wait for the first tick.
	if s.Agents != nil {
		_ = sendSSE(w, "agents", s.Agents.List())
		flusher.Flush()
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	notify := r.Context().Done()
	// Watchdog: if the client never reads and the request context never
	// fires (custom ResponseWriters in tests), exit after 60s instead of
	// pinning the handler goroutine forever. Real browsers always fire
	// notify on disconnect.
	watchdog := time.AfterFunc(60*time.Second, func() {})
	defer watchdog.Stop()
	for {
		select {
		case <-notify:
			return
		case ev := <-ch:
			if err := sendSSE(w, ev.event, ev.data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if s.Agents != nil {
				if err := sendSSE(w, "agents", s.Agents.List()); err != nil {
					return
				}
				flusher.Flush()
			}
		case <-s.streamDone():
			return
		}
	}
}

// streamDone returns a channel closed when the server wants all streams to
// end (currently: never in production; tests may close via CloseStreams).
func (s *Server) streamDone() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.streamClose == nil {
		s.mu.RUnlock()
		s.mu.Lock()
		if s.streamClose == nil {
			s.streamClose = make(chan struct{})
		}
		s.mu.Unlock()
		s.mu.RLock()
	}
	return s.streamClose
}

// CloseStreams terminates all open /api/stream handlers. Used by tests and
// by gateway shutdown.
func (s *Server) CloseStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamClose != nil {
		select {
		case <-s.streamClose:
		default:
			close(s.streamClose)
		}
		s.streamClose = make(chan struct{})
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

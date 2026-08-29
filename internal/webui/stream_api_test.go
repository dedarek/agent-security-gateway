package webui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/activity"
	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

// TestStreamActivityFanOut: a step recorded via /api/activity must reach an
// open /api/stream client as an `activity` SSE event in real time (not only
// on the 5s agents tick).
func TestStreamActivityFanOut(t *testing.T) {
	st, _ := store.Open("")
	reg, _ := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	_ = reg.Upsert(agentregistry.Record{AgentID: "sse-test", AgentType: "claude-code"})
	act := activity.New()
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	s.SetActivityStore(act)

	mux := http.NewServeMux()
	s.RegisterStreamAPI(mux)
	s.RegisterOTLP(mux)

	rec := newStreamRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
	req.RemoteAddr = "127.0.0.1:55001" // loopback → auth middleware lets through
	done := make(chan struct{})
	go func() { mux.ServeHTTP(rec, req); close(done) }()

	if !rec.waitFor("event: agents", 3*time.Second) {
		t.Fatal("no initial agents event")
	}

	body := `{"agent_id":"sse-test","hook_payload":{"tool_name":"Bash","tool_input":{"command":"ls -la"},"session_id":"s-sse"}}`
	areq := httptest.NewRequest(http.MethodPost, "/api/activity", bytes.NewReader([]byte(body)))
	areq.Header.Set("Content-Type", "application/json")
	arec := httptest.NewRecorder()
	mux.ServeHTTP(arec, areq)
	if arec.Code != 200 {
		t.Fatalf("activity: %d %s", arec.Code, arec.Body.String())
	}

	if !rec.waitFor(`"tool_name":"Bash"`, 3*time.Second) {
		t.Fatalf("activity event missing tool_name; got: %s", rec.String())
	}
	s.CloseStreams()
	<-done
}

// streamRecorder is an http.ResponseWriter + http.Flusher with an
// inspectable, goroutine-safe buffer.
type streamRecorder struct {
	mu     sync.Mutex
	header http.Header
	buf    strings.Builder
	code   int
	closer chan struct{}
	once   sync.Once
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: http.Header{}, closer: make(chan struct{})}
}

func (r *streamRecorder) Header() http.Header { return r.header }
func (r *streamRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Write(b)
	return len(b), nil
}
func (r *streamRecorder) WriteHeader(c int) { r.code = c }
func (r *streamRecorder) Flush()            {}

func (r *streamRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *streamRecorder) waitFor(substr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(r.String(), substr) {
			return true
		}
		time.Sleep(15 * time.Millisecond)
	}
	return false
}

func (r *streamRecorder) close() { r.once.Do(func() { close(r.closer) }) }

var _ http.Flusher = (*streamRecorder)(nil)

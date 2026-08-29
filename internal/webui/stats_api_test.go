package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

func TestStatsSummary(t *testing.T) {
	st, _ := store.Open("")
	// two BLOCK by agent-a via behavior.taint, one CONFIRM by agent-b
	for _, c := range []struct {
		agent, tool string
		final       api.Verdict
		engine      string
	}{
		{"agent-a", "Bash", api.VerdictBlock, "behavior.taint"},
		{"agent-a", "Bash", api.VerdictBlock, "behavior.taint"},
		{"agent-b", "Read", api.VerdictConfirm, "sensitive"},
	} {
		_ = st.Write(api.Event{
			SessionID: "s",
			Call: api.ToolCall{
				CallID:    "c-" + c.agent + c.tool,
				ToolID:    c.tool,
				Principal: api.Principal{AgentID: c.agent},
			},
			Decision: api.Decision{
				Final: c.final, Risk: 90,
				Signals: []api.Signal{{Engine: c.engine, Verdict: c.final, Score: 90}},
			},
		})
	}

	s := New(st, nil, nil)
	mux := http.NewServeMux()
	s.RegisterStatsAPI(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil)
	req.RemoteAddr = "127.0.0.1:9"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"block":2`, `"confirm":1`, `behavior.taint`, `agent-a`, `"name":"Bash"`} {
		if !contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

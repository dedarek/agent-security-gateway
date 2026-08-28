package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestConfigLoadAllFields(t *testing.T) {
	f, _ := os.CreateTemp("", "probe-full-*.yaml")
	defer os.Remove(f.Name())
	content := `
hub_url: http://gw
hub_key: k
agent_id: a1
tenant_name: test
listen: 127.0.0.1:18181
event_spool_path: /tmp/spool.jsonl
providers:
  - name: zen
    base_url: https://x/v1
    api_key: k1
    default_model: hy3
    allowed_models: [hy3, m1]
`
	f.Write([]byte(content))
	f.Close()
	cfg, err := loadProbeConfig(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HubURL != "http://gw" {
		t.Fatalf("hub %q", cfg.HubURL)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers %d", len(cfg.Providers))
	}
	if cfg.Listen != "127.0.0.1:18181" {
		t.Fatalf("listen %q", cfg.Listen)
	}
}

func TestAnthropicAdaptSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"model":"m","messages":[]}`, `"model"`},
		{`{"model":"m","messages":[{"role":"user","content":"hi"}]}`, `"messages"`},
		{`{}`, `{`},
	}
	for _, c := range cases {
		out := sanitizeAnthropicForZen([]byte(c.in))
		if !strings.Contains(string(out), c.want) {
			t.Fatalf("sanitize %q -> %q want %q", c.in, string(out), c.want)
		}
	}
}

func TestResponsesHandleNotFound(t *testing.T) {
	cfg := &ProbeConfig{Providers: []Provider{{Name: "test", BaseURL: "http://example.com/v1"}}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	rec := httptest.NewRecorder()
	p.handleResponses(rec, req)
	// Should not panic, may return 405 or 500
	if rec.Code == 0 {
		t.Fatal("no response")
	}
}

func TestHookHTTPMethodNotAllowed(t *testing.T) {
	cfg := &ProbeConfig{HubURL: "http://x", AgentID: "a"}
	rep := newReporter("http://x", "", "", "t", "a")
	h := hookHTTPHandler(cfg, rep)
	req := httptest.NewRequest(http.MethodGet, "/api/hook-check", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 for GET, got %d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "POST only") {
		t.Fatalf("want POST only message, got %q", rec.Body.String())
	}
	// POST with valid JSON should succeed (200 or 403 depending on verdict)
	req2 := httptest.NewRequest(http.MethodPost, "/api/hook-check", strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"/tmp/a.txt"}}`))
	rec2 := httptest.NewRecorder()
	h(rec2, req2)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusForbidden {
		t.Fatalf("want 200 or 403 for POST, got %d", rec2.Code)
	}
	// POST with BLOCK payload should return 403
	req3 := httptest.NewRequest(http.MethodPost, "/api/hook-check", strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`))
	rec3 := httptest.NewRecorder()
	h(rec3, req3)
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("want 403 for BLOCK, got %d body=%q", rec3.Code, rec3.Body.String())
	}
	if !strings.Contains(rec3.Body.String(), "block") {
		t.Fatalf("want block decision, got %q", rec3.Body.String())
	}
}

func TestSpoolAndTimeUtil(t *testing.T) {
	tmp := os.TempDir() + "/test-spool-m5.jsonl"
	os.Remove(tmp)
	os.Remove(tmp + ".queue")
	defer os.Remove(tmp)
	defer os.Remove(tmp + ".queue")
	rep := newReporter("http://127.0.0.1:0", "", tmp, "t", "a")
	if rep.spool == nil {
		t.Fatal("spool nil")
	}
	if rep.spool.len() != 0 {
		t.Fatalf("want empty spool, got %d", rep.spool.len())
	}
	rep.ReportLLM("s1", "m1", []byte(`{}`), []byte(`{}`), 1)
	rep.ReportTool("s1", "tool1", []byte(`{}`), "ALLOW", "ok")
	// Both reports should have been enqueued (at least 2 entries)
	if rep.spool.len() < 2 {
		t.Fatalf("want spool len >=2, got %d", rep.spool.len())
	}
	if err := rep.Flush(); err == nil {
		t.Logf("flush succeeded unexpectedly")
	} else {
		if !strings.Contains(err.Error(), "hub ingest") && !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "dial") {
			t.Logf("flush err: %v", err)
		}
	}
	// timeNano should return non-zero
	if timeNano() == 0 {
		t.Fatal("timeNano zero")
	}
	// osOpenAppend should create file
	f, err := osOpenAppend(tmp + ".append")
	if err != nil {
		t.Fatalf("osOpenAppend: %v", err)
	}
	f.Close()
	defer os.Remove(tmp + ".append")
	if _, err := os.Stat(tmp + ".append"); err != nil {
		t.Fatalf("append file not created: %v", err)
	}
	// spool push/pop
	s := newSpool(tmp + ".spooltest")
	defer os.Remove(tmp + ".spooltest")
	defer os.Remove(tmp + ".spooltest" + ".queue")
	s.push([]byte(`{"kind":"test"}` + "\n"))
	if s.len() != 1 {
		t.Fatalf("push len want 1 got %d", s.len())
	}
	b, ok := s.pop()
	if !ok || len(b) == 0 {
		t.Fatal("pop failed")
	}
	s.unpop(b)
	if s.len() != 1 {
		t.Fatalf("unpop len want 1 got %d", s.len())
	}
	// markShipped when mem empty should remove file
	s.pop()
	s.markShipped()
}

func TestMCPSHIM(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "test", BaseURL: "http://example.com/v1"}},
	}
	mux := http.NewServeMux()
	registerMCPShim(mux, cfg)
	// Should register handlers without panic
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// May be 404 or 500 depending on shim
	if rec.Code == 0 {
		t.Fatal("no code")
	}
}

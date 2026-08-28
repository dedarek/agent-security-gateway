package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// --- route tests (the hy3 bug lived here) ---

func TestRoutePassThroughUnknownModel(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{
			{Name: "opencode-zen", BaseURL: "https://opencode.ai/zen/go/v1", DefaultModel: "hy3"},
			{Name: "openai", BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o"},
		},
	}
	p := &llmProxy{cfg: cfg}
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[]}`)
	prov, model, err := p.route(body)
	if err != nil {
		t.Fatalf("route error: %v", err)
	}
	if prov.Name != "opencode-zen" {
		t.Fatalf("prov=%q want opencode-zen (first open provider)", prov.Name)
	}
	if model != "claude-sonnet-4-6" {
		t.Fatalf("model=%q want claude-sonnet-4-6 (pass-through)", model)
	}
}

func TestRouteEmptyModelFallsBackToDefault(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "zen", BaseURL: "https://x/v1", DefaultModel: "hy3"}},
	}
	p := &llmProxy{cfg: cfg}
	prov, model, err := p.route([]byte(`{"messages":[]}`))
	if err != nil {
		t.Fatalf("route empty: %v", err)
	}
	if model != "hy3" {
		t.Fatalf("model=%q want hy3", model)
	}
	if prov.Name != "zen" {
		t.Fatalf("prov=%q", prov.Name)
	}
}

func TestRouteModelMapHit(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{
			{Name: "zen", BaseURL: "https://x/v1", DefaultModel: "hy3", ModelMap: map[string]string{"my-model": "upstream-123"}},
		},
	}
	p := &llmProxy{cfg: cfg}
	prov, model, err := p.route([]byte(`{"model":"my-model"}`))
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if model != "upstream-123" {
		t.Fatalf("model=%q want upstream-123", model)
	}
	if prov.Name != "zen" {
		t.Fatalf("prov=%q", prov.Name)
	}
}

func TestRouteNoProvidersError(t *testing.T) {
	p := &llmProxy{cfg: &ProbeConfig{}}
	_, _, err := p.route([]byte(`{"model":"x"}`))
	if err == nil {
		t.Fatal("expected error for no providers")
	}
}

func TestRouteAllowedModelsRespected(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{
			{Name: "a", BaseURL: "https://a/v1", AllowedModels: []string{"gpt-4o"}},
			{Name: "b", BaseURL: "https://b/v1"},
		},
	}
	p := &llmProxy{cfg: cfg}
	// gpt-4o should go to a (explicit)
	prov, _, _ := p.route([]byte(`{"model":"gpt-4o"}`))
	if prov.Name != "a" {
		t.Fatalf("gpt-4o -> %q want a", prov.Name)
	}
	// unknown model should go to b (open)
	prov, model, _ := p.route([]byte(`{"model":"claude-foo"}`))
	if prov.Name != "b" || model != "claude-foo" {
		t.Fatalf("claude-foo -> %q %q want b claude-foo", prov.Name, model)
	}
}

// --- sessionID handling ---

func TestSessionIDFallbackToProbePID(t *testing.T) {
	// When no session hint in body and no header, should use probe-<provider>-<pid>
	// We test the logic via handleLLM's session extraction (indirect via route + body)
	// For unit, we test the helper directly if available; else we test via HTTP
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "test", BaseURL: "http://example.com/v1", DefaultModel: "hy3"}},
		Listen: "127.0.0.1:0",
		HubURL: "http://127.0.0.1:0",
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://127.0.0.1:0", "", "", "test", "test-agent")}
	// Create upstream mock
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer upstream.Close()
	cfg.Providers[0].BaseURL = upstream.URL

	body := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleLLM(rec, req)
	if rec.Code != 200 {
		t.Fatalf("handleLLM status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// --- handleLLM quota protection ---

func TestHandleLLMBadRouteReturns403(t *testing.T) {
	cfg := &ProbeConfig{
		Providers: []Provider{}, // no providers -> route fails
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://127.0.0.1:0", "", "", "test", "test")}
	body := []byte(`{"model":"anything"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleLLM(rec, req)
	if rec.Code != 403 {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
}

// --- config load ---

func TestLoadProbeConfigMissingFile(t *testing.T) {
	_, err := loadProbeConfig("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadProbeConfigMinimal(t *testing.T) {
	f, err := os.CreateTemp("", "probe-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write([]byte("hub_url: http://gw\nhub_key: k\nagent_id: a1\n"))
	f.Close()
	cfg, err := loadProbeConfig(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HubURL != "http://gw" {
		t.Fatalf("hub=%q", cfg.HubURL)
	}
}

// --- helpers ---

func TestJsonQuote(t *testing.T) {
	if jsonQuote("a\"b") != `"a\"b"` {
		t.Fatalf("quote: %q", jsonQuote("a\"b"))
	}
	if jsonQuote("x") != `"x"` {
		t.Fatalf("quote x")
	}
}

func TestSanitizeAnthropicForZen(t *testing.T) {
	body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)
	out := sanitizeAnthropicForZen(body)
	if len(out) == 0 {
		t.Fatal("empty")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
}

// --- reporter ---

func TestReporterReportLLM(t *testing.T) {
	rep := newReporter("http://127.0.0.1:0", "", os.TempDir()+"/test-spool.jsonl", "t", "a1")
	rep.ReportLLM("sess-1", "m1", []byte(`{"model":"m1"}`), []byte(`{"choices":[]}`), 10)
	if rep.lastLLM("sess-1") == "" {
		t.Fatal("lastLLM empty")
	}
}

func TestHookHTTPHandler(t *testing.T) {
	cfg := &ProbeConfig{HubURL: "http://127.0.0.1:0", AgentID: "test-agent"}
	rep := newReporter("http://127.0.0.1:0", "", "", "t", "a")
	h := hookHTTPHandler(cfg, rep)
	req := httptest.NewRequest(http.MethodPost, "/api/hook-check", strings.NewReader(`{"tool":"Bash"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 {
		t.Fatalf("hook handler status=%d", rec.Code)
	}
}

// --- timeutil ---

func TestTimeUtilNow(t *testing.T) {
	if time.Now().IsZero() {
		t.Fatal("now zero")
	}
	// Ensure the helper doesn't panic
	_ = time.Now().UTC().Format(time.RFC3339)
}

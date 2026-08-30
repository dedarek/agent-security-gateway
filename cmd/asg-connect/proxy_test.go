package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyCapturesObservedModel(t *testing.T) {
	// Reset global observed state.
	observedModel.Store("")
	observedProvider.Store("")

	// 1) copyRequest captures model field and restores body.
	body := `{"model":"gpt-4o-test","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	captured, err := copyRequest(req)
	if err != nil {
		t.Fatalf("copyRequest error: %v", err)
	}
	if string(captured) != body {
		t.Fatalf("copyRequest body mismatch: got %q want %q", string(captured), body)
	}
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != body {
		t.Fatalf("body not restored: got %q want %q", string(restored), body)
	}
	if got := getObservedModel(); got != "gpt-4o-test" {
		t.Fatalf("observedModel=%q want gpt-4o-test", got)
	}

	// 2) End-to-end via handleLLM: provider + model observed from traffic, not config.
	observedModel.Store("")
	observedProvider.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()

	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "test-provider", BaseURL: upstream.URL, DefaultModel: "old-model"}},
		HubURL:    "http://127.0.0.1:0",
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://127.0.0.1:0", "", "", "t", "a")}
	body2 := []byte(`{"model":"claude-sonnet-4","messages":[]}`)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleLLM(rec, req2)
	if rec.Code != 200 {
		t.Fatalf("handleLLM status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := getObservedModel(); got != "claude-sonnet-4" {
		t.Fatalf("after handleLLM observedModel=%q want claude-sonnet-4", got)
	}
	if got := getObservedProvider(); got != "test-provider" {
		t.Fatalf("after handleLLM observedProvider=%q want test-provider", got)
	}

	// 3) collectAgentRegistration prefers observed over config.
	cfg2 := ProbeConfig{
		Providers:  []Provider{{Name: "old", DefaultModel: "old-model"}},
		TenantName: "tn",
		AgentID:    "aid",
		AgentType:  "test",
	}
	recReg := collectAgentRegistration(cfg2)
	if recReg.Model != "claude-sonnet-4" {
		t.Fatalf("collect prefers observed model: got %q want claude-sonnet-4", recReg.Model)
	}
	if recReg.Provider != "test-provider" {
		t.Fatalf("collect prefers observed provider: got %q want test-provider", recReg.Provider)
	}

	// 4) When observed empty, fallback to config.
	observedModel.Store("")
	observedProvider.Store("")
	recReg2 := collectAgentRegistration(cfg2)
	if recReg2.Model != "old-model" || recReg2.Provider != "old" {
		t.Fatalf("fallback to config: got model=%q provider=%q want old-model/old", recReg2.Model, recReg2.Provider)
	}

	// 5) Invalid JSON should not clobber observed.
	observedModel.Store("keep-me")
	badReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`not json`))
	if _, err := copyRequest(badReq); err != nil {
		t.Fatalf("copyRequest bad json err: %v", err)
	}
	if got := getObservedModel(); got != "keep-me" {
		t.Fatalf("invalid JSON clobbered observedModel: got %q want keep-me", got)
	}

	// 6) Empty model should not overwrite.
	observedModel.Store("keep-me-2")
	emptyModelReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"","messages":[]}`))
	copyRequest(emptyModelReq)
	if got := getObservedModel(); got != "keep-me-2" {
		t.Fatalf("empty model should not overwrite: got %q want keep-me-2", got)
	}

	// 7) Nil body handling.
	nilReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	nilReq.Body = nil
	if _, err := copyRequest(nilReq); err != nil {
		t.Fatalf("nil body err: %v", err)
	}

	// Cleanup
	observedModel.Store("")
	observedProvider.Store("")
}

func TestCopyRequestPreservesBodyForMultipleReads(t *testing.T) {
	observedModel.Store("")
	body := `{"model":"m1","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	b1, _ := copyRequest(req)
	b2, _ := io.ReadAll(req.Body)
	if string(b1) != string(b2) {
		t.Fatalf("body not preserved across reads: %q vs %q", string(b1), string(b2))
	}
}

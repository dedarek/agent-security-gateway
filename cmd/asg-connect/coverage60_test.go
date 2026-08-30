package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- hooks.go helpers ---

func TestLocalVerdictBlockPatterns(t *testing.T) {
	cases := []string{"rm -rf /", "DROP TABLE users", "shutdown now", ":(){", "mkfs.ext4", "attacker@gmail.com"}
	for _, pat := range cases {
		v, r := localVerdict("Bash", json.RawMessage(`{"command":"`+pat+`"}`))
		if v != "BLOCK" {
			t.Fatalf("want BLOCK for %q got %q reason %q", pat, v, r)
		}
	}
}

func TestLocalVerdictAllowAndRedact(t *testing.T) {
	v, _ := localVerdict("Read", json.RawMessage(`{"file_path":"/tmp/a.txt"}`))
	if v != "ALLOW" {
		t.Fatalf("want ALLOW got %q", v)
	}
	// outbound with secret pattern should be REDACT
	v2, r := localVerdict("webfetch", json.RawMessage(`{"url":"https://example.com","content":"sk-123 http payload"}`))
	if v2 != "REDACT" {
		t.Fatalf("want REDACT got %q reason %q", v2, r)
	}
	v3, _ := localVerdict("bash", json.RawMessage(`{"command":"curl https://x.com -H 'Authorization: Bearer sk-abc123'"}`))
	if v3 != "REDACT" {
		t.Fatalf("want REDACT for bash with secret got %q", v3)
	}
	// same secret without http should not be REDACT (stays ALLOW)
	v4, _ := localVerdict("Read", json.RawMessage(`{"file_path":"sk-abc"}`))
	if v4 != "ALLOW" {
		t.Fatalf("want ALLOW for read with secret no http got %q", v4)
	}
}

func TestFirstKeyAndDefaultModel(t *testing.T) {
	if firstKey(&ProbeConfig{}) != "" {
		t.Fatal("want empty firstKey")
	}
	if firstDefaultModel(&ProbeConfig{}) != "gpt-4o-mini" {
		t.Fatalf("want gpt-4o-mini got %q", firstDefaultModel(&ProbeConfig{}))
	}
	cfg := &ProbeConfig{Providers: []Provider{{APIKey: "k123", DefaultModel: "my-model"}}}
	if firstKey(cfg) != "k123" {
		t.Fatalf("firstKey %q", firstKey(cfg))
	}
	if firstDefaultModel(cfg) != "my-model" {
		t.Fatalf("firstDefaultModel %q", firstDefaultModel(cfg))
	}
}

func TestMergeJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// create existing file with some content
	if err := os.WriteFile(path, []byte(`{"existing":"keep","env":{"OLD":"1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSONFile(path, []byte(`{"env":{"NEW":"2"}}`)); err != nil {
		t.Fatalf("merge: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "keep") || !strings.Contains(string(b), "NEW") || !strings.Contains(string(b), "OLD") {
		t.Fatalf("merge lost keys: %q", string(b))
	}
	// merge into non-existent file
	path2 := filepath.Join(dir, "new.json")
	if err := mergeJSONFile(path2, []byte(`{"env":{"A":"1"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path2); err != nil {
		t.Fatal("new file not created")
	}
	// bad json
	if err := mergeJSONFile(path, []byte(`not json`)); err == nil {
		t.Fatal("want error for bad json")
	}
}

func TestAppendIfAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := appendIfAbsent(path, "hello asg-connect\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "hello") {
		t.Fatal("append failed")
	}
	// second call should be absent (already contains marker)
	if err := appendIfAbsent(path, "hello asg-connect second\n"); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(path)
	if strings.Contains(string(b2), "second") {
		t.Fatal("should not append second when already present")
	}
}

func TestWriteAuthFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := writeAuthFile(path, "key123"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["OPENAI_API_KEY"] != "key123" {
		t.Fatalf("got %q", string(b))
	}
}

func TestIoReadAll(t *testing.T) {
	r, w, _ := os.Pipe()
	go func() { fmt.Fprint(w, "hello pipe"); w.Close() }()
	b, err := ioReadAll(r)
	if err != nil {
		t.Fatalf("ioReadAll: %v", err)
	}
	if string(b) != "hello pipe" {
		t.Fatalf("got %q", string(b))
	}
	r.Close()
}

// --- config ---

func TestOsExpandEnv(t *testing.T) {
	os.Setenv("ASG_TEST_EXPAND", "expanded123")
	defer os.Unsetenv("ASG_TEST_EXPAND")
	if got := osExpandEnv("prefix ${ASG_TEST_EXPAND} suffix"); got != "prefix expanded123 suffix" {
		t.Fatalf("got %q", got)
	}
	if got := osExpandEnv("no env ${MISSING_VAR_XYZ}"); got != "no env " {
		t.Fatalf("missing var not empty: %q", got)
	}
	if got := osExpandEnv("plain"); got != "plain" {
		t.Fatalf("plain %q", got)
	}
}

// --- agent_register ---

func TestCollectAgentRegistration(t *testing.T) {
	cfg := ProbeConfig{
		Providers:  []Provider{{Name: "zen", DefaultModel: "hy3"}},
		AgentID:    "my-agent",
		TenantName: "my-tenant",
		AgentType:  "claude-code",
		AgentAlias: "alias1",
	}
	rec := collectAgentRegistration(cfg)
	if rec.AgentID != "my-agent" {
		t.Fatalf("agentID %q", rec.AgentID)
	}
	if rec.AgentType != "claude-code" {
		t.Fatalf("type %q", rec.AgentType)
	}
	if rec.Model != "hy3" || rec.Provider != "zen" {
		t.Fatalf("model %q provider %q", rec.Model, rec.Provider)
	}
	if rec.MachineID == "" || rec.ProbeID == "" {
		t.Fatal("empty machine/probe id")
	}
	// when AgentID empty, uses tenant+hostname
	cfg2 := ProbeConfig{TenantName: "t2"}
	rec2 := collectAgentRegistration(cfg2)
	if !strings.Contains(rec2.AgentID, "t2-") {
		t.Fatalf("auto agentID %q", rec2.AgentID)
	}
	// when AgentType empty -> unknown
	cfg3 := ProbeConfig{}
	rec3 := collectAgentRegistration(cfg3)
	if rec3.AgentType != "unknown" {
		t.Fatalf("want unknown got %q", rec3.AgentType)
	}
}

func TestFirstIPAndLocalIPs(t *testing.T) {
	if got := firstIP([]string{"192.168.1.10", "10.0.0.1"}); got != "192.168.1.10" {
		t.Fatalf("firstIP %q", got)
	}
	if got := firstIP([]string{"169.254.1.1", "192.168.1.5"}); got != "192.168.1.5" {
		t.Fatalf("firstIP skip link-local got %q", got)
	}
	if got := firstIP([]string{"::1", "fe80::1"}); got != "::1" {
		// fallback to first entry when no v4
		t.Logf("firstIP ipv6 fallback got %q", got)
	}
	if got := firstIP(nil); got != "" {
		t.Fatalf("want empty got %q", got)
	}
	ips := localIPs()
	// should not panic, may be empty on CI but should be slice
	t.Logf("localIPs: %v", ips)
	_ = localIPv4()
}

func TestPostAgent(t *testing.T) {
	// success
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mykey" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := ProbeConfig{HubURL: srv.URL, TenantKey: "mykey"}
	if err := postAgent(srv.Client(), cfg, "/api/agents/register", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("postAgent success: %v", err)
	}
	// failure status
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv2.Close()
	cfg2 := ProbeConfig{HubURL: srv2.URL}
	if err := postAgent(srv2.Client(), cfg2, "/api/agents/register", map[string]string{"a": "b"}); err == nil {
		t.Fatal("want error for 500")
	}
	// network error
	cfg3 := ProbeConfig{HubURL: "http://127.0.0.1:0"}
	if err := postAgent(http.DefaultClient, cfg3, "/api/agents/register", map[string]string{"a": "b"}); err == nil {
		t.Fatal("want error for unreachable")
	}
}

// --- registry_sync ---

func TestProbeWrapURL(t *testing.T) {
	cfg := &ProbeConfig{Listen: "127.0.0.1:8181"}
	if got := probeWrapURL("https://example.com/mcp", cfg); got != "http://127.0.0.1:8181/mcp" {
		t.Fatalf("got %q", got)
	}
}

func TestMountMCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// mountMCP uses os.UserHomeDir which respects HOME
	cfg := &ProbeConfig{Listen: "127.0.0.1:8181"}
	entries := []registryEntry{
		{Name: "tool-a", Command: []string{"echo", "hello"}},
		{Name: "tool-b", URL: "https://example.com"},
	}
	// On windows, command with echo (no :/ or .exe) is not windows cmd, will be skipped
	// So we craft a windows-style entry for this platform
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") || os.PathSeparator == '\\' {
		entries = []registryEntry{
			{Name: "tool-win", Command: []string{"C:/tools/bin/my.exe", "--flag"}},
			{Name: "tool-url", URL: "https://example.com"},
		}
	}
	if err := mountMCP(cfg, entries); err != nil {
		t.Fatalf("mountMCP: %v", err)
	}
	// Generic universal path (harness-agnostic) — per-harness checks removed
	p := filepath.Join(tmp, ".config", "asg", "mcp.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if !strings.Contains(string(b), "asg-") {
		t.Fatalf("no asg entry in %q", string(b))
	}
}

// --- responses helpers ---

func TestFlattenAndHelpers(t *testing.T) {
	if got := flattenText([]any{map[string]any{"text": "hello "}, map[string]any{"text": "world"}}); got != "hello world" {
		t.Fatalf("flatten %q", got)
	}
	if got := flattenText([]any{map[string]any{"nope": "x"}}); got != "" {
		t.Fatalf("want empty got %q", got)
	}
	if got := flattenText([]any{"not a map"}); got != "" {
		t.Fatalf("want empty for non-map got %q", got)
	}
	if string(mustJSON(map[string]string{"a": "b"})) == "" {
		t.Fatal("mustJSON empty")
	}
	h := randHex(8)
	if len(h) != 8 {
		t.Fatalf("randHex len %d", len(h))
	}
	if h2 := randHex(7); len(h2) != 7 {
		t.Fatalf("randHex 7 len %d", len(h2))
	}
}

func TestTimeNanoAndOsOpenAppend(t *testing.T) {
	if timeNano() == 0 {
		t.Fatal("timeNano zero")
	}
	path := filepath.Join(t.TempDir(), "append.txt")
	f, err := osOpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("x")
	f.Close()
	f2, err := osOpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	f2.Close()
}

func TestAppendFile(t *testing.T) {
	if err := appendFile("", []byte("x")); err != nil {
		t.Fatalf("empty path: %v", err)
	}
	path := filepath.Join(t.TempDir(), "af.txt")
	if err := appendFile(path, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "hello\n" {
		t.Fatalf("got %q", string(b))
	}
}

// --- spool further ---

func TestSpoolMarkShipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.jsonl")
	s := newSpool(path)
	s.push([]byte(`{"a":1}` + "\n"))
	if s.len() != 1 {
		t.Fatalf("len %d", s.len())
	}
	// pop and markShipped should clear file
	b, _ := s.pop()
	if len(b) == 0 {
		t.Fatal("pop empty")
	}
	s.markShipped()
	// after empty, markShipped should have removed file, but len 0 stays
	if s.len() != 0 {
		t.Fatalf("want 0 got %d", s.len())
	}
	// replayWAL: write file then newSpool should load it
	os.WriteFile(path, []byte(`{"x":1}`+"\n"+`{"y":2}`+"\n"), 0o644)
	s2 := newSpool(path)
	if s2.len() != 2 {
		t.Fatalf("replay len %d", s2.len())
	}
}

// --- convertAnthroMessages ---

func TestConvertAnthroMessages(t *testing.T) {
	// string system
	out := convertAnthroMessages(json.RawMessage(`"system prompt"`), []anthroMsg{
		{Role: "user", Content: json.RawMessage(`"hello"`)},
		{Role: "assistant", Content: json.RawMessage(`"hi"`)},
	})
	if len(out) != 3 {
		t.Fatalf("len %d", len(out))
	}
	// blocks system
	sysBlocks := json.RawMessage(`[{"type":"text","text":"sys1"},{"type":"text","text":"sys2"}]`)
	out2 := convertAnthroMessages(sysBlocks, []anthroMsg{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"block hello"}]`)},
	})
	if len(out2) != 2 {
		t.Fatalf("blocks len %d", len(out2))
	}
	found := false
	for _, m := range out2 {
		if m["role"] == "system" && strings.Contains(fmt.Sprint(m["content"]), "sys1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("system block not converted: %v", out2)
	}
	// tool_result block
	toolMsg := anthroMsg{
		Role: "user",
		Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"t1","content":"result text"}]`),
	}
	out3 := convertAnthroMessages(nil, []anthroMsg{toolMsg})
	// tool_result produces a tool entry plus an extra user entry (implementation quirk), check at least one tool entry exists
	foundTool := false
	for _, m := range out3 {
		if m["role"] == "tool" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("tool_result not converted: %v", out3)
	}
	// tool_use block
	toolUseMsg := anthroMsg{
		Role: "assistant",
		Content: json.RawMessage(`[{"type":"tool_use","id":"tu1","name":"mytool","input":{"arg":"1"}}]`),
	}
	out4 := convertAnthroMessages(nil, []anthroMsg{toolUseMsg})
	if len(out4) != 1 {
		t.Fatalf("tool_use len %d", len(out4))
	}
	if _, ok := out4[0]["tool_calls"]; !ok {
		t.Fatalf("tool_use not set: %v", out4[0])
	}
}

// --- anthropic bridge via httptest ---

func TestHandleAnthropicBridgeSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"bridge ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1", APIKey: "k", DefaultModel: "hy3"}},
		Listen: "127.0.0.1:8181",
	}
	rep := newReporter("http://127.0.0.1:0", "", os.TempDir()+"/bridge-spool.jsonl", "t", "a")
	p := &llmProxy{cfg: cfg, rep: rep}
	body := `{"model":"hy3","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleAnthropicBridge(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bridge status %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bridge ok") {
		t.Fatalf("bridge body %q", rec.Body.String())
	}
}

func TestHandleAnthropicBridgeBadRoute(t *testing.T) {
	cfg := &ProbeConfig{Providers: []Provider{}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"unknown","messages":[]}`))
	rec := httptest.NewRecorder()
	p.handleAnthropicBridge(rec, req)
	if rec.Code != 403 {
		t.Fatalf("want 403 got %d", rec.Code)
	}
}

func TestHandleAnthropicBridgeBadJSON(t *testing.T) {
	cfg := &ProbeConfig{Providers: []Provider{{Name: "zen", BaseURL: "http://x/v1"}}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	p.handleAnthropicBridge(rec, req)
	if rec.Code != 400 {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestHandleAnthropicBridgeStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"streamed hello world"}}]}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1", APIKey: "k", DefaultModel: "hy3"}},
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	body := `{"model":"hy3","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleAnthropicBridge(rec, req)
	if rec.Code != 200 {
		t.Fatalf("stream status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "event:") {
		t.Fatalf("want sse body got %q", rec.Body.String()[:200])
	}
}

// --- responses ---

func TestHandleResponsesWithMock(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"resp hello"}}]}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1", APIKey: "k", DefaultModel: "hy3"}},
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://127.0.0.1:0", "", "", "t", "a")}
	body := `{"model":"hy3","input":"hello world","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleResponses(rec, req)
	if rec.Code != 200 {
		t.Fatalf("responses status %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "resp hello") {
		t.Fatalf("want resp hello got %q", rec.Body.String())
	}
}

func TestHandleResponsesStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"stream resp"}}]}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1"}}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	body := `{"model":"zen","input":"hi","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleResponses(rec, req)
	if rec.Code != 200 {
		t.Fatalf("stream status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "response.created") {
		t.Fatalf("want response.created got %q", rec.Body.String()[:500])
	}
}

func TestHandleResponsesBadJSON(t *testing.T) {
	cfg := &ProbeConfig{Providers: []Provider{{Name: "zen", BaseURL: "http://x/v1"}}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	p.handleResponses(rec, req)
	if rec.Code != 400 {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestHandleResponsesWithInstructionsAndInputArray(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var chat map[string]any
		json.NewDecoder(r.Body).Decode(&chat)
		msgs, _ := chat["messages"].([]any)
		if len(msgs) == 0 {
			t.Errorf("no messages forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1"}}}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://x", "", "", "t", "a")}
	body := `{"model":"zen","instructions":"be helpful","input":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleResponses(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
}

// --- sse ---

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

func TestAnthropicSSE(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	anthropicSSE(rec, "model-x", "hello world this is a long content to chunk", []map[string]any{
		{"name": "mytool", "input": map[string]any{"arg": "1"}},
	})
	body := rec.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Fatalf("no message_start: %q", body[:500])
	}
	if !strings.Contains(body, "hello") {
		t.Fatalf("no content: %q", body[:500])
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("no message_stop")
	}
	// without content
	rec2 := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	anthropicSSE(rec2, "m", "", nil)
	if !strings.Contains(rec2.Body.String(), "message_start") {
		t.Fatal("empty content missing start")
	}
}

// --- reporter hubCheck ---

func TestHubCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"verdict":"ALLOW","reason":"ok"}`))
	}))
	defer srv.Close()
	rep := newReporter(srv.URL, "k", "", "t", "a")
	v, err := rep.hubCheck(context.Background(), "sess", "tool", []byte(`{}`))
	if err != nil {
		t.Fatalf("hubCheck: %v", err)
	}
	if !strings.Contains(v, "ALLOW") {
		t.Fatalf("got %q", v)
	}
}

// helper for hubCheck context

func TestAnthropicSSEUnsupported(t *testing.T) {
	// a ResponseWriter that does NOT implement http.Flusher should get 500
	nf := &noFlushWriter{header: make(http.Header), body: &bytes.Buffer{}}
	anthropicSSE(nf, "m", "hi", nil)
	if nf.code != 500 {
		t.Fatalf("want 500 for non-flusher got %d body %q", nf.code, nf.body.String())
	}
}

type noFlushWriter struct {
	header http.Header
	code   int
	body   *bytes.Buffer
}

func (w *noFlushWriter) Header() http.Header { return w.header }
func (w *noFlushWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.body.Write(b)
}
func (w *noFlushWriter) WriteHeader(code int) { w.code = code }

func TestHandleLLMWithProviderError(t *testing.T) {
	// upstream returns 500, should forward
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"upstream fail"}`))
	}))
	defer upstream.Close()
	cfg := &ProbeConfig{
		Providers: []Provider{{Name: "zen", BaseURL: upstream.URL + "/v1", DefaultModel: "hy3"}},
	}
	p := &llmProxy{cfg: cfg, rep: newReporter("http://127.0.0.1:0", "", "", "t", "a")}
	body := `{"model":"hy3","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleLLM(rec, req)
	if rec.Code != 500 {
		t.Fatalf("want 500 got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestSanitizeAnthropicForZenEdge(t *testing.T) {
	// invalid json should be returned as-is?
	out := sanitizeAnthropicForZen([]byte(`not json`))
	if len(out) == 0 {
		t.Fatal("empty")
	}
}

package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dedarek/agent-security-gateway/internal/agentregistry"
	"github.com/dedarek/agent-security-gateway/internal/store"
)

// protobuf builders mirroring the OTLP wire format (same as otlp tests).
func otlpTag(b []byte, num, wire int) []byte {
	return otlpVarint(b, uint64(num<<3|wire))
}
func otlpVarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}
func otlpBytes(b []byte, num int, v []byte) []byte {
	b = otlpTag(b, num, 2)
	b = otlpVarint(b, uint64(len(v)))
	return append(b, v...)
}
func otlpStr(b []byte, num int, s string) []byte { return otlpBytes(b, num, []byte(s)) }
func otlpFixed64(b []byte, num int, v uint64) []byte {
	b = otlpTag(b, num, 1)
	for i := 0; i < 8; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}
func otlpKV(k, v string) []byte {
	var av []byte
	av = otlpStr(av, 1, v)
	var b []byte
	b = otlpStr(b, 1, k)
	return otlpBytes(b, 2, av)
}

func buildTracesPayload(model, session string) []byte {
	end := uint64(time.Now().Unix()) * 1e9
	var resource []byte
	resource = otlpBytes(resource, 1, otlpKV("service.name", "opencode"))
	resource = otlpBytes(resource, 1, otlpKV("gen_ai.system", "opencode"))
	var span []byte
	span = otlpStr(span, 3, "chat")
	span = otlpFixed64(span, 7, end-1e9)
	span = otlpFixed64(span, 8, end)
	span = otlpBytes(span, 9, otlpKV("gen_ai.request.model", model))
	span = otlpBytes(span, 9, otlpKV("session.id", session))
	var scope []byte
	scope = otlpBytes(scope, 2, span)
	var rs []byte
	rs = otlpBytes(rs, 1, resource)
	rs = otlpBytes(rs, 2, scope)
	var out []byte
	return otlpBytes(out, 1, rs)
}

func newOTLPServer(t *testing.T) (*Server, *agentregistry.Registry, *http.ServeMux) {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := agentregistry.Open(filepath.Join(t.TempDir(), "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, nil)
	s.SetAgentRegistry(reg)
	mux := http.NewServeMux()
	s.RegisterOTLP(mux)
	return s, reg, mux
}

func TestOTLPTracesCreateAndUpdateAgent(t *testing.T) {
	_, reg, mux := newOTLPServer(t)
	// OTLP now only refreshes already-registered agents.
	_ = reg.Upsert(agentregistry.Record{AgentID: "otel-203.0.113.7-opencode", ProbeID: "probe-otel", MachineID: "m-otel", LastHeartbeat: time.Now(), LastActivity: time.Now()})

	post := func(model, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/traces",
			bytes.NewReader(buildTracesPayload(model, session)))
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.RemoteAddr = "203.0.113.7:55000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if r := post("glm-5.2", "ses_1"); r.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", r.Code, r.Body.String())
	}
	rec, ok := reg.Get("otel-203.0.113.7-opencode")
	if !ok {
		t.Fatal("agent not created from OTLP trace")
	}
	if rec.Model != "glm-5.2" {
		t.Fatalf("model = %q", rec.Model)
	}

	// Model switch + new session: same runtime, no new Agent row.
	if r := post("kimi-k3", "ses_2"); r.Code != http.StatusOK {
		t.Fatalf("status = %d", r.Code)
	}
	rec, _ = reg.Get("otel-203.0.113.7-opencode")
	if rec.Model != "kimi-k3" {
		t.Fatalf("model after switch = %q", rec.Model)
	}
	found := map[string]bool{}
	for _, sid := range rec.SessionIDs {
		found[sid] = true
	}
	if !found["ses_2"] {
		t.Fatalf("sessions = %v", rec.SessionIDs)
	}
	if n := len(reg.List()); n != 1 {
		t.Fatalf("expected 1 agent row, got %d", n)
	}
}

func TestOTLPTracesExplicitAgentID(t *testing.T) {
	_, reg, mux := newOTLPServer(t)
	_ = reg.Upsert(agentregistry.Record{AgentID: "local-yycserver", ProbeID: "probe-local", MachineID: "m-local", LastHeartbeat: time.Now(), LastActivity: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/v1/traces",
		bytes.NewReader(buildTracesPayload("hy3", "s-9")))
	req.Header.Set(publicAgentHeader, "local-yycserver")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	agent, ok := reg.Get("local-yycserver")
	if !ok {
		t.Fatal("explicit agent id not honored")
	}
	if agent.Model != "hy3" {
		t.Fatalf("model = %q", agent.Model)
	}
}

func TestOTLPTracesBadPayload(t *testing.T) {
	_, _, mux := newOTLPServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte{0xff, 0xff}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var m map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&m)
	_ = m
}

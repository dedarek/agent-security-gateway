package otlp

import (
	"testing"
	"time"
)

// protobuf test encoder helpers (append-only builders).
func tag(b []byte, num, wire int) []byte {
	return appendVarint(b, uint64(num<<3|wire))
}
func appendVarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}
func bytesField(b []byte, num int, v []byte) []byte {
	b = tag(b, num, 2)
	b = appendVarint(b, uint64(len(v)))
	return append(b, v...)
}
func strField(b []byte, num int, s string) []byte { return bytesField(b, num, []byte(s)) }
func fixed64Field(b []byte, num int, v uint64) []byte {
	b = tag(b, num, 1)
	for i := 0; i < 8; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func kv(key, value string) []byte {
	var anyVal []byte
	anyVal = strField(anyVal, 1, value) // AnyValue.string_value = 1
	var b []byte
	b = strField(b, 1, key)
	b = bytesField(b, 2, anyVal)
	return b
}

func buildExport(t *testing.T, resourceAttrs map[string]string, spanName string, spanAttrs map[string]string, start, end uint64) []byte {
	t.Helper()
	var resource []byte
	for k, v := range resourceAttrs {
		resource = bytesField(resource, 1, kv(k, v))
	}
	var span []byte
	span = strField(span, 3, spanName)
	span = fixed64Field(span, 7, start)
	span = fixed64Field(span, 8, end)
	for k, v := range spanAttrs {
		span = bytesField(span, 9, kv(k, v))
	}
	var scope []byte
	scope = bytesField(scope, 2, span)
	var rs []byte
	rs = bytesField(rs, 1, resource)
	rs = bytesField(rs, 2, scope)
	var out []byte
	out = bytesField(out, 1, rs)
	return out
}

func TestDecodeExportTraceServiceRequest(t *testing.T) {
	end := uint64(1787808000) * 1e9
	body := buildExport(t,
		map[string]string{"service.name": "opencode", "gen_ai.system": "opencode"},
		"chat",
		map[string]string{
			"gen_ai.request.model": "glm-5.2",
			"session.id":           "ses_abc123",
		},
		end-5e9, end,
	)
	batches, err := DecodeExportTraceServiceRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("batches = %d", len(batches))
	}
	sig := SignalFromSpans(batches)
	if sig.Model != "glm-5.2" {
		t.Fatalf("model = %q", sig.Model)
	}
	if sig.SessionID != "ses_abc123" {
		t.Fatalf("session = %q", sig.SessionID)
	}
	if sig.AgentName != "opencode" {
		t.Fatalf("agent name = %q", sig.AgentName)
	}
	want := time.Unix(1787808000, 0).UTC()
	if !sig.Latest.Equal(want) {
		t.Fatalf("latest = %v want %v", sig.Latest, want)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := DecodeExportTraceServiceRequest([]byte{0xff, 0xff}); err == nil {
		t.Fatal("expected error on garbage")
	}
}

func TestSignalFromSpansModelPriority(t *testing.T) {
	body := buildExport(t, nil, "chat",
		map[string]string{"gen_ai.response.model": "actual-model", "model": "fallback"},
		1e9, 2e9)
	sig := SignalFromSpans(mustDecode(t, body))
	if sig.Model != "actual-model" {
		t.Fatalf("model = %q", sig.Model)
	}
}

func mustDecode(t *testing.T, b []byte) []ResourceSpans {
	t.Helper()
	rs, err := DecodeExportTraceServiceRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

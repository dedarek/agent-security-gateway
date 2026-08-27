// Package otlp is a minimal, dependency-free OTLP/HTTP (protobuf) trace
// receiver. It decodes just enough of ExportTraceServiceRequest to power the
// agent console: model name, session id, timestamps. Standard OTLP exporters
// (OpenCode experimental.openTelemetry, Claude Code, Codex, hermes-otel,
// pi-otel, OpenClaw diagnostics-otel) all speak this exact wire format.
//
// Only protobuf encoding is supported (the default for every exporter above).
// JSON OTLP is intentionally not implemented: no major agent emits it.
package otlp

import (
	"fmt"
	"strings"
	"time"
)

// Span is the subset of an OTLP span the console cares about.
type Span struct {
	Name       string
	Attributes map[string]string
	StartNano  uint64
	EndNano    uint64
}

// ResourceSpans groups spans by the resource (service/agent) that emitted them.
type ResourceSpans struct {
	ResourceAttributes map[string]string
	Spans              []Span
}

// ---- protobuf wire primitives ----

type field struct {
	num  int
	wire int
	val  uint64
	buf  []byte
}

func parseFields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		key, n := uvarint(b)
		if n <= 0 {
			return nil, fmt.Errorf("bad varint key")
		}
		b = b[n:]
		f := field{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0: // varint
			v, m := uvarint(b)
			if m <= 0 {
				return nil, fmt.Errorf("bad varint value")
			}
			f.val = v
			b = b[m:]
		case 1: // 64-bit fixed
			if len(b) < 8 {
				return nil, fmt.Errorf("truncated fixed64")
			}
			f.val = uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
				uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
			b = b[8:]
		case 2: // length-delimited
			l, m := uvarint(b)
			if m <= 0 {
				return nil, fmt.Errorf("bad length")
			}
			b = b[m:]
			if uint64(len(b)) < l {
				return nil, fmt.Errorf("truncated bytes")
			}
			f.buf = b[:l]
			b = b[l:]
		case 5: // 32-bit fixed
			if len(b) < 4 {
				return nil, fmt.Errorf("truncated fixed32")
			}
			f.val = uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
			b = b[4:]
		default:
			return nil, fmt.Errorf("unsupported wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

func uvarint(b []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, c := range b {
		if c < 0x80 {
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
		if s >= 64 {
			return 0, -1
		}
	}
	return 0, 0
}

// ---- OTLP message decoding ----
// Field numbers per opentelemetry-proto v1:
//   ExportTraceServiceRequest.resource_spans = 1
//   ResourceSpans: resource = 1, scope_spans = 2
//   Resource.attributes = 1 (repeated KeyValue)
//   ScopeSpans: scope = 1, spans = 2
//   Span: name = 3, start_time_unix_nano = 7 (fixed64), end_time_unix_nano = 8,
//         attributes = 9 (repeated KeyValue)
//   KeyValue: key = 1, value = 2 (AnyValue); AnyValue.string_value = 1,
//         int_value = 3, double_value = 4, bool_value = 6

// DecodeExportTraceServiceRequest parses the protobuf body of POST /v1/traces.
func DecodeExportTraceServiceRequest(body []byte) ([]ResourceSpans, error) {
	fields, err := parseFields(body)
	if err != nil {
		return nil, err
	}
	var out []ResourceSpans
	for _, f := range fields {
		if f.num == 1 && f.wire == 2 {
			rs, err := decodeResourceSpans(f.buf)
			if err != nil {
				return nil, err
			}
			out = append(out, rs)
		}
	}
	return out, nil
}

func decodeResourceSpans(b []byte) (ResourceSpans, error) {
	rs := ResourceSpans{ResourceAttributes: map[string]string{}}
	fields, err := parseFields(b)
	if err != nil {
		return rs, err
	}
	for _, f := range fields {
		switch {
		case f.num == 1 && f.wire == 2: // resource
			attrs, err := decodeResource(f.buf)
			if err != nil {
				return rs, err
			}
			rs.ResourceAttributes = attrs
		case f.num == 2 && f.wire == 2: // scope_spans
			spans, err := decodeScopeSpans(f.buf)
			if err != nil {
				return rs, err
			}
			rs.Spans = append(rs.Spans, spans...)
		}
	}
	return rs, nil
}

func decodeResource(b []byte) (map[string]string, error) {
	attrs := map[string]string{}
	fields, err := parseFields(b)
	if err != nil {
		return attrs, err
	}
	for _, f := range fields {
		if f.num == 1 && f.wire == 2 {
			k, v, err := decodeKeyValue(f.buf)
			if err != nil {
				return attrs, err
			}
			if k != "" {
				attrs[k] = v
			}
		}
	}
	return attrs, nil
}

func decodeScopeSpans(b []byte) ([]Span, error) {
	var out []Span
	fields, err := parseFields(b)
	if err != nil {
		return out, err
	}
	for _, f := range fields {
		if f.num == 2 && f.wire == 2 {
			sp, err := decodeSpan(f.buf)
			if err != nil {
				return out, err
			}
			out = append(out, sp)
		}
	}
	return out, nil
}

func decodeSpan(b []byte) (Span, error) {
	sp := Span{Attributes: map[string]string{}}
	fields, err := parseFields(b)
	if err != nil {
		return sp, err
	}
	for _, f := range fields {
		switch {
		case f.num == 3 && f.wire == 2:
			sp.Name = string(f.buf)
		case f.num == 7 && f.wire == 1:
			sp.StartNano = f.val
		case f.num == 8 && f.wire == 1:
			sp.EndNano = f.val
		case f.num == 9 && f.wire == 2:
			k, v, err := decodeKeyValue(f.buf)
			if err != nil {
				return sp, err
			}
			if k != "" {
				sp.Attributes[k] = v
			}
		}
	}
	return sp, nil
}

func decodeKeyValue(b []byte) (string, string, error) {
	var key, val string
	fields, err := parseFields(b)
	if err != nil {
		return key, val, err
	}
	for _, f := range fields {
		switch {
		case f.num == 1 && f.wire == 2:
			key = string(f.buf)
		case f.num == 2 && f.wire == 2:
			val = decodeAnyValue(f.buf)
		}
	}
	return key, val, nil
}

func decodeAnyValue(b []byte) string {
	fields, err := parseFields(b)
	if err != nil {
		return ""
	}
	for _, f := range fields {
		switch f.num {
		case 1: // string_value
			return string(f.buf)
		case 3: // int_value
			return fmt.Sprintf("%d", int64(f.val))
		case 4: // double_value — raw bits; rarely used for identity attrs
			return fmt.Sprintf("%v", f.val)
		case 6: // bool_value
			if f.val == 1 {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

// ---- attribute extraction (GenAI semantic conventions + agent variants) ----

// modelKeys are checked in order; first hit wins.
var modelKeys = []string{
	"gen_ai.request.model",
	"gen_ai.response.model",
	"llm.model_name",
	"model",
	"ai.model.id",
}

var sessionKeys = []string{
	"session.id",
	"gen_ai.conversation.id",
	"thread.id",
}

var agentNameKeys = []string{
	"gen_ai.agent.name",
	"service.name",
}

var agentTypeKeys = []string{
	"gen_ai.system",
	"telemetry.sdk.name",
}

// Extract returns the useful identity signals from one resource+span batch.
type Signal struct {
	AgentName string
	AgentType string
	SessionID string
	Model     string
	Latest    time.Time
}

// SignalFromSpans reduces a decoded export into the identity signals the
// registry consumes. Zero values are fine — callers merge non-empty fields.
func SignalFromSpans(batches []ResourceSpans) Signal {
	var sig Signal
	for _, rs := range batches {
		if sig.AgentName == "" {
			sig.AgentName = firstKey(rs.ResourceAttributes, agentNameKeys)
		}
		if sig.AgentType == "" {
			sig.AgentType = firstKey(rs.ResourceAttributes, agentTypeKeys)
		}
		for _, sp := range rs.Spans {
			if sig.Model == "" {
				sig.Model = firstKey(sp.Attributes, modelKeys)
			}
			if sig.SessionID == "" {
				sig.SessionID = firstKey(sp.Attributes, sessionKeys)
			}
			if sp.EndNano > 0 {
				if t := time.Unix(0, int64(sp.EndNano)).UTC(); t.After(sig.Latest) {
					sig.Latest = t
				}
			} else if sp.StartNano > 0 {
				if t := time.Unix(0, int64(sp.StartNano)).UTC(); t.After(sig.Latest) {
					sig.Latest = t
				}
			}
		}
	}
	sig.AgentName = strings.TrimSpace(sig.AgentName)
	sig.AgentType = strings.TrimSpace(sig.AgentType)
	sig.SessionID = strings.TrimSpace(sig.SessionID)
	sig.Model = strings.TrimSpace(sig.Model)
	return sig
}

func firstKey(attrs map[string]string, keys []string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(attrs[k]); v != "" {
			return v
		}
	}
	return ""
}

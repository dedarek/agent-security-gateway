package proxy

import (
	"testing"

	"github.com/dedarek/agent-security-gateway/api"
)

func TestApplyRedactionsRewritesBytes(t *testing.T) {
	in := []byte(`token=ops_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345 # comment`)
	rs := []api.Redaction{{Match: "ops_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", Replace: "***"}}
	out := applyRedactions(in, rs)
	if string(out) != "token=*** # comment" {
		t.Fatalf("secret must be scrubbed, got %q", out)
	}
}

func TestApplyRedactionsLongestFirst(t *testing.T) {
	in := []byte("visit https://evil.example.com/x now")
	rs := []api.Redaction{
		{Match: "evil.example.com", Replace: "[host]"},
		{Match: "https://evil.example.com/x", Replace: "[url]"},
	}
	out := applyRedactions(in, rs)
	if string(out) != "visit [url] now" {
		t.Fatalf("longest match must win, got %q", out)
	}
}

func TestApplyRedactionsEmptyMatchNoop(t *testing.T) {
	in := []byte("payload")
	out := applyRedactions(in, []api.Redaction{{Path: "*", Replace: "***"}})
	if string(out) != "payload" {
		t.Fatalf("empty Match must not wipe payload, got %q", out)
	}
}

func TestCollectRedactionsMergesSignals(t *testing.T) {
	signals := []api.Signal{
		{Redactions: []api.Redaction{{Match: "a", Replace: "*"}}},
		{Redactions: []api.Redaction{{Match: "b", Replace: "*"}, {Match: "c", Replace: "*"}}},
		{},
	}
	got := collectRedactions(signals)
	if len(got) != 3 {
		t.Fatalf("want 3 redactions merged, got %d", len(got))
	}
}

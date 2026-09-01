package main

import (
	"encoding/json"
	"testing"
)

// Claude Code can serialize content blocks into a quoted JSON string:
// "[{\"text\":\"hi\",\"type\":\"text\"}]" as the value of content. The bridge
// must decode that layer before parsing blocks, else the upstream gets a
// Python-repr-looking garbage content and the model degrades to
// "[Tool use interrupted]".
func TestConvertAnthroMessagesHandlesStringifiedBlocks(t *testing.T) {
	sys := json.RawMessage(`[]`)
	msgs := []anthroMsg{
		{Role: "user", Content: json.RawMessage(`"Run ls"`)},
		{Role: "assistant", Content: json.RawMessage(`"[{\"text\":\"ok\",\"type\":\"text\"},{\"id\":\"toolu_1\",\"name\":\"Bash\",\"input\":{\"command\":\"ls\"},\"type\":\"tool_use\"}]"`)},
		{Role: "user", Content: json.RawMessage(`"[{\"tool_use_id\":\"toolu_1\",\"content\":\"file1\",\"type\":\"tool_result\"}]"`)},
	}
	out := convertAnthroMessages(sys, msgs)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
	}
	// assistant must have tool_calls with proper arguments
	asst := out[1]
	tcs, ok := asst["tool_calls"].([]map[string]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("assistant tool_calls missing: %#v", asst)
	}
	fn, _ := tcs[0]["function"].(map[string]any)
	args, _ := fn["arguments"].(string)
	if args != `{"command":"ls"}` {
		t.Fatalf("arguments = %q, want {\"command\":\"ls\"}", args)
	}
	// tool message must carry the result
	toolMsg := out[2]
	if toolMsg["role"] != "tool" || toolMsg["content"] != "file1" {
		t.Fatalf("tool message wrong: %#v", toolMsg)
	}
}

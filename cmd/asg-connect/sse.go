package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// anthropicSSE synthesizes an Anthropic-format SSE stream from a complete
// response, so Claude Code's streaming client works even when the upstream
// provider (OpenAI-compatible) doesn't natively support Anthropic SSE.
func anthropicSSE(w http.ResponseWriter, model, content string, toolUses []map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	send := func(event string, payload map[string]any) {
		payload["type"] = event
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	msgID := fmt.Sprintf("msg_%d", timeNano())
	seq := 0

	// message_start
	send("message_start", map[string]any{
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"model": model, "content": []any{},
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	seq++

	// content_block_start (text)
	if content != "" {
		send("content_block_start", map[string]any{
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		seq++
		// Stream text in chunks
		chunkSize := 50
		runes := []rune(content)
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			send("content_block_delta", map[string]any{
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": string(runes[i:end])},
			})
			seq++
		}
		send("content_block_stop", map[string]any{"index": 0})
		seq++
	}

	// tool_use blocks — text is idx 0, tools start at 1
	for ti, tu := range toolUses {
		idx := 1 + ti
		name, _ := tu["name"].(string)
		input := tu["input"]
		inputJSON, _ := json.Marshal(input)
		send("content_block_start", map[string]any{
			"index":         idx,
			"content_block": map[string]any{"type": "tool_use", "id": name, "name": name, "input": map[string]any{}},
		})
		seq++
		send("content_block_delta", map[string]any{
			"index": idx,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)},
		})
		seq++
		send("content_block_stop", map[string]any{"index": idx})
		seq++
	}

	stopReason := "end_turn"
	if len(toolUses) > 0 {
		stopReason = "tool_use"
	}

	// message_delta + message_stop
	send("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
	})
	seq++

	send("message_stop", map[string]any{})
}

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// handleResponses implements the OpenAI **Responses** API surface locally:
// Codex (0.149+) only speaks responses, while most OpenAI-compatible upstreams
// (including OpenCode Zen) reliably serve chat/completions. The probe
// translates responses->chat, forwards, and translates the reply back.
func (p *llmProxy) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := copyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// capture already done by copyRequest; ensure provider will be set after route

	var in struct {
		Model        string           `json:"model"`
		Stream       bool             `json:"stream"`
		Input        json.RawMessage  `json:"input"`
		Instructions string           `json:"instructions"`
		Tools        []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad json: "+err.Error(), 400)
		return
	}

	// --- build chat/completions request ---
	chat := map[string]any{"model": in.Model, "stream": false}
	msgs := []map[string]any{}
	if len(in.Instructions) > 0 && string(in.Instructions) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": in.Instructions})
	}
	appendInput := func(v any) {
		switch c := v.(type) {
		case string:
			msgs = append(msgs, map[string]any{"role": "user", "content": c})
		case []any:
			for _, item := range c {
				m, _ := item.(map[string]any)
				if m == nil {
					continue
				}
				role, _ := m["role"].(string)
				switch role {
				case "developer":
					role = "system" // codex uses 'developer'; chat API wants system
				case "":
					role = "user"
				case "assistant", "system", "tool":
					// keep as-is; tool outputs map below
				}
				content := m["content"]
				switch cc := content.(type) {
				case string:
					msgs = append(msgs, map[string]any{"role": role, "content": cc})
				case []any:
					text := flattenText(cc)
					msgs = append(msgs, map[string]any{"role": role, "content": text})
				}
			}
		}
	}
	switch {
	case len(in.Input) > 0 && in.Input[0] == '{':
		var parsed map[string]any
		if json.Unmarshal(in.Input, &parsed) == nil {
			appendInput(parsed["input"])
		}
	case len(in.Input) > 0 && in.Input[0] == '[':
		var arr []any
		if json.Unmarshal(in.Input, &arr) == nil {
			appendInput(arr)
		}
	default:
		var strVal string
		if json.Unmarshal(in.Input, &strVal) == nil {
			msgs = append(msgs, map[string]any{"role": "user", "content": strVal})
		} else {
			msgs = append(msgs, map[string]any{"role": "user", "content": string(in.Input)})
		}
	}
	// merge consecutive same-role messages (zen requires strict alternation)
	merged := []map[string]any{}
	for _, m := range msgs {
		if n := len(merged); n > 0 && merged[n-1]["role"] == m["role"] {
			prev, _ := merged[n-1]["content"].(string)
			cur, _ := m["content"].(string)
			merged[n-1]["content"] = prev + "\n\n" + cur
			continue
		}
		merged = append(merged, m)
	}
	chat["messages"] = merged

	// tools: responses function format is already close to chat format
	if len(in.Tools) > 0 {
		out := []map[string]any{}
		for _, t := range in.Tools {
			if fn, ok := t["function"].(map[string]any); ok {
				out = append(out, map[string]any{"type": "function", "function": fn})
			}
		}
		if len(out) > 0 {
			chat["tools"] = out
		}
	}

	prov, _, routeErr := p.route(mustJSON(chat))
	if routeErr == nil {
		setObservedProvider(prov.Name)
		if in.Model != "" {
			observedModel.Store(in.Model)
		}
	}
	if routeErr != nil {
		http.Error(w, `{"error":{"type":"quota_protection","message":"model not allowed"}}`, http.StatusForbidden)
		return
	}
	upURL := strings.TrimSuffix(prov.BaseURL, "/")
	if !strings.HasSuffix(upURL, "/v1") {
		upURL += "/v1"
	}
	upURL += "/chat/completions"

	cb, _ := json.Marshal(chat)
	if p := os.Getenv("ASG_DUMP"); p != "" {
		_ = os.WriteFile(p+"_chat.json", cb, 0o644)
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(cb))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+prov.APIKey)
	req.Header.Set("x-api-key", prov.APIKey)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if p := os.Getenv("ASG_DUMP"); p != "" {
		_ = os.WriteFile(p+"_upstream.txt", respBody, 0o644)
	}

	var cc struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(respBody, &cc)
	text := ""
	if len(cc.Choices) > 0 {
		text = cc.Choices[0].Message.Content
	}

	p.rep.ReportLLM("probe-"+prov.Name, in.Model, body, respBody, time.Since(start).Milliseconds())

	out := map[string]any{
		"id":     "resp_" + randHex(8),
		"object": "response",
		"status": "completed",
		"model":  in.Model,
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		}},
	}
	// Codex expects strict SSE event ordering with sequence numbers and full
	// response objects in created/completed.
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl := w.(http.Flusher)
		seq := 0
		send := func(ev string, payload map[string]any) {
			payload["type"] = ev
			payload["sequence_number"] = seq
			seq++
			b, _ := json.Marshal(payload)
			var sb strings.Builder
			sb.WriteString("event: ")
			sb.WriteString(ev)
			sb.WriteString("\ndata: ")
			sb.Write(b)
			sb.WriteString("\n\n")
			w.Write([]byte(sb.String()))
			fl.Flush()
		}
		respID := "resp_" + randHex(12)
		msgID := "msg_" + randHex(12)
		responseObj := func(status string, out []any) map[string]any {
			return map[string]any{
				"id": respID, "object": "response", "status": status,
				"model": in.Model, "output": out,
				"usage":               map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
				"parallel_tool_calls": false,
			}
		}
		send("response.created", map[string]any{"response": responseObj("in_progress", []any{})})
		item := map[string]any{"type": "message", "role": "assistant", "id": msgID, "status": "in_progress",
			"content": []any{}}
		send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		send("response.content_part.added", map[string]any{
			"item_id": msgID, "output_index": 0, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
		chunk := 24
		for i := 0; i < len(text); i += chunk {
			end := i + chunk
			if end > len(text) {
				end = len(text)
			}
			send("response.output_text.delta", map[string]any{
				"item_id": msgID, "output_index": 0, "content_index": 0, "delta": text[i:end],
			})
		}
		send("response.output_text.done", map[string]any{
			"item_id": msgID, "output_index": 0, "content_index": 0, "text": text,
		})
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		send("response.content_part.done", map[string]any{
			"item_id": msgID, "output_index": 0, "content_index": 0, "part": part,
		})
		doneItem := map[string]any{"type": "message", "role": "assistant", "id": msgID, "status": "completed",
			"content": []any{part}}
		send("response.output_item.done", map[string]any{"output_index": 0, "item": doneItem})
		finalOut := []any{doneItem}
		send("response.completed", map[string]any{"response": responseObj("completed", finalOut)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func flattenText(blocks []any) string {
	s := ""
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm == nil {
			continue
		}
		if t, ok := bm["text"].(string); ok {
			s += t
		}
	}
	return s
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func randHex(n int) string {
	need := (n + 1) / 2
	b := make([]byte, need)
	if _, err := rand.Read(b); err != nil {
		// fallback: timestamp-based
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> uint(i*8))
		}
	}
	s := hex.EncodeToString(b)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

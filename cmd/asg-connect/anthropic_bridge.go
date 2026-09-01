package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// handleAnthropicBridge converts Anthropic /v1/messages requests to OpenAI
// /v1/chat/completions format, forwards to the provider, then converts the
// response back to Anthropic format. This lets Claude Code work with ANY
// OpenAI-compatible endpoint through the probe.
func (p *llmProxy) handleAnthropicBridge(w http.ResponseWriter, r *http.Request) {
	body, err := copyRequest(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	var req struct {
		Model       string           `json:"model"`
		MaxTokens   int              `json:"max_tokens"`
		System      json.RawMessage  `json:"system,omitempty"`
		Messages    []anthroMsg      `json:"messages"`
		Tools       []map[string]any `json:"tools,omitempty"`
		Stream      bool             `json:"stream"`
		Temperature *float64         `json:"temperature,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}

	// Route to provider
	prov, upstreamModel, routeErr := p.route([]byte(fmt.Sprintf(`{"model":"%s"}`, req.Model)))
	if routeErr == nil {
		setObservedProvider(prov.Name)
		if req.Model != "" {
			observedModel.Store(req.Model)
		}
		if upstreamModel != "" && upstreamModel != req.Model {
			observedModel.Store(upstreamModel)
		}
	}
	if routeErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": routeErr.Error()}})
		return
	}

	// Build OpenAI chat/completions payload
	chat := map[string]any{
		"model":    upstreamModel,
		"messages": convertAnthroMessages(req.System, req.Messages),
	}
	if req.MaxTokens > 0 {
		chat["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		chat["temperature"] = *req.Temperature
	}

	// Convert tools: anthropic → openai function format
	if len(req.Tools) > 0 {
		openaiTools := []map[string]any{}
		for _, t := range req.Tools {
			name, _ := t["name"].(string)
			desc, _ := t["description"].(string)
			schema := t["input_schema"]
			fn := map[string]any{"name": name, "parameters": schema}
			if desc != "" {
				fn["description"] = desc
			}
			openaiTools = append(openaiTools, map[string]any{"type": "function", "function": fn})
		}
		chat["tools"] = openaiTools
	}

	cb, _ := json.Marshal(chat)

	// Provider key wins; when the config leaves it empty (env-reference style
	// like "${COMMANDCODE_API_KEY}" resolving to ""), fall back to the env
	// var the provider is named after — same rule as responses.go.
	apiKey := prov.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(strings.ToUpper(prov.Name) + "_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("COMMANDCODE_API_KEY")
	}

	// Forward to upstream chat/completions
	upURL := strings.TrimSuffix(prov.BaseURL, "/")
	if !strings.HasSuffix(upURL, "/v1") {
		upURL += "/v1"
	}
	upURL += "/chat/completions"

	httpReq, _ := http.NewRequestWithContext(r.Context(), "POST", upURL, strings.NewReader(string(cb)))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("x-api-key", apiKey)

	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	sessionID := r.Header.Get("x-asg-session")
	if sessionID == "" {
		sessionID = "probe-" + prov.Name
	}
	p.rep.ReportLLM(sessionID, upstreamModel, body, respBody, time.Since(start).Milliseconds())
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	// Parse OpenAI response and convert back to Anthropic format
	var ccResp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(respBody, &ccResp)

	content := ""
	toolUse := []map[string]any{}
	if len(ccResp.Choices) > 0 {
		msg := ccResp.Choices[0].Message
		content = msg.Content
		for i, tc := range msg.ToolCalls {
			toolUse = append(toolUse, map[string]any{
				"type":  "tool_use",
				"id":    fmt.Sprintf("toolu_bridge_%d", time.Now().UnixNano()+int64(i)),
				"name":  tc.Function.Name,
				"input": json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	// Build Anthropic-format response
	anthroContent := []map[string]any{}
	if content != "" {
		anthroContent = append(anthroContent, map[string]any{"type": "text", "text": content})
	}
	anthroContent = append(anthroContent, toolUse...)

	stopReason := "end_turn"
	if len(toolUse) > 0 {
		stopReason = "tool_use"
	}

	// If the client requested streaming, synthesize Anthropic SSE events.
	if req.Stream {
		// Pass the RAW function arguments string so the SSE synth can emit
		// them unescaped. Marshal here would double-escape and break
		// Claude Code's partial_json parser.
		rawToolUses := make([]map[string]any, 0, len(toolUse))
		for _, tu := range toolUse {
			raw := make(map[string]any, len(tu))
			for k, v := range tu {
				raw[k] = v
			}
			if in, ok := raw["input"].(json.RawMessage); ok {
				raw["input"] = string(in)
			}
			rawToolUses = append(rawToolUses, raw)
		}
		anthropicSSE(w, upstreamModel, content, rawToolUses)
		return
	}

	inTokens, outTokens := 0, len(content)/4
	if ccResp.Usage != nil {
		inTokens = ccResp.Usage.PromptTokens
		outTokens = ccResp.Usage.CompletionTokens
	}
	out := map[string]any{
		"id":            "msg_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		"type":          "message",
		"role":          "assistant",
		"content":       anthroContent,
		"model":         upstreamModel,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inTokens,
			"output_tokens": outTokens,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

type anthroMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func convertAnthroMessages(system json.RawMessage, messages []anthroMsg) []map[string]any {
	var out []map[string]any

	// system prompt
	if len(system) > 0 {
		var sysBlocks []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(system, &sysBlocks) == nil && len(sysBlocks) > 0 {
			text := ""
			for _, b := range sysBlocks {
				text += b.Text + "\n"
			}
			out = append(out, map[string]any{"role": "system", "content": strings.TrimSpace(text)})
		} else {
			var s string
			if json.Unmarshal(system, &s) == nil {
				out = append(out, map[string]any{"role": "system", "content": s})
			}
		}
	}

	for _, m := range messages {
		role := m.Role
		var contentText string
		var toolCalls []map[string]any

		// Try parsing as blocks array
		var blocks []map[string]any
		if json.Unmarshal(m.Content, &blocks) == nil {
			for _, block := range blocks {
				bt, _ := block["type"].(string)
				switch bt {
				case "text":
					t, _ := block["text"].(string)
					contentText += t
				case "tool_result":
					id, _ := block["tool_use_id"].(string)
					tc, _ := block["content"].(string)
					if tc == "" {
						if arr, ok := block["content"].([]any); ok {
							for _, item := range arr {
								im, _ := item.(map[string]any)
								if im["type"] == "text" {
									tc += im["text"].(string)
								}
							}
						}
					}
					out = append(out, map[string]any{"role": "tool", "tool_call_id": id, "content": tc})
					continue
				case "tool_use":
					name, _ := block["name"].(string)
					input := block["input"]
					tuID, _ := block["id"].(string)
					// OpenAI requires tool_calls[].function.arguments to be a JSON
					// *string* — the anthropic input object must be marshaled, not
					// embedded as a nested object (upstream rejects the object form
					// with "expected string, received object").
					argsStr := ""
					if input != nil {
						if s, ok := input.(string); ok {
							argsStr = s
						} else if b, err := json.Marshal(input); err == nil {
							argsStr = string(b)
						}
					}
					toolCalls = append(toolCalls, map[string]any{
						"id": tuID, "type": "function",
						"function": map[string]any{"name": name, "arguments": argsStr},
					})
				}
			}
		} else {
			// Fallback: content may be a JSON *string* whose value is itself a
			// blocks array (Claude Code can serialize content blocks into a
			// quoted string). Decode one layer, then re-run the block parser.
			var inner string
			if json.Unmarshal(m.Content, &inner) == nil && strings.TrimSpace(inner) != "" {
				if json.Unmarshal([]byte(inner), &blocks) == nil {
					for _, block := range blocks {
						bt, _ := block["type"].(string)
						switch bt {
						case "text":
							t, _ := block["text"].(string)
							contentText += t
						case "tool_result":
							id, _ := block["tool_use_id"].(string)
							tc, _ := block["content"].(string)
							if tc == "" {
								if arr, ok := block["content"].([]any); ok {
									for _, item := range arr {
										im, _ := item.(map[string]any)
										if im["type"] == "text" {
											tc += im["text"].(string)
										}
									}
								}
							}
							out = append(out, map[string]any{"role": "tool", "tool_call_id": id, "content": tc})
							continue
						case "tool_use":
							name, _ := block["name"].(string)
							input := block["input"]
							tuID, _ := block["id"].(string)
							argsStr := ""
							if input != nil {
								if s, ok := input.(string); ok {
									argsStr = s
								} else if b, err := json.Marshal(input); err == nil {
									argsStr = string(b)
								}
							}
							toolCalls = append(toolCalls, map[string]any{
								"id": tuID, "type": "function",
								"function": map[string]any{"name": name, "arguments": argsStr},
							})
						}
					}
				} else {
					contentText = inner
				}
			} else {
				json.Unmarshal(m.Content, &contentText)
			}
		}

		if role == "user" {
			// Upstream (OpenAI-compatible) rejects user messages with empty
			// content ("user message must have content"). Claude Code can emit
			// an empty user turn after /clear or between tool calls; drop the
			// message instead of forwarding garbage.
			if strings.TrimSpace(contentText) != "" {
				out = append(out, map[string]any{"role": "user", "content": contentText})
			}
		} else if role == "assistant" {
			entry := map[string]any{"role": "assistant", "content": contentText}
			if len(toolCalls) > 0 {
				entry["tool_calls"] = toolCalls
			}
			out = append(out, entry)
		}
	}
	return out
}

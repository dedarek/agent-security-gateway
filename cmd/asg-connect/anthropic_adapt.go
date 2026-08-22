package main

import (
	"encoding/json"
	"strings"
)

// sanitizeAnthropicForZen adapts Anthropic /v1/messages payloads to what the
// OpenCode Zen conversion layer accepts. Discovered by probing upstream:
//   - tools must be OpenAI-function shaped: {"function":{name,description,parameters}}
//     instead of native anthropic {name,description,input_schema}
//   - tool_choice likewise maps: {type:"auto"} -> "auto", {"type":"tool","name":n} -> {"type":"function","function":{"name":n}}
//   - max_tokens is required; thinking/context_management/output_config pass through.
//
// The probe is exactly the right place for this: agents speak standard
// Anthropic, providers speak their dialect, the probe translates + captures.
func sanitizeAnthropicForZen(body []byte) []byte {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body // not JSON we understand; forward untouched
	}
	changed := false

	if toolsRaw, ok := req["tools"].([]any); ok {
		out := make([]any, 0, len(toolsRaw))
		for _, t := range toolsRaw {
			m, ok := t.(map[string]any)
			if !ok {
				out = append(out, t)
				continue
			}
			if _, hasFn := m["function"]; hasFn {
				out = append(out, m) // already function-shaped
				continue
			}
			name, _ := m["name"].(string)
			desc, _ := m["description"].(string)
			schema := m["input_schema"]
			fn := map[string]any{"name": name, "parameters": schema}
			if desc != "" {
				fn["description"] = desc
			}
			out = append(out, map[string]any{"function": fn})
			changed = true
		}
		if len(out) == len(toolsRaw) && changed {
			req["tools"] = out
		}
	}

	if tc, ok := req["tool_choice"].(map[string]any); ok {
		tt, _ := tc["type"].(string)
		switch tt {
		case "auto", "any":
			req["tool_choice"] = strings.ToLower(tt)
			changed = true
		case "tool":
			name, _ := tc["name"].(string)
			req["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": name},
			}
			changed = true
		}
	}

	// Claude Code emits trailing messages with role=="system" (non-standard).
	// Zen rejects them; fold their content into the top-level system blocks.
	if msgs, ok := req["messages"].([]any); ok {
		var sysExtra []any
		kept := make([]any, 0, len(msgs))
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			if mm == nil || mm["role"] != "system" {
				kept = append(kept, m)
				continue
			}
			switch c := mm["content"].(type) {
			case string:
				sysExtra = append(sysExtra, map[string]any{"type": "text", "text": c})
			case []any:
				sysExtra = append(sysExtra, c...)
			}
			changed = true
		}
		if len(sysExtra) > 0 {
			req["messages"] = kept
			sys, _ := req["system"].([]any)
			req["system"] = append(sys, sysExtra...)
		}
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

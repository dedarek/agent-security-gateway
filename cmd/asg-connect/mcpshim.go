package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
)

// registerMCPShim exposes /mcp on the probe and forwards every request to the
// central gateway's MCP endpoint, injecting this machine's tenant key. Agents
// configure the probe URL once; credentials live in the probe config, never in
// the agent config.
func registerMCPShim(mux *http.ServeMux, cfg *ProbeConfig) {
	mcpTarget := os.Getenv("ASG_MCP_TARGET")
	if mcpTarget == "" {
		mcpTarget = "http://127.0.0.1:8080/mcp"
	}
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, mcpTarget, bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+cfg.TenantKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "gateway unreachable: "+err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	})
}

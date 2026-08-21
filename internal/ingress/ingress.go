// Package ingress exposes the Gateway to agent runtimes as a REAL MCP server
// (streamable HTTP). This is the left half of the architecture diagram:
//
//	Agent runtime (Claude Code / Codex / any MCP client)
//	    │  connects to the gateway as if it were a normal MCP server
//	    ▼
//	gateway MCP server  ──►  proxy.Gateway.Handle  ──►  upstream MCP server
//
// The tool list is the upstream's real tools/list, re-published verbatim; every
// tools/call is intercepted and runs the full three-axis Risk Decision Engine
// before anything reaches the upstream. Agents need zero changes: they point
// their MCP client at this URL.
package ingress

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dedarek/agent-security-gateway/api"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
)

// Ingress serves the gateway-facing MCP server.
type Ingress struct {
	Gateway *proxy.Gateway
	Mux     *http.ServeMux
}

// principalFromContext derives who is calling. MVP: single demo tenant until
// auth lands (API key / OIDC per runtime).
func principalFromContext(sessionID string) api.Principal {
	return api.Principal{
		UserID:    "local-user",
		AgentID:   "connected-agent",
		SessionID: sessionID,
		Role:      "employee",
	}
}

// mcpServer builds one MCP server instance exposing the upstream tool surface
// through the gateway decision engine.
func (ing *Ingress) mcpServer(upstream *upstreamSurface) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name: "agent-security-gateway", Version: "0.1.0",
		Title: "Agent Security Gateway",
	}, nil)

	for _, name := range upstream.ToolNames() {
		toolName := name // capture
		t := &mcp.Tool{
			Name:        toolName,
			Description: upstream.ToolDescription(toolName),
			InputSchema: upstream.ToolSchema(toolName),
		}
		srv.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			argsJSON := []byte(req.Params.Arguments)
			if len(argsJSON) == 0 {
				argsJSON = []byte("{}")
			}

			call := &api.ToolCall{
				CallID:    fmt.Sprintf("ing-%s-%d", req.Params.Name, time.Now().UnixNano()),
				Principal: principalFromContext(sessionFromCtx(ctx)),
				ToolID:    "gw." + toolName,
				Resource:  "gw",
				Action:    actionFor(toolName),
				Arguments: argsJSON,
			}
			res, dec, err := ing.Gateway.Handle(ctx, call)
			if err != nil {
				return nil, err
			}
			if dec.Final == api.VerdictBlock {
				reason := dec.Rationale
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "BLOCKED by Agent Security Gateway: " + reason}},
					IsError: true,
				}, nil
			}
			out := ""
			if res != nil {
				out = string(res.Output)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: out}},
			}, nil
		})
	}
	return srv
}

// Serve starts the HTTP listener. It dials the upstream first so the published
// tool list is the real one.
func Serve(ctx context.Context, listen string, gw *proxy.Gateway, upstreamCmd []string) error {
	surface, err := DialUpstream(ctx, upstreamCmd)
	if err != nil {
		return fmt.Errorf("dial upstream for ingress: %w", err)
	}
	defer surface.Close()

	ing := &Ingress{Gateway: gw, Mux: http.NewServeMux()}
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return ing.mcpServer(surface)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	ing.Mux.Handle("/mcp", handler)
	ing.Mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("[ingress] gateway MCP server listening on %s (endpoint /mcp)", listen)
	srv := &http.Server{Addr: listen, Handler: ing.Mux}
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// sessionFromCtx extracts an MCP session id when present (stateless mode may
// not have one); falls back to a stable demo session.
func sessionFromCtx(_ context.Context) string { return "ingress-default" }

// actionFor maps a tool name to the coarse action verb used by receipts.
func actionFor(name string) string {
	switch {
	case contains([]string{"delete_user"}, name):
		return "delete"
	case contains([]string{"send_email", "http_post", "export_all_users"}, name):
		return "network"
	case contains([]string{"read_secret", "get_inbox", "read_customer_db"}, name):
		return "read"
	default:
		return "write"
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

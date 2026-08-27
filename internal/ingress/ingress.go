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
	"github.com/dedarek/agent-security-gateway/internal/authn"
	"github.com/dedarek/agent-security-gateway/internal/proxy"
)

// Ingress serves the gateway-facing MCP server.
type Ingress struct {
	Gateway        *proxy.Gateway
	Mux            *http.ServeMux
	Auth           *authn.Registry // nil => bootstrap single dev tenant
	AllowAnonymous bool
}

// principalFor maps an authenticated tenant to the decision-plane identity.
// When the caller passes X-ASG-Session the value is adopted so that probe
// LLM traces and MCP tool calls land in the same session (enables end-to-end
// taint). Otherwise we fall back to the coarse tenant-scoped session.
func principalFor(t authn.Tenant) api.Principal {
	return principalForWithSession(t, "")
}

func principalForWithSession(t authn.Tenant, session string) api.Principal {
	sid := session
	if sid == "" {
		sid = "tenant-" + t.Name
	}
	return api.Principal{
		UserID:    t.UserID,
		AgentID:   t.Name,
		SessionID: sid,
		Role:      t.Role,
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

			// Multi-tenant identity: resolve the caller from the transport
			// (Authorization header travels in the HTTP request context).
			tenant := authn.BootstrapTenant()
			if ing.Auth != nil {
				var ok bool
				tenant, ok = ing.Auth.Authenticate(headerFromCtx(ctx))
				if !ok {
					if ing.AllowAnonymous {
						tenant = authn.Tenant{Name: "public", Role: "employee", UserID: "public", Enabled: true}
					} else {
						return &mcp.CallToolResult{
							Content: []mcp.Content{&mcp.TextContent{Text: "unauthorized: unknown or disabled API key"}},
							IsError: true,
						}, nil
					}
				}
			}
			principal := principalForWithSession(tenant, sessionFromCtx(ctx))
			if agentID := agentIDFromCtx(ctx); agentID != "" {
				principal.AgentID = agentID
			}
			call := &api.ToolCall{
				CallID:    fmt.Sprintf("ing-%s-%d", req.Params.Name, time.Now().UnixNano()),
				Principal: principal,
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
func Serve(ctx context.Context, listen string, gw *proxy.Gateway, upstreamCmd []string, authRegistry *authn.Registry) error {
	handler, closeFn, err := NewHandler(ctx, gw, upstreamCmd, authRegistry, false)
	if err != nil {
		return err
	}
	defer closeFn()

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("[ingress] gateway MCP server listening on %s (endpoint /mcp)", listen)
	srv := &http.Server{Addr: listen, Handler: mux}
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// NewHandler builds a reusable MCP HTTP handler. The public WebUI facade uses
// the same decision engine without exposing the central :8080 listener.
func NewHandler(ctx context.Context, gw *proxy.Gateway, upstreamCmd []string, authRegistry *authn.Registry, allowAnonymous bool) (http.Handler, func(), error) {
	surface, err := DialUpstream(ctx, upstreamCmd)
	if err != nil {
		return nil, func() {}, fmt.Errorf("dial upstream for ingress: %w", err)
	}
	ing := &Ingress{Gateway: gw, Mux: http.NewServeMux(), Auth: authRegistry, AllowAnonymous: allowAnonymous}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return ing.mcpServer(surface)
	}, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: allowAnonymous})
	return injectAuthHeader(mcpHandler), func() { _ = surface.Close() }, nil
}

func sessionFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(sessionHeaderKey{}).(string); ok && v != "" {
		return v
	}
	return ""
}

type sessionHeaderKey struct{}

// headerFromCtx pulls the Authorization header out of the request-scoped
// context installed by the streamable HTTP handler.
func headerFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(authHeaderKey{}).(string); ok {
		return v
	}
	return ""
}

type authHeaderKey struct{}

// injectAuthHeader copies Authorization + optional X-ASG-Session and X-ASG-Agent-ID.
func injectAuthHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), authHeaderKey{}, r.Header.Get("Authorization"))
		if sid := r.Header.Get("X-ASG-Session"); sid != "" {
			ctx = context.WithValue(ctx, sessionHeaderKey{}, sid)
		}
		if aid := r.Header.Get("X-ASG-Agent-ID"); aid != "" {
			ctx = context.WithValue(ctx, agentIDHeaderKey{}, aid)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type agentIDHeaderKey struct{}

func agentIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(agentIDHeaderKey{}).(string); ok {
		return v
	}
	return ""
}

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

// Package mcpproxy is the real MCP proxy layer: the gateway connects to an
// upstream MCP server over the real MCP wire protocol (JSON-RPC over stdio) and
// forwards approved tool calls to it. This replaces the previous in-memory
// forwarder stub. See docs/ARCHITECTURE.md §7.
package mcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dedarek/agent-security-gateway/api"
)

// Upstream is a live MCP client session to a real upstream MCP server.
type Upstream struct {
	client  *mcp.Client
	session *mcp.ClientSession
	cmd     *exec.Cmd
}

// Dial spawns the upstream MCP server process and completes the MCP handshake.
// command is argv, e.g. []string{"./bin/upstream-mcp"}.
func Dial(ctx context.Context, command []string) (*Upstream, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty upstream command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	transport := &mcp.CommandTransport{Command: cmd}

	client := mcp.NewClient(&mcp.Implementation{Name: "agent-security-gateway", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect upstream: %w", err)
	}
	return &Upstream{client: client, session: session, cmd: cmd}, nil
}

// ListTools returns the upstream tool names (real tools/list over MCP).
func (u *Upstream) ListTools(ctx context.Context) ([]string, error) {
	res, err := u.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Tools))
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// Forward implements proxy.Forwarder: it issues a real MCP tools/call to the
// upstream server. The gateway tool id may be namespaced ("email.get_inbox");
// the MCP tool name is the last segment ("get_inbox").
func (u *Upstream) Forward(ctx context.Context, c *api.ToolCall) (*api.ToolResult, error) {
	var args map[string]any
	if len(c.Arguments) > 0 {
		if err := json.Unmarshal(c.Arguments, &args); err != nil {
			return nil, fmt.Errorf("bad tool arguments: %w", err)
		}
	}
	res, err := u.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName(c.ToolID),
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp call %s: %w", c.ToolID, err)
	}
	out := collectText(res)
	return &api.ToolResult{CallID: c.CallID, Output: []byte(out), Error: res.IsError}, nil
}

// Close terminates the upstream session and process.
func (u *Upstream) Close() error {
	if u.session != nil {
		return u.session.Close()
	}
	return nil
}

func toolName(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

func collectText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

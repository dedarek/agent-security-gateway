// UpstreamTools is the tool-surface view of the upstream MCP server that the
// ingress re-publishes to agents.
package ingress

import (
	"context"
	"fmt"
)

// DialUpstream spawns the upstream MCP server and returns its tool surface
// (names, descriptions, input schemas) fetched over a real tools/list.
func DialUpstream(ctx context.Context, command []string) (*upstreamSurface, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty upstream command")
	}
	up, err := dial(ctx, command)
	if err != nil {
		return nil, err
	}
	res, err := up.Session().ListTools(ctx, nil)
	if err != nil {
		_ = up.Close()
		return nil, fmt.Errorf("upstream tools/list: %w", err)
	}
	names := make([]string, 0, len(res.Tools))
	desc := map[string]string{}
	schema := map[string]any{}
	for _, t := range res.Tools {
		names = append(names, t.Name)
		desc[t.Name] = t.Description
		schema[t.Name] = t.InputSchema
	}
	return &upstreamSurface{up: up, names: names, desc: desc, schema: schema}, nil
}

type upstreamSurface struct {
	up     *upClient
	names  []string
	desc   map[string]string
	schema map[string]any
}

func (u *upstreamSurface) ToolNames() []string             { return u.names }
func (u *upstreamSurface) ToolDescription(n string) string { return u.desc[n] }
func (u *upstreamSurface) ToolSchema(n string) any {
	if s, ok := u.schema[n]; ok && s != nil {
		return s
	}
	return map[string]any{"type": "object"}
}

// Close terminates the upstream session and process.
func (u *upstreamSurface) Close() error { return u.up.Close() }

package ingress

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upClient wraps a live MCP client session to the upstream server.
type upClient struct {
	session *mcp.ClientSession
	cmd     *exec.Cmd
}

func dial(ctx context.Context, command []string) (*upClient, error) {
	cmd := exec.Command(command[0], command[1:]...)
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-security-gateway", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect upstream: %w", err)
	}
	return &upClient{session: session, cmd: cmd}, nil
}

// Session exposes the raw client session (used to fetch full tool metadata).
func (u *upClient) Session() *mcp.ClientSession { return u.session }

// Close terminates the upstream session and process.
func (u *upClient) Close() error {
	if u.session != nil {
		return u.session.Close()
	}
	return nil
}

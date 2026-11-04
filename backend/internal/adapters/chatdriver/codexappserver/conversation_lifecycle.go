package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ReloadMCPServers restarts the provider's tool servers and reports their state.
//
// Worth a typed operation because of the failure it addresses: a server that failed
// to start stays failed for the life of the app-server process, so without this the
// only way to recover a tool the agent needs is to throw the conversation away. The
// provider re-announces every server's startup state as notifications afterwards, so
// the returned list is a convenience for the caller that asked, not the only path by
// which Tallpine learns the outcome.
func (c *conversation) ReloadMCPServers(ctx context.Context) (ports.ChatMCPReloadResult, error) {
	// config/mcpServer/reload takes no params, verified against a live app-server.
	if err := c.conn.request(ctx, codexproto.MethodConfigMcpServerReload, map[string]any{}, nil); err != nil {
		return ports.ChatMCPReloadResult{}, fmt.Errorf("%s: %w", codexproto.MethodConfigMcpServerReload, err)
	}

	// toolsAndAuthOnly avoids the resource schemas from the full response while
	// using the provider-declared enum value. "summary" was never part of the app
	// server protocol and made an otherwise successful reload look unenumerated.
	detail := codexproto.McpServerStatusDetailToolsAndAuthOnly
	threadID := c.thread()
	var cursor *string
	servers := make([]ports.ChatMCPServer, 0)
	seen := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	for {
		var resp codexproto.ListMcpServerStatusResponse
		params := codexproto.ListMcpServerStatusParams{
			Cursor:   cursor,
			Detail:   &detail,
			ThreadID: &threadID,
		}
		if err := c.conn.request(ctx, codexproto.MethodMcpServerStatusList, params, &resp); err != nil {
			// The reload itself succeeded. Mark the inventory unavailable so the
			// controller retains known state rather than mistaking failure for an
			// authoritative empty configuration.
			c.log.Debug("mcp server list after reload failed", "error", err)
			return ports.ChatMCPReloadResult{}, nil
		}
		for _, entry := range resp.Data {
			// serverInfo is supplied by the MCP initialize handshake. A configured
			// server whose startup failed can still have a status-list row, but it
			// must remain failed via its startup notification rather than being
			// promoted to ready merely because configuration knows its name.
			if entry.Name == "" || entry.ServerInfo == nil {
				continue
			}
			if _, duplicate := seen[entry.Name]; duplicate {
				continue
			}
			seen[entry.Name] = struct{}{}
			// A server that answers the inventory call is running. Failed enabled
			// servers arrive through startup-status notifications and are reconciled
			// by the controller with this complete ready-server inventory.
			servers = append(servers, ports.ChatMCPServer{
				Name:   entry.Name,
				Status: string(codexproto.McpServerStartupStateReady),
			})
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		if _, repeated := seenCursors[*resp.NextCursor]; repeated {
			c.log.Debug("mcp server list after reload repeated its cursor", "cursor", *resp.NextCursor)
			return ports.ChatMCPReloadResult{}, nil
		}
		seenCursors[*resp.NextCursor] = struct{}{}
		cursor = resp.NextCursor
	}
	return ports.ChatMCPReloadResult{Servers: servers, Authoritative: true}, nil
}

// Close releases the controller without touching provider-side history.
func (c *conversation) Close() error {
	c.closeOnce.Do(func() {
		if c.proc.terminate == nil {
			c.failPendingApprovals()
		}
		if c.proc.stop != nil {
			c.closeErr = c.proc.stop()
		}
	})
	return c.closeErr
}

// Terminate destroys the provider host. Close only detaches and is used by
// daemon-wide shutdown; explicit session/controller replacement calls this.
func (c *conversation) Terminate() error {
	c.closeOnce.Do(func() {
		c.failPendingApprovals()
		if c.proc.terminate != nil {
			c.closeErr = c.proc.terminate()
		} else if c.proc.stop != nil {
			c.closeErr = c.proc.stop()
		}
	})
	return c.closeErr
}

// CanHibernate reads Codex's live exec registry, since a settled turn can leave
// a terminal running and terminating app-server also terminates those commands.
func (c *conversation) CanHibernate(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Subset of Codex 0.160.1's experimental background-terminal response.
	var resp struct {
		Data       []json.RawMessage `json:"data"`
		NextCursor *string           `json:"nextCursor"`
	}
	if err := c.conn.request(ctx, "thread/backgroundTerminals/list", map[string]any{"threadId": c.thread(), "limit": 1}, &resp); err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return false, nil // Older builds cannot prove that background work ended.
		}
		return false, err
	}
	if resp.Data == nil {
		return false, errors.New("background-terminal response is missing data")
	}
	return len(resp.Data) == 0 && (resp.NextCursor == nil || *resp.NextCursor == ""), nil
}

// Hibernate stops the app-server but leaves its native thread on disk for Resume.
func (c *conversation) Hibernate() error { return c.Terminate() }

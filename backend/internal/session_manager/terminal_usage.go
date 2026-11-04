package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TerminalModelUsage reads a terminal-mode session's spend from its agent's
// transcript. supported is false for a chat-mode session, an agent without a
// transcript reader, or a session whose native id has not landed yet.
func (m *Manager) TerminalModelUsage(ctx context.Context, id domain.SessionID) (usage []domain.ModelUsage, supported bool, err error) {
	rec, reader, ok, err := terminalCapability[ports.TerminalUsageReader](ctx, m, id)
	if err != nil || !ok || rec.Metadata.AgentSessionID == "" {
		return nil, false, err
	}
	return reader.TerminalModelUsage(ctx, rec.Metadata.AgentSessionID)
}

//go:build !windows

package codex

import "context"

func (m *codexAccountManager) prepareLoginTerminalForCommit(_ context.Context, _ string) error {
	return nil
}

package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// AutoHandoffOrchestrator is the idle handoff (B374): the daemon writes the
// fixed bootstrap note, domain.AutoHandoffNote, into the retiring orchestrator's
// artifact directory and runs the same HandoffOrchestrator flow as
// `tallpine orchestrator handoff --note`. The note carries no chat summary: the
// successor rebuilds its state from the daemon, the record of state.
func (s *Service) AutoHandoffOrchestrator(ctx context.Context, id domain.SessionID) error {
	path, err := s.writeAutoHandoffNote(id)
	if err != nil {
		return err
	}
	_, err = s.HandoffOrchestrator(ctx, id, path)
	return err
}

// writeAutoHandoffNote writes domain.AutoHandoffNote into the orchestrator's
// artifact directory and returns its path.
func (s *Service) writeAutoHandoffNote(id domain.SessionID) (string, error) {
	dir := sessionartifacts.Dir(s.dataDir, id)
	if dir == "" {
		return "", fmt.Errorf("auto handoff %s: no artifact directory", id)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("auto handoff %s: %w", id, err)
	}
	name := "auto-handoff-" + time.Now().UTC().Format("20060102-150405") + ".md"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(domain.AutoHandoffNote(id)), 0o600); err != nil {
		return "", fmt.Errorf("auto handoff %s: %w", id, err)
	}
	return path, nil
}

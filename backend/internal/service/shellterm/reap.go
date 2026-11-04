package shellterm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// ReapShellTerminalsFromPreviousAppRuns prunes confirmed-dead user shells and
// destroys abandoned trusted auth terminals. Live or unknown user runtimes
// are preserved with their original associations and attach handles.
func (s *Service) ReapShellTerminalsFromPreviousAppRuns(ctx context.Context) (int64, error) {
	orphans, err := s.store.SelectShellTerminalsFromPreviousAppRuns(ctx, s.appRunID)
	if err != nil {
		return 0, fmt.Errorf("reap shell terminals: %w", err)
	}
	var cleared int64
	for _, rec := range orphans {
		if !rec.Transient {
			alive, probeErr := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
			if probeErr != nil {
				s.log.Warn("shell terminal liveness probe failed; keeping row", "handleId", rec.HandleID, "error", probeErr)
				continue
			}
			if alive {
				continue
			}
		}
		stillAlive, destroyErr := s.destroyConfirmed(ctx, rec)
		if stillAlive {
			s.log.Warn("reaping orphaned shell terminal: runtime still alive after destroy",
				"handleId", rec.HandleID, "appRunId", rec.AppRunID, "error", destroyErr)
			continue
		}
		cleared++
	}
	if cleared > 0 {
		s.log.Info("reaped shell terminals from previous app runs", "count", cleared)
	}
	return cleared, nil
}

// destroyConfirmed handles explicit teardown and confirmed child exit. It
// destroys the runtime and deletes the row only once host death is confirmed,
// so CloseShellTerminal, ReapShellTerminalsFromPreviousAppRuns,
// and BeginSessionTeardown can't each independently forget a shell that
// actually survived.
//
//   - A clean Destroy is confirmed dead.
//   - A Destroy error is followed by an IsAlive check; an IsAlive error is
//     treated the same as "alive" (unknown state must never let a live shell's
//     row vanish) — only an explicit "not alive" counts as confirmed dead.
//
// Returns stillAlive=true when the row was deliberately kept because death
// could not be confirmed. Callers that need 404-for-unknown-handle semantics
// (CloseShellTerminal) look the row up themselves beforehand — by the time
// destroyConfirmed runs, the handle is already known to exist.
func (s *Service) destroyConfirmed(ctx context.Context, rec ShellTerminalRecord) (stillAlive bool, destroyErr error) {
	handleID := rec.HandleID
	stillAlive, destroyErr = s.destroyRuntimeConfirmed(ctx, ports.RuntimeHandle{ID: handleID})
	if stillAlive {
		return true, destroyErr
	}
	if deleted, err := s.store.DeleteShellTerminalByHandleID(ctx, handleID); err != nil {
		s.log.Warn("shell terminal: delete row after destroy failed", "handleId", handleID, "error", err)
	} else if deleted {
		s.cleanupAuthWorkspace(rec.WorkingDir, rec.HandleID)
	}
	return false, nil
}

func (s *Service) destroyRuntimeConfirmed(ctx context.Context, handle ports.RuntimeHandle) (stillAlive bool, destroyErr error) {
	destroyErr = s.runtime.Destroy(ctx, handle)
	if destroyErr == nil {
		return false, nil
	}
	alive, aliveErr := s.runtime.IsAlive(ctx, handle)
	if aliveErr != nil || alive {
		return true, destroyErr
	}
	return false, destroyErr
}

func (s *Service) cleanupAuthWorkspace(workingDir, handleID string) {
	root := filepath.Clean(filepath.Join(s.dataDir, authWorkspaceDirectoryName))
	rel, err := filepath.Rel(root, filepath.Clean(workingDir))
	if err != nil || !strings.HasPrefix(rel, "shellterm-") || filepath.IsAbs(rel) || filepath.Dir(rel) != "." {
		return
	}
	// Runtime handles are opaque and some adapters wrap the requested launch
	// id (for example ptyhost-v1:shellterm-* on macOS). Only delete the direct
	// child whose generated id is the complete handle or its final component.
	if handleID != rel && !strings.HasSuffix(handleID, ":"+rel) {
		return
	}
	if err := os.RemoveAll(workingDir); err != nil {
		s.log.Warn("shell terminal: remove private auth workspace failed", "workingDir", workingDir, "error", err)
	}
}

package session

import (
	"context"
	"errors"
	"os"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// sessionDeleteCommander is optional so focused fakes keep implementing the
// stable commander surface.
type sessionDeleteCommander interface {
	DeleteSession(ctx context.Context, id domain.SessionID, cleanup bool) error
}

// Delete permanently removes a terminated session whose workspace is
// released, then its artifact files; with cleanup it first releases a
// worktree still on disk, never forced. The record goes first: a refused
// delete must not have already thrown the artifacts away.
func (s *Service) Delete(ctx context.Context, id domain.SessionID, cleanup bool) error {
	deleter, ok := s.manager.(sessionDeleteCommander)
	if !ok {
		return apierr.NotImplemented("SESSION_DELETE_UNSUPPORTED", "Session deletion is not supported by this daemon")
	}
	// Read before the record goes: a session created before the per-project
	// layout keeps its artifacts at the flat path its row names.
	dir := sessionartifacts.ResolveForID(ctx, s.dataDir, s.store, id, true)
	if err := deleter.DeleteSession(ctx, id, cleanup); err != nil {
		return toAPIError(err)
	}
	s.cancelTitleRefinement(id)
	s.deleteArtifactDir(ctx, id, dir)
	return nil
}

// deleteArtifactDir removes a deleted session's artifact directory. Failures
// are logged, not returned: the record is already gone.
func (s *Service) deleteArtifactDir(ctx context.Context, id domain.SessionID, dir string) {
	if s.artifactDeleter == nil || dir == "" {
		return
	}
	// The deleter leaves an empty directory for a restore that cannot happen
	// now, so remove that too.
	var err error
	if d, ok := s.artifactDeleter.(interface {
		DeleteDir(ctx context.Context, id domain.SessionID, dir string) error
	}); ok {
		err = d.DeleteDir(ctx, id, dir)
	} else {
		err = s.artifactDeleter.DeleteSession(ctx, id)
	}
	if err == nil {
		err = os.Remove(dir)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && s.logger != nil {
		s.logger.Warn("delete artifacts of deleted session", "session", id, "err", err)
	}
}

package project

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// projectStoreError maps a project store failure to its API error: a project
// whose encrypted env cannot be opened (hardened profile, key unavailable) is
// 409 PROJECT_ENV_LOCKED; anything else is the given 500.
func projectStoreError(err error, code, message string) error {
	if errors.Is(err, ports.ErrProjectEnvLocked) {
		return apierr.Conflict(ports.CodeProjectEnvLocked, ports.ProjectEnvLockedMessage, nil)
	}
	return apierr.Internal(code, message)
}

// envClearer is the store's recovery for a project whose encrypted env key is
// gone (sqlite Store.ClearProjectEnv).
type envClearer interface {
	ClearProjectEnv(ctx context.Context, id string) (bool, error)
}

// getProjectForRemoval reads the project to remove. When its encrypted env
// cannot be opened (key deleted or unavailable), removal clears the encrypted
// values and proceeds: removing is the documented way out of a locked project,
// after which it can be re-added and its values re-entered.
func (m *Service) getProjectForRemoval(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, bool, error) {
	row, ok, err := m.store.GetProject(ctx, string(id))
	clearer, can := m.store.(envClearer)
	if err == nil || !errors.Is(err, ports.ErrProjectEnvLocked) || !can {
		return row, ok, err
	}
	if _, clearErr := clearer.ClearProjectEnv(ctx, string(id)); clearErr != nil {
		return row, ok, errors.Join(err, clearErr)
	}
	row, ok, err = m.store.GetProject(ctx, string(id))
	if raw, can := m.store.(rawProjectReader); can && errors.Is(err, ports.ErrProjectEnvLocked) {
		// Legacy plaintext env that cannot be sealed yet: nothing to clear, and
		// archiving needs no env, so read the row without it (values stay stored).
		return raw.GetProjectWithoutEnv(ctx, string(id))
	}
	return row, ok, err
}

// rawProjectReader reads a project row without opening its env
// (sqlite Store.GetProjectWithoutEnv).
type rawProjectReader interface {
	GetProjectWithoutEnv(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

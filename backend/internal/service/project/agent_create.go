package project

import (
	"context"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

// creatorStore is the part of the store an agent-created project needs: the
// creator row and every registered path.
type creatorStore interface {
	SetProjectCreator(ctx context.Context, project, creator domain.ProjectID, at time.Time) error
	ListAllProjectPaths(ctx context.Context) ([]string, error)
}

// checkAgentCreate applies the rules for a project an orchestrator registers
// (agent policy crossProjectOrchestration): the id must be new in every state
// (an agent never adopts or revives a person's project), and the path may not be
// in the daemon data dir or equal to, inside, or containing any registered project. A derived
// id that is taken becomes a free suggestion; an explicit one is a conflict.
// It returns the store that records the creator and the id to use.
func (m *Service) checkAgentCreate(ctx context.Context, in AddInput, path string, id domain.ProjectID) (creatorStore, domain.ProjectID, error) {
	creators, ok := m.store.(creatorStore)
	if !ok {
		return nil, id, apierr.Internal("PROJECT_ADD_FAILED", "Failed to register project")
	}
	target := resolvedPath(path)
	for _, dir := range m.protectedDirs {
		if pathx.Within(resolvedPath(dir), target) {
			return nil, id, apierr.Invalid("PATH_NOT_ALLOWED", "Agents may not register a project inside the Tallpine data directory.", nil)
		}
	}
	paths, err := creators.ListAllProjectPaths(ctx)
	if err != nil {
		return nil, id, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	for _, other := range paths {
		root := resolvedPath(other)
		if pathx.Within(root, target) || pathx.Within(target, root) {
			return nil, id, apierr.Invalid("PATH_NOT_ALLOWED", "Agents may not register a path equal to, inside, or containing another project.", nil)
		}
	}
	if _, exists, err := m.store.GetProject(ctx, string(id)); err != nil {
		return nil, id, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	} else if exists {
		if in.ProjectID != nil {
			return nil, id, apierr.Conflict("ID_ALREADY_REGISTERED", "A project with this id exists or existed", nil)
		}
		id = m.suggestID(ctx, id)
	}
	return creators, id, nil
}

// resolvedPath resolves symlinks when it can, so a link cannot hide a location.
func resolvedPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

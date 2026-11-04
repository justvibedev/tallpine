package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// UpsertProject inserts or replaces a registered project row.
func (s *Store) UpsertProject(ctx context.Context, r domain.ProjectRecord) error {
	ctx = s.primeEnvKey(ctx, r.ID)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cfg, err := s.keepLocked(ctx, s.qw, r.ID, r.Config)
	if err != nil {
		return err
	}
	config, err := s.encodeProjectConfig(ctx, s.qw, r.ID, cfg)
	if err != nil {
		return err
	}
	return upsertProject(ctx, s.qw, r, config)
}

// UpsertWorkspaceProject inserts or replaces a workspace project and its child
// repository registry in one transaction. The child set is authoritative.
func (s *Store) UpsertWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	return s.writeWorkspaceProject(ctx, "upsert workspace project", r, repos, func(q *gen.Queries, encoded sql.NullString) error {
		return upsertProject(ctx, q, r, encoded)
	})
}

// ImportWorkspaceProject inserts or replaces a workspace project from an
// authoritative import source, including the source registration timestamp.
func (s *Store) ImportWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	return s.writeWorkspaceProject(ctx, "import workspace project", r, repos, func(q *gen.Queries, encoded sql.NullString) error {
		return importProject(ctx, q, r, encoded)
	})
}

func (s *Store) writeWorkspaceProject(ctx context.Context, label string, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord, writeProject func(*gen.Queries, sql.NullString) error) error {
	ctx = s.primeEnvKey(ctx, r.ID)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, label, func(q *gen.Queries) error {
		cfg, err := s.keepLocked(ctx, q, r.ID, r.Config)
		if err != nil {
			return err
		}
		encoded, err := s.encodeProjectConfig(ctx, q, r.ID, cfg)
		if err != nil {
			return err
		}
		if err := writeProject(q, encoded); err != nil {
			return err
		}
		if err := q.DeleteWorkspaceReposByProject(ctx, domain.ProjectID(r.ID)); err != nil {
			return err
		}
		for _, repo := range repos {
			if err := q.UpsertWorkspaceRepo(ctx, gen.UpsertWorkspaceRepoParams{
				ProjectID:     domain.ProjectID(r.ID),
				Name:          repo.Name,
				RelativePath:  repo.RelativePath,
				RepoOriginURL: repo.RepoOriginURL,
				DefaultBranch: repo.DefaultBranch,
				RegisteredAt:  repo.RegisteredAt,
				GitStatus:     string(repo.GitStatus.WithDefault()),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListWorkspaceRepos returns the registered direct child repos for a workspace project.
func (s *Store) ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	rows, err := s.qr.ListWorkspaceRepos(ctx, domain.ProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list workspace repos for %s: %w", projectID, err)
	}
	out := make([]domain.WorkspaceRepoRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.WorkspaceRepoRecord{
			ProjectID:     row.ProjectID,
			Name:          row.Name,
			RelativePath:  row.RelativePath,
			RepoOriginURL: row.RepoOriginURL,
			DefaultBranch: row.DefaultBranch,
			RegisteredAt:  row.RegisteredAt,
			GitStatus:     domain.GitStatus(row.GitStatus),
		})
	}
	return out, nil
}

func upsertProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	kind := r.Kind.WithDefault()
	return q.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

func importProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	existingByID, err := q.GetProject(ctx, domain.ProjectID(r.ID))
	if err == nil {
		if existingByID.ArchivedAt.Valid {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDArchivedTarget,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
		if existingByID.Path != r.Path {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDDifferentActivePath,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project id conflict: %w", err)
	}

	existingByPath, err := q.FindProjectByPath(ctx, r.Path)
	if err == nil {
		if existingByPath.ID != domain.ProjectID(r.ID) {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSamePathDifferentActiveID,
				TargetID:   string(existingByPath.ID),
				TargetPath: existingByPath.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project path conflict: %w", err)
	}

	kind := r.Kind.WithDefault()
	return q.UpsertImportedProject(ctx, gen.UpsertImportedProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

// SetManaged makes every project read return the config with the managed
// settings applied, and every project write keep the stored value of a locked
// field, so removing the profile restores the user's own values. Call it once,
// before the store is shared.
func (s *Store) SetManaged(l *managedcfg.Locks) { s.managed = l }

// keepLocked returns cfg with each locked field taken from the raw (not
// overlaid) stored row of project id. Only a missing row means "nothing
// stored"; any other read error is returned so a managed value is never saved
// as the user's own. Call it with the write lock held.
func (s *Store) keepLocked(ctx context.Context, q *gen.Queries, id string, cfg domain.ProjectConfig) (domain.ProjectConfig, error) {
	if s.managed == nil {
		return cfg, nil
	}
	stored, err := q.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read stored project %s: %w", id, err)
	}
	return s.managed.RestoreLocked(unmarshalProjectConfig(stored.Config), cfg), nil
}

// projectRow decodes a project row, opens its env and applies the managed
// overlay. A sealed env it cannot open fails with ports.ErrProjectEnvLocked.
func (s *Store) projectRow(ctx context.Context, p gen.Project) (domain.ProjectRecord, error) {
	r, err := s.openProjectRow(ctx, p)
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	r.Config = s.managed.ApplyProject(r.Config)
	return r, nil
}

// GetProject returns a project by id, active or archived.
func (s *Store) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	r, err := s.projectRow(ctx, p)
	if err != nil {
		return domain.ProjectRecord{}, false, err
	}
	return r, true, nil
}

// FindProjectByPath returns a project registered at path, active or archived.
func (s *Store) FindProjectByPath(ctx context.Context, path string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.FindProjectByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	r, err := s.projectRow(ctx, p)
	if err != nil {
		return domain.ProjectRecord{}, false, err
	}
	return r, true, nil
}

// ListProjects returns active projects ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]domain.ProjectRecord, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.ProjectRecord, 0, len(rows))
	for _, p := range rows {
		r, err := s.projectRow(ctx, p)
		if err != nil {
			// A locked env must not hide the project from the list; the row is
			// listed without env (never ciphertext), and launches and writes,
			// which read or check the row itself, fail closed.
			r = projectRowFromGen(p)
			r.Config.Env = nil
			r.Config = s.managed.ApplyProject(r.Config)
		}
		out = append(out, r)
	}
	return out, nil
}

// UpdateProjectSettings atomically updates the user-facing display name and
// config for an active project. It returns ok=false when the project is missing
// or archived.
func (s *Store) UpdateProjectSettings(ctx context.Context, id, displayName string, config domain.ProjectConfig) (bool, error) {
	ctx = s.primeEnvKey(ctx, id)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	kept, err := s.keepLocked(ctx, s.qw, id, config)
	if err != nil {
		return false, err
	}
	encodedConfig, err := s.encodeProjectConfig(ctx, s.qw, id, kept)
	if err != nil {
		return false, err
	}
	rows, err := s.qw.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{
		ID:          domain.ProjectID(id),
		DisplayName: displayName,
		Config:      encodedConfig,
	})
	if err != nil {
		return false, fmt.Errorf("update project settings %s: %w", id, err)
	}
	return rows > 0, nil
}

// CountProjectsIncludingArchived returns all registry rows, including projects
// the user archived. It is intentionally separate from ListProjects so first-run
// seeding does not recreate Scratch after any project has existed.
func (s *Store) CountProjectsIncludingArchived(ctx context.Context) (int, error) {
	count, err := s.qr.CountProjectsIncludingArchived(ctx)
	if err != nil {
		return 0, fmt.Errorf("count projects including archived: %w", err)
	}
	return int(count), nil
}

// ArchiveProject soft-deletes a project and reports whether a row was affected.
// Its creator row goes in the same transaction, so a later project that reuses
// the id never inherits the old creator's rights.
func (s *Store) ArchiveProject(ctx context.Context, id string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var archived bool
	err := s.inTx(ctx, "archive project", func(q *gen.Queries) error {
		n, err := q.ArchiveProject(ctx, gen.ArchiveProjectParams{
			ArchivedAt: nullTime(at),
			ID:         domain.ProjectID(id),
		})
		if err != nil {
			return err
		}
		archived = n > 0
		return q.DeleteProjectCreator(ctx, id)
	})
	if err != nil {
		return false, err
	}
	return archived, nil
}

func projectRowFromGen(p gen.Project) domain.ProjectRecord {
	r := domain.ProjectRecord{
		ID:            string(p.ID),
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginURL,
		DisplayName:   p.DisplayName,
		RegisteredAt:  p.RegisteredAt,
		Kind:          domain.ProjectKind(p.Kind).WithDefault(),
		Config:        unmarshalProjectConfig(p.Config),
	}
	if p.ArchivedAt.Valid {
		r.ArchivedAt = p.ArchivedAt.Time
	}
	return r
}

// marshalProjectConfig encodes the typed per-project config into the nullable
// JSON column. An IsZero config stores SQL NULL so an unset config round-trips
// back to a zero value rather than an empty object.
func marshalProjectConfig(cfg domain.ProjectConfig) (sql.NullString, error) {
	if cfg.IsZero() {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal project config: %w", err)
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// unmarshalProjectConfig decodes the nullable JSON column back into the typed
// struct. SQL NULL (an unset config) decodes to a zero value. A damaged config
// (invalid JSON from a direct DB edit or migration bug) also degrades to a zero
// config rather than erroring — a corrupt config must never block access to the
// project row, nor fail an entire ListProjects.
func unmarshalProjectConfig(s sql.NullString) domain.ProjectConfig {
	if !s.Valid || s.String == "" {
		return domain.ProjectConfig{}
	}
	var cfg domain.ProjectConfig
	if err := json.Unmarshal([]byte(s.String), &cfg); err != nil {
		return domain.ProjectConfig{}
	}
	return cfg
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// SetProjectPermissions serializes a focused read-modify-write with other writes.
func (s *Store) SetProjectPermissions(ctx context.Context, id string, permissions domain.PermissionMode) (domain.ProjectRecord, bool, error) {
	ctx = s.primeEnvKey(ctx, id)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row domain.ProjectRecord
	var updated bool
	err := s.inTx(ctx, "set project permissions", func(q *gen.Queries) error {
		stored, err := q.GetProject(ctx, domain.ProjectID(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		row = projectRowFromGen(stored)
		if !row.ArchivedAt.IsZero() {
			return nil
		}
		// Sessions are pinned to what applies today (managed overlay included).
		effective := s.managed.ApplyProject(row.Config)
		worker := effective.AgentConfig.Permissions
		if effective.Worker.AgentConfig.Permissions != "" {
			worker = effective.Worker.AgentConfig.Permissions
		}
		orchestrator := effective.AgentConfig.Permissions
		if effective.Orchestrator.AgentConfig.Permissions != "" {
			orchestrator = effective.Orchestrator.AgentConfig.Permissions
		}
		if worker == "" {
			worker = domain.PermissionModeDefault
		}
		if orchestrator == "" {
			orchestrator = domain.PermissionModeDefault
		}
		for kind, mode := range map[domain.SessionKind]domain.PermissionMode{domain.KindWorker: worker, domain.KindOrchestrator: orchestrator} {
			if err := q.PinProjectSessionPermissions(ctx, gen.PinProjectSessionPermissionsParams{ProjectID: optionalProjectID(domain.ProjectID(id)), Kind: kind, Permissions: string(mode)}); err != nil {
				return err
			}
		}

		row.Config.AgentConfig.Permissions = permissions
		// A locked role override keeps its stored value; the profile decides it.
		if !s.managed.Locked("project.worker") {
			row.Config.Worker.AgentConfig.Permissions = ""
		}
		if !s.managed.Locked("project.orchestrator") {
			row.Config.Orchestrator.AgentConfig.Permissions = ""
		}
		config, err := marshalProjectConfig(row.Config)
		if err != nil {
			return err
		}
		rows, err := q.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{ID: domain.ProjectID(id), DisplayName: row.DisplayName, Config: config})
		updated = rows > 0
		if err != nil {
			return err
		}
		// The stored env is written back as stored; the caller gets it opened.
		if row.Config.Env, err = s.envCodec.Open(ctx, id, row.Config.Env); err != nil {
			row.Config.Env = nil
		}
		row.Config = s.managed.ApplyProject(row.Config)
		return nil
	})
	return row, updated, err
}

// SetProjectCreator records which project's orchestrator registered a project.
// It never overwrites: a project that already has a creator row is an error.
func (s *Store) SetProjectCreator(ctx context.Context, project, creator domain.ProjectID, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetProjectCreator(ctx, gen.SetProjectCreatorParams{ProjectID: string(project), CreatorProjectID: string(creator), CreatedAt: at})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("project %s already has a creator", project)
	}
	return nil
}

// ListAllProjectPaths returns the path of every registered project, archived
// ones included.
func (s *Store) ListAllProjectPaths(ctx context.Context) ([]string, error) {
	paths, err := s.qr.ListAllProjectPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all project paths: %w", err)
	}
	return paths, nil
}

// ProjectCreator returns the project whose orchestrator registered project;
// ok is false for a project a person registered.
func (s *Store) ProjectCreator(ctx context.Context, project domain.ProjectID) (domain.ProjectID, bool, error) {
	creator, err := s.qr.GetProjectCreator(ctx, string(project))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get project creator %s: %w", project, err)
	}
	return domain.ProjectID(creator), true, nil
}

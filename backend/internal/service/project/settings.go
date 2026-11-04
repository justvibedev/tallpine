package project

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// UpdateSettings atomically replaces the project's stored display name and
// config. Both values are validated before a single database update.
func (m *Service) UpdateSettings(ctx context.Context, id domain.ProjectID, in UpdateSettingsInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	inDisplayName := strings.TrimSpace(in.DisplayName)
	if inDisplayName == "" {
		return Project{}, apierr.Invalid("DISPLAY_NAME_REQUIRED", "Display name is required", nil)
	}
	if err := in.Config.Validate(); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if utf8.RuneCountInString(inDisplayName) > maxDisplayNameLen && inDisplayName != strings.TrimSpace(projectDisplayName(row)) {
		return Project{}, apierr.Invalid("DISPLAY_NAME_TOO_LONG", fmt.Sprintf("Display name must be %d characters or fewer", maxDisplayNameLen), nil)
	}
	if err := in.Config.ValidateAgentRulesCap(row.Config.AgentRules); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	if err := m.managed.Refuse(m.managed.LockedProjectChanges(row.Config, in.Config)...); err != nil {
		return Project{}, err
	}
	if row.Kind.WithDefault() == domain.ProjectKindScratch {
		if err := validateScratchProjectConfig(in.Config); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
	}
	if err := in.Config.ValidateCanonicalRepository(row.RepoOriginURL); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	updated, err := m.store.UpdateProjectSettings(ctx, string(id), inDisplayName, in.Config)
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_SETTINGS_UPDATE_FAILED", "Failed to update project settings")
	}
	if !updated {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	m.recordConfig(ctx, id, row.Config, in.Config)
	row.DisplayName = inDisplayName
	row.Config = in.Config
	return m.projectFromRow(ctx, row), nil
}

// SetConfig replaces the project's stored config. The typed config is validated
// here so a bad value is rejected when set rather than surfacing at spawn.
func (m *Service) SetConfig(ctx context.Context, id domain.ProjectID, in SetConfigInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if err := in.Config.Validate(); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if err := in.Config.ValidateAgentRulesCap(row.Config.AgentRules); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	if err := m.managed.Refuse(m.managed.LockedProjectChanges(row.Config, in.Config)...); err != nil {
		return Project{}, err
	}
	if row.Kind.WithDefault() == domain.ProjectKindScratch {
		if err := validateScratchProjectConfig(in.Config); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
	}
	if err := in.Config.ValidateCanonicalRepository(row.RepoOriginURL); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	before := row.Config
	row.Config = in.Config
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, projectStoreError(err, "PROJECT_CONFIG_UPDATE_FAILED", "Failed to update project config")
	}
	m.recordConfig(ctx, id, before, row.Config)
	return m.projectFromRow(ctx, row), nil
}

func validateScratchProjectConfig(cfg domain.ProjectConfig) error {
	if cfg.CanonicalRepoURL != "" {
		return errors.New("scratch projects do not support canonicalRepoURL")
	}
	if strings.TrimSpace(cfg.DefaultBranch) != "" {
		return errors.New("scratch projects do not support defaultBranch")
	}
	if cfg.TrackerIntake.Enabled {
		return errors.New("scratch projects do not support tracker intake")
	}
	if len(cfg.Reviewers) > 0 {
		return errors.New("scratch projects do not support reviewers")
	}
	return nil
}

// Remove stops live project sessions, reclaims safe managed workspaces, then
// archives the project registration. The original repository path and durable
// session/history rows are preserved.
func (m *Service) Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error) {
	if err := validateProjectID(id); err != nil {
		return RemoveResult{}, err
	}
	row, ok, err := m.getProjectForRemoval(ctx, id)
	if err != nil {
		return RemoveResult{}, projectStoreError(err, "PROJECT_REMOVE_FAILED", "Failed to remove project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return RemoveResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if m.sessions != nil {
		if err := m.sessions.TeardownProject(ctx, id); err != nil {
			return RemoveResult{}, err
		}
	}
	ok, err = m.store.ArchiveProject(ctx, string(id), time.Now())
	if err != nil {
		return RemoveResult{}, projectStoreError(err, "PROJECT_REMOVE_FAILED", "Failed to remove project")
	}
	if !ok {
		return RemoveResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return RemoveResult{ProjectID: id, RemovedStorageDir: false}, nil
}

func (m *Service) projectFromRow(ctx context.Context, row domain.ProjectRecord) Project {
	kind := row.Kind.WithDefault()
	folderMissing := false
	exists, err := folderExists(row.Path)
	if err != nil {
		m.logger.Warn("project: stat failed", "path", row.Path, "error", err)
	} else {
		folderMissing = !exists
	}
	defaultBranch := ""
	if kind != domain.ProjectKindScratch {
		defaultBranch = row.Config.WorktreeBaseBranch()
		if defaultBranch == "" {
			if !folderMissing {
				defaultBranch = resolveDefaultBranch(ctx, row.Path)
			}
			if defaultBranch == "" {
				defaultBranch = domain.DefaultBranchAuto
			}
		}
	}
	p := Project{
		ID:            domain.ProjectID(row.ID),
		Name:          projectDisplayName(row),
		Kind:          kind,
		Path:          row.Path,
		Repo:          row.RepoOriginURL,
		DefaultBranch: defaultBranch,
		Agent:         string(m.defaultHarness),
		FolderMissing: folderMissing,
	}
	p.Config = projectConfigPtr(row.Config)
	p.GitHubAccount = m.githubAccount(ctx, row)
	return p
}

func projectConfigPtr(projectConfig domain.ProjectConfig) *domain.ProjectConfig {
	if projectConfig.IsZero() {
		return nil
	}
	cfg := projectConfig
	return &cfg
}

func projectDisplayName(row domain.ProjectRecord) string {
	if strings.TrimSpace(row.DisplayName) != "" {
		return row.DisplayName
	}
	return row.ID
}

// SetPermissions remembers the approval policy for future sessions of either role.
func (m *Service) SetPermissions(ctx context.Context, id domain.ProjectID, in SetPermissionsInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if in.Permissions == "" || !in.Permissions.Valid() {
		return Project{}, apierr.Invalid("INVALID_PERMISSIONS", "A valid permission mode is required", nil)
	}
	if in.SourceHarness != "" && !in.SourceHarness.IsKnown() {
		return Project{}, apierr.Invalid("INVALID_HARNESS", "Unknown source harness", nil)
	}
	// Codex default grants full access; remember its portable equivalent so
	// another harness does not interpret it as its own manual baseline.
	permissions := in.Permissions
	if permissioncap.Of(m.decide(ctx, id)).ForbidsBypass() {
		if permissions == domain.PermissionModeBypassPermissions {
			return Project{}, apierr.Invalid("POLICY_FORBIDS_BYPASS", "Bypass permissions are disabled by the agent policy", nil)
		}
	} else if in.SourceHarness == domain.HarnessCodex && permissions == domain.PermissionModeDefault {
		permissions = domain.PermissionModeBypassPermissions
	}
	return m.storePermissions(ctx, id, permissions)
}

// ForgetPermissions clears the remembered approval policy. Existing sessions keep theirs.
func (m *Service) ForgetPermissions(ctx context.Context, id domain.ProjectID) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	return m.storePermissions(ctx, id, "")
}

func (m *Service) storePermissions(ctx context.Context, id domain.ProjectID, permissions domain.PermissionMode) (Project, error) {
	// Read for the change log only; the store's own read-modify-write decides.
	previous, _, _ := m.store.GetProject(ctx, string(id))
	row, ok, err := m.store.SetProjectPermissions(ctx, string(id), permissions)
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_PERMISSIONS_UPDATE_FAILED", "Failed to update project permissions")
	}
	if !ok {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	m.recordConfig(ctx, id, previous.Config, row.Config)
	return m.projectFromRow(ctx, row), nil
}

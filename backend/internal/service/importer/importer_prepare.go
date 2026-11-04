package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// PrepareGit executes approved missing Git preparation actions for a project
// import. Actions run in a fixed order and are skipped when already satisfied.
func (m *Manager) PrepareGit(ctx context.Context, in GitPreparationInput) (GitPreparationResult, error) {
	importKind := strings.TrimSpace(in.ImportKind)
	if importKind != ImportKindProject && importKind != ImportKindWorkspace {
		return GitPreparationResult{}, apierr.Invalid("UNSUPPORTED_IMPORT_KIND", "Only project and workspace imports are supported.", map[string]any{"importKind": in.ImportKind})
	}
	validation, err := m.Validate(ctx, ImportValidationInput{ImportKind: importKind, Path: in.Path})
	if err != nil {
		return GitPreparationResult{}, err
	}
	canPrepareFirstWorkspaceRepo := importKind == ImportKindWorkspace &&
		len(in.Repositories) > 0 &&
		len(validation.BlockingErrors) == 1 &&
		validation.BlockingErrors[0] == "WORKSPACE_CHILD_REPO_REQUIRED"
	if !validation.IsValid && !canPrepareFirstWorkspaceRepo {
		return GitPreparationResult{Validation: validation}, nil
	}
	targets, err := preparationTargets(ctx, validation, in)
	if err != nil {
		return GitPreparationResult{}, err
	}
	if in.Stepwise {
		for _, target := range targets {
			if err := validatePreparationTarget(target); err != nil {
				return GitPreparationResult{}, err
			}
		}
	}
	events := []GitPreparationEvent{}
preparation:
	for _, target := range targets {
		if !in.Stepwise {
			if err := validatePreparationTarget(target); err != nil {
				return GitPreparationResult{}, err
			}
		}
		required := actionSet(target.Status.RequiredActions)
		for _, action := range []string{GitPreparationActionInit, GitPreparationActionCommit, GitPreparationActionCreateRemoteRepository, GitPreparationActionSetRemote} {
			if !required[action] {
				continue
			}
			events = append(events,
				GitPreparationEvent{RepoPath: target.Status.RepoPath, Action: action, State: GitPreparationEventPending},
				GitPreparationEvent{RepoPath: target.Status.RepoPath, Action: action, State: GitPreparationEventRunning},
			)
			if actionErr := runGitPreparationAction(ctx, target.Status.RepoPath, action, target.Input); actionErr != nil {
				events = append(events, GitPreparationEvent{RepoPath: target.Status.RepoPath, Action: action, State: GitPreparationEventError, Error: actionErr.Error()})
				latest, _ := m.Validate(ctx, ImportValidationInput{ImportKind: importKind, Path: validation.Root.RepoPath})
				if importKind == ImportKindProject && action == GitPreparationActionCreateRemoteRepository && target.Input.GitHubRepository != nil {
					latest.Root.RequiredActions = []string{GitPreparationActionCreateRemoteRepository}
					latest.NextStep = ImportNextStepPrepareGit
				}
				return GitPreparationResult{Events: events, Validation: latest}, nil //nolint:nilerr // action failures are reported in-band as progress events for partial recovery
			}
			events = append(events, GitPreparationEvent{RepoPath: target.Status.RepoPath, Action: action, State: GitPreparationEventSuccess})
			if in.Stepwise {
				break preparation
			}
		}
	}
	latest, err := m.Validate(ctx, ImportValidationInput{ImportKind: importKind, Path: validation.Root.RepoPath})
	if err != nil {
		return GitPreparationResult{}, err
	}
	if importKind == ImportKindProject && latest.IsValid {
		// Stepwise callers loop on nextStep; keep it at prepare_git until the
		// approved optional remote has run.
		if actions := withOptionalRemoteAction(latest.Root, in); len(actions) > len(latest.Root.RequiredActions) {
			latest.Root.RequiredActions = actions
			latest.NextStep = ImportNextStepPrepareGit
		}
	}
	return GitPreparationResult{Events: events, Validation: latest}, nil
}

func validatePreparationTarget(target gitPreparationTarget) error {
	if unsafeImportPath(target.Status.RepoPath) {
		return apierr.Invalid("IMPORT_PATH_UNSAFE", "Selected folder is too broad for automatic Git setup.", map[string]any{"path": target.Status.RepoPath})
	}
	required := actionSet(target.Status.RequiredActions)
	for action := range required {
		if !containsAction(target.Input.ApprovedActions, action) {
			return apierr.Invalid("IMPORT_ACTION_APPROVAL_REQUIRED", "Every missing Git preparation action requires explicit approval.", map[string]any{"repoPath": target.Status.RepoPath, "action": action})
		}
	}
	if required[GitPreparationActionCreateRemoteRepository] && target.Input.GitHubRepository == nil && strings.TrimSpace(target.Input.RemoteURL) == "" {
		return apierr.Invalid("IMPORT_GITHUB_REPOSITORY_REQUIRED", "GitHub repository owner and name are required before Tallpine can create an origin remote.", map[string]any{"repoPath": target.Status.RepoPath})
	}
	if required[GitPreparationActionCreateRemoteRepository] && target.Input.GitHubRepository != nil {
		owner := strings.TrimSpace(target.Input.GitHubRepository.Owner)
		name := strings.TrimSpace(target.Input.GitHubRepository.Name)
		if owner == "" || name == "" {
			return apierr.Invalid("IMPORT_GITHUB_REPOSITORY_REQUIRED", "GitHub repository owner and name are required before Tallpine can create an origin remote.", map[string]any{"repoPath": target.Status.RepoPath})
		}
	}
	if required[GitPreparationActionSetRemote] && strings.TrimSpace(target.Input.RemoteURL) == "" {
		return apierr.Invalid("IMPORT_REMOTE_URL_REQUIRED", "remoteUrl is required before Tallpine can add an origin remote.", map[string]any{"repoPath": target.Status.RepoPath})
	}
	if required[GitPreparationActionSetRemote] || (required[GitPreparationActionCreateRemoteRepository] && target.Input.GitHubRepository == nil) {
		if err := validateImportRemoteURL(target.Input.RemoteURL); err != nil {
			return err
		}
	}
	return nil
}

// withOptionalRemoteAction returns the status's required actions plus the
// optional remote action the caller approved and supplied input for. A project
// never requires a remote; this only runs one the user explicitly opted into.
func withOptionalRemoteAction(status RepoGitStatus, in GitPreparationInput) []string {
	actions := append([]string(nil), status.RequiredActions...)
	if status.HasOrigin || slices.ContainsFunc(actions, func(a string) bool {
		return a == GitPreparationActionSetRemote || a == GitPreparationActionCreateRemoteRepository
	}) {
		return actions
	}
	switch {
	case containsAction(in.ApprovedActions, GitPreparationActionCreateRemoteRepository) && (in.GitHubRepository != nil || strings.TrimSpace(in.RemoteURL) != ""):
		return append(actions, GitPreparationActionCreateRemoteRepository)
	case containsAction(in.ApprovedActions, GitPreparationActionSetRemote) && strings.TrimSpace(in.RemoteURL) != "":
		return append(actions, GitPreparationActionSetRemote)
	}
	return actions
}

type gitPreparationTarget struct {
	Status RepoGitStatus
	Input  GitRepositoryPreparationInput
}

func preparationTargets(ctx context.Context, validation ImportValidationResult, in GitPreparationInput) ([]gitPreparationTarget, error) {
	if validation.ImportKind == ImportKindProject {
		status := validation.Root
		status.RequiredActions = withOptionalRemoteAction(status, in)
		return []gitPreparationTarget{{
			Status: status,
			Input: GitRepositoryPreparationInput{
				RepoPath:         validation.Root.RepoPath,
				ApprovedActions:  in.ApprovedActions,
				RemoteURL:        in.RemoteURL,
				GitHubRepository: in.GitHubRepository,
				InitialCommitMsg: in.InitialCommitMsg,
			},
		}}, nil
	}

	byPath := map[string]GitRepositoryPreparationInput{}
	for _, repo := range in.Repositories {
		path, err := normalizeImportPath(repo.RepoPath)
		if err != nil {
			return nil, apierr.Invalid("INVALID_REPOSITORY_PATH", "Repository path is invalid.", map[string]any{"repoPath": repo.RepoPath})
		}
		repo.RepoPath = path
		byPath[path] = repo
	}
	var targets []gitPreparationTarget
	for _, status := range validation.ChildRepos {
		if len(status.RequiredActions) == 0 {
			continue
		}
		input, ok := byPath[status.RepoPath]
		if !ok {
			return nil, apierr.Invalid("IMPORT_REPOSITORY_APPROVAL_REQUIRED", "Every repository with missing Git preparation requires explicit approval.", map[string]any{"repoPath": status.RepoPath})
		}
		targets = append(targets, gitPreparationTarget{Status: status, Input: input})
	}
	for repoPath, input := range byPath {
		if slices.ContainsFunc(validation.ChildRepos, func(status RepoGitStatus) bool { return sameImportPath(status.RepoPath, repoPath) }) {
			continue
		}
		resolvedPath := comparableImportPath(repoPath)
		if !sameImportPath(filepath.Dir(resolvedPath), comparableImportPath(validation.Root.RepoPath)) {
			return nil, apierr.Invalid("INVALID_REPOSITORY_PATH", "Repository must be a direct child of the workspace.", map[string]any{"repoPath": repoPath})
		}
		info, statErr := os.Stat(resolvedPath)
		if statErr != nil || !info.IsDir() {
			return nil, apierr.Invalid("INVALID_REPOSITORY_PATH", "Repository path is invalid.", map[string]any{"repoPath": repoPath})
		}
		status := inspectImportRepo(ctx, resolvedPath)
		if len(status.BlockingErrors) > 0 {
			return nil, apierr.Invalid("INVALID_REPOSITORY_PATH", "Repository cannot be prepared.", map[string]any{"repoPath": repoPath})
		}
		targets = append(targets, gitPreparationTarget{Status: status, Input: input})
	}
	return targets, nil
}

func directChildImportRepos(ctx context.Context, root string) ([]RepoGitStatus, error) {
	statuses, err := directChildImportStatuses(ctx, root)
	if err != nil {
		return nil, err
	}
	repos := statuses[:0]
	for _, status := range statuses {
		if status.IsRepo || len(status.BlockingErrors) > 0 {
			repos = append(repos, status)
		}
	}
	return repos, nil
}

func directChildImportStatuses(ctx context.Context, root string) ([]RepoGitStatus, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var repos []RepoGitStatus
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".git" {
			continue
		}
		child := filepath.Join(root, entry.Name())
		status := inspectImportRepo(ctx, child)
		if isBareImportRepo(ctx, child) {
			status.BlockingErrors = append(status.BlockingErrors, "BARE_REPOSITORY")
		}
		if hasUnsupportedImportGitMetadata(child) {
			status.BlockingErrors = append(status.BlockingErrors, "UNSUPPORTED_GIT_METADATA")
		}
		repos = append(repos, status)
	}
	return repos, nil
}

func runGitPreparationAction(ctx context.Context, path, action string, in GitRepositoryPreparationInput) error {
	switch action {
	case GitPreparationActionInit:
		_, err := importGitOutput(ctx, path, "init", "-b", domain.DefaultBranchName)
		if err != nil {
			return fmt.Errorf("initialize repository: %w", err)
		}
		if _, err := importGitOutput(ctx, path, "config", "--local", gitdefault.ManagedDefaultConfigKey, domain.DefaultBranchName); err != nil {
			return fmt.Errorf("record default branch: %w", err)
		}
	case GitPreparationActionCommit:
		// Empty clones already have .git and skip the init action. Record their
		// actual initial branch too, before committing so a retry cannot lose it.
		if err := gitdefault.New("git", nil).RecordInitialBranch(ctx, path); err != nil {
			return err
		}
		if _, err := importGitOutput(ctx, path, "add", "-A"); err != nil {
			return fmt.Errorf("stage files: %w", err)
		}
		msg := strings.TrimSpace(in.InitialCommitMsg)
		if msg == "" {
			msg = "initial commit"
		}
		if _, err := importGitOutput(ctx, path, "-c", "user.name=Tallpine", "-c", "user.email=tallpine@example.com", "commit", "--allow-empty", "-m", msg); err != nil {
			return fmt.Errorf("create initial commit: %w", err)
		}
	case GitPreparationActionSetRemote:
		if resolveImportOriginURL(path) != "" {
			return nil
		}
		remoteURL := strings.TrimSpace(in.RemoteURL)
		if err := setImportOriginURL(ctx, path, remoteURL); err != nil {
			return err
		}
	case GitPreparationActionCreateRemoteRepository:
		if in.GitHubRepository != nil {
			owner := strings.TrimSpace(in.GitHubRepository.Owner)
			name := strings.TrimSpace(in.GitHubRepository.Name)
			if owner == "" || name == "" {
				return errors.New("github repository owner and name are required")
			}
			repository := owner + "/" + name
			remoteURL := "https://github.com/" + repository + ".git"
			if currentRemoteURL := resolveImportOriginURL(path); currentRemoteURL == "" {
				visibility := "--private"
				if in.GitHubRepository.Private != nil && !*in.GitHubRepository.Private {
					visibility = "--public"
				}
				if _, err := importGhOutputFunc(ctx, path, "repo", "create", repository, visibility); err != nil {
					return fmt.Errorf("create GitHub repository: %w", err)
				}
				if err := setImportOriginURL(ctx, path, remoteURL); err != nil {
					return err
				}
			} else if currentRemoteURL != remoteURL {
				return nil
			}
			if _, err := importGitOutputFunc(ctx, path, "config", "--local", importGitHubRepositoryConfigKey, repository); err != nil {
				return fmt.Errorf("record GitHub repository preparation: %w", err)
			}
			if _, err := importGitOutputFunc(ctx, path, "push", "-u", "origin", "HEAD"); err != nil {
				return fmt.Errorf("push initial commit: %w", err)
			}
			return nil
		}
		if resolveImportOriginURL(path) != "" {
			return nil
		}
		remoteURL := strings.TrimSpace(in.RemoteURL)
		if err := setImportOriginURL(ctx, path, remoteURL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported action %q", action)
	}
	return nil
}

func setImportOriginURL(ctx context.Context, path, remoteURL string) error {
	if importRemoteExists(path, "origin") {
		if _, err := importGitOutputFunc(ctx, path, "remote", "set-url", "origin", remoteURL); err != nil {
			return fmt.Errorf("set origin remote: %w", err)
		}
		return nil
	}
	if _, err := importGitOutputFunc(ctx, path, "remote", "add", "origin", remoteURL); err != nil {
		return fmt.Errorf("add origin remote: %w", err)
	}
	return nil
}

func importGitHubPreparationIncomplete(ctx context.Context, path string) bool {
	out, err := importGitOutputFunc(ctx, path, "config", "--get", importGitHubRepositoryConfigKey)
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	_, err = importGitOutputFunc(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return err != nil
}

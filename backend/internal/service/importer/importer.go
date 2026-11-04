package importer

import (
	"context"
	"os"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// Service is the controller-facing import contract.
type Service interface {
	Validate(ctx context.Context, in ImportValidationInput) (ImportValidationResult, error)
	PrepareGit(ctx context.Context, in GitPreparationInput) (GitPreparationResult, error)
}

// Import kinds, next steps, Git preparation actions, and event states shared by the import API.
const (
	ImportKindProject   = "project"
	ImportKindWorkspace = "workspace"

	ImportNextStepError            = "error"
	ImportNextStepChooseImportKind = "choose_import_kind"
	ImportNextStepPrepareGit       = "prepare_git"
	ImportNextStepContinue         = "continue"

	GitPreparationActionInit                   = "git_init"
	GitPreparationActionCommit                 = "git_commit"
	GitPreparationActionCreateRemoteRepository = "create_remote_repository"
	GitPreparationActionSetRemote              = "set_remote"

	GitPreparationEventPending = "pending"
	GitPreparationEventRunning = "running"
	GitPreparationEventSuccess = "success"
	GitPreparationEventError   = "error"
)

const importGitHubRepositoryConfigKey = "ao.import.githubRepository"

// ImportValidationInput is the body shape for POST /api/v1/imports/validate.
type ImportValidationInput struct {
	ImportKind string `json:"importKind" enum:"project,workspace" minLength:"1"`
	Path       string `json:"path" minLength:"1"`
}

// GitPreparationInput is the body shape for POST /api/v1/imports/prepare-git.
type GitPreparationInput struct {
	ImportKind       string                          `json:"importKind" enum:"project,workspace" minLength:"1"`
	Path             string                          `json:"path" minLength:"1"`
	ApprovedActions  []string                        `json:"approvedActions,omitempty"`
	RemoteURL        string                          `json:"remoteUrl,omitempty"`
	GitHubRepository *GitHubRepositoryPreparation    `json:"githubRepository,omitempty"`
	InitialCommitMsg string                          `json:"initialCommitMessage,omitempty"`
	Repositories     []GitRepositoryPreparationInput `json:"repositories,omitempty"`
	Stepwise         bool                            `json:"stepwise,omitempty"`
}

// GitHubRepositoryPreparation describes the GitHub repository Tallpine should create for a project import.
type GitHubRepositoryPreparation struct {
	Owner   string `json:"owner,omitempty"`
	Name    string `json:"name,omitempty"`
	Private *bool  `json:"private,omitempty"`
}

// GitRepositoryPreparationInput approves Git preparation for one repository.
type GitRepositoryPreparationInput struct {
	RepoPath         string                       `json:"repoPath" minLength:"1"`
	ApprovedActions  []string                     `json:"approvedActions"`
	RemoteURL        string                       `json:"remoteUrl,omitempty"`
	GitHubRepository *GitHubRepositoryPreparation `json:"githubRepository,omitempty"`
	InitialCommitMsg string                       `json:"initialCommitMessage,omitempty"`
}

// RepoGitStatus describes the Git readiness of one repository candidate.
type RepoGitStatus struct {
	RepoPath        string   `json:"repoPath"`
	IsRepo          bool     `json:"isRepo"`
	HasCommit       bool     `json:"hasCommit"`
	HasOrigin       bool     `json:"hasOrigin"`
	IsEmptyFolder   bool     `json:"isEmptyFolder"`
	NeedsGitInit    bool     `json:"needsGitInit"`
	RequiredActions []string `json:"requiredActions"`
	BlockingErrors  []string `json:"blockingErrors"`
}

// ImportValidationResult is shared by project import validation and future
// workspace import validation.
type ImportValidationResult struct {
	ImportKind     string          `json:"importKind"`
	IsValid        bool            `json:"isValid"`
	BlockingErrors []string        `json:"blockingErrors"`
	Root           RepoGitStatus   `json:"root"`
	ChildRepos     []RepoGitStatus `json:"childRepos,omitempty"`
	// Warning is advisory UI copy for non-blocking classification details.
	Warning  string `json:"warning,omitempty"`
	NextStep string `json:"nextStep" enum:"error,choose_import_kind,prepare_git,continue"`
}

// GitPreparationEvent reports one state transition for a requested Git action.
type GitPreparationEvent struct {
	Action   string `json:"action" enum:"git_init,git_commit,create_remote_repository,set_remote"`
	RepoPath string `json:"repoPath"`
	State    string `json:"state" enum:"pending,running,success,error"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

// GitPreparationResult is the body of POST /api/v1/imports/prepare-git.
type GitPreparationResult struct {
	Events     []GitPreparationEvent  `json:"events"`
	Validation ImportValidationResult `json:"validation"`
}

// Manager implements Service.
type Manager struct{}

var _ Service = (*Manager)(nil)

// New constructs the import service.
func New() *Manager { return &Manager{} }

// Validate inspects a selected folder for project import readiness without
// mutating Git or the filesystem.
func (m *Manager) Validate(ctx context.Context, in ImportValidationInput) (ImportValidationResult, error) {
	importKind := strings.TrimSpace(in.ImportKind)
	if importKind != ImportKindProject && importKind != ImportKindWorkspace {
		return ImportValidationResult{}, apierr.Invalid("UNSUPPORTED_IMPORT_KIND", "Only project and workspace imports are supported.", map[string]any{"importKind": in.ImportKind})
	}
	path, normalizeErr := normalizeImportPath(in.Path)
	if normalizeErr != nil {
		return invalidImportResult(importKind, strings.TrimSpace(in.Path), "INVALID_PATH"), nil //nolint:nilerr // validation failures are reported in-band so the UI can show blocking errors
	}
	if unsafeImportPath(path) {
		return invalidImportResult(importKind, path, "IMPORT_PATH_UNSAFE"), nil
	}
	result := ImportValidationResult{
		ImportKind:     importKind,
		IsValid:        true,
		BlockingErrors: []string{},
		Root:           RepoGitStatus{RepoPath: path, BlockingErrors: []string{}, RequiredActions: []string{}},
		NextStep:       ImportNextStepContinue,
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return invalidImportResult(importKind, path, "INVALID_PATH"), nil //nolint:nilerr // validation failures are reported in-band so the UI can show blocking errors
	}
	if !info.IsDir() {
		return invalidImportResult(importKind, path, "PATH_NOT_DIRECTORY"), nil
	}
	if isBareImportRepo(ctx, path) {
		return invalidImportResult(importKind, path, "BARE_REPOSITORY"), nil
	}
	if hasUnsupportedImportGitMetadata(path) {
		result.Root.BlockingErrors = append(result.Root.BlockingErrors, "UNSUPPORTED_GIT_METADATA")
		result.BlockingErrors = append(result.BlockingErrors, "UNSUPPORTED_GIT_METADATA")
		result.IsValid = false
		result.NextStep = ImportNextStepError
		return result, nil
	}

	root := inspectImportRepo(ctx, path)
	if importKind == ImportKindProject {
		root.RequiredActions = dropProjectRemoteActions(root.RequiredActions)
		if root.HasOrigin && root.HasCommit && importGitHubPreparationIncomplete(ctx, path) && !slices.Contains(root.RequiredActions, GitPreparationActionCreateRemoteRepository) {
			root.RequiredActions = append(root.RequiredActions, GitPreparationActionCreateRemoteRepository)
		}
	}
	result.Root = root
	if len(root.BlockingErrors) > 0 {
		result.BlockingErrors = append(result.BlockingErrors, root.BlockingErrors...)
		result.IsValid = false
		result.NextStep = ImportNextStepError
		return result, nil
	}
	if importKind == ImportKindWorkspace {
		if root.IsRepo && root.HasOrigin {
			result.Warning = "This folder is already a Git project. Tallpine will import it as a project instead of a workspace."
			result.NextStep = ImportNextStepChooseImportKind
			return result, nil
		}
		children, scanErr := directChildImportRepos(ctx, path)
		if scanErr != nil {
			return invalidImportResult(importKind, path, "CHILD_REPO_SCAN_FAILED"), nil //nolint:nilerr // validation failures are reported in-band so the UI can show blocking errors
		}
		result.ChildRepos = children
		if len(children) == 0 {
			result.Root.BlockingErrors = append(result.Root.BlockingErrors, "WORKSPACE_CHILD_REPO_REQUIRED")
			result.BlockingErrors = append(result.BlockingErrors, "WORKSPACE_CHILD_REPO_REQUIRED")
			result.IsValid = false
			result.NextStep = ImportNextStepError
			return result, nil
		}
		for _, child := range children {
			if len(child.BlockingErrors) > 0 {
				result.BlockingErrors = append(result.BlockingErrors, child.BlockingErrors...)
				result.IsValid = false
				result.NextStep = ImportNextStepError
			}
			if result.NextStep != ImportNextStepError && len(child.RequiredActions) > 0 {
				result.NextStep = ImportNextStepPrepareGit
			}
		}
		return result, nil
	}
	children, scanErr := directChildImportRepos(ctx, path)
	if scanErr != nil {
		return invalidImportResult(importKind, path, "CHILD_REPO_SCAN_FAILED"), nil //nolint:nilerr // validation failures are reported in-band so the UI can show blocking errors
	}
	if len(children) > 0 {
		result.ChildRepos = children
	}
	if !root.IsRepo {
		if len(children) > 0 {
			result.NextStep = ImportNextStepChooseImportKind
			return result, nil
		}
	}
	if root.HasOrigin && len(children) > 0 {
		result.Warning = "Selected folder has direct child repositories, but because the root repository already has an origin remote Tallpine will import it as a project, not a workspace."
	}
	if len(root.RequiredActions) > 0 {
		result.NextStep = ImportNextStepPrepareGit
	}
	return result, nil
}

func invalidImportResult(importKind, path, code string) ImportValidationResult {
	if importKind == "" {
		importKind = ImportKindProject
	}
	return ImportValidationResult{
		ImportKind:     importKind,
		IsValid:        false,
		BlockingErrors: []string{code},
		Root: RepoGitStatus{
			RepoPath:        path,
			RequiredActions: []string{},
			BlockingErrors:  []string{code},
		},
		NextStep: ImportNextStepError,
	}
}

func inspectImportRepo(ctx context.Context, path string) RepoGitStatus {
	status := RepoGitStatus{RepoPath: path, BlockingErrors: []string{}, RequiredActions: []string{}}
	status.IsEmptyFolder = isImportFolderEmpty(path)
	status.IsRepo = isImportGitRepo(path)
	status.HasCommit = status.IsRepo && importRepoHasCommit(ctx, path)
	status.HasOrigin = status.IsRepo && resolveImportOriginURL(path) != ""
	status.NeedsGitInit = !status.IsRepo
	if status.IsRepo && status.HasCommit && importRepoHasDetachedHead(ctx, path) {
		status.BlockingErrors = append(status.BlockingErrors, "DETACHED_HEAD")
	}
	if status.NeedsGitInit {
		status.RequiredActions = append(status.RequiredActions, GitPreparationActionInit)
	}
	if !status.HasCommit {
		status.RequiredActions = append(status.RequiredActions, GitPreparationActionCommit)
	}
	if !status.HasOrigin {
		status.RequiredActions = append(status.RequiredActions, GitPreparationActionSetRemote)
	}
	return status
}

// dropProjectRemoteActions removes the remote-setup actions from a project
// import: a project needs init and a commit but never a remote. Creating or
// setting a remote stays available as an optional, explicitly approved step.
func dropProjectRemoteActions(actions []string) []string {
	return slices.DeleteFunc(slices.Clone(actions), func(a string) bool {
		return a == GitPreparationActionSetRemote || a == GitPreparationActionCreateRemoteRepository
	})
}

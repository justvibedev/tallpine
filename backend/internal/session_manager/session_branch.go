package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func seedRecord(decision agentpolicy.Decision, cfg ports.SpawnConfig, projectConfig domain.ProjectConfig, app domain.WorkflowDefaults, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID:         cfg.ProjectID,
		IssueID:           cfg.IssueID,
		AutomationRunID:   cfg.AutomationRunID,
		ClientRequestID:   cfg.ClientRequestID,
		ClientRequestHash: cfg.ClientRequestHash,
		Kind:              cfg.Kind,
		CreatedAt:         now,
		UpdatedAt:         now,
		Harness:           cfg.Harness,
		DisplayName:       cfg.DisplayName,
		Activity:          domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		// Resolved before this point and persisted here. There is no UPDATE
		// statement that can change it afterwards.
		Mode:              domain.NormalizeSessionMode(cfg.RequestedMode),
		Metadata:          domain.SessionMetadata{Permissions: applySpawnAgentConfigUnder(decision, cfg.Harness, effectiveAgentConfigFor(cfg.Harness, cfg.Kind, cfg.Role, projectConfig), cfg.AgentConfig).Permissions},
		RequestedOutput:   cfg.RequestedOutput,
		Role:              cfg.Role,
		OutputType:        domain.SessionOutputNone,
		AutoReviewEnabled: projectConfig.EffectiveAutoReview(app),
		// Workers only: an orchestrator never ends because a PR merged. Unset
		// means on (B407); a project or managed policy turns it off.
		TerminateOnPRMerge: (projectConfig.TerminateOnPRMerge == nil || *projectConfig.TerminateOnPRMerge) && cfg.Kind == domain.KindWorker,
		AutoInjectReview:   true,
		AutoInjectCI:       true,
	}
}

func defaultSessionBranch(id domain.SessionID, kind domain.SessionKind, prefix, branchNamespace string) string {
	if kind == domain.KindOrchestrator {
		return tallpineBranch(branchNamespace, prefix+"-orchestrator")
	}
	// A fresh, unique branch per worker session: gitworktree can't add a worktree
	// on a branch already checked out elsewhere (e.g. main). Put the root work
	// branch under a session namespace so sibling PR branches such as
	// tp/<session>/<topic> remain valid Git refs.
	return tallpineBranch(branchNamespace, string(id), "root")
}

// DefaultSpawnBranch returns Tallpine's generated work branch for a spawn. Explicit
// user-provided branches bypass this helper.
func DefaultSpawnBranch(id domain.SessionID, kind domain.SessionKind, prefix string, projectKind domain.ProjectKind, dataDir string) string {
	if projectKind == domain.ProjectKindScratch {
		return ""
	}
	branchNamespace := generatedBranchNamespace(dataDir)
	if projectKind == domain.ProjectKindWorkspace {
		return tallpineBranch(branchNamespace, string(id))
	}
	return defaultSessionBranch(id, kind, prefix, branchNamespace)
}

// DefaultOrchestratorBranch returns the generated canonical orchestrator branch
// for a project in the current data-dir namespace.
func DefaultOrchestratorBranch(prefix, dataDir string) string {
	return defaultSessionBranch("", domain.KindOrchestrator, prefix, generatedBranchNamespace(dataDir))
}

func tallpineBranch(namespace string, parts ...string) string {
	all := []string{brand.BranchPrefix}
	if namespace != "" {
		all = append(all, namespace)
	}
	all = append(all, parts...)
	return strings.Join(all, "/")
}

func generatedBranchNamespace(dataDir string) string {
	if isDefaultDevDataDir(dataDir) {
		return "dev"
	}
	return ""
}

func isDefaultDevDataDir(dataDir string) bool {
	if strings.TrimSpace(dataDir) == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	want, err := filepath.Abs(filepath.Join(home, brand.HomeDirName, "dev", "data"))
	if err != nil {
		return false
	}
	got, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	return filepath.Clean(got) == filepath.Clean(want)
}

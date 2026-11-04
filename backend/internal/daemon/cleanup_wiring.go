package daemon

import (
	"context"
	"log/slog"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/adapters/container/dockerreap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/sessionretention"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// sessionCleaner is the session manager's per-session workspace cleanup.
type sessionCleaner interface {
	CleanupSession(ctx context.Context, id domain.SessionID, check sessionmanager.CleanupCheck) (sessionmanager.CleanupResult, error)
	SessionWorktreePaths(ctx context.Context, id domain.SessionID) ([]string, error)
}

// worktreeRemover adapts sessionCleaner to the cleanup engine.
type worktreeRemover struct{ mgr sessionCleaner }

func (w worktreeRemover) WorktreePaths(ctx context.Context, id domain.SessionID) ([]string, error) {
	return w.mgr.SessionWorktreePaths(ctx, id)
}

func (w worktreeRemover) CleanupSession(ctx context.Context, id domain.SessionID, check cleanupsvc.WorktreeCheck) (cleanupsvc.WorktreeOutcome, error) {
	var managerCheck sessionmanager.CleanupCheck
	if check != nil {
		managerCheck = sessionmanager.CleanupCheck(check)
	}
	res, err := w.mgr.CleanupSession(ctx, id, managerCheck)
	if err != nil {
		return cleanupsvc.WorktreeOutcome{}, err
	}
	switch {
	case len(res.Skipped) > 0:
		return cleanupsvc.WorktreeOutcome{Kept: res.Skipped[0].Reason}, nil
	case len(res.Cleaned) > 0:
		return cleanupsvc.WorktreeOutcome{Removed: true}, nil
	default:
		return cleanupsvc.WorktreeOutcome{AlreadyGone: true}, nil
	}
}

// cleanupChecked is a session manager whose own cleanup (tallpine session cleanup)
// takes the worktree safety check.
type cleanupChecked interface {
	SetCleanupCheck(sessionmanager.CleanupCheck)
}

// The real manager must keep matching, or newCleanup would silently skip it.
var (
	_ cleanupChecked         = (*sessionmanager.Manager)(nil)
	_ cleanupsvc.BranchGuard = (*sessionmanager.Manager)(nil)
	// Restore refuses a session the retention policy purged; the store must
	// keep answering the session manager's optional check.
	_ interface {
		SessionRetentionPurged(context.Context, domain.SessionID) (bool, error)
	} = (*sqlite.Store)(nil)
)

// newCleanup builds the cleanup engine over the rules this daemon can run. A
// session manager without per-session cleanup leaves the worktree rule out
// rather than guessing.
func newCleanup(dataDir, systemTemp string, store *sqlite.Store, sessMgr any, pruner cleanupsvc.ArtifactPruner, artifacts *artifactstorage.Service, policy *agentpolicy.Engine, log *slog.Logger) *cleanupsvc.Service {
	var rules []cleanupsvc.Rule
	root := filepath.Join(dataDir, "worktrees")
	// Dirty worktrees of ended sessions without an open PR are salvaged
	// (patch, untracked files, unique commits) and removed rather than kept.
	var prs cleanupsvc.PRLookup
	if store != nil {
		prs = store.ListPRsBySession
	}
	salvager := cleanupsvc.NewSalvager(dataDir, prs)
	var remover cleanupsvc.WorktreeRemover
	if cleaner, ok := sessMgr.(sessionCleaner); ok {
		remover = worktreeRemover{mgr: cleaner}
		rule := cleanupsvc.NewWorktreeRule(root, remover, nil)
		rule.SetSalvager(salvager)
		rules = append(rules, rule)
	}
	// Session retention (B161) runs after the worktree rule, under the
	// sessionRetention agent-policy rule: on by default; the user or MDM can turn it off.
	if store != nil && policy != nil {
		rules = append(rules, cleanupsvc.NewRetentionRule(store, retentionPolicy(policy), root, remover, salvager, nil))
	}
	// tallpine session cleanup salvages and refuses the same way, under the same guard.
	if checked, ok := sessMgr.(cleanupChecked); ok {
		checked.SetCleanupCheck(sessionmanager.CleanupCheck(cleanupsvc.SalvagingSafetyCheck(root, nil, salvager)))
	}
	if pruner != nil && artifacts != nil {
		rules = append(rules, cleanupsvc.NewArtifactRule(dataDir, pruner, artifacts))
	}
	rules = append(rules,
		cleanupsvc.NewSessionTempRule(dataDir),
		cleanupsvc.NewHarnessCacheRule(dataDir),
		cleanupsvc.NewGoBuildCacheRule(""),
		cleanupsvc.NewSystemTempRule(systemTemp, dataDir),
	)
	if store != nil {
		// Branch deletion runs under the session manager's guard against
		// restore and spawn; without one the rule only reports.
		guard, _ := sessMgr.(cleanupsvc.BranchGuard)
		rules = append(rules,
			cleanupsvc.NewBranchRule(store, guard, nil),
			cleanupsvc.NewContainerRule(dockerreap.New(), store),
			cleanupsvc.NewBacklogRule(store),
		)
	}
	return cleanupsvc.New(store, rules, cleanupsvc.Config{Logger: log})
}

// retentionPolicy reads the sessionRetention rule per project from the agent
// policy engine, so a managed value overrides and locks the user's.
func retentionPolicy(engine *agentpolicy.Engine) cleanupsvc.RetentionPolicy {
	return func(ctx context.Context, project domain.ProjectID) cleanupsvc.RetentionSettings {
		out := sessionretention.Of(engine.Decide(ctx, agentpolicy.SessionRef{Project: project}))
		return cleanupsvc.RetentionSettings{
			Enabled: out.Enabled, Days: out.Days,
			DropWorktrees:        out.DropWorktrees,
			DropTranscripts:      out.DropTranscripts,
			IncludeOrchestrators: out.IncludeOrchestrators,
		}
	}
}

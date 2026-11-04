package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// CategorySessionRetention is the session retention policy (B161).
const CategorySessionRetention Category = "session_retention"

// RetentionSettings is the effective session retention policy of one project:
// the sessionRetention agent-policy rule after the managed, env, user and
// project layers.
type RetentionSettings struct {
	Enabled         bool
	Days            int
	DropWorktrees   bool
	DropTranscripts bool
	// IncludeOrchestrators purges ended orchestrator sessions too; off, they
	// keep everything because a fresh orchestrator reads their history.
	IncludeOrchestrators bool
}

// RetentionPolicy resolves the retention settings of a project.
type RetentionPolicy func(ctx context.Context, project domain.ProjectID) RetentionSettings

// RetentionStore is the database surface of session retention.
type RetentionStore interface {
	SessionRetentionFootprint(ctx context.Context, id domain.SessionID) (sqlitestore.RetentionFootprint, error)
	PurgeSessionTranscript(ctx context.Context, id domain.SessionID) (int64, error)
	MarkSessionRetentionPurge(ctx context.Context, id domain.SessionID, events, transcript bool, now time.Time) error
	OptimizeStorage(ctx context.Context, maxVacuumPages int64) (sqlitestore.StorageMaintenance, error)
}

// Incremental vacuum after a purge: steps of retentionVacuumPages free pages,
// each under the writer lock on its own, at most retentionVacuumSteps of them
// per run. The storage janitor returns whatever is left on its own schedule.
const (
	retentionVacuumPages int64 = 2048
	retentionVacuumSteps int   = 64
)

// RetentionRule purges what the session retention policy drops from
// terminated sessions older than the policy's age threshold. A dry run lists
// what a run would drop and the space it frees; a dry run limited to this
// category (tallpine retention preview) does so even while the policy is off,
// so the user can preview before opting in. Every other run only looks at
// projects where the policy is on, so general cleanup totals stay honest.
type RetentionRule struct {
	store  RetentionStore
	policy RetentionPolicy
	// Worktree removal reuses the worktree rule's remover and salvage: a dirty
	// worktree's work is saved before it goes. A nil remover drops no worktrees.
	root    string
	remover WorktreeRemover
	salvage *Salvager
	inspect WorktreeInspector
}

// NewRetentionRule builds the rule. remover and salvage may be nil; a nil
// inspect uses GitInspect.
func NewRetentionRule(store RetentionStore, policy RetentionPolicy, root string, remover WorktreeRemover, salvage *Salvager, inspect WorktreeInspector) *RetentionRule {
	if inspect == nil {
		inspect = GitInspect
	}
	return &RetentionRule{store: store, policy: policy, root: root, remover: remover, salvage: salvage, inspect: inspect}
}

// Category implements Rule.
func (*RetentionRule) Category() Category { return CategorySessionRetention }

// Light implements Rule: retention runs in the daily full pass only.
func (*RetentionRule) Light() bool { return false }

// Enabled implements Rule. The cleanup policy has no switch for it: the
// sessionRetention agent-policy rule decides, per project, inside Run.
func (*RetentionRule) Enabled(Policy) bool { return true }

// Run implements Rule.
func (r *RetentionRule) Run(ctx context.Context, run *Run) error {
	if r.store == nil || r.policy == nil {
		return errors.New("session retention is not configured")
	}
	preview := run.DryRun && slices.Equal(run.Only, []Category{CategorySessionRetention})
	settings := map[domain.ProjectID]RetentionSettings{}
	purgedDB := false
	for _, rec := range run.Sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !rec.IsTerminated || rec.IsTaskPreparation {
			continue
		}
		s, ok := settings[rec.ProjectID]
		if !ok {
			s = r.policy(ctx, rec.ProjectID)
			settings[rec.ProjectID] = s
		}
		if !s.Enabled && !preview {
			continue
		}
		if rec.Kind == domain.KindOrchestrator && !s.IncludeOrchestrators {
			continue
		}
		if run.Now.Sub(rec.UpdatedAt) < time.Duration(s.Days)*24*time.Hour {
			continue
		}
		freed, err := r.session(ctx, run, rec, s)
		if err != nil {
			return err
		}
		purgedDB = purgedDB || freed
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	if purgedDB && !run.DryRun {
		r.vacuum(ctx, run)
	}
	return nil
}

// session handles one expired session and reports whether it deleted rows.
func (r *RetentionRule) session(ctx context.Context, run *Run, rec domain.SessionRecord, s RetentionSettings) (bool, error) {
	note := ""
	if !s.Enabled {
		note = "preview: retention is off for this project"
	}
	foot, err := r.store.SessionRetentionFootprint(ctx, rec.ID)
	if err != nil {
		// One unreadable session fails its item, not the whole run.
		run.Add(Item{SessionID: rec.ID, ProjectID: rec.ProjectID, What: "session data", Status: StatusFailed, Reason: err.Error()})
		return false, ctx.Err()
	}
	purged := false
	// Raw provider events are not purged here: app.providerEventRetentionDays
	// (Settings > Storage) is the only rule for deleting them. The sessionRetention
	// dropProviderEvents field is deprecated and ignored.
	if s.DropTranscripts && foot.TranscriptRows > 0 {
		item := Item{SessionID: rec.ID, ProjectID: rec.ProjectID, What: "transcript", Bytes: foot.TranscriptRowsBytes}
		purged = r.purge(ctx, run, rec, item, note, false, true, r.store.PurgeSessionTranscript) || purged
	}
	if s.DropWorktrees {
		r.worktree(ctx, run, rec, note)
	}
	return purged, ctx.Err()
}

// purge runs one purge step after re-reading the session: a session restored
// since the snapshot is live and is never touched.
func (r *RetentionRule) purge(ctx context.Context, run *Run, rec domain.SessionRecord, item Item, note string, events, transcript bool, do func(context.Context, domain.SessionID) (int64, error)) bool {
	if run.DryRun {
		item.Status, item.Reason = StatusWouldFree, note
		run.Add(item)
		return false
	}
	if _, found, live := run.Current(ctx, rec.ID); !found || live {
		item.Bytes, item.Status, item.Reason = 0, StatusKept, "the session is live again"
		run.Add(item)
		return false
	}
	// Mark before deleting: a purge that fails part way, or a restore racing
	// it, must already find the session guarded. A mark on a session that had
	// nothing left to delete is harmless.
	n, err := int64(0), r.store.MarkSessionRetentionPurge(ctx, rec.ID, events, transcript, run.Now)
	if err == nil {
		n, err = do(ctx, rec.ID)
	}
	switch {
	case err != nil:
		item.Status, item.Reason = StatusFailed, err.Error()
	default:
		item.Status = StatusFreed
	}
	run.Add(item)
	return n > 0
}

// worktree removes an expired session's worktree, salvaging dirty work first.
// Retention overrides the worktree rule's own window and requireMerged, but
// keeps every other safety check.
func (r *RetentionRule) worktree(ctx context.Context, run *Run, rec domain.SessionRecord, note string) {
	path := rec.Metadata.WorkspacePath
	// An orchestrator's worktree is shared with the live orchestrator and its
	// predecessors: retention never removes it, whatever the settings.
	if r.remover == nil || path == "" || rec.Kind == domain.KindOrchestrator {
		return
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return
	}
	item := Item{SessionID: rec.ID, ProjectID: rec.ProjectID, What: "worktree", Path: path, Status: StatusKept}
	// The worktree rule ran first: what it freed or would free is its item.
	if run.Claimed(path) {
		return
	}
	if run.LiveWorkspace(path) {
		item.Reason = "in use by a live session"
		run.Add(item)
		return
	}
	if sharedWorkspace(run.Sessions, rec, path) {
		item.Reason = "shared with another session"
		run.Add(item)
		return
	}
	paths, err := r.remover.WorktreePaths(ctx, rec.ID)
	if err != nil {
		item.Reason = "worktree status could not be read"
		run.Add(item)
		return
	}
	sv, why := r.salvage.step(ctx, rec, false)
	if keep := inspectWorktrees(ctx, r.root, r.inspect, paths, false, sv); keep != "" {
		item.Reason = withWhy(keep, why)
		run.Add(item)
		return
	}
	item.Bytes, _ = dirSize(ctx, path)
	if run.DryRun {
		item.Status, item.Reason = StatusWouldFree, joinNote(note, ignoredFilesNote)
		run.Add(item)
		return
	}
	outcome, err := r.remover.CleanupSession(ctx, rec.ID, SalvagingSafetyCheck(r.root, r.inspect, r.salvage))
	switch {
	case err != nil:
		item.Status, item.Reason = StatusFailed, err.Error()
	case outcome.Kept != "":
		item.Bytes, item.Reason = 0, outcome.Kept
	case outcome.AlreadyGone:
		item.Bytes, item.Reason = 0, "already removed"
	default:
		item.Status, item.Reason = StatusFreed, ignoredFilesNote
	}
	run.Add(item)
}

// vacuum returns the pages a purge freed to the OS in short steps.
func (r *RetentionRule) vacuum(ctx context.Context, run *Run) {
	for range retentionVacuumSteps {
		res, err := r.store.OptimizeStorage(ctx, retentionVacuumPages)
		if err != nil || !res.IncrementalVacuum || res.FreedPages == 0 {
			return
		}
		if !run.Pace(ctx) {
			return
		}
	}
}

// sharedWorkspace reports whether any other session, live or ended, uses path.
func sharedWorkspace(sessions []domain.SessionRecord, rec domain.SessionRecord, path string) bool {
	for _, other := range sessions {
		if other.ID != rec.ID && other.Metadata.WorkspacePath != "" && samePath(other.Metadata.WorkspacePath, path) {
			return true
		}
	}
	return false
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return fmt.Sprintf("%s; %s", a, b)
}

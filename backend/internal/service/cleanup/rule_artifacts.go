package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/artifactversions"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// ArtifactPruner removes a session's version history and rebuildable tool
// directories, keeping its deliverable files. It re-reads the session under
// its own lock and refuses with artifactversions.ErrPruneRefused unless the
// session is still terminated and ended before endedBefore.
type ArtifactPruner interface {
	PruneRegenerable(ctx context.Context, id domain.SessionID, endedBefore time.Time) (int64, error)
}

// ArtifactRetainer archives or deletes a terminated session's artifacts. It
// re-checks that the session is terminated, older than the window and keeps
// its artifacts in the managed directory.
type ArtifactRetainer interface {
	Retain(ctx context.Context, action artifactstorage.Action, olderThanDays int, ids []domain.SessionID) ([]artifactstorage.Result, error)
	// Protected lists the sessions a live backlog item references; their
	// artifacts are kept whatever the policy says.
	Protected(ctx context.Context) (map[domain.SessionID]bool, error)
}

// ArtifactRule cleans the artifact directories of sessions that ended longer
// ago than the retention window. Nothing goes without the user's confirmation
// (ConfirmArtifacts) unless the policy opts in: version history and tool
// directories with AutoPrune, deliverable files (archived or deleted) with
// AutoDeliverables. Live sessions and recently ended ones are never touched,
// and the prune re-reads the session right before removing anything.
type ArtifactRule struct {
	dataDir  string
	pruner   ArtifactPruner
	retainer ArtifactRetainer
}

// NewArtifactRule builds the rule.
func NewArtifactRule(dataDir string, pruner ArtifactPruner, retainer ArtifactRetainer) *ArtifactRule {
	return &ArtifactRule{dataDir: dataDir, pruner: pruner, retainer: retainer}
}

// Category implements Rule.
func (*ArtifactRule) Category() Category { return CategoryArtifacts }

// Light implements Rule: measuring artifact trees is left to the full pass.
func (*ArtifactRule) Light() bool { return false }

// Enabled implements Rule.
func (*ArtifactRule) Enabled(p Policy) bool { return p.Artifacts.Enabled }

// artifactUsage splits an artifact directory into rebuildable bytes (version
// history and tool directories) and deliverable bytes.
type artifactUsage struct {
	regenerable  int64
	deliverables int64
}

func measureArtifacts(ctx context.Context, dir string) (artifactUsage, error) {
	var usage artifactUsage
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == dir {
				return walkErr
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil //nolint:nilerr // cannot happen under WalkDir(dir)
		}
		if regenerablePath(rel) {
			usage.regenerable += info.Size()
		} else {
			usage.deliverables += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return artifactUsage{}, nil
	}
	return usage, err
}

func regenerablePath(rel string) bool {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	if len(segs) > 0 && segs[0] == sessionartifacts.VersionsDir {
		return true
	}
	for _, seg := range segs[:max(len(segs)-1, 0)] {
		if sessionartifacts.RegenerableDir(seg) {
			return true
		}
	}
	return false
}

// Run implements Rule.
func (r *ArtifactRule) Run(ctx context.Context, run *Run) error {
	if r.dataDir == "" || r.pruner == nil || r.retainer == nil {
		return errors.New("artifact cleanup is not configured")
	}
	policy := run.Policy.Artifacts
	retention := time.Duration(policy.RetentionDays) * 24 * time.Hour
	protected, err := r.retainer.Protected(ctx)
	if err != nil {
		return fmt.Errorf("read backlog references: %w", err)
	}
	for _, rec := range run.Sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !rec.IsTerminated || rec.IsTaskPreparation {
			continue
		}
		dir := sessionartifacts.Dir(r.dataDir, rec.ID)
		if custom := rec.Metadata.ArtifactDir; custom != "" && !samePath(custom, dir) {
			// Measured by Settings > Storage, but never archived or deleted:
			// the retention service only manages the canonical directory.
			continue
		}
		usage, err := measureArtifacts(ctx, dir)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if usage.regenerable+usage.deliverables == 0 {
			continue
		}
		base := Item{SessionID: rec.ID, ProjectID: rec.ProjectID, Path: dir}
		if protected[rec.ID] {
			item := base
			item.What, item.Status = "artifacts", StatusKept
			item.Bytes = usage.regenerable + usage.deliverables
			item.Reason = artifactstorage.ProtectedReason
			run.Add(item)
			continue
		}
		if run.Now.Sub(rec.UpdatedAt) < retention {
			item := base
			item.What, item.Status = "artifacts", StatusKept
			item.Bytes = usage.regenerable + usage.deliverables
			item.Reason = fmt.Sprintf("ended less than %d day(s) ago", policy.RetentionDays)
			run.Add(item)
			continue
		}
		if usage.regenerable > 0 {
			run.Add(r.prune(ctx, run, base, usage.regenerable, run.Now.Add(-retention)))
		}
		if usage.deliverables > 0 {
			run.Add(r.deliverables(ctx, run, base, usage.deliverables))
		}
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}

func (r *ArtifactRule) prune(ctx context.Context, run *Run, item Item, bytes int64, endedBefore time.Time) Item {
	item.What, item.Bytes = "version history and tool directories", bytes
	if !run.Policy.Artifacts.AutoPrune && !run.ConfirmArtifacts {
		item.Status = StatusNeedsConfirmation
		item.Reason = "removed only after you confirm"
		return item
	}
	if run.DryRun {
		item.Status = StatusWouldFree
		return item
	}
	freed, err := r.pruner.PruneRegenerable(ctx, item.SessionID, endedBefore)
	item.Bytes = freed
	if errors.Is(err, artifactversions.ErrPruneRefused) {
		item.Bytes, item.Status, item.Reason = 0, StatusKept, err.Error()
		return item
	}
	if err != nil {
		item.Status, item.Reason = StatusFailed, err.Error()
		if freed > 0 {
			// Partly removed: count what went, report the rest.
			item.Status, item.Reason = StatusFreed, "partly removed: "+err.Error()
		}
		return item
	}
	item.Status = StatusFreed
	return item
}

func (r *ArtifactRule) deliverables(ctx context.Context, run *Run, item Item, bytes int64) Item {
	policy := run.Policy.Artifacts
	action := artifactstorage.ActionArchive
	if policy.DeliverableAction == DeliverableDelete {
		action = artifactstorage.ActionDelete
	}
	item.What, item.Bytes = "deliverables ("+string(action)+")", bytes
	if !policy.AutoDeliverables && !run.ConfirmArtifacts {
		item.Status = StatusNeedsConfirmation
		item.Reason = "deliverables are only " + string(action) + "d after you confirm"
		return item
	}
	if run.DryRun {
		item.Status = StatusWouldFree
		return item
	}
	results, err := r.retainer.Retain(ctx, action, policy.RetentionDays, []domain.SessionID{item.SessionID})
	if err != nil {
		item.Status, item.Reason = StatusFailed, err.Error()
		return item
	}
	if len(results) != 1 {
		item.Status, item.Reason = StatusFailed, "no result"
		return item
	}
	res := results[0]
	switch res.Status {
	case artifactstorage.StatusArchived, artifactstorage.StatusDeleted:
		item.Status, item.Bytes = StatusFreed, res.FreedBytes
		if res.ArchivePath != "" {
			if info, err := os.Stat(res.ArchivePath); err == nil {
				item.Bytes = max(res.FreedBytes-info.Size(), 0)
			}
			item.Reason = "archived to " + res.ArchivePath
		}
	case artifactstorage.StatusSkipped:
		item.Status, item.Reason = StatusKept, res.Reason
	default:
		item.Status, item.Reason = StatusFailed, res.Reason
	}
	return item
}

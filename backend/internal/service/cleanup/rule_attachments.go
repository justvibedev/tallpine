package cleanup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AttachmentRetention is how long a canonical chat attachment (pasted images,
// uploads and any converted copies) is kept after its last write once its
// session is no longer live. The worktree projection under
// .tallpine/attachments goes with the worktree itself.
const AttachmentRetention = 14 * 24 * time.Hour

// AttachmentRule removes old canonical attachment files under
// <data>/attachments/<session>. Files of live sessions are never touched, and
// the session is re-read right before each removal.
type AttachmentRule struct{ dataDir string }

// NewAttachmentRule builds the rule.
func NewAttachmentRule(dataDir string) *AttachmentRule { return &AttachmentRule{dataDir: dataDir} }

// Category implements Rule.
func (*AttachmentRule) Category() Category { return CategoryAttachments }

// Light implements Rule: the daily full pass is enough.
func (*AttachmentRule) Light() bool { return false }

// Enabled implements Rule.
func (*AttachmentRule) Enabled(Policy) bool { return true }

// Run implements Rule.
func (r *AttachmentRule) Run(ctx context.Context, run *Run) error {
	if r.dataDir == "" {
		return errors.New("attachment cleanup is not configured")
	}
	root := filepath.Join(r.dataDir, "attachments")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	projects := make(map[domain.SessionID]domain.ProjectID, len(run.Sessions))
	liveIDs := make(map[domain.SessionID]bool, len(run.Sessions))
	for _, rec := range run.Sessions {
		projects[rec.ID] = rec.ProjectID
		liveIDs[rec.ID] = !rec.IsTerminated
	}
	cutoff := run.Now.Add(-AttachmentRetention)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := domain.SessionID(entry.Name())
		dir := filepath.Join(root, entry.Name())
		if !entry.IsDir() || liveIDs[id] {
			continue
		}
		var bytes int64
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			if !run.DryRun {
				if _, _, live := run.Current(ctx, id); live {
					break
				}
				if os.Remove(filepath.Join(dir, f.Name())) != nil {
					continue
				}
			}
			bytes += info.Size()
		}
		if bytes == 0 {
			continue
		}
		item := Item{SessionID: id, ProjectID: projects[id], Path: dir, What: "chat attachments", Bytes: bytes, Status: StatusFreed}
		if run.DryRun {
			item.Status = StatusWouldFree
		} else {
			_ = os.Remove(dir) // only succeeds once empty
		}
		run.Add(item)
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}

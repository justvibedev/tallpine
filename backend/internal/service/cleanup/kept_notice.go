package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// KeptNotice is one worktree cleanup kept for a reason the owner can act on.
type KeptNotice struct {
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	Path      string
	// Reason is the full user-facing text; ReasonKey is its stable class, so a
	// notifier can send once per worktree and reason rather than on every pass.
	Reason    string
	ReasonKey string
	// SalvagePath is where the session's salvaged work lives ("" when none).
	SalvagePath string
}

// KeptNotifier tells the owner a worktree was kept. It runs on real runs only
// and must be idempotent per (SessionID, ReasonKey): the rule calls it on
// every pass that keeps the worktree.
type KeptNotifier func(ctx context.Context, n KeptNotice)

// SetKeptNotifier makes the rule report worktrees it keeps for a reason the
// owner can act on.
func (r *WorktreeRule) SetKeptNotifier(n KeptNotifier) { r.notifyKept = n }

// transientSkipReasons are the session manager's skip reasons (session_manager/
// cleanup.go) for a session that is live again, gone, or being worked on by
// another cleanup: they pass without owner action, so they never notify.
var transientSkipReasons = map[string]bool{
	"session is live":                               true,
	"session not found":                             true,
	"session was restored":                          true,
	"workspace cleanup is already running":          true,
	"workspace in use by a live session":            true,
	"worktree is in use; will retry on a later run": true,
}

// keptReasonKey classifies a keep reason. It returns "" for reasons that are
// expected and need no owner attention (inside retention, in use by a live
// session, retried later), and otherwise a stable key that ignores detail such
// as file names, so a repeat of the same problem keeps the same key.
func keptReasonKey(reason string) string {
	switch {
	case reason == "", strings.HasPrefix(reason, "ended less than"), reason == "in use by a live session",
		strings.HasPrefix(reason, "kept at the last attempt"), reason == "the session is live again",
		reason == "already removed", transientSkipReasons[reason]:
		return ""
	case strings.Contains(reason, "untracked nested git repository"):
		return "nested-repo"
	case strings.Contains(reason, "still dirty after salvage"):
		return "dirty-after-salvage"
	}
	key := reason
	if i := strings.IndexAny(key, ";:"); i >= 0 {
		key = key[:i]
	}
	// "uncommitted changes in sub/dir" is the same problem as without the suffix.
	if i := strings.Index(key, " in "); i >= 0 {
		key = key[:i]
	}
	return strings.TrimSpace(key)
}

// SessionDir is the session's salvage directory, or "" when no snapshot with
// content was written (a failed salvage leaves only an empty stamp directory).
func (s *Salvager) SessionDir(id domain.SessionID) string {
	if s == nil || id == "" {
		return ""
	}
	dir := filepath.Join(s.root, string(id))
	stamps, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, st := range stamps {
		if hasContent(filepath.Join(dir, st.Name())) {
			return dir
		}
	}
	return ""
}

// noteKept marks item as needing the owner's attention when its reason does,
// appends where the salvaged work is, and notifies. A dry run only marks it.
func (r *WorktreeRule) noteKept(ctx context.Context, run *Run, item *Item) {
	key := keptReasonKey(item.Reason)
	if key == "" {
		return
	}
	item.Attention = true
	dir := r.salvage.SessionDir(item.SessionID)
	if dir != "" {
		item.Reason += "; salvaged work: " + dir
	}
	if run.DryRun || r.notifyKept == nil {
		return
	}
	r.notifyKept(ctx, KeptNotice{
		SessionID: item.SessionID, ProjectID: item.ProjectID, Path: item.Path,
		Reason: item.Reason, ReasonKey: key, SalvagePath: dir,
	})
}

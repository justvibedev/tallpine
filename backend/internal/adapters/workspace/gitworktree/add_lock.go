package gitworktree

import (
	"context"
	"strings"
)

// gitAddLockReason is the lock git itself writes while `git worktree add`
// checks the tree out, and removes once it finishes.
const gitAddLockReason = "initializing"

// liftStaleAddLock unlocks a registration still carrying git's own add-time
// lock. Destroy only runs on a workspace Tallpine has retired, so no add is
// still in flight there: the lock outlived a `git worktree add` that died
// midway, and it would otherwise block every later removal. Only the lock is
// lifted; removal stays the ordinary non-forced one, so uncommitted work is
// still refused. A lock with any other reason is someone's deliberate "keep"
// and is left alone. Reports whether the lock was lifted.
func (w *Workspace) liftStaleAddLock(ctx context.Context, repo, path string) bool {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return false
	}
	rec, ok := findWorktree(records, path)
	if !ok || !rec.Locked || rec.LockReason != gitAddLockReason {
		return false
	}
	_, err = w.run(ctx, w.binary, "-C", repo, "worktree", "unlock", path)
	return err == nil
}

// readsGitMessages reports a git command whose messages this adapter reads as
// text, so it runs untranslated: `git worktree` (remove's and add's errors,
// and the "initializing" lock reason add writes) and `git status`. Every other
// command, and any hook it runs, keeps the user's locale.
func readsGitMessages(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-C" || args[i] == "-c" {
			i++
			continue
		}
		return args[i] == "worktree" || args[i] == "status"
	}
	return false
}

// untranslatedGitEnv switches only the message language to English, so
// sorting, dates and character type stay the user's. With LC_MESSAGES=C
// gettext ignores LANGUAGE. LC_ALL would override it, so its value moves to
// LANG, where it still sets every other category; the LC_* values it
// overrode go with it.
func untranslatedGitEnv(environ []string) []string {
	all := ""
	for _, kv := range environ {
		if val, ok := strings.CutPrefix(kv, "LC_ALL="); ok {
			all = val
		}
	}
	out := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "LC_ALL" || key == "LC_MESSAGES":
		case all != "" && (key == "LANG" || strings.HasPrefix(key, "LC_")):
		default:
			out = append(out, kv)
		}
	}
	if all != "" {
		out = append(out, "LANG="+all)
	}
	return append(out, "LC_MESSAGES=C")
}

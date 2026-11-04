package cleanup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SalvageRoot is where salvaged work lives, under the data dir:
// <data dir>/salvage/<session id>/<stamp>/.
const SalvageRoot = "salvage"

// SalvageMaxAge is how long salvaged work is kept before the worktree rule
// deletes it.
const SalvageMaxAge = 30 * 24 * time.Hour

// salvageSkipDirs are untracked directories that are only ever installed or
// generated (dependencies and tool caches), so they are left out of the
// archive. Generic names (build, dist, out, target) can hold authored files,
// such as a Go package named build, so they are archived like any other
// directory, under the size caps.
var salvageSkipDirs = map[string]bool{
	"node_modules": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".turbo": true, ".gradle": true, "DerivedData": true, "__pycache__": true,
	".venv": true, ".tox": true,
}

// salvageMaxTotal caps the untracked archive. Past it the salvage fails and
// the worktree is kept, rather than deleting files that were not saved.
var salvageMaxTotal int64 = 1 << 30

// salvageMaxFile leaves single untracked files above this size out of the
// archive (datasets, binaries, disk images); the README lists them.
const salvageMaxFile = 50 << 20

// PRLookup lists a session's pull requests.
type PRLookup func(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)

// Salvager saves what removing an ended session's worktree would lose, so the
// worktree can go even when it is dirty: the uncommitted diff as a patch, the
// untracked files as an archive and commits no other ref keeps as a bundle.
// It only salvages for a session with no open pull request: an open PR means
// the work is still in review, and its worktree is kept as before.
type Salvager struct {
	root string
	prs  PRLookup
	now  func() time.Time
}

// NewSalvager builds a Salvager writing under <dataDir>/salvage. A nil prs
// treats every session as having no pull request.
func NewSalvager(dataDir string, prs PRLookup) *Salvager {
	return &Salvager{root: filepath.Join(dataDir, SalvageRoot), prs: prs, now: time.Now}
}

// Root is the salvage directory.
func (s *Salvager) Root() string { return s.root }

// eligible reports whether the session's worktrees may be salvaged and
// removed, or why not.
func (s *Salvager) eligible(ctx context.Context, id domain.SessionID) (string, bool) {
	if s.prs == nil {
		return "", true
	}
	prs, err := s.prs(ctx, id)
	if err != nil {
		return "pull request state could not be read", false
	}
	for _, pr := range prs {
		switch pr.State() {
		case domain.PRStateMerged, domain.PRStateClosed:
		case domain.PRStateDraft:
			return "its pull request is still a draft", false
		default:
			return "its pull request is still open", false
		}
	}
	return "", true
}

// salvageStep is how inspectWorktrees treats a worktree that holds work:
// with apply nil it only plans (the work would be salvaged), otherwise it
// salvages before allowing removal.
type salvageStep struct {
	apply func(ctx context.Context, path, reason string) error
}

// step returns the salvage step for a session, or a reason to keep it.
func (s *Salvager) step(ctx context.Context, rec domain.SessionRecord, apply bool) (*salvageStep, string) {
	if s == nil {
		return nil, ""
	}
	if why, ok := s.eligible(ctx, rec.ID); !ok {
		return nil, why
	}
	st := &salvageStep{}
	if apply {
		stamp := s.now().UTC().Format("20060102T150405Z")
		st.apply = func(ctx context.Context, path, reason string) error {
			if nested := untrackedNestedRepo(ctx, path); nested != "" {
				return fmt.Errorf("untracked nested git repository %s", nested)
			}
			if err := s.salvage(ctx, rec, stamp, path, reason); err != nil {
				return err
			}
			return discardSalvaged(ctx, path)
		}
	}
	return st, ""
}

// discardSalvaged drops the uncommitted changes salvage just saved. Teardown
// (gitworktree Destroy) refuses any worktree git status reports dirty, so
// leaving them would keep the worktree after all and salvage it again on
// every pass (B537). Commits stay on the branch and stashes in the
// repository; ignored files are left for the removal to take.
func discardSalvaged(ctx context.Context, path string) error {
	if _, err := runGitIn(ctx, path, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return nil //nolint:nilerr // no commit to reset to: the worktree is kept as before
	}
	if _, err := runGitIn(ctx, path, "reset", "-q", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("discard salvaged changes: %w", err)
	}
	if _, err := runGitIn(ctx, path, "clean", "-q", "-f", "-d"); err != nil {
		return fmt.Errorf("discard salvaged untracked files: %w", err)
	}
	if left, err := runGitIn(ctx, path, "status", "--porcelain"); err != nil || left != "" {
		return fmt.Errorf("still dirty after salvage: %s", orNone(strings.Join(limit(strings.Split(left, "\n"), 5), ", ")))
	}
	return nil
}

// untrackedNestedRepo names an untracked directory holding its own git
// repository. git clean -fd leaves those in place, so the worktree would stay
// dirty and be salvaged again on every pass; it is kept before salvaging.
func untrackedNestedRepo(ctx context.Context, path string) string {
	// ls-files lists untracked files one by one and stops at a nested
	// repository, printing it as "dir/" even under an untracked parent
	// (status --porcelain would collapse that parent into one line); -z keeps
	// unusual names unquoted.
	out, err := gitOutput(ctx, path, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return ""
	}
	for _, rel := range strings.Split(string(out), "\x00") {
		if !strings.HasSuffix(rel, "/") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(path, rel, ".git")); err == nil {
			return rel
		}
	}
	return ""
}

// salvage writes one worktree's leftover work to <root>/<id>/<stamp>/<name>,
// building it in a temporary directory first so a partial salvage is never
// mistaken for a complete one.
func (s *Salvager) salvage(ctx context.Context, rec domain.SessionRecord, stamp, path, reason string) error {
	base := filepath.Join(s.root, string(rec.ID), stamp)
	name := "workspace"
	if ws := rec.Metadata.WorkspacePath; ws != "" {
		if rel, err := filepath.Rel(ws, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			name = strings.ReplaceAll(filepath.ToSlash(rel), "/", "__")
		}
	}
	final := filepath.Join(base, name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(base, "."+name+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	branch, _ := runGitIn(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	head, _ := runGitIn(ctx, path, "rev-parse", "HEAD")
	var notes []string
	if head != "" {
		diff, err := gitOutput(ctx, path, "diff", "--binary", "HEAD")
		if err != nil {
			return fmt.Errorf("salvage diff: %w", err)
		}
		if len(diff) > 0 {
			if err := os.WriteFile(filepath.Join(tmp, "changes.patch"), diff, 0o600); err != nil {
				return err
			}
			notes = append(notes, "- `changes.patch`: staged and unstaged changes against HEAD. Restore: `git apply changes.patch`.")
		}
	}
	skipped, n, err := archiveUntracked(ctx, path, filepath.Join(tmp, "untracked.tar.gz"))
	if err != nil {
		return fmt.Errorf("salvage untracked files: %w", err)
	}
	if n > 0 {
		notes = append(notes, fmt.Sprintf("- `untracked.tar.gz`: %d untracked file(s). Restore: `tar xzf untracked.tar.gz` in the checkout.", n))
	} else {
		_ = os.Remove(filepath.Join(tmp, "untracked.tar.gz"))
	}
	if head != "" {
		args := []string{"rev-list", "--max-count=1", "HEAD", "--not", "--remotes", "--tags"}
		if branch != "HEAD" {
			args = append(args, "--exclude="+escapeGlob(branch))
		}
		args = append(args, "--branches")
		if unique, err := runGitIn(ctx, path, args...); err == nil && unique != "" {
			bundle := append([]string{"bundle", "create", filepath.Join(tmp, "commits.bundle"), "HEAD"}, args[3:]...)
			if _, err := runGitIn(ctx, path, bundle...); err != nil {
				return fmt.Errorf("salvage commits: %w", err)
			}
			notes = append(notes, "- `commits.bundle`: commits no other branch, remote or tag holds. Restore: `git fetch commits.bundle HEAD:salvaged`.")
		}
	}
	if len(skipped) > 0 {
		notes = append(notes, "- Left out (dependencies or caches, a symlink or over 50 MB): "+strings.Join(limit(skipped, 20), ", "))
	}
	readme := fmt.Sprintf(`# Salvaged work of session %s

- Worktree: %s
- Branch: %s
- Head: %s
- Kept because: %s
- Pull requests: %s
- Salvaged: %s

%s
Stashes stay in the repository (`+"`git stash list`"+`) and the branch keeps its commits.
Tallpine deletes this directory %d days after it was written.
`, rec.ID, path, orNone(branch), orNone(head), reason, s.prSummary(ctx, rec.ID), s.now().UTC().Format(time.RFC3339),
		strings.Join(notes, "\n")+"\n", int(SalvageMaxAge.Hours()/24))
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte(readme), 0o600); err != nil {
		return err
	}
	if _, err := os.Lstat(final); err == nil {
		final += "-" + filepath.Base(tmp)[len(name)+2:]
	}
	return os.Rename(tmp, final)
}

func (s *Salvager) prSummary(ctx context.Context, id domain.SessionID) string {
	if s.prs == nil {
		return "none"
	}
	prs, err := s.prs(ctx, id)
	if err != nil || len(prs) == 0 {
		return "none"
	}
	var out []string
	for _, pr := range prs {
		state := "open"
		switch {
		case pr.Merged:
			state = "merged"
		case pr.Closed:
			state = "closed"
		}
		out = append(out, pr.URL+" ("+state+")")
	}
	return strings.Join(out, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func limit(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(s[:n:n], fmt.Sprintf("and %d more", len(s)-n))
}

func gitCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	return ports.DefaultGit().Plain(ctx, path, args...)
}

func gitOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := gitCommand(ctx, path, args...)
	return cmd.Output()
}

// archiveUntracked writes the worktree's untracked, non-ignored files to
// dest, skipping rebuilt directories, symlinks and very large files. It
// returns the skipped paths and how many files it archived.
func archiveUntracked(ctx context.Context, root, dest string) ([]string, int, error) {
	out, err := gitOutput(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	var skipped []string
	n := 0
	var total int64
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if skipSalvagePath(rel) {
			skipped = append(skipped, firstSkippedDir(rel))
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > salvageMaxFile {
			skipped = append(skipped, rel)
			continue
		}
		if total += info.Size(); total > salvageMaxTotal {
			return nil, 0, fmt.Errorf("untracked files exceed %d MB", salvageMaxTotal>>20)
		}
		if err := addFile(tw, full, rel, info); err != nil {
			return nil, 0, err
		}
		n++
	}
	if err := tw.Close(); err != nil {
		return nil, 0, err
	}
	if err := gz.Close(); err != nil {
		return nil, 0, err
	}
	return dedupe(skipped), n, f.Close()
}

func addFile(tw *tar.Writer, full, rel string, info fs.FileInfo) error {
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	src, err := os.Open(full)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	_, err = io.CopyN(tw, src, info.Size())
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%s shrank while it was archived", rel)
	}
	return err
}

func skipSalvagePath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if salvageSkipDirs[p] {
			return true
		}
	}
	return false
}

func firstSkippedDir(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts[:len(parts)-1] {
		if salvageSkipDirs[p] {
			return strings.Join(parts[:i+1], "/") + "/"
		}
	}
	return rel
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// pruneSalvage deletes salvaged work older than SalvageMaxAge.
func (s *Salvager) pruneSalvage(ctx context.Context, run *Run) {
	if s == nil {
		return
	}
	sessions, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	cutoff := run.Now.Add(-SalvageMaxAge)
	for _, sess := range sessions {
		if !sess.IsDir() || strings.HasPrefix(sess.Name(), ".") {
			continue
		}
		dir := filepath.Join(s.root, sess.Name())
		stamps, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, st := range stamps {
			info, err := st.Info()
			if err != nil || !st.IsDir() || !info.ModTime().Before(cutoff) {
				continue
			}
			path := filepath.Join(dir, st.Name())
			item := Item{SessionID: domain.SessionID(sess.Name()), What: "salvaged work", Path: path, Status: StatusFreed}
			item.Bytes, _ = dirSize(ctx, path)
			if run.DryRun {
				item.Status = StatusWouldFree
			} else if err := os.RemoveAll(path); err != nil {
				item.Status, item.Reason = StatusFailed, err.Error()
			}
			run.Add(item)
		}
		if !run.DryRun {
			_ = os.Remove(dir) // only succeeds once empty.
		}
	}
}

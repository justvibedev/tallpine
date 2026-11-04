// Package sessioncommit stages and commits in one session's own worktree on the
// session's behalf. A DeepSeek Harness worker's sandbox cannot write the main
// checkout's .git/worktrees/<id>/ directory, so git add/commit fail there; the
// daemon (outside the sandbox) runs exactly those two operations instead. The
// argument vector is fixed, hooks are off, and nothing is ever pushed.
package sessioncommit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// MaxMessageBytes bounds the commit message.
const MaxMessageBytes = 16 * 1024

var (
	// ErrSessionNotFound is returned when: no such session.
	ErrSessionNotFound = errors.New("sessioncommit: session not found")
	// ErrNoWorkspace is returned when: the session has no worktree of its own to commit in.
	ErrNoWorkspace = errors.New("sessioncommit: session has no worktree")
	// ErrNotOwnWorktree is returned when: the recorded workspace is not a git worktree root.
	ErrNotOwnWorktree = errors.New("sessioncommit: recorded workspace is not the root of a git worktree")
	// ErrBadMessage is returned when: the commit message is empty or too long.
	ErrBadMessage = errors.New("sessioncommit: commit message must be 1 to 16384 bytes")
	// ErrNothingToCommit is returned when: the worktree has no changes to commit.
	ErrNothingToCommit = errors.New("sessioncommit: nothing to commit")
)

// Sessions looks sessions up.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Result is the new commit.
type Result struct {
	SHA string
}

// Service commits in session worktrees.
type Service struct {
	sessions Sessions
	log      *slog.Logger
	mu       sync.Mutex // one git operation at a time; index.lock races are not worth handling
}

// New builds the service.
func New(sessions Sessions, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{sessions: sessions, log: log}
}

// Commit stages every change in the session's worktree and commits it with
// message. It only ever touches the worktree recorded for the session.
func (s *Service) Commit(ctx context.Context, id domain.SessionID, message string) (Result, error) {
	message = strings.TrimSpace(message)
	if message == "" || len(message) > MaxMessageBytes {
		return Result{}, ErrBadMessage
	}
	rec, ok, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("load session: %w", err)
	}
	if !ok {
		return Result{}, ErrSessionNotFound
	}
	dir := rec.Metadata.WorkspacePath
	if dir == "" || !filepath.IsAbs(dir) {
		return Result{}, ErrNoWorkspace
	}
	gitDir, err := s.worktreeGitDir(ctx, rec.Metadata.WorkspaceRepoPath, dir)
	if err != nil {
		s.log.Warn("commit refused: session worktree not resolvable", "session", id, "project", rec.ProjectID, "error", err)
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	g := gitCtx{dir: dir, gitDir: gitDir}
	if _, err := s.run(ctx, g, "add", "-A"); err != nil {
		return Result{}, err
	}
	switch _, err := s.run(ctx, g, "diff", "--cached", "--quiet", "--no-ext-diff"); {
	case err == nil:
		return Result{}, ErrNothingToCommit
	case !isExitCode(err, 1):
		return Result{}, err
	}
	if _, err := s.run(ctx, g, "commit", "--no-verify", "--no-gpg-sign", "-m", message); err != nil {
		return Result{}, err
	}
	sha, err := s.run(ctx, g, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}
	s.log.Info("committed on behalf of session", "session", id, "project", rec.ProjectID, "sha", sha)
	return Result{SHA: sha}, nil
}

// worktreeGitDir finds the session worktree's git directory from state the
// sandboxed worker cannot write: <common git dir>/worktrees/<name>, whose
// "gitdir" back-pointer file must name <dir>/.git. The worktree's own .git file
// is worker-controlled (it could point at a git dir with filter or fsmonitor
// config that would run code outside the sandbox) and is never read.
//
// The common git dir is asked of git for the daemon-recorded main checkout, so
// a checkout whose .git is a file (separate git dir) still resolves. The
// back-pointer may be relative to its own directory (worktree.useRelativePaths).
// Every refusal wraps ErrNotOwnWorktree with the check that failed.
func (s *Service) worktreeGitDir(ctx context.Context, repo, dir string) (string, error) {
	if repo == "" || !filepath.IsAbs(repo) {
		return "", fmt.Errorf("%w: no recorded main checkout", ErrNotOwnWorktree)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("%w: worktree directory: %w", ErrNotOwnWorktree, err)
	}
	common := filepath.Join(repo, ".git")
	if fi, statErr := os.Stat(common); statErr != nil || !fi.IsDir() {
		out, gerr := s.commonDir(ctx, repo)
		if gerr != nil {
			return "", fmt.Errorf("%w: main checkout %s has no git dir: %w", ErrNotOwnWorktree, repo, gerr)
		}
		common = out
	}
	root := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotOwnWorktree, err)
	}
	for _, e := range entries {
		gd := filepath.Join(root, e.Name())
		raw, err := os.ReadFile(filepath.Join(gd, "gitdir")) //nolint:gosec // path built from the daemon-recorded repo
		if err != nil {
			continue
		}
		ptr := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(ptr) {
			ptr = filepath.Join(gd, ptr)
		}
		if filepath.Base(ptr) != ".git" {
			continue
		}
		got, err := filepath.EvalSymlinks(filepath.Dir(ptr))
		if err == nil && got == wantDir {
			return gd, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not registered under %s", ErrNotOwnWorktree, wantDir, root)
}

// commonDir asks git for the common git directory of the recorded main checkout.
func (s *Service) commonDir(ctx context.Context, repo string) (string, error) {
	cmd := ports.DefaultGit().Isolated(ctx, repo, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0"}, "-c", "core.fsmonitor=false", "rev-parse", "--path-format=absolute", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" || !filepath.IsAbs(p) {
		return "", errors.New("git returned no absolute common dir")
	}
	return p, nil
}

type gitCtx struct{ dir, gitDir string }

func isExitCode(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

// run executes git with a fixed prefix that disables hooks and anything else a
// repository's config could use to run code outside the sandbox. GIT_DIR and
// GIT_WORK_TREE are explicit so the worktree's .git file is never consulted.
func (s *Service) run(ctx context.Context, g gitCtx, args ...string) (string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "commit.gpgsign=false", "-c", "core.sshCommand=false",
	}, args...)
	cmd := ports.DefaultGit().Isolated(ctx, g.dir, []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"GIT_DIR=" + g.gitDir, "GIT_WORK_TREE=" + g.dir,
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
	}, full...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if len(args) > 0 && args[0] == "diff" {
			return "", err // --quiet signals "has changes" by exit 1
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Package update implements the supported "Update & restart" flow: it reports
// whether a CI-passed build newer than the running one exists and starts a
// detached job (job.sh) that installs it with scripts/release-local, verifies
// the new daemon, rolls back on failure and always leaves an app running.
package update

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
)

//go:embed job.sh
var jobScript []byte

// Errors the controller maps to HTTP statuses.
var (
	ErrNotCIPassed = errors.New("commit has no passing local CI row")
	ErrNotOnMain   = errors.New("commit is not on origin/main")
	ErrDowngrade   = errors.New("commit is older than the installed one; downgrades need explicit confirmation")
	ErrRunning     = errors.New("another update is already running")
	ErrUnavailable = errors.New("update is not available: no release checkout was found")
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Result is the recorded outcome of the latest update job (result.json).
type Result struct {
	State      string `json:"state"` // running|succeeded|failed|rolled_back
	Commit     string `json:"commit"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Message    string `json:"message"`
}

// Status is what the UI, mobile app and `tallpine update status` show.
type Status struct {
	InstalledCommit string  `json:"installedCommit"`
	LatestCommit    string  `json:"latestCommit"`    // origin/main head
	LatestCIPassed  bool    `json:"latestCiPassed"`  // head itself has a pass row
	Candidate       string  `json:"candidate"`       // newest origin/main commit with a pass row
	UpdateAvailable bool    `json:"updateAvailable"` // candidate is newer than installed
	BuildAvailable  bool    `json:"buildAvailable"`  // candidate is already built
	Running         bool    `json:"running"`
	Last            *Result `json:"last,omitempty"`
}

// Service reports update status and starts update jobs.
type Service struct {
	// Installed is the running build's commit.
	Installed string
	// StateDir is the state root (~/.tallpine); the job's files live in StateDir/update.
	StateDir string
	// Repo is the git checkout used for builds; empty discovers it from the
	// release-builds worktrees, or TALLPINE_RELEASE_REPO.
	Repo string
	// CITSV is the local CI results file.
	CITSV string
	// Port is this daemon's port, probed by the job.
	Port int
	// Socket is this daemon's user-only socket; the job probes it when it
	// exists (the daemon has no loopback TCP by default, B176).
	Socket string
	// NoFetch skips `git fetch` (tests).
	NoFetch bool
	// Launch starts the job; nil uses a setsid child. Tests replace it.
	Launch func(script string, env []string, logPath string) (pid int, err error)

	mu        sync.Mutex
	lastFetch time.Time
}

func (s *Service) dir() string { return filepath.Join(s.StateDir, "update") }

func (s *Service) repo() string {
	if s.Repo != "" {
		return s.Repo
	}
	if v := brand.Getenv("RELEASE_REPO"); v != "" {
		return v
	}
	matches, _ := filepath.Glob(filepath.Join(s.StateDir, "release-builds", "*", ".git"))
	for _, m := range matches {
		common, err := ports.DefaultGit().CommonDir(context.Background(), filepath.Dir(m))
		if err == nil {
			if filepath.Base(common) == ".git" {
				return filepath.Dir(common)
			}
		}
	}
	return ""
}

func (s *Service) ciTSV() string {
	if s.CITSV != "" {
		return s.CITSV
	}
	if v := brand.Getenv("RELEASE_CI_TSV"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tallpine", "local-ci", "results", "local-ci.tsv")
}

func git(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := ports.DefaultGit().Run(ctx, repo, args...)
	return strings.TrimSpace(out), err
}

// ciPassed reports whether the latest row for sha is a pass, like
// scripts/release-local's ci_row.
func (s *Service) ciPassed(sha string) bool {
	data, err := os.ReadFile(s.ciTSV())
	if err != nil {
		return false
	}
	pass := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 3 && f[1] == sha {
			pass = f[2] == "pass"
		}
	}
	return pass
}

func (s *Service) fetch(ctx context.Context, repo string) {
	if s.NoFetch {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastFetch) < time.Minute {
		return
	}
	s.lastFetch = time.Now()
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = git(fctx, repo, "fetch", "--quiet", "origin", "main")
}

// LastResult reads the recorded result of the latest job, if any.
func (s *Service) LastResult() *Result {
	data, err := os.ReadFile(filepath.Join(s.dir(), "result.json"))
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(data, &r) != nil || r.State == "" {
		return nil
	}
	return &r
}

// running reports whether a job holds the lock with a live pid.
func (s *Service) running() bool {
	// A job just launched has not taken its lock yet.
	if r := s.LastResult(); r != nil && r.State == "running" && r.Message == "queued" {
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil && time.Since(t) < time.Minute {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(s.dir(), "lock", "pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && pid > 0 && pidAlive(pid)
}

func (s *Service) built(sha string) bool {
	m, _ := filepath.Glob(filepath.Join(s.StateDir, "release-builds", sha, "frontend", "out", "*", "*.app"))
	return len(m) > 0
}

// Status computes the update status.
func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{InstalledCommit: s.Installed, Running: s.running(), Last: s.LastResult()}
	repo := s.repo()
	if repo == "" {
		return st, nil
	}
	s.fetch(ctx, repo)
	head, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "origin/main^{commit}")
	if err != nil {
		return st, nil //nolint:nilerr // no origin/main yet: report what is known
	}
	st.LatestCommit = head
	st.LatestCIPassed = s.ciPassed(head)
	revs, err := git(ctx, repo, "rev-list", "--max-count=100", "origin/main")
	if err != nil {
		return st, nil //nolint:nilerr // history unreadable: report what is known
	}
	for _, sha := range strings.Fields(revs) {
		if s.ciPassed(sha) {
			st.Candidate = sha
			break
		}
	}
	if st.Candidate != "" && st.Candidate != s.Installed {
		st.UpdateAvailable = true
		// An installed commit that already contains the candidate is newer.
		if s.Installed != "" {
			if _, err := git(ctx, repo, "merge-base", "--is-ancestor", st.Candidate, s.Installed); err == nil {
				st.UpdateAvailable = false
			}
		}
	}
	st.BuildAvailable = st.Candidate != "" && s.built(st.Candidate)
	return st, nil
}

// Start validates commit (refusing a downgrade unless allowDowngrade) and launches the detached update job. It refuses a
// commit without a passing CI row or outside origin/main, and refuses while
// another update runs.
func (s *Service) Start(ctx context.Context, commit string, allowDowngrade bool) (Result, error) {
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !shaRE.MatchString(commit) {
		return Result{}, fmt.Errorf("commit must be a hex sha")
	}
	repo := s.repo()
	if repo == "" {
		return Result{}, ErrUnavailable
	}
	s.fetch(ctx, repo)
	full, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", commit+"^{commit}")
	if err != nil {
		return Result{}, fmt.Errorf("unknown commit %s", commit)
	}
	if !s.ciPassed(full) {
		return Result{}, ErrNotCIPassed
	}
	if _, err := git(ctx, repo, "merge-base", "--is-ancestor", full, "origin/main"); err != nil {
		return Result{}, ErrNotOnMain
	}

	if !allowDowngrade && s.Installed != "" && full != s.Installed {
		if _, err := git(ctx, repo, "merge-base", "--is-ancestor", full, s.Installed); err == nil {
			return Result{}, ErrDowngrade
		}
	}

	// Serialize concurrent Starts in this process; job.sh's lock covers the rest.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running() {
		return Result{}, ErrRunning
	}
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return Result{}, err
	}
	script := filepath.Join(s.dir(), "job.sh")
	if err := os.WriteFile(script, jobScript, 0o600); err != nil {
		return Result{}, err
	}
	env := append(os.Environ(),
		"UPDATE_SHA="+full,
		"UPDATE_STATE_DIR="+s.StateDir,
		"UPDATE_RELEASE_LOCAL="+filepath.Join(repo, "scripts", "release-local"),
		"UPDATE_PORT="+strconv.Itoa(s.Port),
		"UPDATE_SOCKET="+s.Socket,
		"PATH="+os.Getenv("PATH")+":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
	)
	launch := s.Launch
	if launch == nil {
		launch = launchDetached
	}
	// Reserve the slot before launching; the job replaces it with its own state.
	res := Result{State: "running", Commit: full, StartedAt: time.Now().UTC().Format(time.RFC3339), Message: "queued"}
	data, _ := json.Marshal(res)
	resultPath := filepath.Join(s.dir(), "result.json")
	prev, _ := os.ReadFile(resultPath)
	if err := os.WriteFile(resultPath, data, 0o600); err != nil {
		return Result{}, err
	}
	if _, err := launch(script, env, filepath.Join(s.dir(), "launch.log")); err != nil {
		if prev != nil {
			_ = os.WriteFile(resultPath, prev, 0o600) // #nosec G703 -- path is the daemon update dir plus a fixed file name
		} else {
			_ = os.Remove(resultPath)
		}
		return Result{}, fmt.Errorf("start update job: %w", err)
	}
	return res, nil
}

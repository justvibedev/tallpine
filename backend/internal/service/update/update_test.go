package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fixture struct {
	t     *testing.T
	repo  string
	svc   *Service
	shas  []string // oldest first, all on origin/main
	state string
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFixture makes a repo with three commits on origin/main.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	os.MkdirAll(repo, 0o700)
	run(t, repo, "init", "-q", "-b", "main")
	f := &fixture{t: t, repo: repo, state: filepath.Join(tmp, "state")}
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(repo, n), []byte(n), 0o600)
		run(t, repo, "add", ".")
		run(t, repo, "commit", "-q", "-m", n)
		f.shas = append(f.shas, run(t, repo, "rev-parse", "HEAD"))
	}
	run(t, repo, "update-ref", "refs/remotes/origin/main", f.shas[2])
	f.svc = &Service{StateDir: f.state, Repo: repo, CITSV: filepath.Join(tmp, "ci.tsv"), NoFetch: true, Port: 3101, Installed: f.shas[0]}
	f.svc.Launch = func(string, []string, string) (int, error) { return 1, nil }
	return f
}

func (f *fixture) ci(sha, verdict string) {
	f.t.Helper()
	fh, err := os.OpenFile(f.svc.CITSV, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	fh.WriteString("main\t" + sha + "\t" + verdict + "\tsummary\n")
}

func TestStatusPicksNewestCIPassedCommit(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[1], "pass")
	f.ci(f.shas[2], "fail")
	st, err := f.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Candidate != f.shas[1] || st.LatestCommit != f.shas[2] || st.LatestCIPassed || !st.UpdateAvailable {
		t.Fatalf("%+v", st)
	}
}

func TestStatusNoUpdateWhenInstalledIsNewerOrEqual(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[1], "pass")
	f.svc.Installed = f.shas[2] // contains the candidate
	st, _ := f.svc.Status(context.Background())
	if st.UpdateAvailable {
		t.Fatalf("%+v", st)
	}
	f.svc.Installed = f.shas[1]
	if st, _ = f.svc.Status(context.Background()); st.UpdateAvailable {
		t.Fatalf("%+v", st)
	}
}

func TestStatusLaterCIFailOverridesPass(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[2], "pass")
	f.ci(f.shas[2], "fail")
	if st, _ := f.svc.Status(context.Background()); st.Candidate == f.shas[2] {
		t.Fatalf("failed commit offered: %+v", st)
	}
}

func TestStatusReportsPrebuiltAppAndLastResult(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[2], "pass")
	os.MkdirAll(filepath.Join(f.state, "release-builds", f.shas[2], "frontend", "out", "x", "Tallpine.app"), 0o700)
	os.MkdirAll(filepath.Join(f.state, "update"), 0o700)
	os.WriteFile(filepath.Join(f.state, "update", "result.json"), []byte(`{"state":"rolled_back","commit":"x","message":"m"}`), 0o600)
	st, _ := f.svc.Status(context.Background())
	if !st.BuildAvailable || st.Last == nil || st.Last.State != "rolled_back" || st.Running {
		t.Fatalf("%+v", st)
	}
}

func TestStartRefusesCommitWithoutPassingCI(t *testing.T) {
	f := newFixture(t)
	launched := false
	f.svc.Launch = func(string, []string, string) (int, error) { launched = true; return 1, nil }
	f.ci(f.shas[2], "fail")
	for _, sha := range []string{f.shas[2], f.shas[1]} { // failing row, no row
		if _, err := f.svc.Start(context.Background(), sha, false); !errors.Is(err, ErrNotCIPassed) {
			t.Fatalf("%s: err = %v", sha, err)
		}
	}
	if launched {
		t.Fatal("job launched for a non-passed commit")
	}
}

func TestStartRefusesCommitOutsideOriginMain(t *testing.T) {
	f := newFixture(t)
	run(t, f.repo, "checkout", "-q", "-b", "side")
	os.WriteFile(filepath.Join(f.repo, "z"), []byte("z"), 0o600)
	run(t, f.repo, "add", ".")
	run(t, f.repo, "commit", "-q", "-m", "z")
	side := run(t, f.repo, "rev-parse", "HEAD")
	f.ci(side, "pass")
	if _, err := f.svc.Start(context.Background(), side, false); !errors.Is(err, ErrNotOnMain) {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRejectsMalformedCommit(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Start(context.Background(), "--upload-pack=x", false); err == nil {
		t.Fatal("accepted a non-sha")
	}
}

func TestStartLaunchesDetachedJobWithCommit(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[2], "pass")
	var env []string
	f.svc.Launch = func(_ string, e []string, _ string) (int, error) { env = e; return 1, nil }
	res, err := f.svc.Start(context.Background(), f.shas[2][:10], false)
	if err != nil || res.State != "running" || res.Commit != f.shas[2] {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(strings.Join(env, "\n"), "UPDATE_SHA="+f.shas[2]+"\n") {
		t.Fatalf("env missing sha: %v", env)
	}
}

func TestStartRefusesWhileAnotherRuns(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[2], "pass")
	n := 0
	f.svc.Launch = func(string, []string, string) (int, error) { n++; return 1, nil }
	if _, err := f.svc.Start(context.Background(), f.shas[2], false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Start(context.Background(), f.shas[2], false); !errors.Is(err, ErrRunning) {
		t.Fatalf("second start err = %v", err)
	}
	if n != 1 {
		t.Fatalf("launched %d jobs", n)
	}
}

func TestStartRefusesWhileJobHoldsLock(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[2], "pass")
	lock := filepath.Join(f.state, "update", "lock")
	os.MkdirAll(lock, 0o700)
	os.WriteFile(filepath.Join(lock, "pid"), []byte(itoaPID()), 0o600)
	if _, err := f.svc.Start(context.Background(), f.shas[2], false); !errors.Is(err, ErrRunning) {
		t.Fatalf("err = %v", err)
	}
}

func itoaPID() string { return strconv.Itoa(os.Getpid()) }

func TestStartRefusesDowngradeUnlessConfirmed(t *testing.T) {
	f := newFixture(t)
	f.ci(f.shas[1], "pass")
	f.svc.Installed = f.shas[2]
	if _, err := f.svc.Start(context.Background(), f.shas[1], false); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.Start(context.Background(), f.shas[1], true); err != nil {
		t.Fatalf("confirmed downgrade: %v", err)
	}
}

func TestCITSVDefaultsUnderTallpineHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TALLPINE_RELEASE_CI_TSV", "")
	want := filepath.Join(home, ".tallpine", "local-ci", "results", "local-ci.tsv")
	if got := (&Service{}).ciTSV(); got != want {
		t.Fatalf("ciTSV() = %q, want %q", got, want)
	}
}

package releaseinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type fixture struct {
	state string
	in    *Installer
	argv  []string
	env   []string
	// verifyArgs is the last codesign invocation; signedBy is the Team ID the
	// fake codesign accepts in the -R requirement.
	verifyArgs []string
	signedBy   string
}

// newFixture lays out release-builds/<sha> with an app and script.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	state := t.TempDir()
	build := filepath.Join(state, "release-builds", testSHA)
	for _, d := range []string{filepath.Join(build, "frontend", "out", "x-arm64", brand.ProductName+".app"), filepath.Join(build, "scripts", "lib")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(build, "scripts", "release-local"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, build)
	f := &fixture{state: state, signedBy: "TEAMOK1234"}
	f.in = &Installer{
		StateDir: state, TeamID: "TEAMOK1234",
		Codesign: func(_ context.Context, args ...string) (string, error) {
			f.verifyArgs = args
			if !strings.Contains(strings.Join(args, " "), `leaf[subject.OU] = "`+f.signedBy+`"`) {
				return "does not satisfy requirement", errors.New("exit 3")
			}
			return "", nil
		},
		Clone: func(_ context.Context, src, dst string) error { return os.CopyFS(dst, os.DirFS(src)) },
		Start: func(argv, env []string, _ string) (int, error) { f.argv, f.env = argv, env; return 42, nil },
	}
	return f
}

func TestInstallStartsVerifiedBuild(t *testing.T) {
	f := newFixture(t)
	res, err := f.in.Install(context.Background(), testSHA)
	if err != nil {
		t.Fatal(err)
	}
	if res.PID != 42 || !strings.HasSuffix(f.argv[0], "/scripts/release-local") {
		t.Fatalf("result %+v argv %v", res, f.argv)
	}
	if strings.Contains(f.argv[0], filepath.Join("release-builds", testSHA)) {
		t.Fatalf("installer must run from the staged copy, not the build tree: %s", f.argv[0])
	}
	if !strings.Contains(f.argv[2], "release-stage") || strings.Contains(f.argv[2], "release-builds") {
		t.Fatalf("installer must receive the staged app clone: %s", f.argv[2])
	}
	staged, err := os.ReadFile(f.argv[0])
	if err != nil || string(staged) != "#!/bin/sh\n" {
		t.Fatalf("staged script %v %q", err, staged)
	}
	got := strings.Join(f.argv[1:], " ")
	if !strings.Contains(got, "--no-fetch") || !slices.Contains(f.env, "TALLPINE_RELEASE_SKIP_STALE_CHECK=1") {
		t.Fatalf("pinned build must skip fetch and the stale check: argv %v env %v", f.argv, f.env)
	}
	// B631: nothing is sent when the install ends; the outcome goes to the result file next to the log.
	if !strings.Contains(got, "install ") || !strings.Contains(got, "--yes") || strings.Contains(got, "--notify") || !strings.HasSuffix(got, "--log "+res.Log) {
		t.Fatalf("argv = %v", f.argv)
	}
	if res.ResultFile != res.Log+".result" {
		t.Fatalf("result file = %q, want next to the log %q", res.ResultFile, res.Log)
	}
}

func TestInstallRefusesBadSHA(t *testing.T) {
	f := newFixture(t)
	for _, sha := range []string{"", "../x", testSHA[:39], strings.ToUpper(testSHA), testSHA + "/.."} {
		if _, err := f.in.Install(context.Background(), sha); !errors.Is(err, ErrRefused) {
			t.Errorf("sha %q: err = %v, want refusal", sha, err)
		}
	}
}

func TestInstallRefusesSymlinkEscape(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, brand.ProductName+".app"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(f.state, "release-builds", testSHA, "frontend", "out", "x-arm64")
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, out); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want refusal for an app that resolves outside the build", err)
	}
	if f.argv != nil {
		t.Fatal("installer must not start")
	}
}

func TestInstallVerifiesWithAnchoredRequirement(t *testing.T) {
	f := newFixture(t)
	if _, err := f.in.Install(context.Background(), testSHA); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.verifyArgs, " ")
	for _, want := range []string{"--verify", "--deep", "--strict", "-R=anchor apple generic and certificate leaf[subject.OU] = \"TEAMOK1234\""} {
		if !strings.Contains(args, want) {
			t.Errorf("codesign args %q lack %q", args, want)
		}
	}
}

func TestInstallRefusesBadSignature(t *testing.T) {
	f := newFixture(t)
	f.signedBy = "OTHER12345"
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) || f.argv != nil {
		t.Fatalf("wrong team: err = %v, started = %v", err, f.argv != nil)
	}
}

func TestInstallRefusesWithoutTeamID(t *testing.T) {
	f := newFixture(t)
	for _, team := range []string{"", "short"} {
		f.in.TeamID, f.signedBy = team, team // the fake codesign would accept it
		if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) || f.argv != nil {
			t.Fatalf("team %q: err = %v, started = %v", team, err, f.argv != nil)
		}
	}
}

func TestInstallRefusesScriptSymlinkEscape(t *testing.T) {
	f := newFixture(t)
	script := filepath.Join(f.state, "release-builds", testSHA, "scripts", "release-local")
	outside := filepath.Join(t.TempDir(), "evil")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, script); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) || f.argv != nil {
		t.Fatalf("err = %v, started = %v", err, f.argv != nil)
	}
}

// writeManifest records the sha256 of scripts/release-local in the app's signed resources.
func writeManifest(t *testing.T, build string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(build, "scripts", "release-local"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	res := filepath.Join(build, "frontend", "out", "x-arm64", brand.ProductName+".app", "Contents", "Resources")
	if err := os.MkdirAll(res, 0o755); err != nil {
		t.Fatal(err)
	}
	m := `{"dirs":["scripts/lib"],"files":{"scripts/release-local":"` + hex.EncodeToString(sum[:]) + `"}}`
	if err := os.WriteFile(filepath.Join(res, manifestName), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRefusesModifiedScript(t *testing.T) {
	f := newFixture(t)
	script := filepath.Join(f.state, "release-builds", testSHA, "scripts", "release-local")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho pwned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) || f.argv != nil {
		t.Fatalf("modified installer must be refused, err=%v argv=%v", err, f.argv)
	}
}

func TestInstallRefusesExtraSourcedFile(t *testing.T) {
	f := newFixture(t)
	lib := filepath.Join(f.state, "release-builds", testSHA, "scripts", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "evil.sh"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) {
		t.Fatalf("unlisted file in a manifest dir must be refused, err=%v", err)
	}
}

func TestInstallRefusesMissingManifest(t *testing.T) {
	f := newFixture(t)
	m := filepath.Join(f.state, "release-builds", testSHA, "frontend", "out", "x-arm64", brand.ProductName+".app", "Contents", "Resources", manifestName)
	if err := os.Remove(m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.in.Install(context.Background(), testSHA); !errors.Is(err, ErrRefused) {
		t.Fatalf("err=%v", err)
	}
}

const mainSHA = "89abcdef0123456789abcdef0123456789abcdef"

// buildFixture: a fake git that "adds" the worktree by creating scripts/release-local in it.
func buildFixture(t *testing.T) (*BuildRequester, *[][]string, *[]string) {
	t.Helper()
	var gitCalls [][]string
	var started []string
	b := &BuildRequester{
		StateDir:    t.TempDir(),
		ProjectPath: func(context.Context, string) (string, error) { return "/owner/checkout", nil },
		Git: func(_ context.Context, dir string, args ...string) (string, error) {
			gitCalls = append(gitCalls, append([]string{dir}, args...))
			switch args[0] {
			case "rev-parse":
				return mainSHA, nil
			case "worktree":
				if args[1] == "add" {
					tree := args[4]
					if err := os.MkdirAll(filepath.Join(tree, "scripts"), 0o755); err != nil {
						return "", err
					}
					return "", os.WriteFile(filepath.Join(tree, "scripts", "release-local"), []byte("#!/bin/sh\n"), 0o755)
				}
			}
			return "", nil
		},
		Start: func(argv, _ []string, _ string) (int, error) { started = argv; return 7, nil },
	}
	return b, &gitCalls, &started
}

func TestBuildRequesterRunsOriginMainWorktreeScript(t *testing.T) {
	b, gitCalls, started := buildFixture(t)
	res, err := b.Request(context.Background(), "p1")
	if err != nil || res.PID != 7 {
		t.Fatalf("Request = %+v, %v", res, err)
	}
	want := filepath.Join(b.StateDir, "release-scripts", mainSHA[:12], "scripts", "release-local")
	if (*started)[0] != want || (*started)[len(*started)-1] != "request-build" {
		t.Fatalf("argv = %v, want script %s (an origin/main worktree, not the owner checkout)", *started, want)
	}
	if (*gitCalls)[0][1] != "fetch" || (*gitCalls)[0][0] != "/owner/checkout" {
		t.Fatalf("git calls = %v; want a fetch in the owner repo first", *gitCalls)
	}
	// A second request for the same commit reuses the tree: no second worktree add.
	adds := 0
	_, _ = b.Request(context.Background(), "p1")
	for _, c := range *gitCalls {
		if len(c) > 2 && c[1] == "worktree" && c[2] == "add" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("worktree add ran %d times, want 1", adds)
	}
}

func TestBuildRequesterRefusals(t *testing.T) {
	ctx := context.Background()
	b, _, _ := buildFixture(t)
	if _, err := b.Request(ctx, ""); !errors.Is(err, ErrRefused) {
		t.Errorf("empty project err = %v", err)
	}
	b.ProjectPath = func(context.Context, string) (string, error) { return "", errors.New("boom") }
	if _, err := b.Request(ctx, "p1"); !errors.Is(err, ErrRefused) {
		t.Errorf("ProjectPath error = %v, want ErrRefused", err)
	}
	b, _, _ = buildFixture(t)
	git := b.Git
	b.Git = func(ctx context.Context, dir string, args ...string) (string, error) {
		if args[0] == "worktree" {
			_, _ = git(ctx, dir, args...)
			return "", os.Remove(filepath.Join(args[4], "scripts", "release-local"))
		}
		return git(ctx, dir, args...)
	}
	if _, err := b.Request(ctx, "p1"); !errors.Is(err, ErrRefused) {
		t.Errorf("missing script err = %v, want ErrRefused", err)
	}
	b, _, _ = buildFixture(t)
	b.Git = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "rev-parse" {
			return "", errors.New("no origin/main")
		}
		return "", nil
	}
	if _, err := b.Request(ctx, "p1"); !errors.Is(err, ErrRefused) {
		t.Errorf("unresolved origin/main err = %v, want ErrRefused", err)
	}
}

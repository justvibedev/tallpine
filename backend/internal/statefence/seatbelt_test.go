package statefence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
)

func protectOn(t *testing.T, fields map[string]any) agentpolicy.Decision {
	t.Helper()
	ps := map[string]any{protectstate.FieldMode: protectstate.On}
	for k, v := range fields {
		ps[k] = v
	}
	return policytest.Decision(t, map[string]map[string]any{protectstate.Name: ps})
}

// protectOnRepo is protectOn for a session whose project repository the daemon
// has on record as repo.
func protectOnRepo(t *testing.T, repo string) agentpolicy.Decision {
	t.Helper()
	return policytest.DecisionForRepo(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}}, repo)
}

type fenceEnv struct {
	root, home, dataDir, tmp, runFile string
	env                               map[string]string
}

func newFenceEnv(t *testing.T) fenceEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fenceEnv{root: root, home: filepath.Join(root, "home"), dataDir: filepath.Join(root, "state", "data"), runFile: filepath.Join(root, "state", "running.json")}
	f.tmp = filepath.Join(f.dataDir, "tmp", "s-1")
	for _, d := range []string{f.home, f.tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.env = map[string]string{brand.Env("RUN_FILE"): f.runFile, brand.Env("DATA_DIR"): f.dataDir, brand.Env("SESSION_ID"): "s-1", "TMPDIR": f.tmp, "HOME": f.home}
	return f
}

// section returns the paths listed under the rule starting with header.
func section(profile, header string) []string {
	var out []string
	for _, block := range strings.Split(profile, "\n(")[1:] {
		lines := strings.Split(block, "\n")
		if lines[0] != header {
			continue
		}
		for _, l := range lines[1:] {
			l = strings.TrimSpace(l)
			if l == ")" || l == "" {
				break
			}
			out = append(out, l)
		}
	}
	return out
}

func sub(p string) string { return `(subpath "` + p + `")` }

func below(p string) string {
	return `(regex "^` + strings.ReplaceAll(regexp.QuoteMeta(p), `\`, `\\`) + `/")`
}

func mustProfile(t *testing.T, env map[string]string, d agentpolicy.Decision, ws string, extra HarnessPaths) string {
	t.Helper()
	p, err := Profile(env, d, ws, extra)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProfileDeniesTallpineStateLast(t *testing.T) {
	f := newFenceEnv(t)
	p := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{})
	denied := section(p, "deny file-read* file-write*")
	for _, want := range []string{filepath.Join(f.dataDir, "ao.db"), filepath.Join(f.root, "state", "electron"), filepath.Join(f.dataDir, "mobile")} {
		if !slices.Contains(denied, sub(want)) {
			t.Errorf("state %s not denied:\n%s", want, p)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(p), ")") || strings.LastIndex(p, "(deny file-read* file-write*") < strings.LastIndex(p, "(allow ") {
		t.Errorf("state deny must be the last rule so no opening overrides it:\n%s", p)
	}
}

func TestProfileWriteSet(t *testing.T) {
	f := newFenceEnv(t)
	ws := filepath.Join(f.root, "ws")
	p := mustProfile(t, f.env, protectOn(t, nil), ws, HarnessPaths{Write: []string{"~/.kiro"}})
	allowed := section(p, "allow file-write*")
	for _, want := range []string{ws, f.tmp, filepath.Join(f.dataDir, "verify-slots"), "/dev", "/tmp/tp-tmux", filepath.Join(f.home, ".kiro")} {
		if !slices.Contains(allowed, sub(want)) {
			t.Errorf("write %s not allowed:\n%s", want, p)
		}
	}
	if slices.Contains(allowed, sub(f.home)) || slices.Contains(allowed, sub(f.dataDir)) {
		t.Errorf("home and data dir must not be writable:\n%s", p)
	}
	if !strings.Contains(p, "(deny file-write*)\n") {
		t.Errorf("writes must default to deny:\n%s", p)
	}
}

func TestProfileLinkedWorktreeGitDirs(t *testing.T) {
	f := newFenceEnv(t)
	common := filepath.Join(f.root, "repo", ".git")
	private := filepath.Join(common, "worktrees", "w")
	ws := filepath.Join(f.root, "w")
	for _, d := range []string{private, ws} {
		_ = os.MkdirAll(d, 0o755)
	}
	_ = os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+private+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(private, "commondir"), []byte("../..\n"), 0o644)
	_ = os.WriteFile(filepath.Join(private, "gitdir"), []byte(filepath.Join(ws, ".git")+"\n"), 0o644)
	p := mustProfile(t, f.env, protectOnRepo(t, filepath.Join(f.root, "repo")), ws, HarnessPaths{})
	if !slices.Contains(section(p, "allow file-write*"), below(common)) {
		t.Errorf("common git dir not writable (commits need objects/refs):\n%s", p)
	}
	denied := section(p, "deny file-write*")
	for _, want := range []string{filepath.Join(common, "config"), filepath.Join(common, "hooks")} {
		if !slices.Contains(denied, sub(want)) {
			t.Errorf("%s must stay read-only:\n%s", want, p)
		}
	}
}

func TestProfileCredentialsHiddenOnlyUnderHardened(t *testing.T) {
	f := newFenceEnv(t)
	extra := HarnessPaths{AuthRead: []string{"~/.aws/config"}}
	if p := mustProfile(t, f.env, protectOn(t, nil), "", extra); strings.Contains(p, "(deny file-read*\n") || strings.Contains(p, ".aws") {
		t.Errorf("profile on must not hide credentials or re-allow auth:\n%s", p)
	}
	p := mustProfile(t, f.env, policytest.Hardened(t), "", extra)
	if !slices.Contains(section(p, "deny file-read*"), sub(filepath.Join(f.home, ".aws"))) {
		t.Errorf("hardened must hide ~/.aws:\n%s", p)
	}
	reAllow := section(p, "allow file-read*")
	for _, want := range []string{filepath.Join(f.home, ".aws", "config"), filepath.Join(f.home, ".ssh", "known_hosts")} {
		if !slices.Contains(reAllow, sub(want)) {
			t.Errorf("hardened must re-allow %s:\n%s", want, p)
		}
	}
}

func TestProfileDevToolingAndDockerOpenings(t *testing.T) {
	f := newFenceEnv(t)
	old := DarwinUserTempDir
	DarwinUserTempDir = func() string { return "/nonexistent/T" }
	t.Cleanup(func() { DarwinUserTempDir = old })
	xcrun := `(regex "^/nonexistent/T/xcrun_db-[^/]*$")`
	if p := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{}); !strings.Contains(p, xcrun) {
		t.Errorf("devTooling allow (default) must open the xcrun cache:\n%s", p)
	}
	strict := protectOn(t, map[string]any{protectstate.FieldDevTooling: protectstate.DevStrict})
	if p := mustProfile(t, f.env, strict, "", HarnessPaths{}); strings.Contains(p, "xcrun_db") || strings.Contains(p, "docker.sock") {
		t.Errorf("strict devTooling and no allowDocker must open nothing:\n%s", p)
	}
	engine, store := policytest.Engine(t, nil)
	store.Data = []byte(`{"version":2,"rules":{"securityMode":{"enabled":true}},"projects":{"p":{"securityMode":{"allowDocker":true}}}}`)
	docker := engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p"})
	if p := mustProfile(t, f.env, docker, "", HarnessPaths{}); !strings.Contains(p, sub("/var/run/docker.sock")) {
		t.Errorf("allowDocker must open the docker socket:\n%s", p)
	}
}

func TestProfileListsResolvedSymlinkForms(t *testing.T) {
	f := newFenceEnv(t)
	target := filepath.Join(f.root, "target")
	link := filepath.Join(f.root, "link")
	_ = os.MkdirAll(target, 0o755)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	p := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{Write: []string{filepath.Join(link, "not-yet")}})
	allowed := section(p, "allow file-write*")
	for _, want := range []string{filepath.Join(link, "not-yet"), filepath.Join(target, "not-yet")} {
		if !slices.Contains(allowed, sub(want)) {
			t.Errorf("missing form %s:\n%s", want, p)
		}
	}
}

func TestProfileEscapesPaths(t *testing.T) {
	f := newFenceEnv(t)
	p := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{Write: []string{`/x/a"b\c`, "/x/a.b*"}})
	for _, want := range []string{`(subpath "/x/a\"b\\c")`, `(regex "^/x/a\\.b[^/]*$")`} {
		if !strings.Contains(p, want) {
			t.Errorf("want %s in:\n%s", want, p)
		}
	}
}

func TestProfileFailsClosedWithoutRunFile(t *testing.T) {
	if _, err := Profile(map[string]string{"HOME": "/h"}, protectOn(t, nil), "", HarnessPaths{}); !errors.Is(err, ErrNoRunFile) {
		t.Fatalf("err = %v, want ErrNoRunFile", err)
	}
}

func TestWrapAndAvailable(t *testing.T) {
	got := Wrap([]string{"kiro-cli", "chat"}, "(version 1)")
	if want := []string{SandboxExec, "-p", "(version 1)", "kiro-cli", "chat"}; !slices.Equal(got, want) {
		t.Fatalf("Wrap = %q, want %q", got, want)
	}
	old := SandboxExec
	t.Cleanup(func() { SandboxExec = old })
	SandboxExec = filepath.Join(t.TempDir(), "missing")
	if Available("darwin") {
		t.Error("missing sandbox-exec must not be available")
	}
	SandboxExec = old
	if Available("linux") || Available("windows") {
		t.Error("only macOS can fence")
	}
	if Applies(policytest.Decision(t, nil), "darwin") {
		t.Error("policy off must not fence")
	}
}

func TestStatePathsCoverLaunchEnvDir(t *testing.T) {
	if !slices.Contains(StatePaths("/s/running.json", "").Dirs, agentlaunch.EnvFileDir()) {
		t.Fatal("the tmux launch env directory must be denied state")
	}
}

func TestTmuxSocketPathsFollowTheDaemonEnv(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "/x")
	t.Setenv(brand.Env("TMUX_SOCKET_NAME"), "tallpine")
	uid := strconv.Itoa(os.Getuid())
	want := []string{"/x/tmux-" + uid + "/default", "/x/tmux-" + uid + "/tallpine"}
	if got := TmuxSocketPaths(); !slices.Equal(got, want) {
		t.Fatalf("TmuxSocketPaths = %v, want %v", got, want)
	}
	t.Setenv(brand.Env("TMUX_SOCKET_NAME"), "")
	t.Setenv("TMUX_TMPDIR", "")
	if got := TmuxSocketPaths(); !slices.Equal(got, []string{"/tmp/tmux-" + uid + "/default"}) {
		t.Fatalf("unconfigured socket: %v", got)
	}
}

// B401: the seatbelt harnesses' refusal is the typed one the API maps to 409.
func TestProfileNoRunFileIsTheTypedRefusal(t *testing.T) {
	_, err := Profile(map[string]string{brand.Env("RUN_FILE"): " "}, protectOn(t, nil), "", HarnessPaths{})
	if !errors.Is(err, agentpolicy.ErrFenceNoRunFile) || !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("err = %v, want the typed refusal", err)
	}
}

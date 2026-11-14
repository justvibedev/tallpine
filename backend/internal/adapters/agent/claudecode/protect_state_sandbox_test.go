package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

type sandboxSettings struct {
	Sandbox *struct {
		Enabled                  bool `json:"enabled"`
		AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
		FailIfUnavailable        bool `json:"failIfUnavailable"`
		Filesystem               struct {
			DenyRead   []string `json:"denyRead"`
			AllowWrite []string `json:"allowWrite"`
		} `json:"filesystem"`
		Network struct {
			AllowedDomains    []string `json:"allowedDomains"`
			AllowLocalBinding bool     `json:"allowLocalBinding"`
			AllowUnixSockets  []string `json:"allowUnixSockets"`
			AllowMachLookup   []string `json:"allowMachLookup"`
		} `json:"network"`
	} `json:"sandbox"`
}

func providerSettings(t *testing.T, env map[string]string, decision agentpolicy.Decision) sandboxSettings {
	t.Helper()
	args := claudeProviderArgs(domain.AgentLaunchOptions{}, "", env, decision, false)
	i := slices.Index(args, "--settings")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("no --settings in %v", args)
	}
	var s sandboxSettings
	if err := json.Unmarshal([]byte(args[i+1]), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func protectOn(t *testing.T) agentpolicy.Decision {
	return policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
}

func skipUnlessDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the protectState sandbox applies on macOS only")
	}
}

func TestProtectStatePolicySandboxesClaudeShell(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	dataDir := filepath.Join(root, "data")
	tmp := filepath.Join(dataDir, "tmp", "s-1")
	_ = os.MkdirAll(tmp, 0o700)
	env := map[string]string{brand.Env("RUN_FILE"): runFile, brand.Env("DATA_DIR"): dataDir, brand.Env("SESSION_ID"): "s-1", "TMPDIR": tmp, "HOME": filepath.Join(root, "home")}

	s := providerSettings(t, env, protectOn(t)).Sandbox
	if s == nil || !s.Enabled {
		t.Fatal("protectState on must enable Claude's sandbox")
	}
	if s.AllowUnsandboxedCommands || !s.FailIfUnavailable {
		t.Fatalf("sandbox must not be escapable or silently absent: %+v", s)
	}
	for _, p := range []string{
		daemontoken.Path(runFile),
		daemontoken.SessionKeyPath(daemontoken.Path(runFile)),
		filepath.Join(dataDir, "ao.db"),
		filepath.Join(dataDir, "ao.db-wal"),
		filepath.Join(dataDir, "github-pat.json"),
		filepath.Join(root, "electron"),
		filepath.Join(dataDir, "mobile"),
	} {
		if !slices.Contains(s.Filesystem.DenyRead, p) {
			t.Errorf("denyRead %v lacks %s", s.Filesystem.DenyRead, p)
		}
	}
	if slices.Contains(s.Filesystem.DenyRead, runFile) {
		t.Error("running.json must stay readable: the CLI finds the daemon through it")
	}
	if !slices.Equal(s.Network.AllowUnixSockets, []string{filepath.Join(root, "sock"), realPath(t, tmp)}) {
		t.Errorf("allowUnixSockets = %v, want the daemon socket dir and the session temp dir", s.Network.AllowUnixSockets)
	}
	if !slices.Equal(s.Network.AllowedDomains, []string{"*"}) {
		t.Errorf("network must stay open (file fence only): %v", s.Network.AllowedDomains)
	}
	if !slices.Contains(s.Network.AllowMachLookup, trustdService) {
		t.Errorf("gh needs trustd: %v", s.Network.AllowMachLookup)
	}
	if slices.Contains(s.Filesystem.AllowWrite, filepath.Join(dataDir, "artifacts")) {
		t.Error("allowWrite must not open all of data/artifacts: it holds other sessions' deliverables")
	}
	for _, p := range s.Filesystem.AllowWrite {
		if p == "/tmp" || p == "/private/tmp" {
			t.Errorf("allowWrite must not open all of %s: live IPC aliases live there", p)
		}
	}
	if !slices.Equal(s.Network.AllowMachLookup, []string{trustdService, coreDeviceService}) {
		t.Errorf("allowMachLookup must be exactly trustd and CoreDeviceService, nothing broader: %v", s.Network.AllowMachLookup)
	}
	if !s.Network.AllowLocalBinding {
		t.Error("dev servers, httptest and port probes need allowLocalBinding")
	}
	for _, p := range s.Network.AllowUnixSockets {
		if strings.Contains(p, "docker") {
			t.Errorf("the Docker socket must not be allowed by default: %s", p)
		}
	}
	home := filepath.Join(root, "home")
	for _, rel := range []string{"Library/Developer/Xcode/DerivedData", ".gradle/caches", ".m2/repository", ".bun/install/cache", ".rustup/downloads"} {
		if !slices.Contains(s.Filesystem.AllowWrite, filepath.Join(home, rel)) {
			t.Errorf("allowWrite %v lacks the build cache %s", s.Filesystem.AllowWrite, rel)
		}
	}
	for _, p := range []string{tmp, filepath.Join(dataDir, "artifacts", "s", "s-1"), filepath.Join(dataDir, "verify-slots"), "/tmp/tp-tmux", "/private/tmp/tp-tmux", filepath.Join(root, "home", ".tallpine", "golangci-cache"), filepath.Join(root, "home", "go", "pkg", "mod")} {
		if !slices.Contains(s.Filesystem.AllowWrite, p) {
			t.Errorf("allowWrite %v lacks %s", s.Filesystem.AllowWrite, p)
		}
	}
}

// B435: strict devTooling keeps the daemon socket dir as the only unix socket the shell may bind.
func TestProtectStateStrictDevToolingKeepsOnlyDaemonSocket(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "TMPDIR": filepath.Join(root, "tmp"), "HOME": root}
	d := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On, protectstate.FieldDevTooling: protectstate.DevStrict}})
	s := providerSettings(t, env, d).Sandbox
	if !slices.Equal(s.Network.AllowUnixSockets, []string{filepath.Join(root, "sock")}) {
		t.Errorf("strict allowUnixSockets = %v, want only the daemon socket dir", s.Network.AllowUnixSockets)
	}
}

// B435: git, clang and cgo write the xcrun cache under the user temp dir; devTooling=allow opens only those files.
func TestProtectStateDevToolingOpensOnlyXcrunCache(t *testing.T) {
	skipUnlessDarwin(t)
	old := statefence.DarwinUserTempDir
	statefence.DarwinUserTempDir = func() string { return "/var/folders/ab/cd/T/" }
	t.Cleanup(func() { statefence.DarwinUserTempDir = old })
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "TMPDIR": filepath.Join(root, "tmp"), "HOME": root}
	const want = "/var/folders/ab/cd/T/xcrun_db-*"
	const wantReal = "/private/var/folders/ab/cd/T/xcrun_db-*"
	const wantFinal = "/private/var/folders/ab/cd/T/xcrun_db" // B463: the shim renames the temp file onto this name
	if w := providerSettings(t, env, protectOn(t)).Sandbox.Filesystem.AllowWrite; !slices.Contains(w, want) || !slices.Contains(w, wantReal) || !slices.Contains(w, wantFinal) || slices.Contains(w, "/var/folders/ab/cd/T") {
		t.Errorf("allowWrite %v must hold only %s of the temp dir", w, want)
	}
	strict := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On, protectstate.FieldDevTooling: protectstate.DevStrict}})
	if w := providerSettings(t, env, strict).Sandbox.Filesystem.AllowWrite; slices.Contains(w, want) {
		t.Errorf("strict: allowWrite %v must not hold the xcrun cache", w)
	}
}

// The shell's TMPDIR is <CLAUDE_CODE_TMPDIR>/claude-<uid>: the session scratch dirs are allowed (subpath), the shared /tmp/claude-<uid> never.
func TestProtectStateDevToolingSocketDirsCoverClaudeTmp(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	_ = os.MkdirAll(filepath.Join(data, "tmp", "s-1", "x"), 0o700)
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), brand.Env("DATA_DIR"): data, "TMPDIR": filepath.Join(data, "tmp", "s-1"), "CLAUDE_CODE_TMPDIR": filepath.Join(data, "tmp", "s-1", "x"), "HOME": root}
	got := providerSettings(t, env, protectOn(t)).Sandbox.Network.AllowUnixSockets
	// The code emits symlink-resolved dirs (t.TempDir may sit under /tmp -> /private/tmp).
	for _, want := range []string{realPath(t, env["TMPDIR"]), realPath(t, env["CLAUDE_CODE_TMPDIR"])} {
		if !slices.Contains(got, want) {
			t.Errorf("allowUnixSockets %v lacks %s", got, want)
		}
	}
}

// A project env can override TMPDIR/CLAUDE_CODE_TMPDIR; only dirs under <data>/tmp may be opened for sockets.
func TestProtectStateDevToolingDropsTmpDirsOutsideSessionScratch(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	// Scratch dirs must exist (the launch creates them) and resolve inside <data>/tmp; a symlink out is dropped.
	scratch := filepath.Join(data, "tmp", "s-1")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scratch, "t")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link, filepath.Join(data, "tmp", "missing"), "/tmp", "/private/tmp/claude-501", root, data, filepath.Join(data, "tmp"), filepath.Join(data, "tmp", ".trash"), filepath.Join(data, "tmp", "..", "x"), "/var/folders/ab/cd/T", "relative/tmp"} {
		env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), brand.Env("DATA_DIR"): data, "TMPDIR": bad, "CLAUDE_CODE_TMPDIR": root, "HOME": root}
		got := providerSettings(t, env, protectOn(t)).Sandbox.Network.AllowUnixSockets
		if !slices.Equal(got, []string{filepath.Join(root, "sock")}) {
			t.Errorf("TMPDIR=%s: allowUnixSockets = %v, want only the daemon socket dir", bad, got)
		}
	}
}

func TestProtectStateOffOrUnlocatedAddsNoSandbox(t *testing.T) {
	runFile := filepath.Join(t.TempDir(), "running.json")
	env := map[string]string{brand.Env("RUN_FILE"): runFile}
	if s := providerSettings(t, env, policytest.Decision(t, nil)).Sandbox; s != nil {
		t.Fatalf("default policy must add no sandbox: %+v", s)
	}
	if s := providerSettings(t, env, agentpolicy.Decision{}).Sandbox; s != nil {
		t.Fatalf("zero decision must add no sandbox: %+v", s)
	}
	if s := providerSettings(t, map[string]string{}, protectOn(t)).Sandbox; s != nil {
		t.Fatalf("no run file must add no sandbox: %+v", s)
	}
}

// A restore does not carry the launch env; the fence must still cover a custom
// data dir and keep the session's scratch temp dir writable.
func TestProtectStateRestoreFencesCustomDataDir(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	dataDir := filepath.Join(t.TempDir(), "custom-data")
	cmd, ok, err := (&Plugin{resolvedBinary: "claude"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Policy: protectOn(t), DataDir: dataDir, RunFilePath: runFile,
		Session: ports.SessionRef{ID: "s-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}},
	})
	if err != nil || !ok {
		t.Fatalf("restore = (ok=%v, err=%v)", ok, err)
	}
	i := slices.Index(cmd, "--settings")
	if i < 0 {
		t.Fatalf("no --settings in %v", cmd)
	}
	var got sandboxSettings
	if err := json.Unmarshal([]byte(cmd[i+1]), &got); err != nil {
		t.Fatal(err)
	}
	s := got.Sandbox
	if s == nil {
		t.Fatal("restored session must be sandboxed")
	}
	for _, p := range []string{filepath.Join(dataDir, "ao.db"), filepath.Join(dataDir, "github-pat.json"), filepath.Join(dataDir, "mobile"), daemontoken.Path(runFile)} {
		if !slices.Contains(s.Filesystem.DenyRead, p) {
			t.Errorf("denyRead %v lacks %s", s.Filesystem.DenyRead, p)
		}
	}
	if tmp := sessionscratch.TempDir(dataDir, "s-1"); !slices.Contains(s.Filesystem.AllowWrite, tmp) {
		t.Errorf("allowWrite %v lacks the session temp dir %s", s.Filesystem.AllowWrite, tmp)
	}
}

func TestRestoreFenceEnvFallsBackToSessionDataDir(t *testing.T) {
	env := statefence.RestoreEnv("/r/running.json", "", ports.SessionRef{ID: "s-2", DataDir: "/d"})
	if env[brand.Env("DATA_DIR")] != "/d" || env["TMPDIR"] != sessionscratch.TempDir("/d", "s-2") ||
		env["CLAUDE_CODE_TMPDIR"] != sessionscratch.TempDir("/d", "s-2") || env[brand.Env("SESSION_ID")] != "s-2" {
		t.Fatalf("env = %v", env)
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if env := statefence.RestoreEnv("/r/running.json", "", ports.SessionRef{ID: "s-3"}); len(env) != 1 {
		t.Fatalf("no data dir must leave only the run file: %v", env)
	}
}

// The sandbox keys used are macOS-only: Linux would block the daemon socket and
// Windows has no sandbox (failIfUnavailable would stop every command).
func TestProtectStateAppliesOnMacOSOnly(t *testing.T) {
	on := protectOn(t)
	if !protectStateApplies(on, "darwin") {
		t.Fatal("protectState on must apply on darwin")
	}
	for _, goos := range []string{"linux", "windows", "freebsd"} {
		if protectStateApplies(on, goos) {
			t.Errorf("protectState must not apply on %s", goos)
		}
	}
	if protectStateApplies(policytest.Decision(t, nil), "darwin") {
		t.Fatal("protectState off must not apply")
	}
}

// Project and local settings run hooks outside the sandbox and merge sandbox
// arrays that widen it, so a protected session loads the user's settings only.
func TestProtectStateLoadsUserSettingsOnly(t *testing.T) {
	skipUnlessDarwin(t)
	args := claudeRepoConfigArgs(false, protectOn(t))
	if !slices.Equal(args, []string{"--setting-sources", "user"}) {
		t.Fatalf("protected session args = %v, want --setting-sources user", args)
	}
	if args := claudeRepoConfigArgs(false, policytest.Decision(t, nil)); len(args) != 0 {
		t.Fatalf("default policy must keep repo config: %v", args)
	}
}

// Another session's chat host descriptor carries that provider host's address
// and bearer token; the sandboxed shell must not read it (security finding 4).
func TestProtectStateDeniesOtherSessionsChatHostDescriptor(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	dataDir := filepath.Join(root, "data")
	other := filepath.Join(persistenthost.HostsRoot(dataDir), "other-session", "host.json")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"version":1,"sessionId":"other-session","address":"127.0.0.1:1","token":"synthetic-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{brand.Env("RUN_FILE"): runFile, brand.Env("DATA_DIR"): dataDir, "TMPDIR": filepath.Join(root, "tmp"), "HOME": filepath.Join(root, "home")}
	s := providerSettings(t, env, protectOn(t)).Sandbox
	if s == nil {
		t.Fatal("protectState on must enable Claude's sandbox")
	}
	denied := slices.ContainsFunc(s.Filesystem.DenyRead, func(p string) bool {
		return p == other || strings.HasPrefix(other, p+string(filepath.Separator))
	})
	if !denied {
		t.Fatalf("denyRead %v leaves another session's chat host descriptor %s readable", s.Filesystem.DenyRead, other)
	}
}

// TestProtectStateSettingsGolden pins the shared fence builder that terminal
// and chat launches both send.
func TestProtectStateSettingsGolden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("golden paths are POSIX")
	}
	root := filepath.Join(string(filepath.Separator), "state")
	env := map[string]string{
		brand.Env("RUN_FILE"):   filepath.Join(root, "running.json"),
		brand.Env("SESSION_ID"): "s-1",
		"TMPDIR":                filepath.Join(root, "tmp"),
		"HOME":                  filepath.Join(string(filepath.Separator), "home"),
	}
	got, err := json.Marshal(ProtectStateSettings(env))
	if err != nil {
		t.Fatal(err)
	}
	state := statefence.StatePaths(env[brand.Env("RUN_FILE")], "")
	want, _ := json.Marshal(map[string]any{
		"permissions": map[string]any{"deny": protectedStateReadRules(env[brand.Env("RUN_FILE")], "")},
		"sandbox": map[string]any{
			"enabled": true, "allowUnsandboxedCommands": false, "failIfUnavailable": true,
			"filesystem": map[string]any{
				"denyRead": append(append([]string{}, state.Files...), state.Dirs...),
				"allowWrite": []string{
					"/state/tmp", "/state/data/artifacts/s/s-1", "/state/data/verify-slots", "/tmp/tp-tmux", "/private/tmp/tp-tmux", "/home/Library/Caches", "/home/Library/Developer/Xcode/DerivedData", "/home/.cache",
					"/home/go/pkg/mod", "/home/.npm", "/home/.cargo/registry", "/home/.gradle/caches", "/home/.gradle/wrapper", "/home/.gradle/daemon", "/home/.gradle/native", "/home/.m2/repository",
					"/home/.bun/install/cache", "/home/.rustup/downloads", "/home/.rustup/tmp", "/home/Library/pnpm/store", "/home/.local/share/pnpm/store", "/home/.tallpine/golangci-cache",
				},
			},
			"network": map[string]any{
				"allowedDomains":    []string{"*"},
				"allowUnixSockets":  []string{filepath.Join(root, daemonsock.DirName)},
				"allowMachLookup":   []string{"com.apple.trustd.agent", "com.apple.CoreDevice.CoreDeviceService"},
				"allowLocalBinding": true,
			},
		},
	})
	if string(got) != string(want) {
		t.Fatalf("ProtectStateSettings =\n%s\nwant\n%s", got, want)
	}
	if len(ProtectStateSettings(map[string]string{})) != 0 {
		t.Fatal("no run file must give no fence settings")
	}
}

// TestTerminalLaunchUsesProtectStateSettings: the terminal --settings carry
// exactly the shared builder's sandbox and deny rules.
func TestTerminalLaunchUsesProtectStateSettings(t *testing.T) {
	skipUnlessDarwin(t)
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "TMPDIR": filepath.Join(root, "tmp")}
	args := claudeProviderArgs(domain.AgentLaunchOptions{}, "", env, protectOn(t), false)
	var terminal map[string]any
	if err := json.Unmarshal([]byte(args[slices.Index(args, "--settings")+1]), &terminal); err != nil {
		t.Fatal(err)
	}
	var shared map[string]any
	raw, _ := json.Marshal(ProtectStateSettingsFor(env, protectOn(t)))
	_ = json.Unmarshal(raw, &shared)
	a, _ := json.Marshal(terminal["sandbox"])
	b, _ := json.Marshal(shared["sandbox"])
	if string(a) != string(b) {
		t.Fatalf("terminal sandbox = %s, shared = %s", a, b)
	}
	a, _ = json.Marshal(terminal["permissions"].(map[string]any)["deny"])
	b, _ = json.Marshal(shared["permissions"].(map[string]any)["deny"])
	if string(a) != string(b) {
		t.Fatalf("terminal deny = %s, shared = %s", a, b)
	}
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

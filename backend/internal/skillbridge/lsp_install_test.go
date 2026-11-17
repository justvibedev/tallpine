package skillbridge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRun records every argv and answers with fn.
type fakeRun struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	fn    func(ctx context.Context, argv []string, out io.Writer) error
}

func (f *fakeRun) run(ctx context.Context, env, argv []string, out io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.envs = append(f.envs, env)
	f.mu.Unlock()
	if f.fn == nil {
		return nil
	}
	return f.fn(ctx, argv, out)
}

func (f *fakeRun) argvs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func tools(names ...string) func(string, string) (string, bool) {
	return func(_, name string) (string, bool) {
		if slices.Contains(names, name) {
			return "/bin/" + name, true
		}
		return "", false
	}
}

func always(ok bool) func(string, string) bool { return func(string, string) bool { return ok } }

func waitJob(t *testing.T, in *LSPInstaller, project string) LSPInstallJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := in.Job(project); ok && j.State != LSPInstallRunning {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("install job did not finish")
	return LSPInstallJob{}
}

func TestLSPInstallRejectsIDsOutsideTheCatalog(t *testing.T) {
	run := &fakeRun{}
	in := &LSPInstaller{Run: run.run, Look: tools("go", "brew", "npm", "claude")}
	for _, req := range []LSPInstallRequest{
		{Project: "p", Language: "cobol", Target: LSPTargetBinary},
		{Project: "p", Language: "go", Target: "go install evil@latest"},
		{Project: "p", Language: "", Target: LSPTargetPlugin},
	} {
		if _, err := in.Start(req); !errors.Is(err, ErrLSPUnknown) {
			t.Fatalf("Start(%+v) = %v, want ErrLSPUnknown", req, err)
		}
	}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "swift", Target: LSPTargetBinary}); !errors.Is(err, ErrLSPNotInstallable) {
		t.Fatalf("swift binary: %v, want ErrLSPNotInstallable", err)
	}
	if _, ok := in.Job("p"); ok || len(run.argvs()) != 0 {
		t.Fatalf("a refused request must not start a job or run anything: %v", run.argvs())
	}
}

func TestLSPInstallBinaryRunsTheTableCommandWithFallback(t *testing.T) {
	cases := []struct {
		name  string
		have  []string
		want  []string
		state string
		err   string
	}{
		{"go present", []string{"go", "brew"}, []string{"/bin/go", "install", "golang.org/x/tools/gopls@latest"}, LSPInstallSucceeded, ""},
		{"go missing, brew fallback", []string{"brew"}, []string{"/bin/brew", "install", "gopls"}, LSPInstallSucceeded, ""},
		{"nothing to install with", nil, nil, LSPInstallFailed, "needs one of go, brew"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &fakeRun{}
			in := &LSPInstaller{Run: run.run, Look: tools(tc.have...), BinaryInstalled: always(true)}
			if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetBinary}); err != nil {
				t.Fatal(err)
			}
			j := waitJob(t, in, "p")
			if j.State != tc.state || !strings.Contains(j.Error, tc.err) {
				t.Fatalf("job = %+v, want state %s error %q", j, tc.state, tc.err)
			}
			got := run.argvs()
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("ran %v without a prerequisite", got)
				}
				return
			}
			if len(got) != 1 || !slices.Equal(got[0], tc.want) {
				t.Fatalf("ran %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLSPInstallMissingNpmSaysSo(t *testing.T) {
	in := &LSPInstaller{Run: (&fakeRun{}).run, Look: tools()}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "typescript", Target: LSPTargetBinary}); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, in, "p")
	if j.State != LSPInstallFailed || !strings.Contains(j.Error, "needs npm, which is not installed") {
		t.Fatalf("job = %+v", j)
	}
}

func TestLSPInstallFailureKeepsReasonAndOutputTail(t *testing.T) {
	run := &fakeRun{fn: func(_ context.Context, _ []string, out io.Writer) error {
		_, _ = io.WriteString(out, strings.Repeat("x", 2*lspOutputTail)+"\nnpm ERR! EACCES permission denied\n")
		return errors.New("exit status 243")
	}}
	in := &LSPInstaller{Run: run.run, Look: tools("npm"), BinaryInstalled: always(true)}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "python", Target: LSPTargetBinary}); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, in, "p")
	if j.State != LSPInstallFailed || !strings.Contains(j.Error, "npm install -g pyright: exit status 243") {
		t.Fatalf("job = %+v", j)
	}
	if len(j.Output) > lspOutputTail || !strings.HasSuffix(j.Output, "EACCES permission denied\n") {
		t.Fatalf("output tail = %d bytes ending %q", len(j.Output), j.Output[max(0, len(j.Output)-60):])
	}
	if j.FinishedAt.IsZero() {
		t.Fatal("finished job has no FinishedAt")
	}
}

func TestLSPInstallRechecksBeforeReportingSuccess(t *testing.T) {
	for _, found := range []bool{true, false} {
		in := &LSPInstaller{Run: (&fakeRun{}).run, Look: tools("brew"), BinaryInstalled: always(found)}
		if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "kotlin", Target: LSPTargetBinary}); err != nil {
			t.Fatal(err)
		}
		j := waitJob(t, in, "p")
		if found && j.State != LSPInstallSucceeded {
			t.Fatalf("found after install: %+v", j)
		}
		if !found && (j.State != LSPInstallFailed || !strings.Contains(j.Error, "kotlin-lsp is not found")) {
			t.Fatalf("still missing after install: %+v", j)
		}
	}
}

func TestLSPInstallPluginAddsMarketplaceOnlyWhenMissing(t *testing.T) {
	for _, known := range []bool{false, true} {
		dir := t.TempDir()
		if known {
			if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "plugins", "known_marketplaces.json"), []byte(`{"`+LSPMarketplace+`":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		run := &fakeRun{}
		var checkedDir string
		in := &LSPInstaller{Run: run.run, Claude: func(context.Context) (string, error) { return "/opt/claude", nil },
			PluginInstalled: func(d, id string) bool { checkedDir = d; return id == "gopls-lsp@"+LSPMarketplace }}
		if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetPlugin, ClaudeConfigDir: dir, Env: map[string]string{"CLAUDE_CONFIG_DIR": dir}}); err != nil {
			t.Fatal(err)
		}
		if j := waitJob(t, in, "p"); j.State != LSPInstallSucceeded {
			t.Fatalf("job = %+v", j)
		}
		want := [][]string{{"/opt/claude", "plugin", "install", "gopls-lsp@" + LSPMarketplace, "--scope", "user"}}
		if !known {
			want = append([][]string{{"/opt/claude", "plugin", "marketplace", "add", LSPMarketplaceSource}}, want...)
		}
		if got := run.argvs(); !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("known=%v ran %v, want %v", known, got, want)
		}
		if !slices.Contains(run.envs[0], "CLAUDE_CONFIG_DIR="+dir) || checkedDir != dir {
			t.Fatalf("plugin install must run and re-check in the session config dir %s (rechecked %s)", dir, checkedDir)
		}
	}
}

func TestLSPInstallPluginWithoutClaudeFails(t *testing.T) {
	in := &LSPInstaller{Run: (&fakeRun{}).run, Claude: func(context.Context) (string, error) { return "", errors.New("not found") }}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetPlugin, ClaudeConfigDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, in, "p"); j.State != LSPInstallFailed || !strings.Contains(j.Error, "claude CLI is not installed") {
		t.Fatalf("job = %+v", j)
	}
}

func TestLSPInstallOneAtATimePerProjectAndTimeout(t *testing.T) {
	release := make(chan struct{})
	run := &fakeRun{fn: func(ctx context.Context, _ []string, _ io.Writer) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	in := &LSPInstaller{Run: run.run, Look: tools("go"), BinaryInstalled: always(true), Timeout: 200 * time.Millisecond}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetBinary}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "python", Target: LSPTargetBinary}); !errors.Is(err, ErrLSPInstallBusy) {
		t.Fatalf("second install of the project: %v, want ErrLSPInstallBusy", err)
	}
	if _, err := in.Start(LSPInstallRequest{Project: "other", Language: "go", Target: LSPTargetBinary}); err != nil {
		t.Fatalf("another project's install must not wait: %v", err)
	}
	j := waitJob(t, in, "p")
	if j.State != LSPInstallFailed || !strings.Contains(j.Error, "timed out after 200ms") {
		t.Fatalf("job = %+v", j)
	}
	close(release)
	waitJob(t, in, "other")
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetBinary}); err != nil {
		t.Fatalf("a finished job must not block the next: %v", err)
	}
	waitJob(t, in, "p")
}

func TestInstallEnvUnsetsBlankedVarsAndAddsToolDirs(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	t.Setenv("PATH", "/usr/bin")
	env := installEnv(map[string]string{"CLAUDE_CONFIG_DIR": "", "FOO": "bar"}, "/home/u")
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") }) {
		t.Fatalf("a blanked variable must be unset: %v", env)
	}
	if !slices.Contains(env, "FOO=bar") {
		t.Fatal("overlay missing")
	}
	i := slices.IndexFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") })
	path := strings.TrimPrefix(env[i], "PATH=")
	if !strings.HasPrefix(path, "/usr/bin:") || !strings.Contains(path, "/home/u/go/bin") || strings.Count(path, "/usr/bin:") != 1 {
		t.Fatalf("PATH = %s", path)
	}
}

func TestRunCommandCapturesOutputAndStopsOnTimeout(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	var out tailWriter
	out.max = 100
	err := runCommand(context.Background(), os.Environ(), []string{"/bin/sh", "-c", "echo to-out; echo to-err >&2; exit 3"}, &out)
	if err == nil || !strings.Contains(out.String(), "to-out") || !strings.Contains(out.String(), "to-err") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := runCommand(ctx, os.Environ(), []string{"/bin/sh", "-c", "sleep 30"}, &out); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout did not stop the command: err=%v after %s", err, time.Since(start))
	}
}

func TestLSPInstallAllInstallsOnlyWhatIsMissing(t *testing.T) {
	plugin := []string{"/opt/claude", "plugin", "install", "gopls-lsp@" + LSPMarketplace, "--scope", "user"}
	binary := []string{"/bin/go", "install", "golang.org/x/tools/gopls@latest"}
	cases := []struct {
		name           string
		plugin, binary bool // installed before the job
		want           [][]string
	}{
		{"both missing", false, false, [][]string{plugin, binary}},
		{"plugin present", true, false, [][]string{binary}},
		{"binary present", false, true, [][]string{plugin}},
		{"nothing missing", true, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "plugins", "known_marketplaces.json"), []byte(`{"`+LSPMarketplace+`":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			havePlugin, haveBinary := tc.plugin, tc.binary
			run := &fakeRun{fn: func(_ context.Context, argv []string, _ io.Writer) error {
				mu.Lock()
				defer mu.Unlock()
				if argv[0] == "/opt/claude" {
					havePlugin = true
				} else {
					haveBinary = true
				}
				return nil
			}}
			in := &LSPInstaller{Run: run.run, Look: tools("go"), Claude: func(context.Context) (string, error) { return "/opt/claude", nil },
				PluginInstalled: func(string, string) bool { mu.Lock(); defer mu.Unlock(); return havePlugin },
				BinaryInstalled: func(string, string) bool { mu.Lock(); defer mu.Unlock(); return haveBinary }}
			if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetAll, ClaudeConfigDir: dir}); err != nil {
				t.Fatal(err)
			}
			if j := waitJob(t, in, "p"); j.State != LSPInstallSucceeded {
				t.Fatalf("job = %+v", j)
			}
			if got := run.argvs(); !slices.EqualFunc(got, tc.want, slices.Equal) {
				t.Fatalf("ran %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLSPInstallAllFailsClearlyForAServerItCannotInstall(t *testing.T) {
	in := &LSPInstaller{Run: (&fakeRun{}).run, PluginInstalled: func(string, string) bool { return true }, BinaryInstalled: always(false)}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "swift", Target: LSPTargetAll}); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, in, "p"); j.State != LSPInstallFailed || !strings.Contains(j.Error, "sourcekit-lsp is not installed and cannot be installed from Tallpine") {
		t.Fatalf("job = %+v", j)
	}
}

func TestLSPInstallFailsWhenTheInstalledServerCannotWork(t *testing.T) {
	prev := lookTool
	lookTool = func(_, name string) (string, bool) { return "/x/" + name, name == "gopls" }
	t.Cleanup(func() { lookTool = prev })
	in := &LSPInstaller{Run: (&fakeRun{}).run, Look: tools("go"), BinaryInstalled: always(true)}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetBinary}); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, in, "p"); j.State != LSPInstallFailed || !strings.Contains(j.Error, "Go toolchain") {
		t.Fatalf("job = %+v", j)
	}
}

func TestLSPInstallGoToolchainRunsBrewAndChecksGoAppears(t *testing.T) {
	prevOS, prevLook := hostOS, lookTool
	hostOS = "darwin"
	goInstalled := false
	lookTool = func(_, name string) (string, bool) {
		return "/x/" + name, name == "gopls" || (name == "go" && goInstalled)
	}
	t.Cleanup(func() { hostOS, lookTool = prevOS, prevLook })
	run := &fakeRun{}
	in := &LSPInstaller{Run: func(ctx context.Context, env, argv []string, out io.Writer) error {
		goInstalled = true
		return run.run(ctx, env, argv, out)
	}, Look: tools("brew"), BinaryInstalled: always(true)}
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetGo}); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, in, "p"); j.State != LSPInstallSucceeded {
		t.Fatalf("job = %+v", j)
	}
	if got := run.argvs(); len(got) != 1 || !slices.Equal(got[0], []string{"/bin/brew", "install", "go"}) {
		t.Fatalf("ran %v", got)
	}
}

func TestLSPInstallGoToolchainRefusedOffMacAndForOtherLanguages(t *testing.T) {
	prev := hostOS
	t.Cleanup(func() { hostOS = prev })
	in := &LSPInstaller{Run: (&fakeRun{}).run}
	hostOS = "linux"
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "go", Target: LSPTargetGo}); !errors.Is(err, ErrLSPNotInstallable) {
		t.Fatalf("linux err = %v", err)
	}
	hostOS = "darwin"
	if _, err := in.Start(LSPInstallRequest{Project: "p", Language: "python", Target: LSPTargetGo}); !errors.Is(err, ErrLSPNotInstallable) {
		t.Fatalf("python err = %v", err)
	}
}

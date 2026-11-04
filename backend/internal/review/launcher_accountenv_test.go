package review

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agentaccount"
	claudereviewer "tallpine.eu/tallpine/backend/internal/adapters/reviewer/claudecode"
	codexreviewer "tallpine.eu/tallpine/backend/internal/adapters/reviewer/codex"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// fakeAccountEnv answers like agentaccounts.Service.SessionLaunchEnv: the
// env of every provider the worker is bound to a non-device account for.
type fakeAccountEnv struct {
	env       map[string]string
	err       error
	calls     int
	providers []domain.AgentAccountProvider
	// errFor fails the lookup of one provider only.
	errFor map[domain.AgentAccountProvider]error
}

func (f *fakeAccountEnv) SessionLaunchEnvFor(_ context.Context, _ domain.SessionID, provider domain.AgentAccountProvider) (map[string]string, error) {
	f.calls++
	f.providers = append(f.providers, provider)
	if err := f.errFor[provider]; err != nil {
		return nil, err
	}
	return f.env, f.err
}

// accountBEnv is the production env for an account folder, not a hand-written copy.
func accountBEnv(home string) map[string]string {
	env := (&agentaccount.Claude{}).LaunchEnv(home)
	env["CODEX_HOME"] = filepath.Join(home, "codex")
	return env
}

func spawnWithAccounts(t *testing.T, src ports.AgentAccountProviderEnv, spec LaunchSpec) (*fakeReviewer, *fakeRuntime, error) {
	t.Helper()
	reviewer := &fakeReviewer{}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, rt, t.TempDir(), WithAgentAccounts(src))
	_, err := l.Spawn(context.Background(), spec)
	return reviewer, rt, err
}

func TestReviewerRunsOnWorkerAccount(t *testing.T) {
	home := t.TempDir()
	reviewer, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: accountBEnv(home)}, launchSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		if got := rt.createCfg.Env[key]; got != home {
			t.Errorf("process env %s = %q, want %q", key, got, home)
		}
		if got := reviewer.gotInv.Env[key]; got != home {
			t.Errorf("invocation env %s = %q, want %q", key, got, home)
		}
	}
}

func TestReviewerAccountEnvDropsOtherProviders(t *testing.T) {
	_, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: accountBEnv(t.TempDir())}, launchSpec())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := rt.createCfg.Env["CODEX_HOME"]; ok {
		t.Fatalf("Claude reviewer got CODEX_HOME=%q", v)
	}
}

func TestReviewerOnDeviceAccountAddsNoKeys(t *testing.T) {
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"PROJECT_ONLY": "1"}
	reviewer, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: map[string]string{}}, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{rt.createCfg.Env, reviewer.gotInv.Env} {
		if _, ok := env["CLAUDE_CONFIG_DIR"]; ok {
			t.Fatalf("device-bound worker got CLAUDE_CONFIG_DIR: %v", env)
		}
		if _, ok := env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]; ok {
			t.Fatalf("device-bound worker got CLAUDE_SECURESTORAGE_CONFIG_DIR: %v", env)
		}
	}
	if rt.createCfg.Env["PROJECT_ONLY"] != "1" {
		t.Fatalf("project env lost: %v", rt.createCfg.Env)
	}
}

func TestReviewerAccountOverridesProjectConfigDir(t *testing.T) {
	// The account neutralises credential variables the daemon itself has set.
	t.Setenv("ANTHROPIC_API_KEY", "sk-daemon")
	home := t.TempDir()
	spec := launchSpec()
	spec.ProjectEnv = map[string]string{"CLAUDE_CONFIG_DIR": "/project/claude", "ANTHROPIC_API_KEY": "sk-project"}
	projectBefore := map[string]string{"CLAUDE_CONFIG_DIR": "/project/claude", "ANTHROPIC_API_KEY": "sk-project"}
	reviewer, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: accountBEnv(home)}, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{rt.createCfg.Env, reviewer.gotInv.Env} {
		if env["CLAUDE_CONFIG_DIR"] != home {
			t.Fatalf("CLAUDE_CONFIG_DIR = %q, want account home %q", env["CLAUDE_CONFIG_DIR"], home)
		}
		if env["ANTHROPIC_API_KEY"] != "" {
			t.Fatalf("project API key survived the account: %q", env["ANTHROPIC_API_KEY"])
		}
	}
	for k, v := range projectBefore {
		if spec.ProjectEnv[k] != v {
			t.Fatalf("caller's ProjectEnv mutated: %v", spec.ProjectEnv)
		}
	}
}

func TestNonClaudeReviewersNeverConsultAccounts(t *testing.T) {
	for _, harness := range []domain.ReviewerHarness{domain.ReviewerCodex, domain.ReviewerKiro, "deepseek-harness"} {
		t.Run(string(harness), func(t *testing.T) {
			src := &fakeAccountEnv{env: accountBEnv(t.TempDir())}
			spec := launchSpec()
			spec.Harness = harness
			_, rt, err := spawnWithAccounts(t, src, spec)
			if err != nil {
				t.Fatal(err)
			}
			if src.calls != 0 {
				t.Fatalf("accounts consulted %d times for %s", src.calls, harness)
			}
			for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CODEX_HOME"} {
				if _, ok := rt.createCfg.Env[key]; ok {
					t.Fatalf("%s reviewer got %s", harness, key)
				}
			}
		})
	}
}

func TestCodexReviewerEnvUnchangedByAccounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spec := launchSpec()
	spec.Harness = domain.ReviewerCodex
	env := func(src ports.AgentAccountProviderEnv) map[string]string {
		rt := &fakeRuntime{}
		var opts []LauncherOption
		if src != nil {
			opts = append(opts, WithAgentAccounts(src))
		}
		l := NewLauncher(fakeReviewerResolver{reviewer: codexreviewer.New(), ok: true}, rt, t.TempDir(), opts...)
		if _, err := l.Spawn(context.Background(), spec); err != nil {
			t.Skipf("codex reviewer cannot launch here: %v", err)
		}
		out := map[string]string{}
		for k, v := range rt.createCfg.Env {
			if k != "PATH" && k != "TALLPINE_DATA_DIR" && !slices.Contains(reviewerTempKeys, k) && k != sessionscratch.TempPrefixEnv {
				out[k] = v
			}
		}
		return out
	}
	with := env(&fakeAccountEnv{env: accountBEnv(t.TempDir())})
	without := env(nil)
	if len(with) != len(without) {
		t.Fatalf("env differs with accounts: %v vs %v", with, without)
	}
	for k, v := range without {
		if with[k] != v {
			t.Fatalf("env[%s] = %q with accounts, %q without", k, with[k], v)
		}
	}
}

func TestReviewerAccountLookupErrorFailsLaunch(t *testing.T) {
	boom := errors.New("accounts down")
	_, rt, err := spawnWithAccounts(t, &fakeAccountEnv{err: boom}, launchSpec())
	if !errors.Is(err, boom) {
		t.Fatalf("Spawn err = %v, want wrapped %v", err, boom)
	}
	if rt.createCfg.Env != nil {
		t.Fatal("reviewer launched despite account lookup error")
	}
}

func TestAccountEnvSlotBindsLate(t *testing.T) {
	home := t.TempDir()
	slot := &AccountEnvSlot{}
	_, rt, err := spawnWithAccounts(t, slot, launchSpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.createCfg.Env["CLAUDE_CONFIG_DIR"]; ok {
		t.Fatal("unset slot added account env")
	}
	slot.Set(&fakeAccountEnv{env: accountBEnv(home)})
	_, rt, err = spawnWithAccounts(t, slot, launchSpec())
	if err != nil {
		t.Fatal(err)
	}
	if rt.createCfg.Env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("bound slot env = %v", rt.createCfg.Env)
	}
}

type chatProbe struct {
	ReviewerChatController
	started ReviewerChatStart
	restore bool
}

func (c *chatProbe) SupportsReviewChat(domain.AgentHarness) bool { return true }
func (c *chatProbe) StartReviewChat(_ context.Context, s ReviewerChatStart) (string, error) {
	c.started = s
	return "p", nil
}
func (c *chatProbe) RestoreReviewChat(_ context.Context, s ReviewerChatStart) (string, error) {
	c.started, c.restore = s, true
	return "p", nil
}

type claudeChatReviewer struct{ fakeReviewer }

func (*claudeChatReviewer) ReviewChatHarness() domain.AgentHarness { return domain.HarnessClaudeCode }

// Plumbing only: Claude reviewers use the terminal today.
func TestReviewerChatStartCarriesAccountEnv(t *testing.T) {
	home := t.TempDir()
	chat := &chatProbe{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &claudeChatReviewer{}, ok: true}, &fakeRuntime{}, t.TempDir(),
		WithReviewerChat(chat), WithAgentAccounts(&fakeAccountEnv{env: accountBEnv(home)}))
	if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
		t.Fatal(err)
	}
	if got := chat.started.Env["CLAUDE_CONFIG_DIR"]; got != home {
		t.Fatalf("chat start env CLAUDE_CONFIG_DIR = %q, want %q", got, home)
	}
	if _, err := l.RestoreTerminal(context.Background(), launchSpec()); err != nil {
		t.Fatal(err)
	}
	if !chat.restore || chat.started.Env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("chat restore env = %v", chat.started.Env)
	}
}

// launchOnly hides the real adapter's PreLaunch (it writes hooks and trust into
// the workspace and ~/.claude.json) while keeping its restore path.
type launchOnly struct{ r *claudereviewer.Reviewer }

func (l launchOnly) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	return l.r.ReviewCommand(ctx, inv)
}
func (l launchOnly) ReviewMessage(ctx context.Context, inv ports.ReviewInvocation) (string, error) {
	return l.r.ReviewMessage(ctx, inv)
}
func (l launchOnly) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	return l.r.ReviewRestoreCommand(ctx, inv)
}

func TestReviewerRestoreFindsTranscriptThroughAccountSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	device, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", device)
	bin := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The shared store holds the transcript; account B only links to it, and the
	// device folder has no projects/ at all.
	id := claudecode.SessionUUID("review-mer-1")
	shared := t.TempDir()
	if err := os.MkdirAll(filepath.Join(shared, "projects", "-ws-mer-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "projects", "-ws-mer-1", id+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(shared, "projects"), filepath.Join(home, "projects")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	restore := func(src ports.AgentAccountProviderEnv) bool {
		restorer := &restoreProbe{launchOnly: launchOnly{r: claudereviewer.New()}}
		l := NewLauncher(fakeReviewerResolver{reviewer: restorer, ok: true}, &fakeRuntime{}, t.TempDir(), WithAgentAccounts(src))
		spec := launchSpec()
		spec.AgentSessionID = id
		if _, err := l.Spawn(context.Background(), spec); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		return restorer.ok
	}
	if !restore(&fakeAccountEnv{env: accountBEnv(home)}) {
		t.Fatal("restore did not find the transcript through account B's projects link")
	}
	if restore(&fakeAccountEnv{env: map[string]string{}}) {
		t.Fatal("restore found a transcript in the device folder, which has none")
	}
}

type restoreProbe struct {
	launchOnly
	ok bool
}

func (r *restoreProbe) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, ok, err := r.launchOnly.ReviewRestoreCommand(ctx, inv)
	r.ok = ok
	return cmd, ok, err
}

func TestReviewerOtherProviderErrorKeepsClaudeAccount(t *testing.T) {
	home := t.TempDir()
	_, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: accountBEnv(home), err: errors.New("codex account failed to prepare")}, launchSpec())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.createCfg.Env["CLAUDE_CONFIG_DIR"] != home || rt.createCfg.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != home {
		t.Fatalf("Claude account env lost: %v", rt.createCfg.Env)
	}
}

func TestReviewerErrorWithoutClaudeKeysFailsLaunch(t *testing.T) {
	boom := errors.New("codex account failed to prepare")
	_, rt, err := spawnWithAccounts(t, &fakeAccountEnv{env: map[string]string{"CODEX_HOME": "/x"}, err: boom}, launchSpec())
	if !errors.Is(err, boom) || rt.createCfg.Env != nil {
		t.Fatalf("Spawn err = %v, launched = %v; want failure", err, rt.createCfg.Env != nil)
	}
}

// switchingAccounts answers with whichever account is current, like the
// service does after the worker's binding moves.
type switchingAccounts struct {
	home  string
	email string
}

func (s *switchingAccounts) SessionLaunchEnvFor(context.Context, domain.SessionID, domain.AgentAccountProvider) (map[string]string, error) {
	return accountBEnv(s.home), nil
}

func (s *switchingAccounts) SessionAccount(context.Context, domain.SessionID, domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	return domain.AgentAccount{ID: "id-" + s.email, Email: s.email, Home: s.home}, true, nil
}

func TestReusedReviewerIsStaleAfterAccountSwitch(t *testing.T) {
	src := &switchingAccounts{home: t.TempDir(), email: "old@example.com"}
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir(), WithAgentAccounts(src)).(*agentLauncher)
	res, err := l.Spawn(context.Background(), launchSpec())
	if err != nil {
		t.Fatal(err)
	}
	stale := func() bool {
		return l.AccountStale(context.Background(), res.HandleID, "mer-1", domain.ReviewerClaudeCode)
	}
	if stale() {
		t.Fatal("reviewer on the worker's current account must be reused")
	}
	src.home, src.email = t.TempDir(), "new@example.com"
	if !stale() {
		t.Fatal("reviewer started on the previous account must be replaced after a switch")
	}
}

func TestReviewerLaunchFailureNamesAccountEmail(t *testing.T) {
	src := &switchingAccounts{home: t.TempDir(), email: "me@example.com"}
	rt := &fakeRuntime{createErr: errors.New("boom")}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir(), WithAgentAccounts(src))
	_, err := l.Spawn(context.Background(), launchSpec())
	if err == nil || !strings.Contains(err.Error(), "me@example.com") {
		t.Fatalf("err = %v, want it to name the account email", err)
	}
}

package skillbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

// What an LSP install job installs.
const (
	LSPTargetPlugin = "plugin"
	LSPTargetBinary = "binary"
	// LSPTargetGo installs the Go toolchain gopls needs (language "go" only).
	LSPTargetGo = "go"
	// LSPTargetAll installs whichever of the two is missing, plugin first.
	LSPTargetAll = "all"
)

// LSP install job states.
const (
	LSPInstallRunning   = "running"
	LSPInstallSucceeded = "succeeded"
	LSPInstallFailed    = "failed"
)

var (
	// ErrLSPUnknown is a language or target that is not in LSPCatalog.
	ErrLSPUnknown = errors.New("unknown language or install target")
	// ErrLSPNotInstallable is a catalog entry the daemon has no command for.
	ErrLSPNotInstallable = errors.New("this language server cannot be installed from Tallpine")
	// ErrLSPInstallBusy is a second install while one runs for the project.
	ErrLSPInstallBusy = errors.New("an install is already running for this project")
)

// hostOS is runtime.GOOS; tests replace it.
var hostOS = runtime.GOOS

// lspOutputTail is how much of the combined output a job keeps.
const lspOutputTail = 4000

// LSPInstallJob is one install run and its outcome.
type LSPInstallJob struct {
	Language string
	Target   string
	State    string
	// Command is the argv being run, for display.
	Command string
	// Output is the tail of the commands' combined output.
	Output string
	// Error says why a failed job failed.
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// LSPInstallRequest names what to install for a project. Language and Target
// pick an LSPCatalog entry; the commands come from that entry only.
type LSPInstallRequest struct {
	Project  string
	Language string
	Target   string
	// Env is laid over the daemon's environment: the project env with the
	// Claude account Tallpine's sessions launch under on top.
	Env map[string]string
	// Home is the account home whose tool dirs are searched.
	Home string
	// ClaudeConfigDir is where those sessions read plugins.
	ClaudeConfigDir string
}

// LSPInstaller runs the fixed install commands of LSPCatalog, one job at a
// time per project, and keeps each project's last job.
type LSPInstaller struct {
	// Claude resolves the claude CLI. Nil looks it up like any other tool.
	Claude func(context.Context) (string, error)
	// Timeout bounds a whole job. Zero means 10 minutes.
	Timeout time.Duration
	// Look, Run, PluginInstalled and BinaryInstalled are seams for tests; nil
	// uses LookTool, exec, DiscoverPlugins and BinaryInstalled.
	Look            func(home, name string) (string, bool)
	Run             func(ctx context.Context, env, argv []string, out io.Writer) error
	PluginInstalled func(claudeDir, id string) bool
	BinaryInstalled func(home, name string) bool

	mu   sync.Mutex
	jobs map[string]*lspJob
}

type lspJob struct {
	job LSPInstallJob
	out *tailWriter
}

// Job returns the project's current or last job.
func (i *LSPInstaller) Job(project string) (LSPInstallJob, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	j, ok := i.jobs[project]
	if !ok {
		return LSPInstallJob{}, false
	}
	snap := j.job
	snap.Output = j.out.String()
	return snap, true
}

// Start validates req against LSPCatalog and runs the install in the
// background. It refuses an unknown entry and a second job for the project.
func (i *LSPInstaller) Start(req LSPInstallRequest) (LSPInstallJob, error) {
	lang, ok := LSPByLanguage(req.Language)
	if !ok || (req.Target != LSPTargetPlugin && req.Target != LSPTargetBinary && req.Target != LSPTargetGo && req.Target != LSPTargetAll) {
		return LSPInstallJob{}, ErrLSPUnknown
	}
	if req.Target == LSPTargetGo {
		if _, cmds := GoToolchainInstall(hostOS); lang.Binary != "gopls" || len(cmds) == 0 {
			return LSPInstallJob{}, ErrLSPNotInstallable
		}
	}
	if req.Target == LSPTargetBinary && len(lang.BinaryCommands) == 0 {
		return LSPInstallJob{}, ErrLSPNotInstallable
	}
	i.mu.Lock()
	if j, busy := i.jobs[req.Project]; busy && j.job.State == LSPInstallRunning {
		i.mu.Unlock()
		return LSPInstallJob{}, ErrLSPInstallBusy
	}
	if i.jobs == nil {
		i.jobs = map[string]*lspJob{}
	}
	j := &lspJob{job: LSPInstallJob{Language: lang.Language, Target: req.Target, State: LSPInstallRunning, StartedAt: time.Now()}, out: &tailWriter{max: lspOutputTail}}
	i.jobs[req.Project] = j
	snap := j.job
	i.mu.Unlock()
	slog.Info("lsp install started", "project", req.Project, "language", lang.Language, "target", req.Target)
	go i.run(j, lang, req)
	return snap, nil
}

func (i *LSPInstaller) run(j *lspJob, lang LSPLanguage, req LSPInstallRequest) {
	timeout := i.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	env := installEnv(req.Env, req.Home)
	var err error
	switch req.Target {
	case LSPTargetPlugin:
		err = i.pluginStep(ctx, j, lang, req, env)
	case LSPTargetBinary:
		err = i.binaryStep(ctx, j, lang, req, env)
	case LSPTargetGo:
		err = i.goStep(ctx, j, lang, req, env)
	default:
		err = i.installMissing(ctx, j, lang, req, env)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	j.job.FinishedAt = time.Now()
	j.job.State = LSPInstallSucceeded
	if err != nil {
		j.job.State, j.job.Error = LSPInstallFailed, err.Error()
		slog.Warn("lsp install failed", "project", req.Project, "language", lang.Language, "target", req.Target, "state", j.job.State, "error", err, "elapsed", j.job.FinishedAt.Sub(j.job.StartedAt).Round(time.Millisecond))
		return
	}
	slog.Info("lsp install finished", "project", req.Project, "language", lang.Language, "target", req.Target, "state", j.job.State, "elapsed", j.job.FinishedAt.Sub(j.job.StartedAt).Round(time.Millisecond))
}

// installMissing installs the plugin and then the language server, each only
// when it is missing (LSPTargetAll, the install switching a plugin on starts).
func (i *LSPInstaller) installMissing(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	if i.pluginCheck(lang, req) != nil {
		if err := i.pluginStep(ctx, j, lang, req, env); err != nil {
			return err
		}
	}
	if i.binaryCheck(lang, req) == nil {
		return nil
	}
	if len(lang.BinaryCommands) == 0 {
		return fmt.Errorf("%s is not installed and cannot be installed from Tallpine: %s", lang.Binary, lang.BinaryInstall)
	}
	return i.binaryStep(ctx, j, lang, req, env)
}

// pluginStep and binaryStep install one part and confirm it took: a command
// can exit 0 without the plugin or binary appearing where sessions look.
func (i *LSPInstaller) pluginStep(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	if err := i.installPlugin(ctx, j, lang, req, env); err != nil {
		return err
	}
	return i.pluginCheck(lang, req)
}

func (i *LSPInstaller) binaryStep(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	if err := i.installBinary(ctx, j, lang, req, env); err != nil {
		return err
	}
	return i.binaryCheck(lang, req)
}

func (i *LSPInstaller) installPlugin(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	claude, err := i.claudePath(ctx, req.Home)
	if err != nil {
		return fmt.Errorf("the claude CLI is not installed or not found: %w", err)
	}
	if !marketplaceKnown(req.ClaudeConfigDir, LSPMarketplace) {
		if err := i.exec(ctx, j, env, claude, "plugin", "marketplace", "add", LSPMarketplaceSource); err != nil {
			return fmt.Errorf("adding the %s marketplace failed: %w", LSPMarketplace, err)
		}
	}
	return i.exec(ctx, j, env, claude, "plugin", "install", lang.PluginID(), "--scope", "user")
}

// goStep installs the Go toolchain and confirms gopls no longer reports no-go.
func (i *LSPInstaller) goStep(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	_, cmds := GoToolchainInstall(hostOS)
	if err := i.runFirst(ctx, j, req, env, lang.Binary, cmds); err != nil {
		return err
	}
	if BinaryProblem(req.Home, lang) == LSPProblemNoGo {
		return errors.New(LSPProblemText(LSPProblemNoGo))
	}
	return nil
}

func (i *LSPInstaller) installBinary(ctx context.Context, j *lspJob, lang LSPLanguage, req LSPInstallRequest, env []string) error {
	return i.runFirst(ctx, j, req, env, lang.Binary, lang.BinaryCommands)
}

// runFirst runs the first command whose tool is installed.
func (i *LSPInstaller) runFirst(ctx context.Context, j *lspJob, req LSPInstallRequest, env []string, binary string, commands []InstallCommand) error {
	tools := make([]string, 0, len(commands))
	for _, c := range commands {
		tools = append(tools, c.Tool)
		if p, ok := i.look(req.Home, c.Tool); ok {
			return i.exec(ctx, j, env, p, c.Args...)
		}
	}
	if len(tools) == 1 {
		return fmt.Errorf("%s needs %s, which is not installed; install it first, or run the command yourself", binary, tools[0])
	}
	return fmt.Errorf("%s needs one of %s, and none is installed; install one first, or run the command yourself", binary, strings.Join(tools, ", "))
}

func (i *LSPInstaller) pluginCheck(lang LSPLanguage, req LSPInstallRequest) error {
	installed := i.PluginInstalled
	if installed == nil {
		installed = pluginInstalledIn
	}
	if !installed(req.ClaudeConfigDir, lang.PluginID()) {
		return fmt.Errorf("%s is not installed in %s", lang.PluginID(), req.ClaudeConfigDir)
	}
	return nil
}

func (i *LSPInstaller) binaryCheck(lang LSPLanguage, req LSPInstallRequest) error {
	installed := i.BinaryInstalled
	if installed == nil {
		installed = BinaryInstalled
	}
	if !installed(req.Home, lang.Binary) {
		return fmt.Errorf("%s is not found on PATH or in the usual tool folders", lang.Binary)
	}
	if problem := BinaryProblem(req.Home, lang); problem != "" {
		return errors.New(LSPProblemText(problem))
	}
	return nil
}

func (i *LSPInstaller) exec(ctx context.Context, j *lspJob, env []string, bin string, args ...string) error {
	argv := append([]string{bin}, args...)
	line := filepath.Base(bin) + " " + strings.Join(args, " ")
	i.mu.Lock()
	j.job.Command = line
	i.mu.Unlock()
	_, _ = fmt.Fprintf(j.out, "$ %s\n", line)
	run := i.Run
	if run == nil {
		run = runCommand
	}
	if err := run(ctx, env, argv, j.out); err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	return nil
}

func (i *LSPInstaller) look(home, name string) (string, bool) {
	if i.Look != nil {
		return i.Look(home, name)
	}
	return LookTool(home, name)
}

func (i *LSPInstaller) claudePath(ctx context.Context, home string) (string, error) {
	if i.Claude != nil {
		return i.Claude(ctx)
	}
	if p, ok := i.look(home, "claude"); ok {
		return p, nil
	}
	return "", exec.ErrNotFound
}

func runCommand(ctx context.Context, env, argv []string, out io.Writer) error {
	cmd := process.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // argv comes from LSPCatalog only
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.Stdin = env, out, out, nil
	process.ConfigureTreeCancellation(cmd)
	return cmd.Run()
}

// installEnv is the daemon env with overlay on top (an empty value unsets) and the usual tool dirs on
// PATH, so a GUI-launched daemon finds brew, go and npm, and npm finds node.
func installEnv(overlay map[string]string, home string) []string {
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	for k, v := range overlay {
		if v == "" {
			// An account env blanks a variable to remove it (unset).
			delete(vars, k)
			continue
		}
		vars[k] = v
	}
	path := vars["PATH"]
	for _, d := range toolDirs(home) {
		if !strings.Contains(string(os.PathListSeparator)+path+string(os.PathListSeparator), string(os.PathListSeparator)+d+string(os.PathListSeparator)) {
			path += string(os.PathListSeparator) + d
		}
	}
	vars["PATH"] = strings.TrimPrefix(path, string(os.PathListSeparator))
	out := make([]string, 0, len(vars))
	for k, v := range vars {
		out = append(out, k+"="+v)
	}
	return out
}

// marketplaceKnown reports whether claudeDir's known_marketplaces.json names
// the marketplace.
func marketplaceKnown(claudeDir, name string) bool {
	b, err := os.ReadFile(filepath.Join(claudeDir, "plugins", "known_marketplaces.json"))
	if err != nil {
		return false
	}
	var known map[string]json.RawMessage
	if json.Unmarshal(b, &known) != nil {
		return false
	}
	_, ok := known[name]
	return ok
}

func pluginInstalledIn(claudeDir, id string) bool {
	for _, p := range DiscoverPlugins(Options{ClaudeConfigDir: claudeDir}) {
		if p.ID == id && p.Installed {
			return true
		}
	}
	return false
}

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

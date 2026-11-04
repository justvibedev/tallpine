package shellterm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/browser"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
)

// ShellRuntime is the slice of the runtime adapter a shell terminal needs:
// spawn a PTY around an argv, exchange reviewed auth input, tear it down, and
// distinguish child-process liveness from a host retaining scrollback.
type ShellRuntime interface {
	ports.RuntimeChildInspector
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
	SendInput(ctx context.Context, handle ports.RuntimeHandle, input string) error
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// ProjectRootLocator resolves a project id to the directory a shell should
// start in. The daemon wiring adapts the project service to it.
type ProjectRootLocator interface {
	ProjectRoot(ctx context.Context, id domain.ProjectID) (string, error)
	ProjectEnv(ctx context.Context, id domain.ProjectID) (map[string]string, error)
}

// SessionWorkspaceLocator resolves a session id to the workspace it is
// currently running in, plus the project it belongs to. The project id lets
// the caller fall back to the project root when the session has no workspace
// of its own yet. The daemon wiring adapts the session service to it.
type SessionWorkspaceLocator interface {
	SessionWorkspace(ctx context.Context, id domain.SessionID) (workspacePath string, projectID domain.ProjectID, err error)
	CueCommandSessionTarget(ctx context.Context, id domain.SessionID) (CueCommandSessionTarget, error)
}

// Service opens, lists, and closes standalone shell terminals.
//
// User shells survive desktop and daemon restarts. appRunID scopes trusted
// auth terminals, whose owning authentication flow ends with the app launch.
type Service struct {
	runtime             ShellRuntime
	store               Store
	projects            ProjectRootLocator
	sessions            SessionWorkspaceLocator
	dataDir             string
	appRunID            string
	log                 *slog.Logger
	previewCapabilities *browser.Authority
	pendingPreviewMu    sync.Mutex
	pendingPreview      map[domain.SessionID]string

	// now and newHandleID are injectable so tests can assert on exact ids and
	// timestamps without a clock or entropy dependency.
	now         func() time.Time
	newHandleID func() (string, error)
	executable  func() (string, error)
	// initialInputTimeout bounds the wait for an auth terminal's ready marker;
	// tests shorten it to exercise the timeout path.
	initialInputTimeout time.Duration

	// gatesMu guards gates itself (the map), not the individual gate mutexes it
	// holds.
	gatesMu sync.Mutex
	// gates holds one entry per session with an in-flight or completed
	// BeginSessionTeardown/OpenShellTerminal gate acquisition. OpenShellTerminal
	// validates a session id BEFORE calling sessionGateFor, so an invalid id
	// never allocates an entry here — only real sessions do, bounding growth to
	// the shape Tallpine's single-user daemon actually runs.
	gates map[domain.SessionID]*sessionGate
	// onSessionGateWait, when set, is called the instant a session-scoped
	// OpenShellTerminal or CloseShellTerminal is about to attempt gate.mu.Lock()
	// — before the (possibly blocking) call, so it fires whether or not the
	// lock is free. Nil in production; tests use it to know a call has
	// genuinely reached the acquisition point before asserting it's blocked
	// there, rather than inferring "still blocked" from a goroutine that simply
	// hasn't run yet.
	onSessionGateWait func(domain.SessionID)
}

// IsShellTerminalChildAlive reports whether the command inside a shell
// terminal is still running. Detached terminal hosts may outlive their child
// to retain scrollback, so callers that coordinate a command workflow must use
// this instead of treating host liveness as command liveness.
func (s *Service) IsShellTerminalChildAlive(ctx context.Context, handleID string) (bool, error) {
	return s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: strings.TrimSpace(handleID)})
}

// NewService builds the shell terminal service. dataDir is the fallback working
// directory for a shell opened with no project context. A nil logger falls back
// to slog.Default.
func NewService(shellRuntime ShellRuntime, store Store, projects ProjectRootLocator, sessions SessionWorkspaceLocator, dataDir, appRunID string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		runtime:             shellRuntime,
		store:               store,
		projects:            projects,
		sessions:            sessions,
		dataDir:             dataDir,
		appRunID:            appRunID,
		log:                 log,
		previewCapabilities: browser.NewAuthority(),
		pendingPreview:      make(map[domain.SessionID]string),
		now:                 time.Now,
		newHandleID:         newShellTerminalHandleID,
		executable:          os.Executable,
		initialInputTimeout: initialInputTimeout,
		gates:               map[domain.SessionID]*sessionGate{},
	}
}

func (s *Service) pinnedEnv(projectEnv map[string]string) map[string]string {
	path, err := agentlaunch.PinnedPATH(s.executable, os.Getenv, projectEnv, s.dataDir)
	if err != nil {
		s.log.Warn("shell terminal PATH not pinned to the daemon binary; a bare `tallpine` may resolve to a different install", "err", err)
		return agentlaunch.MergeEnv(projectEnv, nil)
	}
	return agentlaunch.MergeEnv(projectEnv, map[string]string{"PATH": path})
}

// ValidPreviewCapability accepts a shell's preview-only bearer while its
// launch is in flight or its durable row still belongs to the target session.
func (s *Service) ValidPreviewCapability(ctx context.Context, sessionID domain.SessionID, token string) (bool, error) {
	if sessionID == "" || token == "" {
		return false, nil
	}
	s.pendingPreviewMu.Lock()
	pending := s.pendingPreview[sessionID]
	s.pendingPreviewMu.Unlock()
	if s.previewCapabilities.Valid(sessionID, token, pending) {
		return true, nil
	}
	recs, err := s.store.SelectShellTerminalsBySessionID(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("validate shell preview capability: %w", err)
	}
	for _, rec := range recs {
		if s.previewCapabilities.Valid(sessionID, token, rec.PreviewCapabilityVerifier) {
			alive, err := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
			if err != nil {
				return false, fmt.Errorf("probe shell preview owner %s: %w", rec.HandleID, err)
			}
			return alive, nil
		}
	}
	return false, nil
}

// sessionGateFor returns the gate for id, creating it on first use.
func (s *Service) sessionGateFor(id domain.SessionID) *sessionGate {
	s.gatesMu.Lock()
	defer s.gatesMu.Unlock()
	g, ok := s.gates[id]
	if !ok {
		g = newSessionGate()
		s.gates[id] = g
	}
	return g
}

// acquireSessionGate resolves and takes a session's gate in one step, firing
// the test hook at the moment the wait begins.
func (s *Service) acquireSessionGate(ctx context.Context, id domain.SessionID) (release func(), err error) {
	gate := s.sessionGateFor(id)
	if s.onSessionGateWait != nil {
		s.onSessionGateWait(id)
	}
	return gate.acquire(ctx, id)
}

// OpenShellTerminal spawns a shell PTY and records it against the current app
// run. The runtime is created BEFORE the row is written, and rolled back if the
// write fails, so a persisted row always names a PTY that actually exists —
// otherwise a restart would try to re-attach to a handle that was never spawned.
//
// A session-scoped open holds that session's gate for the whole call: without
// this, a shell could be inserted between BeginSessionTeardown's snapshot read
// and Session Manager's own worktree destroy a moment later, landing a fresh
// shell in a directory that is about to disappear. Holding the gate here means
// the open either runs entirely before a teardown starts, or blocks until the
// teardown (and the gate) releases — at which point resolveShellTerminalWorkingDir's
// existence check sees the worktree is gone and falls back to the project root.
func (s *Service) OpenShellTerminal(ctx context.Context, in OpenShellTerminalInput) (ShellTerminal, error) {
	title := strings.TrimSpace(in.Title)
	if utf8.RuneCountInString(title) > maxShellTerminalTitleLen {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	if in.SessionID != "" {
		if s.sessions == nil {
			return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
		}
		// Validate the session exists BEFORE allocating a gate for it: a stream
		// of requests naming made-up session ids would otherwise grow the gate
		// map forever, since nothing else ever removes an entry. This lookup's
		// result is deliberately discarded — resolveShellTerminalWorkingDir
		// below re-resolves inside the gate, where a concurrent teardown can't
		// race it. An unknown session errors the same way it would have further
		// down, just before touching any gate state.
		if _, _, err := s.sessions.SessionWorkspace(ctx, in.SessionID); err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: resolve session %s: %w", in.SessionID, err)
		}
		release, err := s.acquireSessionGate(ctx, in.SessionID)
		if err != nil {
			return ShellTerminal{}, err
		}
		defer release()
	}
	workingDir, projectID, err := s.resolveShellTerminalWorkingDir(ctx, in.ProjectID, in.SessionID)
	if err != nil {
		return ShellTerminal{}, err
	}
	var projectEnv map[string]string
	if projectID != "" {
		projectEnv, err = s.projects.ProjectEnv(ctx, projectID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: resolve project environment: %w", err)
		}
	}
	if title == "" {
		openTerminals, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: list existing terminals: %w", err)
		}
		title = nextShellTerminalTitle(openTerminals)
	}
	argv, usedFallback := resolveUserLoginShell(in.Shell)
	if usedFallback {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_SHELL_UNAVAILABLE",
			fmt.Sprintf("The selected shell is unavailable: %s. Choose another shell in Settings.", in.Shell), nil)
	}
	if len(argv) == 0 {
		return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SHELL",
			"Could not determine a shell to launch. Set SHELL (macOS/Linux) or ComSpec (Windows).")
	}
	env := s.pinnedEnv(projectEnv)
	if env == nil {
		env = make(map[string]string, 3)
	}
	// A user shell must never inherit the worker-only browser automation bearer
	// from an unusual daemon launch environment.
	env["TALLPINE_BROWSER_CAPABILITY"] = ""
	env["TALLPINE_PREVIEW_CAPABILITY"] = ""
	// Marks the user's own shell, so the session process reaper spares it.
	env[sessionreap.UserShellEnv] = "1"
	var verifier string
	if in.SessionID != "" {
		env["TALLPINE_SESSION_ID"] = string(in.SessionID)
		var token string
		token, verifier, err = s.previewCapabilities.Issue(in.SessionID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: preview capability: %w", err)
		}
		env["TALLPINE_PREVIEW_CAPABILITY"] = token
		// The shell can issue its first CLI request before runtime.Create returns
		// and its durable row is inserted. Admit it only during this launch gap.
		s.pendingPreviewMu.Lock()
		s.pendingPreview[in.SessionID] = verifier
		s.pendingPreviewMu.Unlock()
		defer func() {
			s.pendingPreviewMu.Lock()
			delete(s.pendingPreview, in.SessionID)
			s.pendingPreviewMu.Unlock()
		}()
	}
	return s.openTerminal(ctx, openTerminalConfig{
		argv: argv, env: env, projectEnv: projectEnv, projectID: projectID, sessionID: in.SessionID,
		workingDir: workingDir, title: title,
		previewVerifier: verifier,
		startOnAttach:   in.StartOnAttach,
	})
}

const authWorkspaceDirectoryName = "auth-workspace"

// OpenCommandTerminal opens a daemon-trusted command in a standalone terminal.
// Unlike OpenShellTerminal, it never receives public HTTP input. Interactive
// coding agents start in a dedicated workspace so they cannot mistake the
// daemon's database/config/runtime directory for a project.
func (s *Service) OpenCommandTerminal(ctx context.Context, in OpenCommandTerminalInput) (ShellTerminal, error) {
	if err := validateOpenCommandTerminalInput(in); err != nil {
		return ShellTerminal{}, err
	}
	handleID, err := s.newHandleID()
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("open command terminal: handle id: %w", err)
	}

	workingDir := in.WorkingDir
	cleanupWorkingDirOnError := false
	if workingDir == "" {
		authWorkspaceRoot := filepath.Join(s.dataDir, authWorkspaceDirectoryName)
		if err := os.MkdirAll(authWorkspaceRoot, 0o700); err != nil {
			return ShellTerminal{}, fmt.Errorf("open command terminal: create auth workspace: %w", err)
		}
		workingDir = filepath.Join(authWorkspaceRoot, handleID)
		if err := os.Mkdir(workingDir, 0o700); err != nil {
			return ShellTerminal{}, fmt.Errorf("open command terminal: create private auth workspace: %w", err)
		}
		cleanupWorkingDirOnError = true
	}
	terminal, err := s.openTerminal(ctx, openTerminalConfig{
		handleID:                 handleID,
		argv:                     in.Argv,
		env:                      in.Env,
		workingDir:               workingDir,
		title:                    in.Title,
		transient:                true,
		cleanupWorkingDirOnError: cleanupWorkingDirOnError,
	})
	if err != nil {
		return ShellTerminal{}, err
	}
	if len(in.InitialInputReadyStates) > 0 {
		go s.sendInitialInputWhenReady(context.WithoutCancel(ctx), ports.RuntimeHandle{ID: terminal.HandleID}, in.InitialInput, in.InitialInputReadyStates, in.SendInitialInputOnReadyTimeout)
	}
	return terminal, nil
}

// RunCueCommand opens a new normal shell terminal for each trusted command.
// It never creates or messages an agent session.
func (s *Service) RunCueCommand(ctx context.Context, in RunCueCommandInput) (ShellTerminal, error) {
	if err := ctx.Err(); err != nil {
		return ShellTerminal{}, err
	}
	if in.ProjectID == "" {
		return ShellTerminal{}, apierr.Invalid("CUE_COMMAND_PROJECT_REQUIRED", "A project is required to run a command Cue", nil)
	}
	if strings.TrimSpace(in.Command) == "" {
		return ShellTerminal{}, apierr.Invalid("CUE_COMMAND_REQUIRED", "A command is required to run a command Cue", nil)
	}
	workingDir, projectID, err := s.resolveCueCommandWorkingDir(ctx, in.ProjectID, in.SessionID)
	if err != nil {
		return ShellTerminal{}, err
	}
	if in.SessionID != "" {
		release, acquireErr := s.acquireSessionGate(ctx, in.SessionID)
		if acquireErr != nil {
			return ShellTerminal{}, acquireErr
		}
		defer release()
		// Recheck inside the teardown gate before opening a shell.
		workingDir, projectID, err = s.resolveCueCommandWorkingDir(ctx, in.ProjectID, in.SessionID)
		if err != nil {
			return ShellTerminal{}, err
		}
	}
	records, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: list terminals: %w", err)
	}
	argv, usedFallback := resolveUserLoginShell(in.Shell)
	if usedFallback {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_SHELL_UNAVAILABLE",
			fmt.Sprintf("The selected shell is unavailable: %s. Choose another shell in Settings.", in.Shell), nil)
	}
	if len(argv) == 0 {
		return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SHELL", "Could not determine a shell to launch. Set SHELL (macOS/Linux) or ComSpec (Windows).")
	}
	projectEnv, err := s.projects.ProjectEnv(ctx, projectID)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: resolve project environment: %w", err)
	}
	env := s.pinnedEnv(projectEnv)
	readiness, err := prepareCueShellReadiness(s.dataDir, argv, env)
	if err != nil {
		return ShellTerminal{}, err
	}
	defer readiness.cleanup()
	env = agentlaunch.MergeEnv(env, readiness.env)
	terminal, err := s.openTerminal(ctx, openTerminalConfig{argv: readiness.argv, env: env, projectEnv: projectEnv, projectID: projectID,
		sessionID: in.SessionID, workingDir: workingDir, title: nextShellTerminalTitle(records)})
	if err != nil {
		return ShellTerminal{}, err
	}
	if err := ctx.Err(); err != nil {
		return ShellTerminal{}, err
	}
	if err := s.waitForCueShellReady(ctx, ports.RuntimeHandle{ID: terminal.HandleID}, readiness.file); err != nil {
		return ShellTerminal{}, err
	}
	if err := s.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: terminal.HandleID}, in.Command); err != nil {
		return ShellTerminal{}, fmt.Errorf("run cue command: send to terminal %s: %w", terminal.HandleID, err)
	}
	return terminal, nil
}

func (s *Service) resolveCueCommandWorkingDir(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID) (string, domain.ProjectID, error) {
	if sessionID == "" {
		root, err := s.resolveProjectRootOrDataDir(ctx, projectID)
		if err != nil {
			return "", "", err
		}
		if !filepath.IsAbs(root) {
			return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The project root is unavailable", nil)
		}
		info, statErr := os.Stat(root)
		if statErr != nil || !info.IsDir() {
			return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The project root is unavailable", nil)
		}
		return filepath.Clean(root), projectID, nil
	}
	if s.sessions == nil {
		return "", "", apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
	}
	target, err := s.sessions.CueCommandSessionTarget(ctx, sessionID)
	if err != nil {
		return "", "", fmt.Errorf("run cue command: resolve session %s: %w", sessionID, err)
	}
	if target.ProjectID != projectID {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session belongs to another project", nil)
	}
	if target.IsTerminated || target.Activity == domain.ActivityExited || target.Activity == domain.ActivityBlocked {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session cannot run command Cues", nil)
	}
	workspace := strings.TrimSpace(target.WorkspacePath)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session worktree is unavailable", nil)
	}
	info, statErr := os.Stat(workspace)
	if statErr != nil || !info.IsDir() {
		return "", "", apierr.Conflict("CUE_TARGET_CONFLICT", "The selected session worktree is unavailable", nil)
	}
	return filepath.Clean(workspace), target.ProjectID, nil
}

const (
	initialInputTimeout      = 10 * time.Second
	initialInputPollInterval = 50 * time.Millisecond
	initialInputOutputLines  = 100

	initialInputFallbackSendTimeout = 5 * time.Second
)

type openTerminalConfig struct {
	handleID                 string
	argv                     []string
	env                      map[string]string
	projectEnv               map[string]string
	projectID                domain.ProjectID
	sessionID                domain.SessionID
	workingDir               string
	title                    string
	transient                bool
	cleanupWorkingDirOnError bool
	previewVerifier          string
	startOnAttach            bool
}

// openTerminal creates and persists a terminal, rolling the runtime back on
// every failure after Create so an untracked PTY cannot leak.
func (s *Service) openTerminal(ctx context.Context, cfg openTerminalConfig) (ShellTerminal, error) {
	handleID := cfg.handleID
	if handleID == "" {
		var err error
		handleID, err = s.newHandleID()
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("open shell terminal: handle id: %w", err)
		}
	}

	// Stamped before the runtime spawns: concurrent opens finish spawning in
	// any order, and the list is ordered by creation, so tabs opened in quick
	// succession must keep the order they were opened in.
	createdAt := s.now().UTC()
	// SessionID is the runtime adapters' name for "what to call this PTY"; it
	// is not a session row and no sessions record is ever created. The
	// shellterm- prefix keeps the two namespaces disjoint.
	handle, err := s.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(handleID),
		WorkspacePath: cfg.workingDir,
		Argv:          cfg.argv,
		Env:           cfg.env,
		// A user shell's exit is final, just like a trusted command's exit.
		// Durability across app launches is a separate persistence policy.
		ExitOnCommandCompletion: true,
		StartOnAttach:           cfg.startOnAttach,
		// Only agent terminals are probed for their rendered screen.
		LazyStyledOutput: true,
	})
	if err != nil {
		if cfg.cleanupWorkingDirOnError {
			s.cleanupAuthWorkspace(cfg.workingDir, handleID)
		}
		return ShellTerminal{}, fmt.Errorf("open shell terminal %s: runtime: %w", handleID, agentlaunch.RedactError(err, cfg.projectEnv))
	}

	rec := ShellTerminalRecord{
		HandleID: handle.ID,
		// The resolved project, not the requested one: a session-scoped open
		// that named no project still belongs to the session's project, and
		// persisting "" there would leave the row unattributable on the board.
		ProjectID:                 cfg.projectID,
		SessionID:                 cfg.sessionID,
		WorkingDir:                cfg.workingDir,
		Title:                     cfg.title,
		AppRunID:                  s.appRunID,
		Transient:                 cfg.transient,
		CreatedAt:                 createdAt,
		PreviewCapabilityVerifier: cfg.previewVerifier,
	}
	if err := s.store.InsertShellTerminal(ctx, rec); err != nil {
		stillAlive, destroyErr := s.destroyRuntimeConfirmed(context.WithoutCancel(ctx), handle)
		if stillAlive {
			destroyErr = agentlaunch.RedactError(destroyErr, cfg.projectEnv)
			s.log.Warn("shell terminal rollback failed; retaining workspace for live runtime",
				"handleId", handle.ID, "workingDir", cfg.workingDir, "error", destroyErr)
		} else if cfg.cleanupWorkingDirOnError {
			s.cleanupAuthWorkspace(cfg.workingDir, handle.ID)
		}
		return ShellTerminal{}, fmt.Errorf("open shell terminal %s: persist: %w", handle.ID, err)
	}
	s.log.Info("shell terminal opened", "handleId", handle.ID, "workingDir", cfg.workingDir)
	return shellTerminalFromRecord(rec), nil
}

// nextShellTerminalTitle assigns a name at creation time. It never depends on
// a tab's current UI position, so closing or reordering another tab cannot
// rename an existing terminal.
func nextShellTerminalTitle(terminals []ShellTerminalRecord) string {
	maxNumber := 0
	for _, terminal := range terminals {
		if terminal.Title == "Terminal" {
			if maxNumber < 1 {
				maxNumber = 1
			}
			continue
		}
		var number int
		if _, err := fmt.Sscanf(terminal.Title, "Terminal %d", &number); err == nil && number > maxNumber {
			maxNumber = number
		}
	}
	return fmt.Sprintf("Terminal %d", maxNumber+1)
}

// maxShellTerminalTitleLen bounds a user-supplied tab name. Tabs are truncated
// in the UI anyway; this only stops an unbounded string reaching the DB.
const maxShellTerminalTitleLen = 80

func validateOpenCommandTerminalInput(in OpenCommandTerminalInput) error {
	if len(in.Argv) == 0 {
		return apierr.Invalid("SHELL_TERMINAL_COMMAND_REQUIRED", "A shell terminal command is required", nil)
	}
	if strings.TrimSpace(in.Title) == "" {
		return apierr.Invalid("SHELL_TERMINAL_TITLE_REQUIRED", "A shell terminal title is required", nil)
	}
	if utf8.RuneCountInString(in.Title) > maxShellTerminalTitleLen {
		return apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	if in.InitialInput != "" && len(in.InitialInputReadyStates) == 0 {
		return apierr.Invalid("SHELL_TERMINAL_INITIAL_INPUT_READY_TEXT_REQUIRED", "A reviewed readiness marker is required for automatic terminal input", nil)
	}
	for _, state := range in.InitialInputReadyStates {
		if strings.TrimSpace(state.Text) == "" {
			return apierr.Invalid("SHELL_TERMINAL_INITIAL_INPUT_READY_TEXT_REQUIRED", "A reviewed readiness marker is required for automatic terminal input", nil)
		}
	}
	return nil
}

// RenameShellTerminal sets a shell terminal's tab title. The title is trimmed
// and must be non-empty and within the length bound; an unknown handle is a 404.
func (s *Service) RenameShellTerminal(ctx context.Context, handleID, title string) (ShellTerminal, error) {
	if handleID == "" {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_ID_REQUIRED", "A shell terminal id is required", nil)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_REQUIRED", "A shell terminal title is required", nil)
	}
	if utf8.RuneCountInString(title) > maxShellTerminalTitleLen {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_TITLE_TOO_LONG",
			fmt.Sprintf("A shell terminal title must be at most %d characters", maxShellTerminalTitleLen), nil)
	}
	rec, found, err := s.store.UpdateShellTerminalTitle(ctx, handleID, title)
	if err != nil {
		return ShellTerminal{}, fmt.Errorf("rename shell terminal %s: %w", handleID, err)
	}
	if !found {
		return ShellTerminal{}, apierr.NotFound("SHELL_TERMINAL_NOT_FOUND", "No such shell terminal: "+handleID)
	}
	s.log.Info("shell terminal renamed", "handleId", handleID)
	return shellTerminalFromRecord(rec), nil
}

// CloseShellTerminal destroys a shell's PTY and forgets it — but only once
// death is confirmed (see destroyConfirmed): a shell that survives Destroy
// keeps its row, so it stays visible/re-attachable instead of vanishing from
// tracking while still running against a directory a later session teardown
// might remove.
//
// A session-scoped shell is closed under that session's gate — the same one
// BeginSessionTeardown takes — so this can't race a concurrent worktree
// teardown for the same session: whichever gets the gate first runs to
// completion before the other's SELECT (here, an unconditional destroy;
// there, SelectShellTerminalsBySessionID) can observe a half-finished state.
// Without this, a close whose row deletion raced BeginSessionTeardown's scan
// could vanish from that scan and leave a still-alive shell invisible to it
// while the worktree it's rooted in gets removed anyway.
func (s *Service) CloseShellTerminal(ctx context.Context, handleID string) error {
	if handleID == "" {
		return apierr.Invalid("SHELL_TERMINAL_ID_REQUIRED", "A shell terminal id is required", nil)
	}
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return fmt.Errorf("close shell terminal %s: %w", handleID, err)
	}
	if !found {
		return apierr.NotFound("SHELL_TERMINAL_NOT_FOUND", "No such shell terminal: "+handleID)
	}

	if rec.SessionID != "" {
		release, err := s.acquireSessionGate(ctx, rec.SessionID)
		if err != nil {
			return err
		}
		defer release()
	}

	stillAlive, destroyErr := s.destroyConfirmed(ctx, rec)
	if stillAlive {
		s.log.Warn("close shell terminal: runtime still alive after destroy", "handleId", handleID, "error", destroyErr)
		return apierr.Conflict("SHELL_TERMINAL_STILL_RUNNING",
			"The shell process is still running; try closing it again in a moment", nil)
	}
	return nil
}

// ListShellTerminalsForCurrentAppRun returns durable shells from every launch
// plus the current launch's trusted auth terminals,
// closing any whose child exited (the user typed `exit`, or the machine
// rebooted out from under a persisted row). Retained hosts are destroyed before
// their rows are removed; failed cleanup keeps the row available for retry.
//
// A liveness probe that ERRORS is not treated as proof of death — the same rule
// internal/terminal applies on attach — so a transient runtime hiccup cannot
// silently delete a working terminal.
func (s *Service) ListShellTerminalsForCurrentAppRun(ctx context.Context) ([]ShellTerminal, error) {
	recs, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return nil, fmt.Errorf("list shell terminals: %w", err)
	}
	out := make([]ShellTerminal, 0, len(recs))
	for _, rec := range recs {
		alive, err := s.runtime.IsChildAlive(ctx, ports.RuntimeHandle{ID: rec.HandleID})
		if err != nil {
			s.log.Warn("shell terminal liveness probe failed; keeping row",
				"handleId", rec.HandleID, "error", err)
			out = append(out, shellTerminalFromRecord(rec))
			continue
		}
		if !alive {
			// Native hosts retain scrollback after child exit. Tear down that
			// host before forgetting its row; preserve failures for retry.
			if stillAlive, destroyErr := s.destroyConfirmed(ctx, rec); stillAlive {
				s.log.Warn("pruning exited shell terminal: runtime survived cleanup", "handleId", rec.HandleID, "error", destroyErr)
				out = append(out, shellTerminalFromRecord(rec))
			}
			continue
		}
		out = append(out, shellTerminalFromRecord(rec))
	}
	return out, nil
}

// BeginSessionTeardown drains every shell terminal scoped to a session and
// locks out new ones, ahead of Session Manager tearing down the session's
// worktree (Kill, Cleanup, RetireForReplacement, the reconcile/shutdown
// save-and-teardown path). It is the write side of the same gate
// OpenShellTerminal reads: taking the gate here is what makes a concurrent
// Open either finish first or wait until the gate releases, so nothing can
// insert a new shell terminal into a worktree that is about to disappear.
//
// A runtime that cannot be confirmed dead (see destroyConfirmed) is NOT
// deleted — its row survives so the terminal is still visible/re-attachable —
// and its error is folded into the returned aggregate. On error the gate is
// already released and the caller MUST NOT touch the worktree: some scoped
// shell may still be reading or writing under it.
//
// On success this returns a release function the caller MUST call exactly
// once (typically via defer) once its own worktree work finishes, whatever
// the outcome — release is tied to THIS acquisition specifically (a fresh
// closure over this exact lock), so there is no way for a call meant for a
// different Begin, or a duplicate/stray call, to release a lock it doesn't
// own. Never call EndSessionTeardown-style bookkeeping by session id alone:
// that was the earlier design, and it let an unmatched release for session X
// unlock a DIFFERENT, still in-flight teardown for the same X.
func (s *Service) BeginSessionTeardown(ctx context.Context, sessionID domain.SessionID) (release func(), err error) {
	release, err = s.acquireSessionGate(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	recs, err := s.store.SelectShellTerminalsBySessionID(ctx, sessionID)
	if err != nil {
		release()
		return nil, fmt.Errorf("close shell terminals for session %s: %w", sessionID, err)
	}

	var stillAlive []error
	for _, rec := range recs {
		alive, destroyErr := s.destroyConfirmed(ctx, rec)
		if alive {
			s.log.Warn("close shell terminal for session: runtime still alive after destroy",
				"sessionID", sessionID, "handleId", rec.HandleID, "error", destroyErr)
			stillAlive = append(stillAlive, fmt.Errorf("%s: %w", rec.HandleID, destroyErr))
		}
	}

	if len(stillAlive) > 0 {
		release()
		return nil, fmt.Errorf("close shell terminals for session %s: %d still alive: %w",
			sessionID, len(stillAlive), errors.Join(stillAlive...))
	}
	return release, nil
}

// newShellTerminalHandleID mints a runtime handle id for a shell pane.
//
// The shellterm- prefix keeps shell handles trivially distinguishable from
// session handles in logs, the DB, and the mux. The character set is
// constrained by the runtime adapters, which are stricter than they look:
// conpty rejects anything outside ^[a-zA-Z0-9_-]+$ and tmux uses the id as a
// session name — so hex, not base64.
func newShellTerminalHandleID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "shellterm-" + hex.EncodeToString(buf), nil
}

func shellTerminalFromRecord(rec ShellTerminalRecord) ShellTerminal {
	return ShellTerminal{
		HandleID:   rec.HandleID,
		ProjectID:  rec.ProjectID,
		SessionID:  rec.SessionID,
		WorkingDir: rec.WorkingDir,
		Title:      rec.Title,
		CreatedAt:  rec.CreatedAt,
	}
}

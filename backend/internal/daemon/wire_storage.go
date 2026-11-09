package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/runtimeselect"
	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/browserstream"
	"tallpine.eu/tallpine/backend/internal/buildinfo"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/diag"
	"tallpine.eu/tallpine/backend/internal/fsperm"
	"tallpine.eu/tallpine/backend/internal/hostsock"
	"tallpine.eu/tallpine/backend/internal/notify"
	"tallpine.eu/tallpine/backend/internal/previewserver"
	"tallpine.eu/tallpine/backend/internal/runfile"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
	notificationsvc "tallpine.eu/tallpine/backend/internal/service/notification"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
	"tallpine.eu/tallpine/backend/internal/skillassets"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

// wireStorage: config, locks, the store, the runtime and the lifecycle stack.
func (b *boot) wireStorage() error {
	var err error
	if err := b.loadConfigAndLogging(); err != nil {
		return err
	}
	if err := b.initBrowserRuntime(); err != nil {
		return err
	}
	if err := b.lockDaemonInstance(); err != nil {
		return err
	}
	if err := b.initSessionTokens(); err != nil {
		return err
	}
	if err := b.openStore(); err != nil {
		return err
	}
	// signal.NotifyContext cancels ctx on SIGINT/SIGTERM, which drives the
	// graceful shutdown inside Server.Run and stops the background goroutines.
	b.ctx, b.stop = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go diag.Run(b.ctx, b.log, diag.Interval)
	b.cleanup(b.stop)
	b.cdcPipe, err = startCDC(b.ctx, b.store, b.log)
	if err != nil {
		return err
	}

	// Terminal streaming: the selected platform runtime supplies the
	// attach Stream and liveness; the CDC broadcaster feeds the session-state channel. The manager
	// is handed to httpd, which mounts it at /mux. Raw PTY bytes never flow
	// through the CDC change_log -- only session-state events do.
	b.runtimeAdapter = runtimeselect.New(b.log, b.cfg.RunFilePath)
	b.managedPreview = previewserver.New(b.log, b.cfg.DataDir)
	b.termMgr = terminal.NewManager(b.runtimeAdapter, b.cdcPipe.Broadcaster, b.log)
	b.cleanup(func() { b.termMgr.Close() })

	// The agent messenger sends validated user input to the session's live
	// runtime pane. Keep this path small until durable inbox semantics are needed.
	// Built before the Lifecycle Manager so the LCM can use it for SCM-driven
	// agent nudges (CI failure, review feedback, merge conflict).
	b.lifecycleMessenger = newModeAwareMessenger()
	b.notificationHub = notify.NewHub()
	notificationBarrier := &sync.Mutex{}
	b.notifier = notificationsvc.New(notificationsvc.Deps{
		Store: b.store, Publisher: b.notificationHub, Barrier: notificationBarrier, Logger: b.log,
	})
	b.notificationWriter = notify.New(notify.Deps{Store: b.store, Publisher: b.notificationHub, Barrier: notificationBarrier})
	// Resolution transitions that happened while the daemon was down never
	// reached lifecycle, so re-check open notifications against the durable
	// session/PR facts before serving. Best-effort: a failure here only leaves
	// stale rows in the unresolved list, never blocks startup.
	if err := b.notificationWriter.Reconcile(b.ctx); err != nil {
		b.log.Warn("notification resolution reconcile failed", "err", err)
	}

	// Bring up the Lifecycle Manager and the reaper first: it makes the session
	// lifecycle write path live (reducer write -> store -> DB trigger ->
	// change_log -> poller -> broadcaster) and gives startSession the shared LCM.
	// The agent resolver is built before the LCM so lifecycle can consume the
	// adapter-declared active-turn steering capability; startSession reuses it.
	b.agents, err = buildAgentResolver(normalizeDefaultAgent(b.cfg.Agent, b.log), b.log)
	if err != nil {
		b.stop()
		if cdcErr := b.cdcPipe.Stop(); cdcErr != nil {
			b.log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("wire agent resolver: %w", err)
	}
	b.messenger = newSessionMessenger(b.store, b.runtimeAdapter, b.log, b.agents)

	b.lcStack = startLifecycle(b.ctx, b.cfg.DataDir, b.store, b.runtimeAdapter, b.lifecycleMessenger, b.notificationWriter, b.agents, b.cfg.ArtifactVersionsKeep, b.log)
	return nil
}

// loadConfigAndLogging loads config, hardens permissions and starts logging.
func (b *boot) loadConfigAndLogging() error {
	// Before config.Load can create the new home: a legacy install must be migrated first.
	if err := guardLegacyInstall(); err != nil {
		return err
	}
	// Read before the scrub below removes the session variables.
	sessionID, inSession := config.InAgentSession(os.Getenv)
	// Before anything can spawn: a daemon started from inside an agent must not
	// hand that agent's session identity to the sessions it launches.
	scrubbedEnv := agentlaunch.ScrubInheritedHostEnv()
	var err error
	b.cfg, err = config.Load()
	if err != nil {
		return err
	}
	if inSession {
		if err := config.CheckDaemonIsolation(b.cfg, sessionID); err != nil {
			return err
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		b.cfg.StartupWorkingDirectory = cwd
	}
	permWarnings, err := fsperm.Harden(b.cfg.HardenRoot(), b.cfg.DataDir)
	if err != nil {
		return fmt.Errorf("lock down data root: %w", err)
	}
	if err := stabilizeWorkingDirectory(b.cfg.DataDir); err != nil {
		return err
	}
	hostsock.SetDir(b.cfg.DataDir)
	ignoreBrokenPipeSignal()

	var closeLog func()
	b.log, closeLog = newLogger(b.cfg.DataDir)
	b.cleanup(closeLog)
	b.cleanup(installDefaultLogger(b.log))
	for _, w := range permWarnings {
		b.log.Warn("data root permissions: could not tighten", "error", w)
	}
	b.log.Info("daemon: starting", "version", buildinfo.Version, "commit", buildinfo.Commit, "buildDate", buildinfo.Date, "dirty", buildinfo.IsDirty())
	if len(scrubbedEnv) > 0 {
		b.log.Info("daemon: dropped inherited agent-host identity from the environment", "keys", scrubbedEnv)
	}
	return nil
}

// initBrowserRuntime reads or mints the browser runtime token and builds the brokers.
func (b *boot) initBrowserRuntime() error {
	var err error
	var browserRuntimeToken string
	if os.Getenv(browserruntime.RuntimeTokenStdinEnv) == "1" {
		browserRuntimeToken, err = browserruntime.ReadRuntimeToken(os.Stdin)
		if err != nil {
			return err
		}
	}
	if browserRuntimeToken == "" {
		browserRuntimeToken, err = browserruntime.NewToken()
		if err != nil {
			return err
		}
	}
	b.browserAuthority = browsersvc.NewAuthority()
	b.browserBroker = browserruntime.New(b.log, browserRuntimeToken)
	b.browserStreamBroker = browserstream.New(b.log, browserRuntimeToken)
	return nil
}

// lockDaemonInstance refuses to start over a live predecessor and takes the data-dir lock.
func (b *boot) lockDaemonInstance() error {
	// Fail fast only if a daemon is genuinely still serving the recorded port.
	// CheckStale confirms the run-file's PID is alive, but that alone is not
	// proof a predecessor owns the port: the file leaks when the daemon is hard
	// killed without a graceful shutdown (the norm on Windows, where the desktop
	// supervisor can only TerminateProcess it), and Windows reuses the recorded
	// PID for unrelated processes. So a "live" PID is verified against an actual
	// /healthz probe; a run-file left by a crashed/hard-killed/reused-PID
	// predecessor is treated as stale and overwritten when the new server starts.
	if live, err := runfile.CheckStale(b.cfg.RunFilePath); err != nil {
		return fmt.Errorf("inspect run-file: %w", err)
	} else if live != nil && runFileOwnerServing(staleProbeClient(live), config.LoopbackHost, live) {
		return fmt.Errorf("daemon already running (pid %d, port %d); refusing to start", live.PID, live.Port)
	}

	// Take the data-directory lock before touching SQLite. The run-file check
	// above only proves nobody else owns this port/run file; it says nothing
	// about the data dir, so a second daemon with a different TALLPINE_PORT/TALLPINE_RUN_FILE
	// but the same TALLPINE_DATA_DIR would otherwise open and hot-migrate the database
	// underneath the first daemon, invalidating its prepared statements against
	// the new schema (issue #3716). The lock is scoped to the data dir alone and
	// is held until Run returns; a crashed holder's lock is dropped by the OS, so
	// a successor reclaims it with no stale-lock bookkeeping.
	//
	// A desktop takeover spawns this daemon as soon as the old one's /healthz
	// goes dark, which happens when its shutdown starts. With T = ShutdownTimeout,
	// the old daemon's capped shutdown (end of Run) then takes at most 8T+18s:
	//   - the HTTP drain and seven stages capped at T each: failure dispatcher,
	//     harness installer, background workers, agent-switch workers, Codex
	//     account-switch workers, chat StopAll, LAN listener;
	//   - fixed caps: 5s agent-switch fault enqueue, 3s `tailscale serve off`,
	//     5s closed-lid lease release plus 3s caffeinate exit (keep-awake).
	// Wait 9T+18s (108s by default), one stage of margin. Run's uncapped waits
	// (startup reconcile, preview servers, chat side stops, usage, pricing,
	// lifecycle stack) are not capped, so the predecessor's shutdown backstop
	// forces it out first, and a predecessor still holding the lock once the
	// wait is over is killed if it marked itself stopping (B564).
	dataDirLock, err := lockDataDir(context.Background(), b.cfg.DataDir, b.cfg.RunFilePath, 9*b.cfg.ShutdownTimeout+dataDirLockFixedWait, b.log)
	if err != nil {
		return err
	}
	b.dataDirLock = dataDirLock
	b.cleanup(func() { _ = dataDirLock.Release() })
	return nil
}

// dataDirLockFixedWait is the fixed part (the sum of the fixed shutdown caps)
// of the data-dir lock wait; the e2e build tag makes it settable.
var dataDirLockFixedWait = 18 * time.Second

// initSessionTokens rotates the install token and derives the session-token minter.
func (b *boot) initSessionTokens() error {
	// Agents get scoped tokens derived from the install token instead of the
	// install token itself (docs/adr/0008).

	if !b.cfg.DaemonAuthDisabled {
		// Only after the already-running check and the data-dir lock: a refused
		// second start must not rotate the token a live daemon still serves.
		// The install token is rotated on every start; session tokens are
		// keyed on a separate stable secret so running agents stay valid.
		// The HTTP server then adopts the rotated file via Ensure.
		_, sessionKey, err := daemontoken.RotateOnStart(daemontoken.Path(b.cfg.RunFilePath))
		if err != nil {
			return fmt.Errorf("daemon auth: %w", err)
		}
		b.sessionTokens = sessiontoken.New(sessionKey)
	}
	return nil
}

// openStore opens the durable store and its housekeeping.
func (b *boot) openStore() error {
	var err error
	// Open the durable store and bring up the CDC substrate: DB triggers capture
	// changes into change_log, the poller tails it, and the broadcaster fans
	// events out to live transports.
	b.store, err = sqlite.Open(b.cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	b.cleanup(func() { _ = b.store.Close() })
	b.managedLocks = newManagedLocks()
	b.store.SetManaged(b.managedLocks)
	if b.sessionTokens != nil {
		b.sessionTokens = rotateIdleSessionKey(context.Background(), daemontoken.Path(b.cfg.RunFilePath), b.store, b.sessionTokens, b.log)
	}
	if _, err := b.store.RequeueClaimedReports(context.Background()); err != nil {
		return fmt.Errorf("recover report delivery claims: %w", err)
	}
	if err := b.store.ReleaseStaleLaneHolds(context.Background()); err != nil {
		return fmt.Errorf("release stale lane holds: %w", err)
	}
	b.ghAccounts = newGitHubAccounts(b.store, b.log)
	b.cleanup(installActiveGitHub(b.ghAccounts, b.cfg.GitHubEnterprise))
	logAutoPickBypass(b.log)
	// Refresh the embedded using-tallpine skill into the data dir so worker sessions
	// in any project can read the tallpine CLI catalog from a stable absolute path.
	// Non-fatal: the skill is an enhancement over `tallpine --help`, not required.
	if err := skillassets.Install(b.cfg.DataDir); err != nil {
		b.log.Warn("install using-tallpine skill", "err", err)
	}
	// One-time cleanup: pre-launch-flag Tallpine hook entries left in workspace settings
	// fail with "command not found" when the owner runs a plain agent there.
	go stripStaleWorkspaceHooks(context.Background(), filepath.Join(b.cfg.DataDir, "worktrees"), b.log)
	return nil
}

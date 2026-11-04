// Package daemon owns the Tallpine backend process: config loading,
// loopback HTTP serving, durable storage, CDC fan-out, lifecycle wiring, and
// graceful shutdown.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	claudecodeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	codexagent "tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agentaccount"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/datadirlock"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

// usageReconcileTick is the safety-net interval for the usage pipeline
// reconcile: hookless resume transcripts and silently lost fsnotify watches
// are healed on this cadence even when no event or hook fires.
const usageReconcileTick = 3 * time.Minute

const chatHibernateSweepTick = 30 * time.Second

// Session Manager asserts the terminal input gate's optional capabilities at
// runtime: a signature drift compiles but silently turns the send guard's
// keystroke fence off (#5711).
var _ interface {
	sessionmanager.TerminalInputGate
	sessionmanager.TerminalInputObserver
	sessionmanager.TerminalInputFencer
} = (*terminal.Manager)(nil)

// Run starts the daemon and blocks until it exits. SIGINT/SIGTERM drive
// graceful shutdown through the HTTP server and background workers.
func Run() error {
	b := &boot{}
	defer b.runCleanups()
	for _, stage := range b.bootStages() {
		if err := stage(); err != nil {
			return err
		}
	}
	return nil
}

type reportSemanticSession interface {
	SendSemantic(context.Context, domain.SessionID, string, string) error
	InterruptTUI(context.Context, domain.SessionID) error
}

type reportSemanticDelivery struct {
	chat     *chatsvc.Service
	sessions reportSemanticSession
	store    interface {
		GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	}
}

func (d reportSemanticDelivery) Submit(ctx context.Context, id domain.SessionID, message, idempotencyKey string) error {
	if d.store != nil {
		rec, found, err := d.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if found && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
			if d.chat == nil {
				return reportsvc.ErrReportDeliveryModeUnsupported
			}
			return d.chat.SubmitReports(ctx, id, message, idempotencyKey)
		}
	}
	if d.sessions == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	err := d.sessions.SendSemantic(ctx, id, message, idempotencyKey)
	if errors.Is(err, sessionmanager.ErrComposerBusy) {
		return fmt.Errorf("%w: %w", reportsvc.ErrOrchestratorBusy, err)
	}
	return err
}

func (d reportSemanticDelivery) Interrupt(ctx context.Context, id domain.SessionID) error {
	if d.store == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	rec, ok, err := d.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return sessionmanager.ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		if d.sessions == nil {
			return reportsvc.ErrReportDeliveryModeUnsupported
		}
		return d.sessions.InterruptTUI(ctx, id)
	}
	if d.chat == nil {
		return reportsvc.ErrReportDeliveryModeUnsupported
	}
	err = d.chat.Interrupt(ctx, id)
	if errors.Is(err, chatsvc.ErrNoActiveTurn) {
		return nil
	}
	return err
}

func installedAgentHarness(target systeminstall.Target) (string, bool) {
	if target == systeminstall.TargetClaude {
		return "claude-code", true
	}
	if systeminstall.IsAgentTarget(target) {
		return string(target), true
	}
	return "", false
}

// harnessBinaryCache holds agent plugins that cache the executable they resolved.
type harnessBinaryCache interface {
	InvalidateBinaryResolution(harness domain.AgentHarness)
}

// resolverBinaryCache is the plugins an AgentResolver hands out.
type resolverBinaryCache struct{ agents ports.AgentResolver }

func (c resolverBinaryCache) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if agent, ok := c.agents.Agent(harness); ok {
		if invalidator, ok := agent.(ports.AgentBinaryResolutionInvalidator); ok {
			invalidator.InvalidateBinaryResolution()
		}
	}
}

// pluginBinaryCache is one harness's plugin held outside any resolver.
type pluginBinaryCache struct {
	harness domain.AgentHarness
	plugin  ports.AgentBinaryResolutionInvalidator
}

func (c pluginBinaryCache) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if harness == c.harness {
		c.plugin.InvalidateBinaryResolution()
	}
}

// invalidateHarnessBinary makes the harness's executable resolve again
// everywhere. Launches and install verification, the agent service, Chat
// drivers and model discovery each hold their own plugin instances, each
// caching the path it found: missing one leaves it on the binary from before
// the install.
func invalidateHarnessBinary(readiness ports.AgentReadinessProvider, harness string, caches ...harnessBinaryCache) {
	readiness.InvalidateAgentInstallation(harness)
	for _, cache := range caches {
		cache.InvalidateBinaryResolution(domain.AgentHarness(harness))
	}
}

func usagePipelineWatchRoots(roots usagesvc.SourceRoots) []string {
	return []string{
		roots.ClaudeProjects,
		roots.CodexSessions,
		roots.CodexArchived,
	}
}

// lockDataDir takes the data-dir lock. When another Tallpine process holds it, as a
// predecessor still shutting down during a takeover does, it logs and waits up
// to wait for that process to exit, then refuses to start. On a file system
// that cannot lock it returns a nil lock (Release is a no-op) and no error.
func lockDataDir(ctx context.Context, dataDir, runFile string, wait time.Duration, log *slog.Logger) (*datadirlock.Lock, error) {
	lock, err := lockDataDirWait(ctx, dataDir, wait, log)
	lockPath, ino := datadirlock.Describe(dataDir)
	// Always recorded, so a second daemon surviving an install can be told apart by data dir, run file and lock inode (B469).
	log.Info("daemon start", "pid", os.Getpid(), "data_dir", dataDir, "run_file", runFile,
		"lock_path", lockPath, "lock_inode", ino, "guarded", lock != nil, "err", err)
	return lock, err
}

func lockDataDirWait(ctx context.Context, dataDir string, wait time.Duration, log *slog.Logger) (*datadirlock.Lock, error) {
	lock, err := datadirlock.Acquire(ctx, dataDir, 0)
	if errors.Is(err, datadirlock.ErrUnsupported) {
		// Some NFS, SMB and FUSE mounts cannot lock files. Start unguarded, as Tallpine
		// did before the lock, rather than refuse to run on them.
		log.Error("UNGUARDED START: data dir file system cannot lock files; a second daemon can run on this data dir", "data_dir", dataDir, "pid", os.Getpid(), "err", err)
		return nil, nil
	}
	if !errors.Is(err, datadirlock.ErrLocked) {
		return lock, err
	}
	log.Warn("waiting for another Tallpine process to release the data dir", "err", err, "wait", wait)
	start := time.Now()
	lock, err = datadirlock.Acquire(ctx, dataDir, wait)
	if errors.Is(err, datadirlock.ErrLocked) {
		return nil, fmt.Errorf("%w; waited %s for it to exit, refusing to start", err, wait)
	}
	if err != nil {
		return nil, err
	}
	log.Info("data dir lock acquired", "waited", time.Since(start).Round(time.Millisecond))
	return lock, nil
}

func stabilizeWorkingDirectory(dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("daemon working directory: data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("daemon working directory: create %s: %w", dataDir, err)
	}
	if err := os.Chdir(dataDir); err != nil {
		return fmt.Errorf("daemon working directory: chdir %s: %w", dataDir, err)
	}
	return nil
}

// claudeDeviceFolder is the computer's own Claude folder and config file. An
// inherited CLAUDE_CONFIG_DIR is ignored (the daemon may have been started from
// another account's session), matching Claude.LaunchEnv("").
func claudeDeviceFolder() (home, configFile string) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	return filepath.Join(h, ".claude"), filepath.Join(h, ".claude.json")
}

// newAgentAccounts builds the Claude Code and Codex account catalog. Each
// account is a config folder under the state dir; "This computer" is the CLI's
// own folder, which Tallpine never changes the login of.
func newAgentAccounts(cfg config.Config, store *sqlite.Store, codexHome string, codexAccounts ports.CodexAccountClientFactory, log *slog.Logger) *agentaccounts.Service {
	claudeHome, claudeConfig := claudeDeviceFolder()
	providers := []ports.AgentAccountProviderCLI{
		&agentaccount.Claude{Binary: claudecodeagent.New().ResolveBinary, DeviceHome: claudeHome, DeviceConfig: claudeConfig},
		&agentaccount.Codex{Binary: codexagent.New().ResolveBinary, Accounts: codexAccounts, DeviceHome: codexHome},
	}
	return agentaccounts.New(store, providers, filepath.Join(cfg.StateDir, "agent-accounts"), nil, log)
}

// statusReadinessSource exposes the session manager's live readiness signal to
// the SSE stream. A nil result (manager without the capability) leaves the
// stream without readiness frames; clients fall back to their slow poll.
func statusReadinessSource(mgr any) interface {
	SubscribeStatusReadiness(func()) (unsubscribe func())
} {
	src, _ := mgr.(interface {
		SubscribeStatusReadiness(func()) (unsubscribe func())
	})
	return src
}

// updateSocket is the daemon socket the update job probes, or "" without a
// run-file (no socket is bound then).
func updateSocket(runFilePath string) string {
	if runFilePath == "" {
		return ""
	}
	return daemonsock.Path(runFilePath)
}

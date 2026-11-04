package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/processenv"
	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// connect spawns app-server and completes the initialize handshake.
func (d *Driver) connect(ctx context.Context, workdir string, env map[string]string, providerScopeID string) (*conversation, error) {
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}

	proc, err := d.spawn(ctx, bin, workdir, codexProcessEnv(ctx, bin, env))
	if err != nil {
		return nil, fmt.Errorf("%w: launch app-server: %w", ports.ErrChatDriverUnavailable, err)
	}

	conv := newConversation(proc, d.log, providerScopeID)
	if err := d.initialize(ctx, conv); err != nil {
		_ = conv.Close()
		return nil, err
	}
	return conv, nil
}

func (d *Driver) connectSession(
	ctx context.Context,
	sessionID domain.SessionID,
	dataDir, workdir string,
	env map[string]string,
	prepareEnv func(context.Context) (map[string]string, error),
	providerScopeID string,
	reconnectOnly bool,
	fence func(argv []string, env map[string]string) ([]string, error),
) (*conversation, bool, error) {
	// Injected driver tests intentionally retain the direct pipe launcher. The
	// shipped driver uses spawnAppServer and therefore the persistent host.
	if !d.persistent {
		if reconnectOnly {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if prepareEnv != nil {
			var err error
			env, err = prepareEnv(ctx)
			if err != nil {
				return nil, false, err
			}
		}
		conv, err := d.connect(ctx, workdir, env, providerScopeID)
		return conv, false, err
	}
	var argv []string
	if !reconnectOnly {
		bin, err := d.plugin.ResolveBinary(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
		}
		argv = []string{bin, "app-server"}
		// Without a Prepare hook the env is final here; with one, Prepare
		// fences against the prepared env instead.
		if prepareEnv == nil {
			if argv, err = fence(argv, env); err != nil {
				return nil, false, err
			}
		}
	}
	hostConfig := persistenthost.Config{
		SessionID:     string(sessionID),
		ReconnectOnly: reconnectOnly,
		DataDir:       dataDir,
		Workdir:       workdir,
		Env:           envSlice(env),
		Argv:          argv,
	}
	if prepareEnv != nil {
		hostConfig.Prepare = func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			preparedEnv, prepareErr := prepareEnv(prepareCtx)
			if prepareErr != nil {
				return persistenthost.PreparedProvider{}, prepareErr
			}
			preparedArgv, fenceErr := fence(argv, preparedEnv)
			if fenceErr != nil {
				return persistenthost.PreparedProvider{}, fenceErr
			}
			return persistenthost.PreparedProvider{Env: envSlice(preparedEnv), Argv: preparedArgv}, nil
		}
	}
	transport, err := d.connectHost(ctx, hostConfig)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, false, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) ||
			errors.Is(err, persistenthost.ErrAttached) ||
			errors.Is(err, persistenthost.ErrIncompatible) ||
			errors.Is(err, persistenthost.ErrUnauthorized) {
			return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, false, fmt.Errorf("%w: persistent host: %w", ports.ErrChatDriverUnavailable, err)
	}
	proc := &process{
		stdin:         transport.Stdin,
		stdout:        transport.Stdout,
		reconnected:   transport.Reconnected,
		nextRequestID: transport.NextRequestID,
		stop:          transport.Stdin.Close,
		terminate: func() error {
			_ = transport.Stdin.Close()
			// As for ACP hosts: the host's own shutdown can take about 6 s.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return persistenthost.Shutdown(shutdownCtx, dataDir, string(sessionID))
		},
	}
	conv := newConversation(proc, d.log, providerScopeID)
	if transport.Reconnected {
		// A daemon stopped mid-initialize leaves a host whose app-server never
		// was, failing every call "Not initialized". An initialized one refuses
		// a second initialize and keeps its state.
		fresh, err := initializeIfNeeded(ctx, conv.conn)
		if err != nil {
			_ = conv.Close()
			return nil, false, err
		}
		if !fresh {
			return conv, true, nil
		}
		proc.reconnected = false
		return conv, false, nil
	}
	if err := d.initialize(ctx, conv); err != nil {
		_ = conv.Terminate()
		return nil, false, err
	}
	return conv, false, nil
}

func (d *Driver) initialize(ctx context.Context, conv *conversation) error {
	return initializeConnection(ctx, conv.conn)
}

// initializeIfNeeded initializes an app-server reached through a live host. It
// reports whether the app-server had not been initialized before.
func initializeIfNeeded(ctx context.Context, connection *conn) (bool, error) {
	err := initializeConnection(ctx, connection)
	var refused *rpcError
	if errors.As(err, &refused) && refused.Code == -32600 && strings.EqualFold(refused.Message, "Already initialized") {
		return false, nil
	}
	return err == nil, err
}

func initializeConnection(ctx context.Context, connection *conn) error {
	initCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := connection.request(initCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": clientName, "title": clientTitle, "version": clientVersion},
		"capabilities": map[string]any{"experimentalApi": true, "optOutNotificationMethods": nil},
	}, nil); err != nil {
		return fmt.Errorf("%w: initialize: %w", ports.ErrChatDriverIncompatible, err)
	}
	if err := connection.notify("initialized", nil); err != nil {
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

// spawnAppServer is the real launcher.
func spawnAppServer(ctx context.Context, bin, workdir string, env []string) (*process, error) {
	args := []string{"app-server"}
	cmd := tallpineprocess.Command(bin, args...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}

	// Drain stderr so a chatty provider cannot fill the pipe buffer and wedge
	// its own process.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	var stopped bool
	return &process{
		stdin:  stdin,
		stdout: stdout,
		stop: func() error {
			if stopped {
				return nil
			}
			stopped = true
			// Closing stdin is the graceful shutdown; kill only if it lingers.
			_ = stdin.Close()
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
			return nil
		},
	}, nil
}

// envSlice merges Tallpine's session env OVER the daemon's own, in the KEY=VALUE form
// exec wants. Sorted so a relaunch is byte-identical, which makes process diffs
// readable.
//
// The merge is the point. Tallpine's map is an OVERLAY -- session id, project id, the
// HookPATH-pinned PATH -- not a whole environment; the terminal path gets away
// with treating it as one only because tmux inherits the daemon's env underneath.
// Using it as a replacement launched the provider with eight variables and no
// HOME, USER, TMPDIR, LANG or SSH_AUTH_SOCK. The provider itself survived that
// (its home-directory lookup falls back to the passwd database), which is why it
// went unnoticed, but every shell command the agent runs inherits this env too:
// no SSH agent means `git push` over SSH fails, no HOME means global git config
// and every toolchain cache is missing. Found while writing the Claude driver,
// where the same shape failed outright with "Not logged in".
func envSlice(env map[string]string) []string {
	return processenv.Merge(env)
}

// codexProcessEnv applies the same executable-aware PATH augmentation as the
// TUI runtime before either the compatibility probe or app-server starts. The
// resolved npm launcher can be absolute and still depend on `#!/usr/bin/env
// node`; Finder-launched daemons commonly resolve the launcher from inventory
// while omitting the Node version manager from PATH.
func codexProcessEnv(ctx context.Context, bin string, env map[string]string) []string {
	overlay := make(map[string]string, len(env)+1)
	for key, value := range env {
		overlay[key] = value
	}
	if _, ok := overlay["PATH"]; !ok {
		overlay["PATH"] = os.Getenv("PATH")
	}
	agentlaunch.AugmentRuntimePATHForLaunchBinary(ctx, overlay, []string{bin}, exec.LookPath, agentlaunch.PinnedDir(os.Executable, overlay["TALLPINE_DATA_DIR"]))
	return envSlice(overlay)
}

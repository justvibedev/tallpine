package persistenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/processalive"
)

// ConnectOrStart attaches to an existing compatible host or starts one with the
// current Tallpine executable. Failed/incompatible probes never terminate that host.
func ConnectOrStart(ctx context.Context, cfg Config) (*Transport, error) {
	if d, err := readDescriptor(cfg.DataDir, cfg.SessionID); err == nil {
		if err := validateDescriptor(cfg, d); err != nil && processalive.Alive(d.PID) {
			return nil, err
		}
		transport, attachErr := attach(ctx, d, true)
		if attachErr == nil {
			return transport, nil
		}
		if errors.Is(attachErr, ErrAttached) {
			// Desktop updater handoff can briefly start the replacement daemon
			// before the old controller has detached. Wait for exclusive ownership;
			// never turn that overlap into a competing provider process.
			deadline := time.Now().Add(startupTimeout)
			for time.Now().Before(deadline) && processalive.Alive(d.PID) {
				timer := time.NewTimer(20 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
				transport, attachErr = attach(ctx, d, true)
				if attachErr == nil {
					return transport, nil
				}
				if !errors.Is(attachErr, ErrAttached) {
					break
				}
			}
		}
		// A failed socket probe is not proof that a provider is dead. Only an OS
		// process observation permits clearing the descriptor and falling back to
		// native resume in a new host.
		if processalive.Alive(d.PID) {
			return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, attachErr)
		}
		// Do not remove stale ownership files here. Another starter may have
		// already reclaimed the dead lock and published a replacement descriptor
		// since this client read d. The detached host's O_EXCL lock acquisition is
		// the single authority for reclaiming dead ownership.
	} else if !errors.Is(err, os.ErrNotExist) {
		// A malformed or unreadable ownership record is not proof that no host
		// exists. Fail closed instead of launching a competing process.
		return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, err)
	}
	if cfg.ReconnectOnly {
		return nil, ErrNotRunning
	}
	if !filepath.IsAbs(cfg.Workdir) {
		return nil, errors.New("chat host start requires an absolute workdir")
	}
	if cfg.Prepare != nil {
		prepared, err := cfg.Prepare(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = prepared.Env
		cfg.Argv = prepared.Argv
	}
	if len(cfg.Argv) == 0 {
		return nil, errors.New("chat host start requires provider argv")
	}
	if err := spawnDetached(ctx, cfg); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(startupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
		if err == nil {
			if compatibleErr := validateDescriptor(cfg, d); compatibleErr != nil {
				return nil, compatibleErr
			}
			transport, attachErr := attach(ctx, d, false)
			if attachErr == nil {
				return transport, nil
			}
			lastErr = attachErr
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d, readErr := readDescriptor(cfg.DataDir, cfg.SessionID); readErr == nil && processalive.Alive(d.PID) {
		return nil, fmt.Errorf("%w: %w", ErrOwnershipInconclusive, lastErr)
	}
	return nil, fmt.Errorf("start chat host: %w", lastErr)
}

func attach(ctx context.Context, d Descriptor, reconnected bool) (*Transport, error) {
	if d.Version != ProtocolVersion {
		return nil, fmt.Errorf("%w: host=%d daemon=%d", ErrIncompatible, d.Version, ProtocolVersion)
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := dialHost(handshakeCtx, d.Address)
	if err != nil {
		return nil, err
	}
	finishHandshake := bindConnToContext(handshakeCtx, conn)
	reader := bufio.NewReader(conn)
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "attach"}); err != nil {
		_ = conn.Close()
		return nil, finishHandshake(err)
	}
	var response helloResponse
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, finishHandshake(err)
	}
	if err := finishHandshake(nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := json.Unmarshal(line, &response); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !response.OK {
		_ = conn.Close()
		switch response.Error {
		case ErrAttached.Error():
			return nil, ErrAttached
		case ErrUnauthorized.Error():
			return nil, ErrUnauthorized
		default:
			return nil, errors.New(response.Error)
		}
	}
	return &Transport{
		Stdin: conn, Stdout: reader, Reconnected: reconnected,
		NextRequestID: response.NextRequestID, ACPState: response.ACPState,
	}, nil
}

func bindConnToContext(ctx context.Context, conn net.Conn) func(error) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func(err error) error {
		stop()
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = conn.Close()
			return ctxErr
		}
		return err
	}
}

// HostPID returns the live provider host pid recorded for sessionID, for
// memory accounting of runtime-less Chat sessions. A missing descriptor or an
// exited host reports false; nothing is ever started or stopped here.
func HostPID(dataDir, sessionID string) (int, bool) {
	d, err := readDescriptor(dataDir, sessionID)
	if err != nil || d.PID <= 0 || !processalive.Alive(d.PID) {
		return 0, false
	}
	return d.PID, true
}

// Shutdown terminates current session ownership and waits for it to end.
// Missing/dead hosts are harmless; unknown live owners fail closed.
// The protocol acknowledgement only confirms that shutdown was requested.
func Shutdown(ctx context.Context, dataDir, sessionID string) error {
	return shutdown(ctx, dataDir, sessionID, false)
}

// ShutdownQuick is Shutdown for a provider that gains nothing from the stdin
// grace (session closed, or deaf to stdin). An older host waits the full grace.
func ShutdownQuick(ctx context.Context, dataDir, sessionID string) error {
	return shutdown(ctx, dataDir, sessionID, true)
}

func shutdown(ctx context.Context, dataDir, sessionID string, quick bool) error {
	d, err := readDescriptor(dataDir, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Version != ProtocolVersion {
		return ErrIncompatible
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := dialHost(handshakeCtx, d.Address)
	if err != nil {
		if !processalive.Alive(d.PID) {
			return nil
		}
		return err
	}
	defer func() { _ = conn.Close() }()
	finishHandshake := bindConnToContext(handshakeCtx, conn)
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "shutdown", QuickStop: quick}); err != nil {
		return finishHandshake(err)
	}
	var response helloResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return finishHandshake(err)
	}
	if err := finishHandshake(nil); err != nil {
		return err
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	// The host's own shutdown can take about 6 s: 3 s for the provider to exit
	// once stdin closes, a 1 s reap, and 2 s for its process group to exit on
	// SIGTERM before SIGKILL. Wait past that, with room for a loaded machine.
	waitCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := readDescriptor(dataDir, sessionID)
		if readErr == nil && current.Token != d.Token {
			return nil // A new owner could only publish after the old lock released.
		}
		if errors.Is(readErr, os.ErrNotExist) {
			path, _ := lockPath(dataDir, sessionID)
			if _, lockErr := os.Stat(path); errors.Is(lockErr, os.ErrNotExist) {
				return nil
			} else if lockErr != nil {
				return lockErr
			}
		} else if readErr != nil {
			return readErr
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for chat host shutdown: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

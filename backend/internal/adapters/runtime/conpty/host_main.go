// host_main.go is the RunHost entrypoint for the "tallpine pty-host" subcommand.
// It is cross-platform: the loopback TCP bind and signal wiring work on all
// OSes; native PTY creation is OS-gated via build tags.
package conpty

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"tallpine.eu/tallpine/backend/internal/hostsock"
)

// RunHost is the "tallpine pty-host" entrypoint. argv is everything after the
// subcommand name: [--start=attach] <sessionId> <cwd> <shellCmd> [shellArg...]
//
// --start=attach defers starting the process until a client first reports its
// grid (see deferredPTY), so it starts at the size its viewer shows. Without it
// the process starts immediately at the default grid.
//
// It binds 127.0.0.1:0 (OS assigns the port), creates the native PTY, prints
// "READY:<pid> <port>\n" to stdout (the parent process reads this to learn the
// port), installs SIGTERM/SIGINT handlers, then runs Serve. Returns a process
// exit code.
//
// The loopback port is reachable by every local user, so each connection must
// open with a MsgAuth frame carrying the per-host random token, which only the
// spawning daemon learns (via the READY line on the stdout pipe).
func RunHost(args []string, stdout io.Writer) int {
	opts, args := splitHostOptionArgs(args)
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: tallpine pty-host [--start=attach] [--surface=lazy] <sessionId> <cwd> <shellCmd> [shellArg...]\n")
		return 1
	}

	sessionID := args[0]
	cwd := args[1]
	shellCmd := args[2]
	shellArgs := args[3:]
	if err := os.Chdir(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: chdir %s: %v\n", sessionID, cwd, err)
		return 1
	}

	// Bind before creating the PTY so we can report READY atomically.
	ln, endpoint, err := listenHost()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: listen: %v\n", sessionID, err)
		return 1
	}
	token, err := newHostToken()
	if err != nil {
		_ = ln.Close()
		fmt.Fprintf(os.Stderr, "pty-host [%s]: token: %v\n", sessionID, err)
		return 1
	}

	var pty ptyConn
	if opts.StartOnAttach {
		pty = newDeferredPTY(func(cols, rows uint16) (ptyConn, error) {
			conn, err := newConPTY(cwd, shellCmd, shellArgs, cols, rows)
			if err != nil {
				fmt.Fprintf(os.Stderr, "pty-host [%s]: newConPTY: %v\n", sessionID, err)
			}
			return conn, err
		})
	} else {
		pty, err = newConPTY(cwd, shellCmd, shellArgs, initialConPTYColumns, initialConPTYRows)
		if err != nil {
			_ = ln.Close()
			fmt.Fprintf(os.Stderr, "pty-host [%s]: newConPTY: %v\n", sessionID, err)
			return 1
		}
	}

	// Print READY after both the listener and the PTY are up.
	if hostsock.IsUnix(endpoint) {
		_, _ = fmt.Fprintf(stdout, "READY:%d %s %s\n", pty.PID(), token, endpoint)
	} else {
		_, _ = fmt.Fprintf(stdout, "READY:%d %s %s\n", pty.PID(), endpoint, token)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Install signal handlers so SIGTERM/SIGINT trigger graceful shutdown.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigC:
			fmt.Fprintf(os.Stderr, "pty-host [%s]: signal %v, shutting down\n", sessionID, sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	ring := NewRing()
	cfg := ServeConfig{
		SessionID:   sessionID,
		Listener:    ln,
		Token:       token,
		PTY:         pty,
		Ring:        ring,
		LazySurface: opts.LazySurface,
	}

	if err := Serve(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "pty-host [%s]: serve: %v\n", sessionID, err)
		return 1
	}
	return 0
}

// newHostToken returns a fresh 256-bit hex token.
func newHostToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// listenHost binds the host endpoint: a private Unix socket where supported,
// loopback TCP only on Windows. The returned endpoint is "unix:<path>" or the
// decimal TCP port.
func listenHost() (net.Listener, string, error) {
	if hostsock.Supported {
		dir, err := hostsock.DefaultDir()
		if err != nil {
			return nil, "", err
		}
		return hostsock.Listen(dir)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, "", errors.New("listener is not TCP")
	}
	return ln, strconv.Itoa(tcpAddr.Port), nil
}

package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// exitingSpawn is an agent that exits before answering anything.
func exitingSpawn(Launch, string) (*process, error) {
	stdinR, stdinW := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, stdinR) }()
	return &process{stdin: stdinW, stdout: strings.NewReader(""), stop: func() error { return stdinW.Close() }}, nil
}

func TestStartRetriesAnAgentThatExitsDuringItsFirstHandshake(t *testing.T) {
	for _, retry := range []bool{true, false} {
		agent := &fakeAgent{}
		spawns := 0
		driver := New(Config{
			Harness:          domain.HarnessClaudeCode,
			Capabilities:     ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
			Probe:            func(context.Context) error { return nil },
			Launch:           func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			RetryStartupExit: retry,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(func(launch Launch, dir string) (*process, error) {
			spawns++
			if spawns == 1 {
				return exitingSpawn(launch, dir)
			}
			return fakeSpawn(agent)(launch, dir)
		})

		conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
		if retry {
			if err != nil || spawns != 2 {
				t.Fatalf("with retry: err = %v after %d starts, want a working second start", err, spawns)
			}
			_ = conv.Close()
		} else if err == nil || spawns != 1 {
			t.Fatalf("without retry: err = %v after %d starts, want the first exit reported", err, spawns)
		}
	}
}

func useFastSessionNewRetry(t *testing.T) {
	t.Helper()
	timeout, delays := sessionNewTimeout, sessionNewRetryDelays
	sessionNewTimeout, sessionNewRetryDelays = 100*time.Millisecond, []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { sessionNewTimeout, sessionNewRetryDelays = timeout, delays })
}

// hangingSessionNewDriver starts a fresh agent per process; the first
// hangFirst of them never answer session/new.
func hangingSessionNewDriver(t *testing.T, hangFirst int) (*Driver, *int) {
	t.Helper()
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	spawns := 0
	driver := New(fakeConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(func(launch Launch, dir string) (*process, error) {
		spawns++
		agent := &fakeAgent{}
		if spawns <= hangFirst {
			agent.newSessionHang = hang
		}
		return fakeSpawn(agent)(launch, dir)
	})
	return driver, &spawns
}

func TestStartRetriesASessionNewThatTimesOut(t *testing.T) {
	useFastSessionNewRetry(t)
	driver, spawns := hangingSessionNewDriver(t, 1)

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start = %v after %d starts, want the second start to open the session", err, *spawns)
	}
	defer func() { _ = conv.Close() }()
	if *spawns != 2 {
		t.Fatalf("starts = %d, want 2 (one timed out, one fresh process)", *spawns)
	}
}

func TestStartGivesUpAfterRepeatedSessionNewTimeouts(t *testing.T) {
	useFastSessionNewRetry(t)
	driver, spawns := hangingSessionNewDriver(t, 100)

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if !errors.Is(err, errSessionNewTimeout) {
		t.Fatalf("Start error = %v, want errSessionNewTimeout", err)
	}
	if want := len(sessionNewRetryDelays) + 1; *spawns != want {
		t.Fatalf("starts = %d, want %d", *spawns, want)
	}
}

func TestStartDoesNotRetryWhenTheCallerGivesUp(t *testing.T) {
	useFastSessionNewRetry(t)
	sessionNewTimeout = time.Minute
	driver, spawns := hangingSessionNewDriver(t, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := driver.Start(ctx, ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err == nil || errors.Is(err, errSessionNewTimeout) {
		t.Fatalf("Start error = %v, want the caller's deadline, not a session/new timeout", err)
	}
	if *spawns != 1 {
		t.Fatalf("starts = %d, want 1: an ended caller context is not retried", *spawns)
	}
}

//go:build windows

package codex

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/service/shellterm"
)

func TestPrepareLoginTerminalForCommitClosesExitedWindowsHost(t *testing.T) {
	terminal := &fakeCodexLoginTerminal{
		childExited: true,
		result:      shellterm.ShellTerminal{HandleID: "shellterm-login"},
	}
	manager := &codexAccountManager{terminal: terminal}
	if err := manager.prepareLoginTerminalForCommit(context.Background(), terminal.result.HandleID); err != nil {
		t.Fatal(err)
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if len(terminal.closed) != 1 || terminal.closed[0] != terminal.result.HandleID {
		t.Fatalf("closed terminals = %#v", terminal.closed)
	}
}

func TestPrepareLoginTerminalForCommitKeepsRunningWindowsHost(t *testing.T) {
	terminal := &fakeCodexLoginTerminal{result: shellterm.ShellTerminal{HandleID: "shellterm-login"}}
	manager := &codexAccountManager{terminal: terminal}
	if err := manager.prepareLoginTerminalForCommit(context.Background(), terminal.result.HandleID); err == nil {
		t.Fatal("running login command was closed for commit")
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if len(terminal.closed) != 0 {
		t.Fatalf("closed terminals = %#v", terminal.closed)
	}
}

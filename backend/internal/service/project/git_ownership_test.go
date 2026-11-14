package project_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// ownershipGit simulates git rejecting every repo for dubious ownership.
type ownershipGit struct{ ports.Git }

const dubiousStderr = "fatal: detected dubious ownership in repository at 'C:/repo'\n" +
	"To add an exception for this directory, call:\n\tgit config --global --add safe.directory C:/repo"

func (ownershipGit) TopLevel(context.Context, string) (string, error) {
	return "", &ports.GitError{Args: []string{"rev-parse", "--show-toplevel"}, Detail: dubiousStderr, Err: errors.New("exit status 128")}
}

func TestManager_AddNamesDubiousOwnership(t *testing.T) {
	orig := ports.DefaultGit()
	ports.SetDefaultGit(ownershipGit{Git: orig})
	t.Cleanup(func() { ports.SetDefaultGit(orig) })

	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var logs bytes.Buffer
	m := project.NewWithDeps(project.Deps{Store: store, Logger: slog.New(slog.NewTextHandler(&logs, nil))})

	dir := t.TempDir()
	_, err = m.Add(context.Background(), project.AddInput{Path: dir})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "NOT_A_GIT_REPO" {
		t.Fatalf("err = %v, want NOT_A_GIT_REPO", err)
	}
	if !strings.Contains(e.Message, "dubious ownership") {
		t.Errorf("message %q does not name ownership", e.Message)
	}
	if fix, _ := e.Details["suggestedFix"].(string); !strings.Contains(fix, "safe.directory") {
		t.Errorf("suggestedFix %q lacks safe.directory hint", fix)
	}
	if !strings.Contains(logs.String(), "dubious ownership") {
		t.Errorf("git stderr not logged: %s", logs.String())
	}
}

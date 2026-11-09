package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

func TestBackgroundTaskUsesItsOwnTempDirNotTheWorkers(t *testing.T) {
	launcher := &suggestionRecordingLauncher{recordingLauncher: &recordingLauncher{}}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{WorkspacePath: "/foreground", Permissions: ports.PermissionModeAuto}}
	st.sessions[rec.ID] = rec
	if _, err := m.RunPromptSuggestionTask(t.Context(), rec.ID, "turn", "suggestion only", "Suggest"); err != nil {
		t.Fatal(err)
	}
	cfg := launcher.background[0]
	tmp := cfg.Env["TMPDIR"]
	if tmp == "" || !strings.HasPrefix(tmp, cfg.WorkspacePath+string(filepath.Separator)) {
		t.Fatalf("TMPDIR = %q, want a dir inside the task workspace %q", tmp, cfg.WorkspacePath)
	}
	if got := cfg.Env[sessionscratch.TempPrefixEnv]; got != sessionscratch.TempPrefix(tmp) {
		t.Fatalf("TMPPREFIX = %q, want zsh temp files in %q (B521)", got, tmp)
	}
	if cfg.Env["TMP"] != tmp || cfg.Env["TEMP"] != tmp {
		t.Fatalf("TMP/TEMP = %q/%q, want %q", cfg.Env["TMP"], cfg.Env["TEMP"], tmp)
	}
	if _, err := os.Stat(sessionscratch.TempDir(m.dataDir, rec.ID)); !os.IsNotExist(err) {
		t.Fatalf("the worker's temp dir was created for a background task: %v", err)
	}
}

func TestBackgroundTaskTempEnvKeepsTheProjectsTempDir(t *testing.T) {
	workspace := t.TempDir()
	env := map[string]string{}
	if err := applyBackgroundTaskTempEnv(env, map[string]string{"TMPDIR": "/project/tmp"}, workspace, false); err != nil {
		t.Fatal(err)
	}
	if env["TMPDIR"] != "/project/tmp" || env["TMP"] != "/project/tmp" {
		t.Fatalf("env = %v, want the project's temp dir", env)
	}
	if env[sessionscratch.TempPrefixEnv] != sessionscratch.TempPrefix("/project/tmp") {
		t.Fatalf("TMPPREFIX = %q, want it under the project's temp dir", env[sessionscratch.TempPrefixEnv])
	}
	if _, err := os.Stat(filepath.Join(workspace, "tmp")); !os.IsNotExist(err) {
		t.Fatal("workspace temp dir created although the project names one")
	}
	env = map[string]string{}
	if err := applyBackgroundTaskTempEnv(env, map[string]string{"tmpdir": "/win/tmp"}, workspace, true); err != nil || env["TMPDIR"] != "/win/tmp" {
		t.Fatalf("case-insensitive project TMPDIR: env=%v err=%v", env, err)
	}
}

// B439: a Claude signed in through a project-level CLAUDE_CONFIG_DIR must be
// signed in for the task-naming call too, not only for the worker's session.
func TestBackgroundTaskCarriesTheProjectsEnv(t *testing.T) {
	launcher := &suggestionRecordingLauncher{recordingLauncher: &recordingLauncher{}}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	project := st.projects[string(chatTestProject)]
	project.Config.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/project/claude", "PROJECT_TOKEN": "secret"}
	st.projects[string(chatTestProject)] = project
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{WorkspacePath: "/foreground", Permissions: ports.PermissionModeAuto}}
	st.sessions[rec.ID] = rec
	if _, err := m.RunPromptSuggestionTask(t.Context(), rec.ID, "turn", "suggestion only", "Suggest"); err != nil {
		t.Fatal(err)
	}
	if got := launcher.background[0].Env["CLAUDE_CONFIG_DIR"]; got != "/project/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the project's folder", got)
	}
	if _, leaked := launcher.background[0].Env["PROJECT_TOKEN"]; leaked {
		t.Fatal("background task inherited a project secret")
	}
}

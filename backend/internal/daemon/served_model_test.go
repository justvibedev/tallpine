package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeServedAccounts struct{ account domain.AgentAccount }

func (f fakeServedAccounts) SessionAccount(context.Context, domain.SessionID, domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	return f.account, true, nil
}

type fakeServedProjects struct{ config domain.ProjectConfig }

func (f fakeServedProjects) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{Config: f.config}, true, nil
}

const servedSID = "8320947f-5806-4579-943f-b23a943bac1f"

func writeServedTranscript(t *testing.T, cfg string, models ...string) {
	t.Helper()
	dir := filepath.Join(cfg, "projects", "-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, m := range models {
		body += `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","message":{"model":"` + m + `"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, servedSID+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func servedSession(mode domain.SessionMode) domain.Session {
	return domain.Session{SessionRecord: domain.SessionRecord{
		FirstSignalAt: time.Now().Add(-time.Hour),
		Harness:       domain.HarnessClaudeCode, Mode: mode, ProjectID: "p",
		Metadata: domain.SessionMetadata{AgentSessionID: servedSID},
	}}
}

// The transcript lives only under the session's account folder; the daemon's own
// CLAUDE_CONFIG_DIR points elsewhere.
func TestServedModelFindsTranscriptUnderAccountFolder(t *testing.T) {
	account, other := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	writeServedTranscript(t, account, "claude-haiku-5-5", "claude-fable-5-2")
	r := servedModelReader{accounts: fakeServedAccounts{domain.AgentAccount{Home: account}}}
	if got := r.ServedModel(context.Background(), servedSession(domain.SessionModeTUI)); got != "claude-fable-5-2" {
		t.Fatalf("model = %q", got)
	}
}

func TestServedModelFindsTranscriptUnderProjectConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	writeServedTranscript(t, dir, "claude-fable-5-2")
	r := servedModelReader{projects: fakeServedProjects{domain.ProjectConfig{Env: map[string]string{"CLAUDE_CONFIG_DIR": dir}}}}
	if got := r.ServedModel(context.Background(), servedSession(domain.SessionModeTUI)); got != "claude-fable-5-2" {
		t.Fatalf("model = %q", got)
	}
}

// Chat persists the model on every ChatUI selection, newer than the transcript.
func TestServedModelKeepsPersistedModelInChatAndForOtherHarnesses(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeServedTranscript(t, cfg, "claude-fable-5-2")
	if got := (servedModelReader{}).ServedModel(context.Background(), servedSession(domain.SessionModeChat)); got != "" {
		t.Fatalf("chat model = %q, want empty", got)
	}
	sess := servedSession(domain.SessionModeTUI)
	sess.Harness = domain.HarnessCodex
	if got := (servedModelReader{}).ServedModel(context.Background(), sess); got != "" {
		t.Fatalf("codex model = %q", got)
	}
}

// A device-account session launches with the daemon's inherited folder blanked,
// so its transcript is under ~/.claude even though the daemon names another dir.
func TestServedModelDeviceAccountUsesDefaultDirWhenDaemonHasConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	writeServedTranscript(t, filepath.Join(home, ".claude"), "claude-fable-5-2")
	r := servedModelReader{accounts: fakeServedAccounts{domain.AgentAccount{}}}
	if got := r.ServedModel(context.Background(), servedSession(domain.SessionModeTUI)); got != "claude-fable-5-2" {
		t.Fatalf("model = %q", got)
	}
}

// After a relaunch the agent runs the persisted model; a transcript entry from
// before this launch (or no signal yet) must not be reported.
func TestServedModelIgnoresTranscriptEntriesFromBeforeTheCurrentLaunch(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeServedTranscript(t, cfg, "claude-fable-5-2")
	sess := servedSession(domain.SessionModeTUI)
	sess.FirstSignalAt = time.Now().Add(time.Hour)
	if got := (servedModelReader{}).ServedModel(context.Background(), sess); got != "" {
		t.Fatalf("pre-relaunch model = %q, want empty", got)
	}
	sess.FirstSignalAt = time.Time{}
	if got := (servedModelReader{}).ServedModel(context.Background(), sess); got != "" {
		t.Fatalf("no-signal model = %q, want empty", got)
	}
}

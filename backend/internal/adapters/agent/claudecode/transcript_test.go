package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const testSession = "0199c1a2-3b4c-4d5e-8f90-a1b2c3d4e5f6"

func TestZeroByteTranscriptProbeEqualsExists(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, testSession+".jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{}
	exists, err := p.NativeConversationExists(context.Background(), ports.SessionRef{}, testSession, map[string]string{"CLAUDE_CONFIG_DIR": cfg})
	if err != nil || exists {
		t.Fatalf("exists = %v, %v", exists, err)
	}
	got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: testSession, ConfigDir: cfg})
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("probe = %v, %v; want unavailable like Exists", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, testSession+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: testSession, ConfigDir: cfg}); got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe non-empty = %v", got)
	}
}

func TestMissingHomeIsErrorNotRelativeClaudeDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := claudeCredentialsPath(); got != "" {
		t.Fatalf("credentials path = %q, want empty", got)
	}
	if _, _, err := claudeLaunchConfigPaths(nil); err == nil {
		t.Fatal("want error for missing HOME")
	}
	if _, err := (&Plugin{}).NativeSessionConfigDir(context.Background(), nil); err == nil {
		t.Fatal("NativeSessionConfigDir: want error")
	}
}

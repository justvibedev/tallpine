package kiro

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestContinuationCapabilitiesProviderAssigned(t *testing.T) {
	if got := (&Plugin{}).ContinuationCapabilities().FreshNativeSessionID; got != ports.FreshNativeSessionIDProviderAssigned {
		t.Fatalf("mode = %q", got)
	}
}

func TestNativeSessionConfigDirUsesLaunchHome(t *testing.T) {
	got, err := (&Plugin{}).NativeSessionConfigDir(context.Background(), map[string]string{"HOME": "/h"})
	if err != nil || got != filepath.Join("/h", ".kiro") {
		t.Fatalf("dir = %q, %v", got, err)
	}
}

func TestProbeNativeSession(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "sessions", "06b5d9f7c853d6e8", "sess_abc-123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{}
	cases := []struct {
		name, id, cfg string
		want          ports.NativeSessionAvailability
		wantErr       bool
	}{
		{"prefixed", "sess_abc-123", cfg, ports.NativeSessionAvailabilityAvailable, false},
		{"bare", "abc-123", cfg, ports.NativeSessionAvailabilityAvailable, false},
		{"missing", "sess_zzz", cfg, ports.NativeSessionAvailabilityUnknown, false},
		{"no config dir", "sess_abc-123", "", ports.NativeSessionAvailabilityUnknown, false},
		{"bad id", "../x", cfg, ports.NativeSessionAvailabilityUnknown, true},
	}
	for _, c := range cases {
		got, err := p.ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: c.id, ConfigDir: c.cfg})
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: got (%q,%v)", c.name, got, err)
		}
	}
}

// The full pane captured from kiro-cli 3.0.0 (`chat --v3 --tui`) with the
// approval bar open, including the surrounding transcript.
func TestParseTerminalApprovalLive300Capture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "approval_bar_3.0.0.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := (&Plugin{}).ParseTerminalApproval(string(data))
	if !ok || len(got.Options) != 4 || got.Command != "shell → echo hi" || !got.Options[0].Selected {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

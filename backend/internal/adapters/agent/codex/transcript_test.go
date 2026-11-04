package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

const testThread = "0199c1a2-3b4c-7d5e-8f90-a1b2c3d4e5f6"

func writeRolloutFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// accountHome mirrors agentaccount: the account home's sessions is a symlink
// to the device home's sessions.
func accountHome(t *testing.T) (account, device string) {
	t.Helper()
	root := t.TempDir()
	device = filepath.Join(root, "device")
	account = filepath.Join(root, "account")
	if err := os.MkdirAll(filepath.Join(device, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(device, "sessions"), filepath.Join(account, "sessions")); err != nil {
		t.Fatal(err)
	}
	return account, device
}

func TestRolloutLookupThroughSymlinkedSessionsDir(t *testing.T) {
	account, device := accountHome(t)
	writeRolloutFile(t, filepath.Join(device, "sessions", "2026", "11", "02"), "rollout-2026-11-02T00-00-00-"+testThread+".jsonl", "{}\n")
	p := &Plugin{}
	ref := ports.NativeSessionRef{NativeSessionID: testThread, ConfigDir: account}
	if got, err := p.ProbeNativeSession(context.Background(), ref); err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %v, %v", got, err)
	}
	path, ok, err := p.LocateTranscript(context.Background(), ref)
	if err != nil || !ok {
		t.Fatalf("locate = %v, %v", ok, err)
	}
	if !strings.HasPrefix(path, filepath.Join(account, "sessions")+string(filepath.Separator)) {
		t.Fatalf("locate returned %q, want a path under the caller's %q", path, filepath.Join(account, "sessions"))
	}
	if ok, err := p.NativeConversationExists(context.Background(), ports.SessionRef{}, testThread, map[string]string{"CODEX_HOME": account}); err != nil || !ok {
		t.Fatalf("exists = %v, %v", ok, err)
	}
}

func TestRolloutLookupFindsZstd(t *testing.T) {
	home := t.TempDir()
	writeRolloutFile(t, filepath.Join(home, "sessions", "d"), "rollout-x-"+testThread+".jsonl.zst", "zst")
	p := &Plugin{}
	ref := ports.NativeSessionRef{NativeSessionID: testThread, ConfigDir: home}
	if got, _ := p.ProbeNativeSession(context.Background(), ref); got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %v", got)
	}
	if ok, _ := p.NativeConversationExists(context.Background(), ports.SessionRef{}, testThread, map[string]string{"CODEX_HOME": home}); !ok {
		t.Fatal("exists missed .jsonl.zst")
	}
}

func TestRolloutLookupUpperCaseUUID(t *testing.T) {
	home := t.TempDir()
	writeRolloutFile(t, filepath.Join(home, "sessions"), "rollout-x-"+testThread+".jsonl", "{}")
	upper := strings.ToUpper(testThread)
	p := &Plugin{}
	ref := ports.NativeSessionRef{NativeSessionID: upper, ConfigDir: home}
	if got, _ := p.ProbeNativeSession(context.Background(), ref); got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe upper = %v", got)
	}
	if _, ok, _ := p.LocateTranscript(context.Background(), ref); !ok {
		t.Fatal("locate missed upper-case id")
	}
}

func TestCodexHomeExplicitEmptyUsesEnvHome(t *testing.T) {
	daemonHome := t.TempDir()
	envHome := t.TempDir()
	t.Setenv("CODEX_HOME", daemonHome)
	writeRolloutFile(t, filepath.Join(envHome, ".codex", "sessions"), "rollout-x-"+testThread+".jsonl", "{}")
	p := &Plugin{}
	env := map[string]string{"CODEX_HOME": "", "HOME": envHome}
	ok, err := p.NativeConversationExists(context.Background(), ports.SessionRef{}, testThread, env)
	if err != nil || !ok {
		t.Fatalf("exists = %v, %v (explicit-empty CODEX_HOME must fall to env HOME, not daemon CODEX_HOME)", ok, err)
	}
}

func TestProbeLaunchFlagsAllPresent(t *testing.T) {
	probes := DoctorLaunchProbes()
	flagProbe := strings.Join(probes[0], "\x00")
	override := strings.Join(probes[1], "\x00")
	flags := agentruntime.CodexLaunchFlags()
	for i := 0; i < len(flags); {
		if flags[i] == "-c" {
			want := "-c\x00" + flags[i+1]
			i += 2
			if !strings.Contains(override, want) {
				t.Errorf("override probe missing %q: %q", want, probes[1])
			}
			if strings.Contains(flagProbe, want) {
				t.Errorf("flag probe must not carry -c override %q", want)
			}
			continue
		}
		bare := flags[i]
		i++
		if !strings.Contains(flagProbe, bare) {
			t.Errorf("flag probe missing %q: %q", bare, probes[0])
		}
		if strings.Contains(override, bare) {
			t.Errorf("override probe must not carry bare flag %q (kept out of probe 2): %q", bare, probes[1])
		}
	}
}

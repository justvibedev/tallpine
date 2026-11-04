package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripStaleWorkspaceHooks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proj", "sess", ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"vo hooks codex stop"},{"type":"command","command":"impeccable live"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	orch := filepath.Join(root, "proj", "orchestrator", "x-orchestrator", ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(orch), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orch, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := StripStaleWorkspaceHooks(context.Background(), root); n != 2 {
		t.Fatalf("changed = %d, want 2", n)
	}
	if od, _ := os.ReadFile(orch); strings.Contains(string(od), "hooks codex") {
		t.Fatalf("orchestrator entries remain: %s", od)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hooks codex") || !strings.Contains(string(data), "impeccable live") {
		t.Fatalf("unexpected result: %s", data)
	}
	if n := StripStaleWorkspaceHooks(context.Background(), root); n != 0 {
		t.Fatalf("second run changed = %d, want 0", n)
	}
}

func TestStripStaleWorkspaceHooksCoversNewEvents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "proj", "sess", ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"tallpine hooks codex pre-tool-use"}]}],` +
		`"SessionEnd":[{"hooks":[{"type":"command","command":"tallpine hooks codex session-end"}]}],` +
		`"PreCompact":[{"hooks":[{"type":"command","command":"tallpine hooks codex pre-compact"},{"type":"command","command":"keep-me"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := StripStaleWorkspaceHooks(context.Background(), root); n != 1 {
		t.Fatalf("changed = %d, want 1", n)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hooks codex") || !strings.Contains(string(data), "keep-me") {
		t.Fatalf("unexpected result: %s", data)
	}
}

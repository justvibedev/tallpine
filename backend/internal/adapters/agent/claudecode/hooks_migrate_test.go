package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const staleSettings = `{"permissions":{"allow":["Bash"]},"hooks":{
"PreToolUse":[{"hooks":[{"type":"command","command":"vo hooks claude-code pre-tool-use"},{"type":"command","command":"impeccable live"}]}],
"Stop":[{"hooks":[{"type":"command","command":"tallpine hooks claude-code stop"}]}]}}`

func TestStripStaleWorkspaceHooks(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "proj", "sess")
	path := filepath.Join(ws, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(staleSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	orch := filepath.Join(root, "proj", "orchestrator", "x-orchestrator", ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(orch), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orch, []byte(staleSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := StripStaleWorkspaceHooks(context.Background(), root); n != 2 {
		t.Fatalf("changed = %d, want 2", n)
	}
	if od, _ := os.ReadFile(orch); strings.Contains(string(od), "hooks claude-code") {
		t.Fatalf("orchestrator entries remain: %s", od)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "hooks claude-code") {
		t.Fatalf("Tallpine entries remain: %s", got)
	}
	if !strings.Contains(got, "impeccable live") || !strings.Contains(got, `"permissions"`) {
		t.Fatalf("foreign content lost: %s", got)
	}
	if n := StripStaleWorkspaceHooks(context.Background(), root); n != 0 {
		t.Fatalf("second run changed = %d, want 0 (idempotent)", n)
	}
	after, _ := os.ReadFile(path)
	if string(after) != got {
		t.Fatal("idempotent run rewrote the file")
	}
}

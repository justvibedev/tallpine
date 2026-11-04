package activitydispatch

import (
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestKiroStopHookReadsReplyFromSessionLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".kiro", "sessions", "abc123", "sess_x")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	log := `{"payload":{"type":"user","content":"old"}}
{"payload":{"type":"assistant","operationType":"Say","content":"stale"}}
{"payload":{"type":"user","content":"Reply only: OK"}}
{"payload":{"type":"assistant","operationType":"Say","content":"OK"}}
`
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	got := hookConversationFacts(domain.HarnessKiro, "stop", []byte(`{"session_id":"sess_x","hook_event_name":"Stop","cwd":"/w"}`))
	if got.FinalAssistantMessage != "OK" {
		t.Fatalf("FinalAssistantMessage = %q, want OK", got.FinalAssistantMessage)
	}
	if other := hookConversationFacts(domain.HarnessKiro, "stop", []byte(`{"session_id":"../x"}`)); other.FinalAssistantMessage != "" {
		t.Fatalf("unsafe id read %q", other.FinalAssistantMessage)
	}
}

package kiro

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Lines are the shapes kiro-cli 3.0.0 wrote for a real failed turn (bogus
// --model: no Stop or SessionEnd hook fired) and an Esc-cancelled one.
const (
	kiroUser      = `{"id":"u","timestamp":"2026-11-05T10:00:00.000Z","payload":{"type":"user","content":"say hi"}}` + "\n"
	kiroTurnStart = `{"id":"s","timestamp":"2026-11-05T10:00:00.100Z","payload":{"type":"turn_start","turnId":"t"}}` + "\n"
	kiroUsageFail = `{"id":"t-usage","timestamp":"2026-11-05T10:00:01.000Z","payload":{"type":"usage_summary","status":"failed"}}` + "\n"
	kiroEndError  = `{"id":"t-turn-end","timestamp":"2026-11-05T10:00:01.200Z","payload":{"type":"turn_end","stopReason":"error","turnId":"t"}}` + "\n"
	kiroEndCancel = `{"id":"t-turn-end","timestamp":"2026-11-05T10:00:02.000Z","payload":{"type":"turn_end","stopReason":"cancelled","turnId":"t"}}` + "\n"
	kiroEndOK     = `{"id":"t-turn-end","timestamp":"2026-11-05T10:00:03.000Z","payload":{"type":"turn_end","stopReason":"end_turn","turnId":"t"}}` + "\n"
)

func lastFailure(t *testing.T, records ...string) (failed, interrupted bool, message string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sess_x", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var body string
	for _, r := range records {
		body += r
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, ok, err := New().LastTurnFailure(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return ok, f.Interrupted, f.Message
}

func TestLastTurnFailure(t *testing.T) {
	if failed, interrupted, msg := lastFailure(t, kiroUser, kiroTurnStart, kiroUsageFail, kiroEndError); !failed || interrupted || msg == "" {
		t.Fatalf("error turn: failed=%v interrupted=%v msg=%q", failed, interrupted, msg)
	}
	if failed, interrupted, _ := lastFailure(t, kiroUser, kiroTurnStart, kiroEndCancel); !failed || !interrupted {
		t.Fatalf("cancelled turn: failed=%v interrupted=%v", failed, interrupted)
	}
	if failed, _, _ := lastFailure(t, kiroUser, kiroTurnStart, kiroEndOK); failed {
		t.Fatal("successful turn reported as failed")
	}
	// A newer prompt after the failed end means a later turn is running.
	if failed, _, _ := lastFailure(t, kiroUser, kiroTurnStart, kiroEndError, kiroUser); failed {
		t.Fatal("old failure replayed after a newer prompt")
	}
	if failed, _, _ := lastFailure(t, kiroUser, kiroEndError, kiroTurnStart); failed {
		t.Fatal("old failure replayed after a newer turn_start")
	}
}

func TestLastTurnFailureIgnoresForeignPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.jsonl")
	if err := os.WriteFile(path, []byte(kiroEndError), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := New().LastTurnFailure(context.Background(), path); ok || err != nil {
		t.Fatalf("foreign file: ok=%v err=%v", ok, err)
	}
	missing := filepath.Join(t.TempDir(), "messages.jsonl")
	if _, ok, err := New().LastTurnFailure(context.Background(), missing); ok || err != nil {
		t.Fatalf("missing log: ok=%v err=%v", ok, err)
	}
}

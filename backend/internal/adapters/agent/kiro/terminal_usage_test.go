package kiro

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTerminalModelUsageSumsUsageSummaries(t *testing.T) {
	p := &Plugin{sessionsRoot: "testdata/sessions"}
	for _, id := range []string{"sess_fixture-1", "fixture-1"} {
		got, ok, err := p.TerminalModelUsage(context.Background(), id)
		if err != nil || !ok || len(got) != 1 {
			t.Fatalf("%s: %+v ok=%v err=%v", id, got, ok, err)
		}
		if got[0].Model != "qwen3-coder-next" || got[0].Turns != 2 || got[0].Credits < 0.7992 || got[0].Credits > 0.7993 {
			t.Fatalf("%s: unexpected usage %+v", id, got[0])
		}
		// The last contextUsage record wins, not the first.
		if got[0].ContextPercent == nil || *got[0].ContextPercent != 4.25 {
			t.Fatalf("%s: context percent %v, want 4.25", id, got[0].ContextPercent)
		}
	}
}

func TestTerminalModelUsageUnknownSession(t *testing.T) {
	p := &Plugin{sessionsRoot: "testdata/sessions"}
	if _, ok, err := p.TerminalModelUsage(context.Background(), "nope"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, ok, _ := p.TerminalModelUsage(context.Background(), ""); ok {
		t.Fatal("empty id matched")
	}
}

func writeTranscript(t *testing.T, root, hash, id string, mod time.Time, credit string) {
	t.Helper()
	dir := filepath.Join(root, hash, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"payload":{"type":"usage_summary","promptTurnSummaries":[{"unit":"credit","usage":` + credit + `}]}}` + "\n"
	file := filepath.Join(dir, "messages.jsonl")
	if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalModelUsageRejectsGlobIDs(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "h1", "sess_other", time.Now(), "1")
	p := &Plugin{sessionsRoot: root}
	for _, id := range []string{"*", "sess_*", "?", "[a-z]*", "../h1/sess_other", "h1/sess_other"} {
		if _, ok, err := p.TerminalModelUsage(context.Background(), id); ok || err != nil {
			t.Fatalf("id %q matched: ok=%v err=%v", id, ok, err)
		}
	}
}

func TestTerminalModelUsagePrefersNewestWorkspaceHash(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeTranscript(t, root, "a_old", "sess_dup", now.Add(-time.Hour), "1")
	writeTranscript(t, root, "z_new", "sess_dup", now, "2")
	writeTranscript(t, root, "m_old", "sess_dup", now.Add(-2*time.Hour), "4")
	p := &Plugin{sessionsRoot: root}
	got, ok, err := p.TerminalModelUsage(context.Background(), "dup")
	if err != nil || !ok || len(got) != 1 || got[0].Credits != 2 {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}

func TestTerminalModelUsageNoContextRecordLeavesPercentNil(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "h1", "sess_plain", time.Now(), "1")
	got, ok, err := (&Plugin{sessionsRoot: root}).TerminalModelUsage(context.Background(), "plain")
	if err != nil || !ok || len(got) != 1 || got[0].ContextPercent != nil {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}

package localcialerts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type sent struct {
	to  domain.SessionID
	msg string
}

type fixture struct {
	t      *testing.T
	dir    string
	sent   []sent
	fail   error
	reader *Reader
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir()}
	f.reader = &Reader{
		Dir: f.dir,
		Target: func(_ context.Context, id domain.SessionID) (domain.SessionID, bool) {
			return "live-" + id, true
		},
		Send: func(_ context.Context, to domain.SessionID, msg string) error {
			if f.fail != nil {
				return f.fail
			}
			f.sent = append(f.sent, sent{to, msg})
			return nil
		},
	}
	f.write("notify-target", " orch-9 \n")
	return f
}

func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) appendAlert(body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, "results", "alerts.tsv")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	h, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	if _, err := h.WriteString(body); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) tick() {
	f.t.Helper()
	if err := f.reader.Tick(context.Background()); err != nil {
		f.t.Fatalf("Tick: %v", err)
	}
}

func (f *fixture) cursor() (string, bool) {
	raw, err := os.ReadFile(filepath.Join(f.dir, "results", "alerts.cursor"))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// B680: the build-done alert the launchd loop records reaches the
// notify-target orchestrator once, with a header marking it machine-written.
func TestTickDeliversBuildAlertToNotifyTargetOnce(t *testing.T) {
	f := newFixture(t)
	f.appendAlert("2026-11-12T08:09:05Z\t[local-ci] build 4801751600 OK: /x/Tallpine.app. Log: /x/build.log\n")
	f.tick()
	if len(f.sent) != 1 {
		t.Fatalf("sent %d messages, want 1: %+v", len(f.sent), f.sent)
	}
	if f.sent[0].to != "live-orch-9" {
		t.Fatalf("sent to %q, want the resolved notify-target", f.sent[0].to)
	}
	want := Header + "\n2026-11-12T08:09:05Z [local-ci] build 4801751600 OK: /x/Tallpine.app. Log: /x/build.log"
	if f.sent[0].msg != want {
		t.Fatalf("message:\n%s\nwant:\n%s", f.sent[0].msg, want)
	}
	f.tick()
	if len(f.sent) != 1 {
		t.Fatalf("second tick resent: %+v", f.sent)
	}
	f.appendAlert("2026-11-12T09:00:00Z\t[local-ci] build 5 FAILED (rc 1). Log: /x\n")
	f.tick()
	if len(f.sent) != 2 || !strings.HasSuffix(f.sent[1].msg, "\n2026-11-12T09:00:00Z [local-ci] build 5 FAILED (rc 1). Log: /x") || strings.Contains(f.sent[1].msg, "OK:") {
		t.Fatalf("only the new alert should follow: %+v", f.sent)
	}
}

func TestTickWaitsForAPartialLine(t *testing.T) {
	f := newFixture(t)
	f.appendAlert("t1\tfirst\nt2\tsec")
	f.tick()
	if len(f.sent) != 1 || strings.Contains(f.sent[0].msg, "sec") {
		t.Fatalf("partial line delivered: %+v", f.sent)
	}
	f.appendAlert("ond\n")
	f.tick()
	if len(f.sent) != 2 || !strings.HasSuffix(f.sent[1].msg, "\nt2 second") {
		t.Fatalf("completed line: %+v", f.sent)
	}
}

// Without a target nothing is delivered and the cursor is not stamped, so
// tp_record_alert keeps showing alerts on the desktop.
func TestTickWithoutTargetLeavesCursorUnstamped(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.dir, "notify-target")); err != nil {
		t.Fatal(err)
	}
	f.appendAlert("t\tm\n")
	f.tick()
	if _, ok := f.cursor(); ok || len(f.sent) != 0 {
		t.Fatalf("no target: sent %+v, cursor stamped %v", f.sent, ok)
	}
	f.write("notify-target", "orch-9")
	f.reader.Target = func(context.Context, domain.SessionID) (domain.SessionID, bool) { return "", false }
	f.tick()
	if _, ok := f.cursor(); ok || len(f.sent) != 0 {
		t.Fatalf("dead target: sent %+v, cursor stamped %v", f.sent, ok)
	}
}

func TestTickKeepsAlertWhenSendFails(t *testing.T) {
	f := newFixture(t)
	f.appendAlert("t\tm\n")
	f.fail = errors.New("busy")
	if err := f.reader.Tick(context.Background()); err == nil {
		t.Fatal("Tick hid the send failure")
	}
	if _, ok := f.cursor(); ok {
		t.Fatal("cursor advanced past an undelivered alert")
	}
	f.fail = nil
	f.tick()
	if len(f.sent) != 1 {
		t.Fatalf("alert not redelivered: %+v", f.sent)
	}
}

// The cursor is the one scripts/local-ci/alerts uses: a plain byte offset, and
// a file shorter than it is read from the start.
func TestTickSharesTheScriptCursorAndRestartsOnTruncation(t *testing.T) {
	f := newFixture(t)
	f.appendAlert("t1\told\n")
	f.write("results/alerts.cursor", "7")
	f.tick()
	if len(f.sent) != 0 {
		t.Fatalf("an alert the script already passed on was resent: %+v", f.sent)
	}
	if c, _ := f.cursor(); c != "7" {
		t.Fatalf("cursor %q, want 7", c)
	}
	f.write("results/alerts.tsv", "t\tn\n")
	f.write("results/alerts.cursor", "999")
	f.tick()
	if len(f.sent) != 1 || !strings.HasSuffix(f.sent[0].msg, "\nt n") {
		t.Fatalf("truncated file not reread: %+v", f.sent)
	}
	if c, _ := f.cursor(); c != "4" {
		t.Fatalf("cursor %q, want 4", c)
	}
}

func TestTickSplitsALongBacklog(t *testing.T) {
	f := newFixture(t)
	line := "t\t" + strings.Repeat("x", 1000) + "\n"
	for range 20 {
		f.appendAlert(line)
	}
	f.tick()
	f.tick()
	f.tick()
	total := 0
	for _, s := range f.sent {
		if len(s.msg) > maxMessageBytes+len(Header)+1 {
			t.Fatalf("message of %d bytes", len(s.msg))
		}
		total += strings.Count(s.msg, "\nt ")
	}
	if total != 20 || len(f.sent) < 2 {
		t.Fatalf("delivered %d lines in %d messages", total, len(f.sent))
	}
}

func TestTickWithoutLocalCIDirDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.reader.Dir = filepath.Join(f.dir, "missing")
	f.tick()
	if _, err := os.Stat(f.reader.Dir); !errors.Is(err, os.ErrNotExist) || len(f.sent) != 0 {
		t.Fatalf("missing dir touched: %v %+v", err, f.sent)
	}
}

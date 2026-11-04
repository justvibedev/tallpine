package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

type fakeLive struct{ recs []domain.SessionRecord }

func (f fakeLive) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.recs, nil
}

func TestRotateIdleSessionKey(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenPath := filepath.Join(t.TempDir(), daemontoken.FileName)
	_, key, err := daemontoken.RotateOnStart(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	cur := sessiontoken.New(key)

	if got := rotateIdleSessionKey(context.Background(), tokenPath, fakeLive{recs: make([]domain.SessionRecord, 1)}, cur, log); got != cur {
		t.Fatal("a live session must keep the current authority")
	}
	if b, _ := os.ReadFile(daemontoken.SessionKeyPath(tokenPath)); strings.TrimSpace(string(b)) != key {
		t.Fatal("session key file changed while a session was live")
	}

	got := rotateIdleSessionKey(context.Background(), tokenPath, fakeLive{}, cur, log)
	if got == cur {
		t.Fatal("no live sessions: the key must rotate")
	}
	b, _ := os.ReadFile(daemontoken.SessionKeyPath(tokenPath))
	if strings.TrimSpace(string(b)) == key || len(b) == 0 {
		t.Fatalf("session key file not rotated: %q", b)
	}
	tok := cur.IssueSession("s1", "v")
	if got.VerifySession(tok, "s1", "v") {
		t.Fatal("a token minted under the old key must not verify after rotation")
	}
}

type failingLive struct{}

func (failingLive) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, os.ErrClosed
}

func TestRotateIdleSessionKeyKeepsKeyWhenListingFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenPath := filepath.Join(t.TempDir(), daemontoken.FileName)
	_, key, err := daemontoken.RotateOnStart(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	cur := sessiontoken.New(key)
	if got := rotateIdleSessionKey(context.Background(), tokenPath, failingLive{}, cur, log); got != cur {
		t.Fatal("a listing error must keep the current authority")
	}
	if b, _ := os.ReadFile(daemontoken.SessionKeyPath(tokenPath)); strings.TrimSpace(string(b)) != key {
		t.Fatal("session key file changed after a listing error")
	}
}

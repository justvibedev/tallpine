package scm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestGuardRepos_LogsPersistentFailureOncePerError(t *testing.T) {
	provider := &fakeProvider{guardErr: errors.New("404 not found")}
	var buf bytes.Buffer
	o := newTestObserver(&fakeStore{}, provider, nil, time.Unix(0, 0))
	o.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	repos := []sessionRepo{{repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}}}
	for i := 0; i < 5; i++ {
		o.guardRepos(context.Background(), repos)
	}
	if n := strings.Count(buf.String(), "guard failed"); n != 1 {
		t.Fatalf("error logged %d times, want 1:\n%s", n, buf.String())
	}
	provider.guardErr = errors.New("500 boom")
	o.guardRepos(context.Background(), repos)
	if n := strings.Count(buf.String(), "guard failed"); n != 2 {
		t.Fatalf("changed error logged %d times total, want 2", n)
	}
}

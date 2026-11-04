package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Every session response goes through the one builder, so a paused fake and a
// waiting fake show up in both fields for a single and a listed session.
func TestViewAppliesPausedAndWaitingTogether(t *testing.T) {
	until := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	c := &SessionsController{UsageLimits: pauseLister{"w-1": until}, BackgroundTasks: taskLister{}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	one := c.view(context.Background(), r, domain.Session{ID: "w-1"})
	many := c.views(context.Background(), r, []domain.Session{{ID: "w-1"}})[0]
	for name, v := range map[string]SessionView{"view": one, "views": many} {
		if v.PausedUntil == nil || !v.PausedUntil.Equal(until) {
			t.Errorf("%s pausedUntil = %v", name, v.PausedUntil)
		}
		if len(v.WaitingOn) == 0 || len(v.BackgroundTasks) != 1 {
			t.Errorf("%s waitingOn = %v tasks = %v", name, v.WaitingOn, v.BackgroundTasks)
		}
	}
}

var sessionViewLiteral = regexp.MustCompile(`(^|[^\]\w.])SessionView\{|\bvar \w+ SessionView\b|\bbaseSessionView\(`)

// Handlers must not build a SessionView or call baseSessionView: only the
// builder file may, so no response can skip the runtime facts.
func TestOnlyBuilderConstructsSessionView(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "sessions_views.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := sessionViewLiteral.FindIndex(b); loc != nil {
			t.Errorf("%s builds a SessionView outside the builder: %q", f, b[loc[0]:loc[1]])
		}
	}
}

package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type pauseLister map[domain.SessionID]time.Time

func (p pauseLister) UsageLimitPausedUntil(_ context.Context, id domain.SessionID) (time.Time, bool) {
	until, ok := p[id]
	return until, ok
}

// A live usage-limited session lists when it may run again; others and
// terminated sessions list nothing.
func TestSessionViewsListPausedUntil(t *testing.T) {
	until := time.Date(2026, 11, 1, 20, 20, 0, 0, time.FixedZone("x", 3600))
	c := &SessionsController{UsageLimits: pauseLister{"w-1": until, "w-3": until}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	views := c.views(context.Background(), r, []domain.Session{
		{ID: "w-1"}, {ID: "w-2"}, {ID: "w-3", IsTerminated: true},
	})
	if views[0].PausedUntil == nil || !views[0].PausedUntil.Equal(until) || views[0].PausedUntil.Location() != time.UTC {
		t.Fatalf("w-1 pausedUntil = %v, want %v in UTC", views[0].PausedUntil, until)
	}
	if views[1].PausedUntil != nil || views[2].PausedUntil != nil {
		t.Fatalf("unpaused/terminated views = %v / %v, want none", views[1].PausedUntil, views[2].PausedUntil)
	}
	if got := (&SessionsController{}).view(context.Background(), r, domain.Session{ID: "w-1"}); got.PausedUntil != nil {
		t.Fatal("a controller without a reader must leave views untouched")
	}
}

type reauthLister struct {
	pauseLister
	reauth map[domain.SessionID]bool
}

func (r reauthLister) ReauthRequired(_ context.Context, id domain.SessionID) bool {
	return r.reauth[id]
}

// B520: a session whose provider refused its login says so on the view, so it
// does not read as a silent waiting_input.
func TestSessionViewsListReauthRequired(t *testing.T) {
	c := &SessionsController{UsageLimits: reauthLister{reauth: map[domain.SessionID]bool{"w-1": true}}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	views := c.views(context.Background(), r, []domain.Session{{ID: "w-1"}, {ID: "w-2"}})
	if !views[0].ReauthRequired || views[1].ReauthRequired {
		t.Fatalf("reauthRequired = %v / %v, want true / false", views[0].ReauthRequired, views[1].ReauthRequired)
	}
	if got := (&SessionsController{UsageLimits: pauseLister{}}).view(context.Background(), r, domain.Session{ID: "w-1"}); got.ReauthRequired {
		t.Fatal("a usage source without reauth facts must report none")
	}
}

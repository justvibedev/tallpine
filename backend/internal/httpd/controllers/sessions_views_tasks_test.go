package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type taskLister struct{}

func (taskLister) Waiting(domain.SessionID) []string   { return []string{"tail -f"} }
func (taskLister) ExpectStop(domain.SessionID, string) {}
func (taskLister) CancelStop(domain.SessionID, string) {}
func (taskLister) Running(id domain.SessionID) []bgtasks.Info {
	if id != "w-1" {
		return nil
	}
	return []bgtasks.Info{{ID: "bg-7", Name: "tail -f"}}
}

// A live session lists the ids a client needs to stop its tasks; one with none
// lists none, and a terminated session is left alone.
func TestSessionViewsListBackgroundTasksWithIDs(t *testing.T) {
	c := &SessionsController{BackgroundTasks: taskLister{}}
	views := c.views(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), []domain.Session{
		{ID: "w-1"}, {ID: "w-2"}, {ID: "w-1", IsTerminated: true},
	})
	if got := views[0].BackgroundTasks; len(got) != 1 || got[0].ID != "bg-7" || got[0].Name != "tail -f" {
		t.Fatalf("live view tasks = %+v", got)
	}
	if views[1].BackgroundTasks != nil || views[2].BackgroundTasks != nil {
		t.Fatalf("others = %+v / %+v, want none", views[1].BackgroundTasks, views[2].BackgroundTasks)
	}
}

package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

type runningAgentsFake struct {
	*fakeSessionService
	running []domain.SessionID
	stop    sessionsvc.StopAllAgentsOutcome
	stops   int
}

func (f *runningAgentsFake) RunningAgents(context.Context) ([]domain.SessionID, error) {
	return f.running, nil
}

func (f *runningAgentsFake) StopAllAgents(context.Context) (sessionsvc.StopAllAgentsOutcome, error) {
	f.stops++
	return f.stop, nil
}

// B753: the desktop reads how many agents keep running before it quits, and
// its quit dialog and tray stop them all.
func TestSessionsAPI_RunningAgentsAndStopAll(t *testing.T) {
	svc := &runningAgentsFake{fakeSessionService: newFakeSessionService(), running: []domain.SessionID{"tp-1", "tp-2"}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/running-agents", "")
	if status != http.StatusOK {
		t.Fatalf("running-agents status = %d body = %s", status, body)
	}
	var running controllers.RunningAgentsResponse
	mustJSON(t, body, &running)
	if len(running.SessionIDs) != 2 {
		t.Fatalf("running = %#v", running)
	}

	svc.stop = sessionsvc.StopAllAgentsOutcome{Stopped: []domain.SessionID{"tp-1"}, Failures: []string{"tp-2: busy"}}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/running-agents/stop", "")
	if status != http.StatusOK || svc.stops != 1 {
		t.Fatalf("stop status = %d stops = %d body = %s", status, svc.stops, body)
	}
	var stopped controllers.StopAllAgentsResponse
	mustJSON(t, body, &stopped)
	if stopped.OK || len(stopped.Stopped) != 1 || len(stopped.Failures) != 1 {
		t.Fatalf("stop = %#v, want a partial stop reported as not ok", stopped)
	}

	// None running answers an empty list, not null, so the desktop can count it.
	svc.running = nil
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/sessions/running-agents", "")
	if string(body) != `{"sessionIds":[]}`+"\n" && string(body) != `{"sessionIds":[]}` {
		t.Fatalf("empty running body = %q", body)
	}
}

package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func taskStopServer(t *testing.T, status int, body string, posted *string) Deps {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*posted = r.Method + " " + r.URL.Path + " " + string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return Deps{ProcessAlive: func(int) bool { return true }}
}

func TestSessionTaskStopPostsTheTaskID(t *testing.T) {
	var posted string
	deps := taskStopServer(t, http.StatusOK, `{"stopped":true}`, &posted)
	out, _, err := executeCLI(t, deps, "session", "task", "stop", "w-1", "bg-7")
	if err != nil || !strings.Contains(out, "stopped task bg-7") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if posted != `POST /api/v1/sessions/w-1/background-tasks/stop {"taskId":"bg-7"}` {
		t.Fatalf("posted %q", posted)
	}
}

func TestSessionTaskStopReportsAnUnknownTask(t *testing.T) {
	var posted string
	deps := taskStopServer(t, http.StatusNotFound,
		`{"error":"not_found","code":"BACKGROUND_TASK_NOT_FOUND","message":"no running background task nope in this session"}`, &posted)
	_, _, err := executeCLI(t, deps, "session", "task", "stop", "w-1", "nope")
	if err == nil || !strings.Contains(err.Error(), "no running background task nope") {
		t.Fatalf("err = %v", err)
	}
}

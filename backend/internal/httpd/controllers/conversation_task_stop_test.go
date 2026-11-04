package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

type taskStopStub struct {
	*fakeConversationService
	stopped bool
	err     error
	seen    []string
}

func (s *taskStopStub) StopAsyncTask(_ context.Context, id domain.SessionID, task string) (bool, error) {
	s.seen = append(s.seen, string(id)+"/"+task)
	return s.stopped, s.err
}

type runningTasks map[domain.SessionID][]bgtasks.Info

func (r runningTasks) Running(id domain.SessionID) []bgtasks.Info { return r[id] }
func (r runningTasks) ExpectStop(domain.SessionID, string)        {}
func (r runningTasks) CancelStop(domain.SessionID, string)        {}

func postStop(t *testing.T, svc controllers.ConversationService, tasks controllers.BackgroundTaskLister, body string) (int, map[string]any) {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.ConversationsController{Svc: svc, BackgroundTasks: tasks}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/w-1/background-tasks/stop", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestStopBackgroundTaskStopsAKnownTask(t *testing.T) {
	svc := &taskStopStub{fakeConversationService: &fakeConversationService{}, stopped: true}
	status, body := postStop(t, svc, runningTasks{"w-1": {{ID: "bg-7", Name: "tail -f"}}}, `{"taskId":"bg-7"}`)
	if status != http.StatusOK || body["stopped"] != true {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if len(svc.seen) != 1 || svc.seen[0] != "w-1/bg-7" {
		t.Fatalf("service saw %v, want w-1/bg-7", svc.seen)
	}
}

// An id the daemon does not know is a clear 404 and never reaches the agent.
func TestStopBackgroundTaskUnknownIDIsNotFound(t *testing.T) {
	svc := &taskStopStub{fakeConversationService: &fakeConversationService{}, stopped: true}
	status, body := postStop(t, svc, runningTasks{"w-1": {{ID: "bg-7"}}}, `{"taskId":"nope"}`)
	if status != http.StatusNotFound || body["code"] != "BACKGROUND_TASK_NOT_FOUND" {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if len(svc.seen) != 0 {
		t.Fatalf("an unknown task reached the agent: %v", svc.seen)
	}
}

func TestStopBackgroundTaskRefusalsAreConflictsAndEmptyIDIsInvalid(t *testing.T) {
	tasks := runningTasks{"w-1": {{ID: "bg-7"}}}
	for name, tc := range map[string]struct {
		svc    *taskStopStub
		body   string
		status int
		code   string
	}{
		"declined":    {&taskStopStub{fakeConversationService: &fakeConversationService{}}, `{"taskId":"bg-7"}`, http.StatusConflict, "BACKGROUND_TASK_NOT_STOPPED"},
		"unsupported": {&taskStopStub{fakeConversationService: &fakeConversationService{}, err: chatsvc.ErrAsyncTaskStopUnsupported}, `{"taskId":"bg-7"}`, http.StatusConflict, "BACKGROUND_TASK_STOP_UNSUPPORTED"},
		"no agent":    {&taskStopStub{fakeConversationService: &fakeConversationService{}, err: chatsvc.ErrNoController}, `{"taskId":"bg-7"}`, http.StatusConflict, "CHAT_NO_CONTROLLER"},
		"empty id":    {&taskStopStub{fakeConversationService: &fakeConversationService{}}, `{"taskId":" "}`, http.StatusBadRequest, "BACKGROUND_TASK_ID_REQUIRED"},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := postStop(t, tc.svc, tasks, tc.body)
			if status != tc.status || body["code"] != tc.code {
				t.Fatalf("status=%d body=%v, want %d %s", status, body, tc.status, tc.code)
			}
		})
	}
}

type markingTasks struct {
	runningTasks
	log *[]string
}

func (m markingTasks) ExpectStop(_ domain.SessionID, id string) {
	*m.log = append(*m.log, "expect "+id)
}
func (m markingTasks) CancelStop(_ domain.SessionID, id string) {
	*m.log = append(*m.log, "cancel "+id)
}

// A deliberate stop is announced before the request (the harness can report the
// end first) and withdrawn when the stop did not happen.
func TestStopBackgroundTaskMarksTheStopAndWithdrawsItOnRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		stopped bool
		want    []string
	}{
		"stopped": {true, []string{"expect bg-7"}},
		"refused": {false, []string{"expect bg-7", "cancel bg-7"}},
	} {
		t.Run(name, func(t *testing.T) {
			var log []string
			tasks := markingTasks{runningTasks{"w-1": {{ID: "bg-7"}}}, &log}
			postStop(t, &taskStopStub{fakeConversationService: &fakeConversationService{}, stopped: tc.stopped}, tasks, `{"taskId":"bg-7"}`)
			if strings.Join(log, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("calls = %v, want %v", log, tc.want)
			}
		})
	}
}

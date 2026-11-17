package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// newScopedReviewTestServer is newReviewTestServer behind a stand-in for the
// agent-token gate: X-Test-Agent: <role>:<session> puts that agent in scope,
// as the real gate does from the token's session kind (httpd/agent_scope.go).
func newScopedReviewTestServer(t *testing.T, svc *fakeReviewService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reviews: svc}, httpd.ControlDeps{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Test-Agent"); h != "" {
			role, sess, _ := strings.Cut(h, ":")
			r = r.WithContext(requestscope.WithAgent(r.Context(), requestscope.Agent{Role: requestscope.AgentRole(role), SessionID: domain.SessionID(sess), ProjectID: "p1"}))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func postTrigger(t *testing.T, srv *httptest.Server, agent, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/mer-1/reviews/trigger", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if agent != "" {
		req.Header.Set("X-Test-Agent", agent)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// B743: the focus author comes from the caller's credentials. A body naming
// another author is ignored, so a worker can never pass as the owner.
func TestReviewsTriggerFocusAuthorFromCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, agent string
		want        domain.ReviewFocusAuthor
	}{
		{"owner", "", domain.ReviewFocusOwner},
		{"orchestrator", "orchestrator:p1-orch", domain.ReviewFocusOrchestrator},
		{"worker", "worker:mer-1", domain.ReviewFocusWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeReviewService{}
			srv := newScopedReviewTestServer(t, svc)
			status, body := postTrigger(t, srv, tc.agent, `{"focus":"check the migration","focusAuthor":"owner","source":"manual"}`)
			if status != http.StatusCreated {
				t.Fatalf("status = %d body=%s", status, body)
			}
			if got := svc.triggerRequest; got.Focus != "check the migration" || got.FocusAuthor != tc.want {
				t.Fatalf("forwarded focus = %q/%q, want %q", got.Focus, got.FocusAuthor, tc.want)
			}
		})
	}
}

// B743: focus over 2000 characters is a 400 and never reaches the service.
func TestReviewsTriggerRejectsOverlongFocus(t *testing.T) {
	svc := &fakeReviewService{}
	srv := newScopedReviewTestServer(t, svc)
	status, body := postTrigger(t, srv, "worker:mer-1", `{"focus":"`+strings.Repeat("é", domain.MaxReviewFocusChars+1)+`"}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "REVIEW_FOCUS_TOO_LONG") {
		t.Fatalf("status = %d body=%s, want 400 REVIEW_FOCUS_TOO_LONG", status, body)
	}
	if svc.triggerRequest.Focus != "" {
		t.Fatalf("overlong focus reached the service: %d chars", len(svc.triggerRequest.Focus))
	}
	status, body = postTrigger(t, srv, "worker:mer-1", `{"focus":"`+strings.Repeat("é", domain.MaxReviewFocusChars)+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("focus at the cap: status = %d body=%s", status, body)
	}
	// The cap counts the trimmed focus, as the CLI and the engine do.
	status, body = postTrigger(t, srv, "worker:mer-1", `{"focus":"   `+strings.Repeat("é", domain.MaxReviewFocusChars)+`\n"}`)
	if status != http.StatusCreated || strings.HasPrefix(svc.triggerRequest.Focus, " ") {
		t.Fatalf("padded focus at the cap: status = %d body=%s", status, body)
	}
}

package controllers_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

type fakeApprovalNotifier struct{ intents []ports.NotificationIntent }

func (f *fakeApprovalNotifier) Notify(_ context.Context, in ports.NotificationIntent) error {
	f.intents = append(f.intents, in)
	return nil
}

var testNotifier *fakeApprovalNotifier

// testNotifierOrNil avoids a typed-nil interface when a test has no notifier.
func testNotifierOrNil() controllers.ApprovalNotifier {
	if testNotifier == nil {
		return nil
	}
	return testNotifier
}

func approvalsRouter(svc *fakeConversationService, agent *requestscope.Agent) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if agent != nil {
				req = req.WithContext(requestscope.WithAgent(req.Context(), *agent))
			}
			next.ServeHTTP(w, req)
		})
	})
	(&controllers.ConversationsController{Svc: svc, Notifier: testNotifierOrNil()}).Register(r)
	return r
}

func pendingSnapshot() chatsvc.Snapshot {
	return chatsvc.Snapshot{Activities: []domain.ConversationActivity{{
		Kind: domain.ActivityKindApproval, Status: domain.ActivityStatusPending, RequestID: "req-1",
		Summary: "run tool",
		Detail:  []byte(`{"command":"go build /tmp/x","decisions":[{"id":"a","label":"Allow","kind":"allow_once"},{"id":"always","label":"Always","kind":"allow_always"},{"id":"r","label":"Reject","kind":"reject_once"}]}`),
	}}}
}

func postResolve(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/w-1/conversation/approvals/req-1/resolve", strings.NewReader(body)))
	return rec
}

var orchAgent = requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "o-1", ProjectID: "p"}

func TestOrchestratorResolveStampsAudit(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	agent := orchAgent
	rec := postResolve(approvalsRouter(svc, &agent), `{"decisionId":"a","note":"harmless tmp build"}`)
	if rec.Code != 204 {
		t.Fatalf("code %d %s", rec.Code, rec.Body)
	}
	if svc.approvalDecision.ResolvedBy != "orchestrator:o-1" || svc.approvalDecision.Note != "harmless tmp build" {
		t.Fatalf("audit not stamped: %+v", svc.approvalDecision)
	}
}

func TestHumanResolveCarriesNoActor(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	if rec := postResolve(approvalsRouter(svc, nil), `{"decisionId":"always","note":"x"}`); rec.Code != 204 {
		t.Fatalf("code %d", rec.Code)
	}
	if svc.approvalDecision.ResolvedBy != "" || svc.approvalDecision.Note != "" {
		t.Fatalf("human answer was stamped: %+v", svc.approvalDecision)
	}
}

func TestOrchestratorCannotGrantStandingAllowOrUnknown(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	agent := orchAgent
	h := approvalsRouter(svc, &agent)
	if rec := postResolve(h, `{"decisionId":"always"}`); rec.Code != 403 {
		t.Fatalf("allow_always = %d", rec.Code)
	}
	if rec := postResolve(h, `{"decisionId":"bogus"}`); rec.Code != 400 {
		t.Fatalf("unknown decision = %d", rec.Code)
	}
	if svc.approvalRequestID != "" {
		t.Fatal("a refused answer reached the provider")
	}
	svc.snapshot = chatsvc.Snapshot{}
	if rec := postResolve(h, `{"decisionId":"a"}`); rec.Code != 404 {
		t.Fatalf("not pending = %d", rec.Code)
	}
}

func TestListApprovalsShowsExactRequest(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	rec := httptest.NewRecorder()
	approvalsRouter(svc, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/w-1/conversation/approvals", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "go build /tmp/x") || !strings.Contains(rec.Body.String(), "req-1") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func withNotifier(t *testing.T) *fakeApprovalNotifier {
	t.Helper()
	testNotifier = &fakeApprovalNotifier{}
	t.Cleanup(func() { testNotifier = nil })
	return testNotifier
}

func TestOrchestratorResolveNotifiesTheHuman(t *testing.T) {
	n := withNotifier(t)
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	agent := orchAgent
	if rec := postResolve(approvalsRouter(svc, &agent), `{"decisionId":"a","note":"tmp build"}`); rec.Code != 204 {
		t.Fatalf("code %d", rec.Code)
	}
	if len(n.intents) != 1 {
		t.Fatalf("notifications = %d, want 1", len(n.intents))
	}
	in := n.intents[0]
	if in.Type != domain.NotificationOrchestratorApproval || in.SessionID != "w-1" || in.ProjectID != "p" ||
		!strings.Contains(in.Title, "orchestrator:o-1") || !strings.Contains(in.Body, "go build /tmp/x") || !strings.Contains(in.Body, "tmp build") {
		t.Fatalf("intent = %+v", in)
	}
}

func TestHumanAndRefusedResolvesDoNotNotify(t *testing.T) {
	n := withNotifier(t)
	svc := &fakeConversationService{snapshot: pendingSnapshot()}
	if rec := postResolve(approvalsRouter(svc, nil), `{"decisionId":"a"}`); rec.Code != 204 {
		t.Fatalf("human code %d", rec.Code)
	}
	agent := orchAgent
	if rec := postResolve(approvalsRouter(svc, &agent), `{"decisionId":"always"}`); rec.Code != 403 {
		t.Fatalf("refused code %d", rec.Code)
	}
	if len(n.intents) != 0 {
		t.Fatalf("unexpected notifications: %+v", n.intents)
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestAuditLoggedOnlyAfterSuccessfulResolveAndBounded(t *testing.T) {
	logs := captureLogs(t)
	snap := pendingSnapshot()
	secret := strings.Repeat("S", 5000)
	snap.Activities[0].Detail = []byte(`{"command":"` + secret + `","content":"top-secret-file","decisions":[{"id":"a","label":"Allow","kind":"allow_once"}]}`)
	svc := &fakeConversationService{snapshot: snap, resolveErr: errors.New("provider rejected")}
	agent := orchAgent
	postResolve(approvalsRouter(svc, &agent), `{"decisionId":"a"}`)
	if strings.Contains(logs.String(), "orchestrator answered") {
		t.Fatalf("audit line written for a failed resolve: %s", logs)
	}
	svc.resolveErr = nil
	if rec := postResolve(approvalsRouter(svc, &agent), `{"decisionId":"a"}`); rec.Code != 204 {
		t.Fatalf("code %d", rec.Code)
	}
	out := logs.String()
	if !strings.Contains(out, "orchestrator answered a worker prompt") || strings.Contains(out, "top-secret-file") || len(out) > 3000 {
		t.Fatalf("audit missing, unbounded or leaking (%d bytes): %s", len(out), out)
	}
}

type fakeTerminalApprovals struct {
	resolved []string
	err      error
}

func (f *fakeTerminalApprovals) PendingTerminalApprovals(context.Context, domain.SessionID) ([]ports.PendingTerminalApproval, bool, error) {
	return []ports.PendingTerminalApproval{{RequestID: "term-1", Approval: ports.TerminalApproval{
		Summary: "shell requires approval", Command: "shell → touch x",
		Options: []ports.TerminalApprovalOption{
			{ID: "allow_once", Label: "Allow once", Kind: ports.ChatDecisionAllowOnce},
			{ID: "allow_always", Label: "Create allow rule", Kind: ports.ChatDecisionAllowAlways, Index: 2},
			{ID: "reject_once", Label: "Deny once", Kind: ports.ChatDecisionRejectOnce, Index: 1},
		},
	}}}, true, nil
}

func (f *fakeTerminalApprovals) ResolveTerminalApproval(_ context.Context, _ domain.SessionID, requestID, decisionID string) error {
	f.resolved = append(f.resolved, requestID+"/"+decisionID)
	return f.err
}

func terminalRouter(svc *fakeConversationService, term controllers.TerminalApprovalService, agent *requestscope.Agent) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if agent != nil {
				req = req.WithContext(requestscope.WithAgent(req.Context(), *agent))
			}
			next.ServeHTTP(w, req)
		})
	})
	(&controllers.ConversationsController{Svc: svc, Terminal: term}).Register(r)
	return r
}

func TestTerminalModeApprovalsListAndResolve(t *testing.T) {
	svc := &fakeConversationService{snapshotErr: chatsvc.ErrNotChatMode, resolveErr: chatsvc.ErrNotChatMode}
	term := &fakeTerminalApprovals{}
	agent := orchAgent
	h := terminalRouter(svc, term, &agent)

	list := httptest.NewRecorder()
	h.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/sessions/w-1/conversation/approvals", nil))
	if list.Code != 200 || !strings.Contains(list.Body.String(), "term-1") || !strings.Contains(list.Body.String(), "touch x") {
		t.Fatalf("list %d %s", list.Code, list.Body)
	}
	req := func(id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/w-1/conversation/approvals/"+id+"/resolve", strings.NewReader(body)))
		return rec
	}
	if rec := req("term-1", `{"decisionId":"reject_once"}`); rec.Code != 204 {
		t.Fatalf("resolve %d %s", rec.Code, rec.Body)
	}
	if len(term.resolved) != 1 || term.resolved[0] != "term-1/reject_once" {
		t.Fatalf("resolved = %v", term.resolved)
	}
	if rec := req("term-1", `{"decisionId":"allow_always"}`); rec.Code != 403 {
		t.Fatalf("standing allow = %d", rec.Code)
	}
	term.err = ports.ErrTerminalApprovalNotPending
	if rec := req("term-1", `{"decisionId":"allow_once"}`); rec.Code != 404 {
		t.Fatalf("stale = %d", rec.Code)
	}
}

func TestTerminalModeWithoutServiceKeepsModeMismatch(t *testing.T) {
	svc := &fakeConversationService{snapshotErr: chatsvc.ErrNotChatMode}
	rec := httptest.NewRecorder()
	terminalRouter(svc, nil, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/w-1/conversation/approvals", nil))
	if rec.Code != 409 {
		t.Fatalf("code %d", rec.Code)
	}
}

func pendingInputSnapshot() chatsvc.Snapshot {
	return chatsvc.Snapshot{Activities: []domain.ConversationActivity{{
		Kind: domain.ActivityKindUserInput, Status: domain.ActivityStatusPending, RequestID: "in-1",
		Summary: "Switch to Opus 4.8?", Detail: []byte(`{"inputMode":"form"}`),
	}}}
}

func TestListApprovalsIncludesPendingInChatQuestions(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingInputSnapshot()}
	rec := httptest.NewRecorder()
	approvalsRouter(svc, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/w-1/conversation/approvals", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"inputs":[{"requestId":"in-1"`) || !strings.Contains(rec.Body.String(), "Switch to Opus 4.8?") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestOrchestratorAnswersOnlyAPendingInChatQuestion(t *testing.T) {
	svc := &fakeConversationService{snapshot: pendingInputSnapshot()}
	agent := orchAgent
	h := approvalsRouter(svc, &agent)
	post := func(id string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/w-1/conversation/inputs/"+id+"/resolve", strings.NewReader(`{"action":"decline"}`)))
		return rec.Code
	}
	if code := post("nope"); code != 404 || svc.inputRequestID != "" {
		t.Fatalf("unlisted request = %d, reached provider %q", code, svc.inputRequestID)
	}
	if code := post("in-1"); code != 204 || svc.inputRequestID != "in-1" || svc.inputResponse.Action != "decline" {
		t.Fatalf("pending = %d %q %#v", code, svc.inputRequestID, svc.inputResponse)
	}
}

func TestInChatQuestionAnswerNotifiesTheHumanOnlyForAnAgent(t *testing.T) {
	n := withNotifier(t)
	post := func(agent *requestscope.Agent) {
		svc := &fakeConversationService{snapshot: pendingInputSnapshot()}
		rec := httptest.NewRecorder()
		approvalsRouter(svc, agent).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/w-1/conversation/inputs/in-1/resolve", strings.NewReader(`{"action":"decline"}`)))
		if rec.Code != 204 {
			t.Fatalf("code = %d", rec.Code)
		}
	}
	post(nil)
	if len(n.intents) != 0 {
		t.Fatalf("human answer notified: %#v", n.intents)
	}
	agent := orchAgent
	post(&agent)
	if len(n.intents) != 1 || !strings.Contains(n.intents[0].Title, "declined a question") || !strings.Contains(n.intents[0].Body, "Switch to Opus 4.8?") {
		t.Fatalf("intents = %#v", n.intents)
	}
}

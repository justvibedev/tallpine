package ask

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCreateSystemFilesAMainChatAskWithoutASession(t *testing.T) {
	r := newRig()
	a, err := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Origin != domain.AskOriginContextHealth || a.Lane != "" || a.SessionID != "" || a.State != domain.AskOpen || !a.FiledByDaemon() {
		t.Fatalf("ask = %+v", a)
	}
	if r.anchor.calls != 0 {
		t.Fatalf("a daemon ask was anchored to a turn: %d", r.anchor.calls)
	}
	if len(r.note.opened) != 1 {
		t.Fatalf("owner notified %d times, want once", len(r.note.opened))
	}
	if _, err := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?")); code(t, err) != "ASK_ALREADY_OPEN" {
		t.Fatalf("a repeat while open: %v", err)
	}
	if _, err := r.svc.CreateSystem(context.Background(), "p1", "", in("Other?")); code(t, err) != "ASK_ORIGIN_INVALID" {
		t.Fatalf("an empty origin: %v", err)
	}
}

func TestDaemonAskAnswerGoesToItsHandlerNotTheRouter(t *testing.T) {
	r := newRig()
	var got []domain.OwnerAsk
	r.svc.d.AnswerHandlers = map[string]func(context.Context, domain.OwnerAsk) error{
		domain.AskOriginContextHealth: func(_ context.Context, a domain.OwnerAsk) error { got = append(got, a); return nil },
	}
	a, _ := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?"))
	_, failed, err := r.svc.Answer(context.Background(), "p1", Actor{Kind: domain.BacklogOriginHuman}, a.Ref, AnswerInput{Option: "a"})
	if err != nil || failed {
		t.Fatalf("answer failed=%v err=%v", failed, err)
	}
	if len(got) != 1 || got[0].Answer == nil || got[0].Answer.OptionID != "a" {
		t.Fatalf("handler saw %+v", got)
	}
	if len(r.router.sent) != 0 {
		t.Fatalf("the answer was sent to an orchestrator: %v", r.router.sent)
	}
}

func TestDaemonAskHandlerFailureIsReportedAndStillNotRouted(t *testing.T) {
	r := newRig()
	r.svc.d.AnswerHandlers = map[string]func(context.Context, domain.OwnerAsk) error{
		domain.AskOriginContextHealth: func(context.Context, domain.OwnerAsk) error { return errors.New("boom") },
	}
	a, _ := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?"))
	closed, failed, err := r.svc.Answer(context.Background(), "p1", Actor{Kind: domain.BacklogOriginHuman}, a.Ref, AnswerInput{Option: "a"})
	if err != nil || !failed || closed.State != domain.AskAnswered || len(r.router.sent) != 0 {
		t.Fatalf("closed=%v failed=%v err=%v sent=%v", closed.State, failed, err, r.router.sent)
	}
}

func TestWithdrawSystemClosesAnOpenDaemonAsk(t *testing.T) {
	r := newRig()
	a, _ := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?"))
	w, err := r.svc.WithdrawSystem(context.Background(), "p1", a.Ref, "back under budget")
	if err != nil || w.State != domain.AskWithdrawn || w.WithdrawReason != "back under budget" {
		t.Fatalf("withdrawn = %+v %v", w, err)
	}
	if _, err := r.svc.WithdrawSystem(context.Background(), "p1", a.Ref, "again"); code(t, err) != "ASK_NOT_OPEN" {
		t.Fatalf("second withdraw: %v", err)
	}
}

func TestOrchestratorCannotWithdrawADaemonAsk(t *testing.T) {
	r := newRig()
	a, _ := r.svc.CreateSystem(context.Background(), "p1", domain.AskOriginContextHealth, in("Too big?"))
	if _, err := r.svc.Withdraw(context.Background(), "p1", orch, a.Ref, "mine"); code(t, err) != "ASK_DAEMON_OWNED" {
		t.Fatalf("orchestrator withdraw: %v", err)
	}
	if w, err := r.svc.Withdraw(context.Background(), "p1", owner, a.Ref, "mine"); err != nil || w.State != domain.AskWithdrawn {
		t.Fatalf("owner withdraw = %+v %v", w, err)
	}
}

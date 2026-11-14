package ask

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestWithdraw(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	if _, err := r.svc.Withdraw(ctx, "p1", Actor{Kind: domain.BacklogOriginWorker}, a.Ref, "x"); code(t, err) != "ASK_WITHDRAW_FORBIDDEN" {
		t.Fatalf("err = %v", err)
	}
	got, err := r.svc.Withdraw(ctx, "p1", orch, a.Ref, "no longer needed")
	if err != nil || got.State != domain.AskWithdrawn || got.WithdrawReason != "no longer needed" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if _, err := r.svc.Withdraw(ctx, "p1", orch, a.Ref, "again"); code(t, err) != "ASK_NOT_OPEN" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Option: "a"}); code(t, err) != "ASK_NOT_OPEN" {
		t.Fatalf("answer after withdraw: err = %v", err)
	}
}

// An ask belongs to its lane: another chat's orchestrator cannot withdraw it,
// the asking lane and the owner can.
func TestWithdraw_OnlyOwnLaneOrOwner(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	laneAsk, _ := r.svc.Create(ctx, "p1", laneO, in("Lane question"))
	mainAsk, _ := r.svc.Create(ctx, "p1", orch, in("Main question"))
	if _, err := r.svc.Withdraw(ctx, "p1", orch, laneAsk.Ref, "x"); code(t, err) != "ASK_WRONG_LANE" {
		t.Fatalf("main withdrawing a lane ask: err = %v", err)
	}
	if _, err := r.svc.Withdraw(ctx, "p1", laneO, mainAsk.Ref, "x"); code(t, err) != "ASK_WRONG_LANE" {
		t.Fatalf("lane withdrawing a main ask: err = %v", err)
	}
	for _, ref := range []string{laneAsk.Ref, mainAsk.Ref} {
		if got, _, _ := r.st.GetOwnerAsk(ctx, "p1", ref); got.State != domain.AskOpen {
			t.Fatalf("%s = %s after a refused withdraw", ref, got.State)
		}
	}
	if _, err := r.svc.Withdraw(ctx, "p1", laneO, laneAsk.Ref, "mine"); err != nil {
		t.Fatalf("own lane: %v", err)
	}
	if _, err := r.svc.Withdraw(ctx, "p1", owner, mainAsk.Ref, "owner"); err != nil {
		t.Fatalf("owner: %v", err)
	}
}

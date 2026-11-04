package store_test

import (
	"context"
	"testing"
	"time"
)

func TestSessionPolicyDecisionRoundTripReplaceAndCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "pol")
	session, err := s.CreateSession(ctx, sampleRecord("pol"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.SessionPolicyDecision(ctx, session.ID); err != nil || ok {
		t.Fatalf("a session that launched before the policy has no record: ok=%v err=%v", ok, err)
	}
	t1 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if err := s.SetSessionPolicyDecision(ctx, session.ID, []byte(`{"v":2,"a":1}`), t1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionPolicyDecision(ctx, session.ID, []byte(`{"v":2,"a":2}`), t1.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, at, ok, err := s.SessionPolicyDecision(ctx, session.ID)
	if err != nil || !ok || string(got) != `{"v":2,"a":2}` || !at.Equal(t1.Add(time.Hour)) {
		t.Fatalf("the latest launch's record must replace the earlier one: %s %v %v %v", got, at, ok, err)
	}
}

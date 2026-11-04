package store_test

import (
	"context"
	"testing"
	"time"
)

func TestAgentPolicyStore_DefaultsToEmptyAndRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	got, err := s.AgentPolicy(ctx)
	if err != nil || string(got) != "{}" {
		t.Fatalf("fresh row = %q, %v; want {}", got, err)
	}
	if err := s.SetAgentPolicy(ctx, []byte(`{"maxPermission":"auto"}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = s.AgentPolicy(ctx)
	if string(got) != `{"maxPermission":"auto"}` {
		t.Fatalf("round trip = %q", got)
	}
}

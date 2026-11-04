package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TriggerWithOptions rejects malformed requests before it reads any state.
// These are the messages callers (CLI, HTTP) already show.
func TestTriggerWithOptionsRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	engine := newEngineForTest(nil, nil, nil, nil, nil)
	cases := []struct {
		name   string
		worker domain.SessionID
		opts   TriggerOptions
		want   string
	}{
		{"auto rerun", "w", TriggerOptions{Source: domain.ReviewTriggerAuto, Rerun: true}, "automatic review cannot request a rerun or same-commit rejection"},
		{"auto reject reviewed head", "w", TriggerOptions{Source: domain.ReviewTriggerAuto, RejectReviewedHead: true}, "automatic review cannot request a rerun or same-commit rejection"},
		{"empty worker", "", TriggerOptions{}, "worker session id is required"},
		{"unknown harness", "w", TriggerOptions{Harness: "nope"}, `unknown reviewer harness "nope"`},
		{"unknown source", "w", TriggerOptions{Source: "weird"}, `unknown review trigger source "weird"`},
		{"unknown mode", "w", TriggerOptions{InterfaceMode: "weird"}, `unknown reviewer interface mode "weird"`},
		{"auto with mode", "w", TriggerOptions{Source: domain.ReviewTriggerAuto, InterfaceMode: domain.ReviewerInterfaceChat}, "automatic review cannot override interface mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := engine.TriggerWithOptions(context.Background(), tc.worker, tc.opts)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrInvalid containing %q", err, tc.want)
			}
		})
	}
}

package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestDrainFailureCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"unverified quiescence", fmt.Errorf("wrap: %w", errDrainQuiescenceUnverified), "DRAIN_QUIESCENCE_UNVERIFIED"},
		{"pending decision", fmt.Errorf("wrap: %w", errDrainDecisionPending), "DRAIN_DECISION_PENDING"},
		{"other", errors.New("boom"), "SOURCE_QUIESCE_FAILED"},
	} {
		if got := drainFailureCode(tc.err); got != tc.want {
			t.Errorf("%s: code = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTargetStartFailureCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"load failed", fmt.Errorf("x: %w", ports.ErrChatHistoryLoadFailed), "TARGET_HISTORY_LOAD_FAILED"},
		{"unavailable", fmt.Errorf("x: %w", ports.ErrChatHistoryUnavailable), "TARGET_HISTORY_UNAVAILABLE"},
		{"unsettled", fmt.Errorf("x: %w", ports.ErrChatHistoryUnsettled), "TARGET_HISTORY_UNSETTLED"},
		{"generic", errors.New("boom"), "TARGET_RESUME_FAILED"},
	} {
		if got := targetStartFailureCode(tc.err); got != tc.want {
			t.Errorf("%s: code = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestShouldRetryUnsettledTarget(t *testing.T) {
	t.Parallel()
	chat := domain.SessionInterfaceTransition{TargetMode: domain.SessionModeChat}
	tui := domain.SessionInterfaceTransition{TargetMode: domain.SessionModeTUI}
	unsettled := fmt.Errorf("x: %w", ports.ErrChatHistoryUnsettled)
	for _, tc := range []struct {
		name string
		err  error
		tr   domain.SessionInterfaceTransition
		want bool
	}{
		{"unsettled into chat retries", unsettled, chat, true},
		{"unsettled into tui does not", unsettled, tui, false},
		{"no error does not", nil, chat, false},
		{"load refusal does not", errors.Join(unsettled, ports.ErrChatHistoryLoadFailed), chat, false},
		{"inconclusive recovery does not", errors.Join(unsettled, ports.ErrChatRecoveryInconclusive), chat, false},
	} {
		if got := shouldRetryUnsettledTarget(tc.err, tc.tr); got != tc.want {
			t.Errorf("%s: retry = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTransitionSagaAdmitRejectsChangedSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, st, _, _, _ := newTransitionManager(t, domain.SessionModeTUI)
	tr, _, err := st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID: "admit-1", SessionID: "session-1", SourceMode: domain.SessionModeChat, TargetMode: domain.SessionModeTUI,
		Phase: domain.SessionInterfaceTransitionRequested, NativeConversationID: "native-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	saga := &transitionSaga{m: m, transition: tr}
	defer saga.cleanup()
	code, err := saga.admit(ctx)
	if err == nil || code != "SESSION_CHANGED" {
		t.Fatalf("admit = (%q, %v), want SESSION_CHANGED for a session no longer in the source mode", code, err)
	}
	saga = &transitionSaga{m: m, transition: domain.SessionInterfaceTransition{ID: "admit-1", SessionID: "missing", SourceMode: domain.SessionModeTUI}}
	if code, err := saga.admit(ctx); err == nil || code != "SESSION_NOT_FOUND" {
		t.Fatalf("admit(missing) = (%q, %v), want SESSION_NOT_FOUND", code, err)
	}
}

func TestTransitionSagaCommitAccountSkipsModeMoves(t *testing.T) {
	t.Parallel()
	saga := &transitionSaga{transition: domain.SessionInterfaceTransition{SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat}}
	if code, err := saga.commitAccount(context.Background()); err != nil || code != "" {
		t.Fatalf("commitAccount on a mode move = (%q, %v), want no-op", code, err)
	}
}

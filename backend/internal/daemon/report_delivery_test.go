package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type reportDeliverySession struct {
	sentID      domain.SessionID
	sentMessage string
	sentKey     string
	interrupted domain.SessionID
	sendErr     error
}

func (s *reportDeliverySession) SendSemantic(_ context.Context, id domain.SessionID, message, key string) error {
	s.sentID, s.sentMessage, s.sentKey = id, message, key
	return s.sendErr
}

func (s *reportDeliverySession) InterruptTUI(_ context.Context, id domain.SessionID) error {
	s.interrupted = id
	return nil
}

type reportDeliveryStore struct{ rec domain.SessionRecord }

func (s reportDeliveryStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, true, nil
}

func TestReportSemanticDeliveryUsesAcceptedTUIBoundary(t *testing.T) {
	sessions := &reportDeliverySession{}
	delivery := reportSemanticDelivery{
		sessions: sessions,
		store: reportDeliveryStore{rec: domain.SessionRecord{
			ID: "tp-1", Mode: domain.SessionModeTUI,
		}},
	}
	if err := delivery.Submit(context.Background(), "tp-1", "report context", "report-batch:abc123"); err != nil {
		t.Fatal(err)
	}
	if sessions.sentID != "tp-1" || sessions.sentMessage != "report context" || sessions.sentKey != "report-batch:abc123" {
		t.Fatalf("semantic send = %+v", sessions)
	}
	if err := delivery.Interrupt(context.Background(), "tp-1"); err != nil {
		t.Fatal(err)
	}
	if sessions.interrupted != "tp-1" {
		t.Fatalf("interrupted = %q", sessions.interrupted)
	}
}

func TestReportSemanticDeliveryMarksAComposerDraftAsBusy(t *testing.T) {
	// The coordinator backs off a busy orchestrator instead of retrying it ahead
	// of every other project, so the draft refusal must reach it as busy.
	sessions := &reportDeliverySession{sendErr: fmt.Errorf("send tp-1: %w", sessionmanager.ErrComposerBusy)}
	delivery := reportSemanticDelivery{sessions: sessions}
	err := delivery.Submit(context.Background(), "tp-1", "report context", "report-batch:abc123")
	if !errors.Is(err, reportsvc.ErrOrchestratorBusy) || !errors.Is(err, sessionmanager.ErrComposerBusy) {
		t.Fatalf("Submit error = %v, want ErrOrchestratorBusy wrapping ErrComposerBusy", err)
	}

	sessions.sendErr = sessionmanager.ErrTerminated
	if err := delivery.Submit(context.Background(), "tp-1", "report context", "report-batch:abc123"); errors.Is(err, reportsvc.ErrOrchestratorBusy) {
		t.Fatalf("Submit error = %v, want other failures left as they are", err)
	}
}

// B545: a terminal orchestrator that is mid-turn, still starting, or otherwise
// unavailable for now takes the 30s busy retry. As a failure it would back off to
// minutes, and to an hourly retry after enough attempts.
func TestReportSemanticDeliveryTreatsTransientRefusalsAsBusy(t *testing.T) {
	for _, cause := range []error{
		sessionmanager.ErrSemanticAcceptancePending,
		sessionmanager.ErrStartupPending,
		sessionmanager.ErrOperationInProgress,
		sessionmanager.ErrAwaitingDecision,
	} {
		sessions := &reportDeliverySession{sendErr: fmt.Errorf("send tp-1: %w", cause)}
		delivery := reportSemanticDelivery{sessions: sessions}
		err := delivery.Submit(context.Background(), "tp-1", "report context", "report-batch:abc123")
		if !errors.Is(err, reportsvc.ErrOrchestratorBusy) || !errors.Is(err, cause) {
			t.Errorf("Submit error = %v, want ErrOrchestratorBusy wrapping %v", err, cause)
		}
	}

	sessions := &reportDeliverySession{sendErr: sessionmanager.ErrSemanticAcceptanceUnsupported}
	err := reportSemanticDelivery{sessions: sessions}.Submit(context.Background(), "tp-1", "report context", "report-batch:abc123")
	if errors.Is(err, reportsvc.ErrOrchestratorBusy) {
		t.Fatalf("Submit error = %v, want a real failure to stay a failure", err)
	}
}

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestAccountAdoptionStoreKeysItemsAndRunningBatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	batch := domain.AccountAdoption{
		ID: "b1", AccountID: "acct-b", Provider: domain.AgentAccountProviderClaude, Policy: domain.AccountAdoptionWait,
		RequestKey: "k1", CreatedAt: now,
		Items: []domain.AccountAdoptionItem{{SessionID: "s1", Position: 0}, {SessionID: "s2", Position: 1, IsOrchestrator: true}},
	}
	mustNoError(t, s.CreateAccountAdoption(ctx, batch))
	if err := s.CreateAccountAdoption(ctx, batch); !errors.Is(err, ports.ErrAccountAdoptionExists) {
		t.Fatalf("same key again = %v, want exists", err)
	}
	got, ok, err := s.GetAccountAdoptionByKey(ctx, "acct-b", "k1")
	mustNoError(t, err)
	if !ok || len(got.Items) != 2 || got.Items[0].State != domain.AccountItemQueued || !got.Items[1].IsOrchestrator {
		t.Fatalf("stored batch = %+v", got)
	}
	mustNoError(t, s.UpdateAccountAdoptionItem(ctx, "b1", domain.AccountAdoptionItem{
		SessionID: "s1", State: domain.AccountItemFailed, ErrorCode: "X", ErrorMessage: "why", UpdatedAt: now,
	}))
	if running, ok, _ := s.GetRunningAccountAdoption(ctx, domain.AgentAccountProviderClaude); !ok || running.ID != "b1" || running.Items[0].ErrorCode != "X" {
		t.Fatalf("running = %+v ok=%v", running, ok)
	}
	mustNoError(t, s.CompleteAccountAdoption(ctx, "b1"))
	if _, ok, _ := s.GetRunningAccountAdoption(ctx, domain.AgentAccountProviderClaude); ok {
		t.Fatal("completed batch still running")
	}
	if latest, ok, _ := s.GetLatestAccountAdoption(ctx, "acct-b"); !ok || latest.State != domain.AccountAdoptionCompleted {
		t.Fatalf("latest = %+v", latest)
	}
}

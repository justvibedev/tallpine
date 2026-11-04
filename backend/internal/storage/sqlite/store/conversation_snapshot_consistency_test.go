package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// A snapshot is several queries. A projection that commits between two of them
// must not tear it: every activity a snapshot shows on a turn needs that turn to
// be in the same snapshot, or the timeline cannot group it.
func TestConversationSnapshotIsNotTornByConcurrentCommits(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	const turns = 40

	torn := func(snapshot store.ConversationSnapshot) string {
		known := make(map[string]bool, len(snapshot.Turns))
		for _, turn := range snapshot.Turns {
			known[turn.ID] = true
		}
		for _, activity := range snapshot.Activities {
			if activity.TurnID != "" && !known[activity.TurnID] {
				return fmt.Sprintf("activity %s names turn %s, which the snapshot (%d turns) lacks",
					activity.ID, activity.TurnID, len(snapshot.Turns))
			}
		}
		return ""
	}

	done := make(chan struct{})
	var readers sync.WaitGroup
	failures := make(chan string, 2)
	for _, load := range []func() (store.ConversationSnapshot, error){
		func() (store.ConversationSnapshot, error) { return s.LoadConversationSnapshot(ctx, conversation) },
		func() (store.ConversationSnapshot, error) {
			return s.LoadConversationSnapshotPage(ctx, conversation, 0, 500)
		},
	} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				snapshot, err := load()
				if err != nil {
					failures <- err.Error()
					return
				}
				if reason := torn(snapshot); reason != "" {
					failures <- reason
					return
				}
			}
		}()
	}

	// Each turn and its first activity commit separately, the way a provider-started
	// turn is adopted and then its items are projected.
	for i := range turns {
		providerTurnID := fmt.Sprintf("provider-turn-%d", i)
		at := histClock.Add(time.Duration(i) * time.Second)
		if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", fmt.Sprintf("turn-%d", i), providerTurnID, at); err != nil {
			t.Fatalf("adopt %s: %v", providerTurnID, err)
		}
		if err := s.UpsertActivity(ctx, conversation, providerTurnID, domain.ConversationActivity{
			ID:             fmt.Sprintf("act-%d", i),
			Kind:           domain.ActivityKindCommand,
			Status:         domain.ActivityStatusCompleted,
			Summary:        "rg --files",
			ProviderItemID: fmt.Sprintf("exec-%d", i),
		}, at); err != nil {
			t.Fatalf("activity on %s: %v", providerTurnID, err)
		}
	}
	close(done)
	readers.Wait()
	close(failures)
	for reason := range failures {
		t.Error(reason)
	}

	snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != turns || len(snapshot.Activities) != turns {
		t.Fatalf("final snapshot has %d turns and %d activities, want %d of each",
			len(snapshot.Turns), len(snapshot.Activities), turns)
	}
}

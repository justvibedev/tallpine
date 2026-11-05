package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Large rows must not make one snapshot read unbounded: pages shrink to the byte
// budget, still return at least one row, and the cursor keeps advancing with no gap.
func TestConversationSnapshotPagesAcrossByteBoundedFetches(t *testing.T) {
	for _, size := range []int{3 << 20, 5 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			text := strings.Repeat("x", size)
			for i := 1; i <= 4; i++ {
				id := fmt.Sprintf("large-%d", i)
				created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman}, "turn-"+id, histClock.Add(time.Duration(i)*time.Second))
				if err != nil || !created {
					t.Fatalf("append: created=%v err=%v", created, err)
				}
			}
			before := int64(0)
			for want := 4; want >= 1; want-- {
				page, err := s.LoadConversationSnapshotPage(ctx, conversation, before, 200)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Messages) != 1 || page.Messages[0].ID != fmt.Sprintf("large-%d", want) || page.Messages[0].Text != text {
					t.Fatalf("page %d: got %d messages; expected complete large-%d", want, len(page.Messages), want)
				}
				if page.HasMoreBefore != (want > 1) {
					t.Fatalf("page %d hasMore=%v", want, page.HasMoreBefore)
				}
				if before != 0 && page.OldestSequence >= before {
					t.Fatal("cursor did not advance")
				}
				before = page.OldestSequence
			}
		})
	}
}

// Small activities interleaved with large messages: the activity read reaches
// below where the message read was cut, so the window must be floored or older
// activities would appear on a page without their neighbouring messages.
func TestConversationSnapshotByteBoundedPagesLeaveNoGaps(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	text := strings.Repeat("x", 3<<20)
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("large-%d", i)
		at := histClock.Add(time.Duration(i) * time.Second)
		if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman}, "turn-"+id, at); err != nil || !created {
			t.Fatalf("append: created=%v err=%v", created, err)
		}
		if err := s.UpsertActivity(ctx, conversation, "", domain.ConversationActivity{
			ID: fmt.Sprintf("act-%d", i), Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Summary: "go test",
		}, at); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]int{}
	before := int64(0)
	for pages := 0; pages < 20; pages++ {
		page, err := s.LoadConversationSnapshotPage(ctx, conversation, before, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page.Messages {
			seen[m.ID]++
			if m.Sequence < page.OldestSequence {
				t.Fatalf("message %s below page oldest %d", m.ID, page.OldestSequence)
			}
		}
		for _, a := range page.Activities {
			seen[a.ID]++
			if a.Sequence < page.OldestSequence {
				t.Fatalf("activity %s seq %d below page oldest %d", a.ID, a.Sequence, page.OldestSequence)
			}
		}
		if !page.HasMoreBefore {
			break
		}
		before = page.OldestSequence
	}
	for i := 1; i <= 4; i++ {
		for _, id := range []string{fmt.Sprintf("large-%d", i), fmt.Sprintf("act-%d", i)} {
			if seen[id] != 1 {
				t.Fatalf("%s returned %d times, want exactly once (seen=%v)", id, seen[id], seen)
			}
		}
	}
}

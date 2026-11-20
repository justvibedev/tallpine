package store_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// hostTurn records one persistent-host turn: its turn.started and two streamed
// deltas with acp-host journal identities, settled when settle is set.
func hostTurn(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, turn string, settle bool) {
	t.Helper()
	ctx := context.Background()
	if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "tallpine-"+turn, turn, histClock); err != nil {
		t.Fatal(err)
	}
	host := func(n int) string { return fmt.Sprintf("acp-host:launch-1:%s-%d", turn, n) }
	projectArchived(t, s, conversation, session, host(0), "turn.started",
		fmt.Sprintf(`{"kind":"turn.started","providerTurnId":%q}`, turn), histClock, nil)
	item := "msg-" + turn
	for i, chunk := range []string{"Hello", " host"} {
		projectArchived(t, s, conversation, session, host(i+1), "message.delta", deltaPayload(turn, item, chunk), histClock,
			func(txCtx context.Context) error {
				return s.AppendAssistantDelta(txCtx, conversation, item, turn, chunk, "m-"+turn, histClock)
			})
	}
	if settle {
		if err := s.SettleTurn(ctx, conversation, turn, domain.TurnStateCompleted, "", histClock); err != nil {
			t.Fatal(err)
		}
	}
}

func identityKeys(t *testing.T, s *sqlite.Store, conversation string) []string {
	t.Helper()
	keys, err := s.ProviderEventKeysForTest(context.Background(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// A persistent host replays a settled prompt when the controller died before
// acknowledging it. Its deltas stay archived, so the replay is refused, until a
// later turn proves the acknowledgement arrived; then they go without a key.
func TestPersistentHostDeltasWaitForALaterTurnAndLeaveNoKey(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	hostTurn(t, s, conversation, session, "t1", true)
	compactAll(t, s, conversation, 1000)
	if got := len(archiveMethods(t, s, conversation)["message.delta"]); got != 2 {
		t.Fatalf("message.delta rows = %d, want both held until a later turn", got)
	}
	if keys := identityKeys(t, s, conversation); len(keys) != 0 {
		t.Fatalf("identity keys = %v, want none for held host deltas", keys)
	}
	if projectArchived(t, s, conversation, session, "acp-host:launch-1:t1-1", "message.delta", deltaPayload("t1", "msg-t1", "Hello"), histClock,
		func(txCtx context.Context) error {
			return s.AppendAssistantDelta(txCtx, conversation, "msg-t1", "t1", "Hello", "m-dup", histClock)
		}) {
		t.Fatal("a replayed host delta was projected again")
	}

	hostTurn(t, s, conversation, session, "t2", false)
	compactAll(t, s, conversation, 1000)
	for _, payload := range archiveMethods(t, s, conversation)["message.delta"] {
		if !strings.Contains(payload, `"providerTurnId":"t2"`) {
			t.Fatalf("t1 delta survived a later turn: %s", payload)
		}
	}
	if keys := identityKeys(t, s, conversation); len(keys) != 0 {
		t.Fatalf("identity keys = %v, want none: the host can no longer replay t1", keys)
	}
	snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(snapshot.Messages); !reflect.DeepEqual(got, []string{"Hello host", "Hello host"}) {
		t.Fatalf("messages = %q", got)
	}
}

// The one-time sweep turns '{}' tombstones written before identity keys into
// keys and deletes them. A host identity before a later turn.started needs no
// key; every other identity keeps refusing its replay. Rows that are not
// tombstones, and rows newer than the sweep's end, are left alone.
func TestTombstoneSweepTurnsLegacyTombstonesIntoKeys(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	for _, row := range []struct{ id, method string }{
		{"acp-host:launch-1:x-1", "activity.started"},
		{"acp-host:launch-1:x-2", "turn.started"},
		{"acp-host:launch-1:x-3", "activity.completed"},
	} {
		projectArchived(t, s, conversation, session, row.id, row.method, `{"kind":"`+row.method+`","text":"payload"}`, histClock, nil)
	}
	for _, stmt := range []string{
		`UPDATE conversation_provider_events SET payload_json = '{}'
		 WHERE method = 'message.delta' OR provider_event_id IN ('acp-host:launch-1:x-1', 'acp-host:launch-1:x-3')`,
		`UPDATE provider_event_tombstone_sweep SET swept_through_id = 0,
		 through_id = (SELECT MAX(id) FROM conversation_provider_events)`,
	} {
		if err := s.ExecWriterForTest(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	// Newer than the sweep's end: a real event whose payload is '{}'.
	projectArchived(t, s, conversation, session, "late", "activity.text", `{}`, histClock, nil)
	before := archiveMethods(t, s, conversation)

	var deleted, keyed int64
	for i := 0; ; i++ {
		result, err := s.SweepProviderEventTombstones(ctx, 3, histClock)
		if err != nil {
			t.Fatal(err)
		}
		deleted, keyed = deleted+result.Deleted, keyed+result.Keyed
		if result.Done {
			break
		}
		if i > 100 {
			t.Fatal("sweep never finished")
		}
	}
	// message.delta: 3 rows, 2 with an identity; x-1 needs no key, x-3 does.
	if deleted != 2 || keyed != 3 {
		t.Fatalf("sweep deleted %d and kept %d keys, want 2 and 3", deleted, keyed)
	}
	if keys := identityKeys(t, s, conversation); !reflect.DeepEqual(keys, []string{"acp-host:launch-1:x-3", "t1:delta:0", "t1:delta:2"}) {
		t.Fatalf("identity keys = %v", keys)
	}
	after := archiveMethods(t, s, conversation)
	if len(after["message.delta"]) != 0 || len(after["activity.completed"]) != len(before["activity.completed"])-1 ||
		len(after["activity.started"]) != len(before["activity.started"])-1 || len(after["turn.started"]) != len(before["turn.started"]) ||
		len(after["activity.text"]) != 1 {
		t.Fatalf("archive after sweep = %v\nbefore %v", after, before)
	}
	if result, err := s.SweepProviderEventTombstones(ctx, 3, histClock); err != nil || !result.Done || result.Deleted+result.Keyed != 0 {
		t.Fatalf("sweep after done = %+v err=%v, want nothing to do", result, err)
	}
	if projectArchived(t, s, conversation, session, "t1:delta:0", "message.delta", deltaPayload("t1", "msg-t1", "Hello"), histClock,
		func(txCtx context.Context) error {
			return s.AppendAssistantDelta(txCtx, conversation, "msg-t1", "t1", "Hello", "m-dup", histClock)
		}) {
		t.Fatal("a swept identity was projected again")
	}
}

// Terminating a session destroys its persistent host, and a new host mints
// identities under a new launch token, so neither retention nor the sweep
// keeps a key for a terminated session's host identities. Other identities
// still keep one.
func TestTerminatedSessionHostIdentitiesLeaveNoKey(t *testing.T) {
	now := time.Now().UTC()
	t.Run("expiry", func(t *testing.T) {
		s, session, conversation := conversationFixture(t)
		hostTurn(t, s, conversation, session, "t1", true)
		projectArchived(t, s, conversation, session, "acp-history:t1", "activity.completed", `{"kind":"activity.completed"}`, histClock, nil)
		terminate(t, s, session, now.Add(-30*24*time.Hour))
		expireAll(t, s, now.Add(-2*24*time.Hour), 1000)
		if keys := identityKeys(t, s, conversation); !reflect.DeepEqual(keys, []string{"acp-history:t1"}) {
			t.Fatalf("identity keys = %v, want only acp-history:t1", keys)
		}
	})
	t.Run("sweep", func(t *testing.T) {
		s, session, conversation := conversationFixture(t)
		ctx := context.Background()
		hostTurn(t, s, conversation, session, "t1", true)
		for _, stmt := range []string{
			`UPDATE conversation_provider_events SET payload_json = '{}' WHERE method = 'message.delta'`,
			`UPDATE provider_event_tombstone_sweep SET swept_through_id = 0,
			 through_id = (SELECT MAX(id) FROM conversation_provider_events)`,
		} {
			if err := s.ExecWriterForTest(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		terminate(t, s, session, now)
		for {
			result, err := s.SweepProviderEventTombstones(ctx, 1000, histClock)
			if err != nil {
				t.Fatal(err)
			}
			if result.Done {
				break
			}
		}
		if got := len(archiveMethods(t, s, conversation)["message.delta"]); got != 0 {
			t.Fatalf("message.delta tombstones left = %d", got)
		}
		if keys := identityKeys(t, s, conversation); len(keys) != 0 {
			t.Fatalf("identity keys = %v, want none for a terminated session's host", keys)
		}
	})
}

// A held host delta waits for a later turn.started or for its session to end.
// The hold mark moves on either, so the janitor knows when to look again, and
// terminating the session releases the deltas without a key.
func TestTerminatingTheSessionReleasesHeldHostDeltas(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	hostTurn(t, s, conversation, session, "t1", true)
	mark := func() sqlitestore.ProviderEventHoldMark {
		t.Helper()
		m, err := s.ProviderEventHoldMark(ctx, conversation)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	held, err := s.CompactProviderEvents(ctx, conversation, 0, 0, 1000, histClock)
	if err != nil || !held.Done || !held.Holding {
		t.Fatalf("first window = %+v err=%v, want the host deltas held", held, err)
	}
	idle := mark()
	if again, err := s.CompactProviderEvents(ctx, conversation, held.Watermark, held.Watermark, 1000, histClock); err != nil || !again.Holding || mark() != idle {
		t.Fatalf("rescan = %+v err=%v, want held again with the mark unchanged", again, err)
	}

	projectArchived(t, s, conversation, session, "acp-history:x", "activity.completed", `{"kind":"activity.completed"}`, histClock, nil)
	event := mark()
	if event == idle {
		t.Fatalf("hold mark %+v did not move on a new event", event)
	}
	terminate(t, s, session, time.Now().UTC())
	if mark() == event {
		t.Fatalf("hold mark %+v did not move when the session terminated", event)
	}
	compactAll(t, s, conversation, 1000)
	if got := len(archiveMethods(t, s, conversation)["message.delta"]); got != 0 {
		t.Fatalf("message.delta rows = %d, want a terminated host's deltas released", got)
	}
	if keys := identityKeys(t, s, conversation); len(keys) != 0 {
		t.Fatalf("identity keys = %v, want none for a terminated session's host", keys)
	}
}

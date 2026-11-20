package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// bigOutput is far larger than a pruned activity keeps.
var bigOutput = strings.Repeat("line of command output\n", 1000)

// recordTurn archives one settled turn at its own time, the way a persistent
// ACP host delivers it: host-journal identities (acp-host:...), one identity a
// native-history replay can re-send (acp-history:...), one row without an
// identity, a command with large output in both its stream and its detail, and
// an answer.
func recordTurn(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, turn string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "tallpine-"+turn, turn, at); err != nil {
		t.Fatal(err)
	}
	host := func(n int) string { return fmt.Sprintf("acp-host:launch-1:%s-%d", turn, n) }
	projectArchived(t, s, conversation, session, host(0), "turn.started",
		fmt.Sprintf(`{"kind":"turn.started","providerTurnId":%q}`, turn), at, nil)
	command := "cmd-" + turn
	detail, err := json.Marshal(map[string]any{"command": "go test ./...", "output": bigOutput, "exitCode": 0})
	if err != nil {
		t.Fatal(err)
	}
	projectArchived(t, s, conversation, session, host(1), "activity.started",
		fmt.Sprintf(`{"kind":"activity.started","providerTurnId":%q,"providerItemId":%q}`, turn, command), at,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, turn, domain.ConversationActivity{
				ID: "a-" + command, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
				Summary: "Run tests", Detail: []byte(`{"command":"go test ./..."}`), ProviderItemID: command,
			}, at)
		})
	projectArchived(t, s, conversation, session, "", "command.output.delta",
		fmt.Sprintf(`{"kind":"command.output.delta","providerTurnId":%q,"providerItemId":%q}`, turn, command), at,
		func(txCtx context.Context) error {
			_, err := s.AppendCommandOutput(txCtx, conversation, command, bigOutput, at)
			return err
		})
	projectArchived(t, s, conversation, session, "acp-history:"+turn, "activity.completed",
		fmt.Sprintf(`{"kind":"activity.completed","providerTurnId":%q,"providerItemId":%q,"output":%q}`, turn, command, bigOutput), at,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, turn, domain.ConversationActivity{
				ID: "a2-" + command, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
				Summary: "Run tests", Detail: detail, ProviderItemID: command,
			}, at)
		})
	item := "msg-" + turn
	projectArchived(t, s, conversation, session, host(2), "message.completed",
		fmt.Sprintf(`{"kind":"message.completed","providerTurnId":%q,"providerItemId":%q,"text":"done"}`, turn, item), at,
		func(txCtx context.Context) error {
			return s.SettleAssistantMessage(txCtx, conversation, item, turn, "Answer "+turn, "m-"+turn, at)
		})
	projectArchived(t, s, conversation, session, host(3), "turn.completed",
		fmt.Sprintf(`{"kind":"turn.completed","providerTurnId":%q,"turnState":"completed"}`, turn), at,
		func(txCtx context.Context) error {
			return s.SettleTurn(txCtx, conversation, turn, domain.TurnStateCompleted, "", at)
		})
}

func recordCompaction(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, id string, at time.Time) {
	t.Helper()
	projectArchived(t, s, conversation, session, id, "thread.compacted", `{"kind":"thread.compacted"}`, at, nil)
}

// pruneHistoryAll runs the janitor's history step to completion with the given
// batch size and reports what it did.
func pruneHistoryAll(t *testing.T, s *sqlite.Store, batch int64) (deleted, keyed, activities int64) {
	t.Helper()
	ctx := context.Background()
	candidates, err := s.HistoryRetentionCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		boundary, at, err := s.SettledHistoryBoundary(ctx, c.ConversationID, c.BoundaryEventID)
		if err != nil {
			t.Fatal(err)
		}
		if boundary == 0 {
			continue
		}
		for after := c.EventsPrunedThroughID; after < boundary; {
			result, err := s.PruneHistoryEvents(ctx, c.ConversationID, boundary, after, batch, histClock)
			if err != nil {
				t.Fatal(err)
			}
			deleted, keyed, after = deleted+result.Deleted, keyed+result.Keyed, result.Next
			if result.Done {
				break
			}
		}
		if c.ActivitiesDoneBoundaryID >= boundary {
			continue
		}
		watermark, after := c.ActivitiesPrunedThroughSeq, c.ActivitiesPrunedThroughSeq
		for range 1000 {
			result, err := s.PruneHistoryActivities(ctx, c.ConversationID, boundary, at, watermark, after, batch, histClock)
			if err != nil {
				t.Fatal(err)
			}
			activities += result.Pruned
			if result.Done {
				break
			}
			watermark, after = result.Watermark, result.Next
		}
	}
	return deleted, keyed, activities
}

type archivedRow struct{ id, method, payload string }

func archiveRows(t *testing.T, s *sqlite.Store, conversation string) []archivedRow {
	t.Helper()
	events, err := s.ProviderEventsSince(context.Background(), conversation, 0, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]archivedRow, 0, len(events))
	for _, e := range events {
		out = append(out, archivedRow{e.ProviderEventID, e.Method, e.PayloadJson})
	}
	return out
}

func commandActivity(t *testing.T, s *sqlite.Store, conversation, turn string) domain.ConversationActivity {
	t.Helper()
	return commandActivityByItem(t, s, conversation, "cmd-"+turn)
}

// Break caught: compaction only shrank the model's context. A long-lived
// orchestrator conversation kept every provider event and every command's full
// output (585k events, 21k activities), which slowed its queries to 30 s. Below a
// settled compaction the archive now keeps only what dedupe and verification
// read, activities keep a short head, and the visible transcript is unchanged.
func TestHistoryBelowSettledCompactionIsPruned(t *testing.T) {
	for _, batch := range []int64{1000, 2} {
		t.Run(fmt.Sprintf("batch=%d", batch), func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			recordTurn(t, s, conversation, session, "t1", histClock)
			// A steer is what the user typed, shown as a user message and
			// replayed by retry: it must survive pruning byte for byte.
			steerDetail := fmt.Sprintf(`{"event":"steer","text":%q,"providerInputText":%q}`, bigOutput, bigOutput)
			if err := s.UpsertActivity(ctx, conversation, "t1", domain.ConversationActivity{
				ID: "a-steer", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
				Summary: "Steer", Detail: []byte(steerDetail), ProviderItemID: "steer-1",
			}, histClock.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendActivityStreamedText(ctx, conversation, "steer-1", bigOutput, histClock.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			recordCompaction(t, s, conversation, session, "acp-host:launch-1:compact", histClock.Add(time.Minute))
			recordTurn(t, s, conversation, session, "t2", histClock.Add(2*time.Minute))

			before, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := s.CountForTest(ctx, `SELECT COUNT(*) FROM change_log`)
			if err != nil {
				t.Fatal(err)
			}
			deleted, keyed, activities := pruneHistoryAll(t, s, batch)
			// Below the boundary: acp-host turn.started, activity.started and
			// message.completed plus the identity-less output delta are deleted;
			// the acp-history row a native replay can re-send leaves its key.
			if deleted != 4 || keyed != 1 || activities != 1 {
				t.Fatalf("pruned deleted=%d keyed=%d activities=%d, want 4/1/1", deleted, keyed, activities)
			}
			if keys, err := s.ProviderEventKeysForTest(ctx, conversation); err != nil || !reflect.DeepEqual(keys, []string{"acp-history:t1"}) {
				t.Fatalf("identity keys = %v err=%v, want only acp-history:t1", keys, err)
			}

			rows := archiveRows(t, s, conversation)
			want := []archivedRow{
				{"acp-host:launch-1:t1-3", "turn.completed", `{"kind":"turn.completed","providerTurnId":"t1","turnState":"completed"}`},
				{"acp-host:launch-1:compact", "thread.compacted", `{"kind":"thread.compacted"}`},
			}
			if !reflect.DeepEqual(rows[:2], want) {
				t.Fatalf("archive below boundary = %+v\nwant %+v", rows[:2], want)
			}
			// Above the boundary nothing changed: t2's rows are whole.
			if len(rows) != 2+6 {
				t.Fatalf("archive rows = %d, want 2 kept below the boundary and t2's 6", len(rows))
			}
			for _, row := range rows[2:] {
				if row.payload == "{}" {
					t.Fatalf("event above the boundary was pruned: %+v", row)
				}
			}

			after, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Messages, after.Messages) || !reflect.DeepEqual(before.Turns, after.Turns) {
				t.Fatal("pruning changed messages or turns")
			}
			old := commandActivity(t, s, conversation, "t1")
			if len(old.CommandOutput) != sqlitestore.HistoryPruneKeepBytes || !old.CommandOutputTruncated ||
				!strings.HasPrefix(bigOutput, old.CommandOutput) {
				t.Fatalf("t1 output = %d bytes truncated=%v, want a %d-byte head", len(old.CommandOutput), old.CommandOutputTruncated, sqlitestore.HistoryPruneKeepBytes)
			}
			var detail map[string]any
			if err := json.Unmarshal(old.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			// The completion snapshot's output copy was never stored: the
			// streamed column above carries it (B842), so the detail is small
			// and pruning leaves it whole.
			if want := map[string]any{"command": "go test ./...", "exitCode": float64(0)}; !reflect.DeepEqual(detail, want) {
				t.Fatalf("t1 detail = %v, want %v", detail, want)
			}
			if old.Summary != "Run tests" || old.Status != domain.ActivityStatusCompleted {
				t.Fatalf("t1 activity lost its summary or status: %+v", old)
			}
			if steer := commandActivityByItem(t, s, conversation, "steer-1"); string(steer.Detail) != steerDetail || steer.StreamedText != bigOutput {
				t.Fatalf("steer row was rewritten: detail %d bytes, streamed %d bytes", len(steer.Detail), len(steer.StreamedText))
			}
			if recent := commandActivity(t, s, conversation, "t2"); recent.CommandOutput != bigOutput {
				t.Fatalf("t2 output above the boundary was pruned to %d bytes", len(recent.CommandOutput))
			}
			// Old history was rewritten in place without a revision bump, so open
			// clients are not told to refetch it.
			if got, _ := s.CountForTest(ctx, `SELECT COUNT(*) FROM change_log`); got != changes {
				t.Fatalf("change_log rows %d -> %d, want no rows from pruning", changes, got)
			}
			// Done: the conversation drops out of the candidate list.
			if candidates, err := s.HistoryRetentionCandidates(ctx); err != nil || len(candidates) != 0 {
				t.Fatalf("candidates after pruning = %+v err=%v, want none", candidates, err)
			}
		})
	}
}

// A native-history import re-sends acp-history identities. After pruning, the
// identity key must still reject them, or the activity would be imported again.
func TestReplayAfterHistoryPruneIsStillDeduplicated(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	recordTurn(t, s, conversation, session, "t1", histClock)
	recordCompaction(t, s, conversation, session, "acp-history:compact", histClock.Add(time.Minute))
	recordTurn(t, s, conversation, session, "t2", histClock.Add(2*time.Minute))
	pruneHistoryAll(t, s, 1000)

	replayed := projectArchived(t, s, conversation, session, "acp-history:t1", "activity.completed", `{}`, histClock,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, "t1", domain.ConversationActivity{
				ID: "a3-cmd-t1", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
				Summary: "Run tests", Detail: []byte(`{"output":"replayed"}`), ProviderItemID: "cmd-t1",
			}, histClock)
		})
	if replayed {
		t.Fatal("replayed native-history identity was projected again after pruning")
	}
}

// A compaction is a boundary only once a later turn started: until then the
// host may still replay the prompt it belongs to. An auto-compaction lands
// mid-turn, so later events of that same turn (usage, its completion) prove
// nothing.
func TestUnsettledCompactionPrunesNothing(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	recordTurn(t, s, conversation, session, "t1", histClock)
	recordCompaction(t, s, conversation, session, "acp-host:launch-1:compact", histClock.Add(time.Minute))
	projectArchived(t, s, conversation, session, "acp-host:launch-1:usage", "usage", `{"kind":"usage"}`, histClock.Add(time.Minute), nil)
	projectArchived(t, s, conversation, session, "acp-host:launch-1:done", "turn.completed",
		`{"kind":"turn.completed","providerTurnId":"t1","turnState":"completed"}`, histClock.Add(time.Minute), nil)
	before := archiveRows(t, s, conversation)

	if deleted, tombstoned, activities := pruneHistoryAll(t, s, 1000); deleted+tombstoned+activities != 0 {
		t.Fatalf("pruned %d/%d/%d before the compaction settled", deleted, tombstoned, activities)
	}
	if got := archiveRows(t, s, conversation); !reflect.DeepEqual(before, got) {
		t.Fatal("archive changed before the compaction settled")
	}
	if got := commandActivity(t, s, conversation, "t1"); got.CommandOutput != bigOutput {
		t.Fatal("activity pruned before the compaction settled")
	}
}

// An activity still running before the boundary is never shortened. It holds
// the cursor only while its turn is live, so a later pass prunes it once it
// settles. A stale one, left running by a turn that ended (real archives hold
// weeks-old plan and approval rows like that), is skipped without holding, or
// every pass would rescan the conversation from it.
func TestRunningActivityHoldsHistoryCursorOnlyInLiveTurn(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	recordTurn(t, s, conversation, session, "t1", histClock)
	running := func(id, turn string, at time.Time) {
		t.Helper()
		if err := s.UpsertActivity(ctx, conversation, turn, domain.ConversationActivity{
			ID: "a-" + id, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
			Summary: id, Detail: []byte(fmt.Sprintf(`{"output":%q}`, bigOutput)), ProviderItemID: id,
		}, at); err != nil {
			t.Fatal(err)
		}
	}
	// Stale: t1 completed long ago, its plan row never settled.
	running("stale", "t1", histClock.Add(time.Second))
	// Live: a background turn still running when the compaction settled.
	if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "tallpine-bg", "bg-turn", histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	running("live", "bg-turn", histClock.Add(3*time.Second))
	recordTurn(t, s, conversation, session, "t0", histClock.Add(4*time.Second))
	recordCompaction(t, s, conversation, session, "acp-host:launch-1:compact", histClock.Add(time.Minute))
	recordTurn(t, s, conversation, session, "t2", histClock.Add(2*time.Minute))

	if _, _, activities := pruneHistoryAll(t, s, 1); activities != 2 {
		t.Fatalf("first pass pruned %d activities, want t1 and t0 but neither running one", activities)
	}
	for _, id := range []string{"stale", "live"} {
		if got := commandActivityByItem(t, s, conversation, id); got.CommandOutput != "" || len(got.Detail) < len(bigOutput) {
			t.Fatalf("running activity %s was shortened: status=%s output=%d detail=%d", id, got.Status, len(got.CommandOutput), len(got.Detail))
		}
	}
	candidates, err := s.HistoryRetentionCandidates(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].ActivitiesDoneBoundaryID != 0 {
		t.Fatalf("candidates = %+v err=%v, want the conversation kept with activities not done", candidates, err)
	}
	if got, want := candidates[0].ActivitiesPrunedThroughSeq, commandActivityByItem(t, s, conversation, "live").Sequence-1; got != want {
		t.Fatalf("activity cursor = %d, want it held just below the live activity (%d), not the stale one", got, want)
	}
	if err := s.UpsertActivity(ctx, conversation, "bg-turn", domain.ConversationActivity{
		ID: "a-live-done", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
		Summary: "live", Detail: []byte(fmt.Sprintf(`{"output":%q}`, bigOutput)), ProviderItemID: "live",
	}, histClock.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, activities := pruneHistoryAll(t, s, 1); activities != 1 {
		t.Fatalf("second pass pruned %d activities, want the now-settled one", activities)
	}
	if candidates, err := s.HistoryRetentionCandidates(ctx); err != nil || len(candidates) != 0 {
		t.Fatalf("candidates after settling = %+v err=%v, want none", candidates, err)
	}
}

func commandActivityByItem(t *testing.T, s *sqlite.Store, conversation, item string) domain.ConversationActivity {
	t.Helper()
	snapshot, err := s.LoadConversationSnapshot(context.Background(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range snapshot.Activities {
		if a.ProviderItemID == item {
			return a
		}
	}
	t.Fatalf("no activity %s", item)
	return domain.ConversationActivity{}
}

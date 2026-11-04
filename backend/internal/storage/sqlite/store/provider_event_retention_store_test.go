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
)

// projectArchived writes one provider event with its projection, like the chat
// controller does.
func projectArchived(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID,
	providerEventID, method, payload string, at time.Time, project func(context.Context) error,
) bool {
	t.Helper()
	if project == nil {
		project = func(context.Context) error { return nil }
	}
	projected, err := s.ProjectProviderEvent(context.Background(), conversation, session, "gen-1",
		providerEventID, method, payload, at, project)
	if err != nil {
		t.Fatalf("archive %s %s: %v", method, providerEventID, err)
	}
	return projected
}

func deltaPayload(turn, item, delta string) string {
	return fmt.Sprintf(`{"kind":"message.delta","providerTurnId":%q,"providerItemId":%q,"delta":%q}`, turn, item, delta)
}

// streamTurn records one provider turn the way a live controller does: deltas
// with and without native identities, a reasoning activity with streamed text,
// a command with streamed output, the settled message, and the completion.
func streamTurn(t *testing.T, s *sqlite.Store, conversation string, session domain.SessionID, turn string, settle bool) {
	t.Helper()
	ctx := context.Background()
	at := histClock
	if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "tallpine-"+turn, turn, at); err != nil {
		t.Fatal(err)
	}
	projectArchived(t, s, conversation, session, turn+":started", "turn.started",
		fmt.Sprintf(`{"kind":"turn.started","providerTurnId":%q}`, turn), at, nil)
	item := "msg-" + turn
	for i, chunk := range []string{"Hello", ", ", "world"} {
		id := ""
		if i%2 == 0 {
			id = fmt.Sprintf("%s:delta:%d", turn, i)
		}
		projectArchived(t, s, conversation, session, id, "message.delta", deltaPayload(turn, item, chunk), at,
			func(txCtx context.Context) error {
				return s.AppendAssistantDelta(txCtx, conversation, item, turn, chunk, "m-"+turn, at)
			})
	}
	reasoning := "think-" + turn
	projectArchived(t, s, conversation, session, turn+":reasoning", "activity.started",
		fmt.Sprintf(`{"kind":"activity.started","providerTurnId":%q,"providerItemId":%q}`, turn, reasoning), at,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, turn, domain.ConversationActivity{
				ID: "a-" + reasoning, Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusRunning,
				Summary: "Reasoning", ProviderItemID: reasoning,
			}, at)
		})
	projectArchived(t, s, conversation, session, "", "reasoning.delta",
		fmt.Sprintf(`{"kind":"reasoning.delta","providerTurnId":%q,"providerItemId":%q,"delta":"pondering"}`, turn, reasoning), at,
		func(txCtx context.Context) error {
			_, err := s.AppendActivityStreamedText(txCtx, conversation, reasoning, "pondering", at)
			return err
		})
	command := "cmd-" + turn
	projectArchived(t, s, conversation, session, turn+":cmd", "activity.started",
		fmt.Sprintf(`{"kind":"activity.started","providerTurnId":%q,"providerItemId":%q}`, turn, command), at,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, turn, domain.ConversationActivity{
				ID: "a-" + command, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
				Summary: "Run tests", Detail: []byte(`{"command":"go test"}`), ProviderItemID: command,
			}, at)
		})
	for i, chunk := range []string{"ok  pkg/a\n", "ok  pkg/b\n"} {
		projectArchived(t, s, conversation, session, fmt.Sprintf("%s:out:%d", turn, i), "command.output.delta",
			fmt.Sprintf(`{"kind":"command.output.delta","providerTurnId":%q,"providerItemId":%q,"delta":%q}`, turn, command, chunk), at,
			func(txCtx context.Context) error {
				_, err := s.AppendCommandOutput(txCtx, conversation, command, chunk, at)
				return err
			})
	}
	if !settle {
		return
	}
	projectArchived(t, s, conversation, session, turn+":cmd:done", "activity.completed",
		fmt.Sprintf(`{"kind":"activity.completed","providerTurnId":%q,"providerItemId":%q}`, turn, command), at,
		func(txCtx context.Context) error {
			return s.UpsertActivity(txCtx, conversation, turn, domain.ConversationActivity{
				ID: "a2-" + command, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
				Summary: "Run tests", Detail: []byte(`{"command":"go test","exitCode":0}`), ProviderItemID: command,
			}, at)
		})
	projectArchived(t, s, conversation, session, turn+":msg:done", "message.completed",
		fmt.Sprintf(`{"kind":"message.completed","providerTurnId":%q,"providerItemId":%q,"text":"Hello, world"}`, turn, item), at,
		func(txCtx context.Context) error {
			return s.SettleAssistantMessage(txCtx, conversation, item, turn, "Hello, world", "m2-"+turn, at)
		})
	projectArchived(t, s, conversation, session, turn+":completed", "turn.completed",
		fmt.Sprintf(`{"kind":"turn.completed","providerTurnId":%q,"turnState":"completed"}`, turn), at,
		func(txCtx context.Context) error {
			return s.SettleTurn(txCtx, conversation, turn, domain.TurnStateCompleted, "", at)
		})
}

func archiveMethods(t *testing.T, s *sqlite.Store, conversation string) map[string][]string {
	t.Helper()
	events, err := s.ProviderEventsSince(context.Background(), conversation, 0, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, event := range events {
		out[event.Method] = append(out[event.Method], event.PayloadJson)
	}
	return out
}

func compactAll(t *testing.T, s *sqlite.Store, conversation string, batch int64) {
	t.Helper()
	candidates, err := s.ProviderEventCompactionCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	watermark := int64(0)
	for _, candidate := range candidates {
		if candidate.ConversationID == conversation {
			watermark = candidate.CompactedThroughID
		}
	}
	after := watermark
	for range 100 {
		result, err := s.CompactProviderEvents(context.Background(), conversation, watermark, after, batch, histClock)
		if err != nil {
			t.Fatal(err)
		}
		// Nothing else writes here, so the batch reports its own hold and no
		// contention for the janitor's pacing.
		if result.Held <= 0 || result.Contended {
			t.Fatalf("writer hold = %+v, want a positive uncontended hold", result.WriterHold)
		}
		watermark, after = result.Watermark, result.Next
		if result.Done {
			return
		}
	}
	t.Fatal("compaction did not finish")
}

// Compaction removes only archive rows the timeline never reads: the history a
// client renders is identical before and after, page by page.
func TestCompactingSettledTurnsKeepsRenderedHistory(t *testing.T) {
	for _, batch := range []int64{1000, 3} {
		t.Run(fmt.Sprintf("batch=%d", batch), func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			streamTurn(t, s, conversation, session, "t1", true)
			streamTurn(t, s, conversation, session, "t2", true)

			before, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			beforePage, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 4)
			if err != nil {
				t.Fatal(err)
			}
			compactAll(t, s, conversation, batch)
			after, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			afterPage, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 4)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("compaction changed the rendered history:\nbefore %+v\nafter  %+v", before, after)
			}
			if !reflect.DeepEqual(beforePage, afterPage) {
				t.Fatalf("compaction changed a history page:\nbefore %+v\nafter  %+v", beforePage, afterPage)
			}
			if got := texts(after.Messages); !reflect.DeepEqual(got, []string{"Hello, world", "Hello, world"}) {
				t.Fatalf("messages = %q", got)
			}
			for _, activity := range after.Activities {
				if activity.Kind == domain.ActivityKindCommand && activity.CommandOutput != "ok  pkg/a\nok  pkg/b\n" {
					t.Fatalf("command output lost: %+v", activity)
				}
				if activity.Kind == domain.ActivityKindReasoning && activity.StreamedText != "pondering" {
					t.Fatalf("reasoning text lost: %+v", activity)
				}
			}

			methods := archiveMethods(t, s, conversation)
			// Identity-less deltas are gone; identity-bearing ones are '{}'
			// tombstones; everything else is untouched.
			for _, payload := range append(append(methods["message.delta"], methods["command.output.delta"]...), methods["reasoning.delta"]...) {
				if payload != "{}" {
					t.Errorf("delta survived compaction: %s", payload)
				}
			}
			if got := len(methods["message.delta"]); got != 4 {
				t.Errorf("message.delta rows = %d, want 4 identity tombstones (2 per turn)", got)
			}
			if got := len(methods["reasoning.delta"]); got != 0 {
				t.Errorf("identity-less reasoning.delta rows = %d, want 0", got)
			}
			for _, method := range []string{"turn.started", "turn.completed", "message.completed", "activity.started", "activity.completed"} {
				if len(methods[method]) != 2 && (method != "activity.started" || len(methods[method]) != 4) {
					t.Errorf("%s rows = %d, want them all kept", method, len(methods[method]))
				}
				for _, payload := range methods[method] {
					if payload == "{}" {
						t.Errorf("%s was compacted", method)
					}
				}
			}
		})
	}
}

// A compacted delta must still be rejected as a duplicate: a persistent host
// replays an unacknowledged prompt from its start, and a replayed delta that got
// through would append its text to the settled message again.
func TestCompactedDeltaIdentityStillDeduplicatesReplay(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	compactAll(t, s, conversation, 1000)

	replayed := projectArchived(t, s, conversation, session, "t1:delta:0", "message.delta",
		deltaPayload("t1", "msg-t1", "Hello"), histClock,
		func(txCtx context.Context) error {
			return s.AppendAssistantDelta(txCtx, conversation, "msg-t1", "t1", "Hello", "m-dup", histClock)
		})
	if replayed {
		t.Fatal("replayed delta identity was projected again after compaction")
	}
	snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(snapshot.Messages); !reflect.DeepEqual(got, []string{"Hello, world"}) {
		t.Fatalf("messages after replay = %q", got)
	}
}

// Deltas of a turn still running are kept, and the watermark stays below them
// so the pass after the turn settles compacts them.
func TestCompactionWaitsForRunningTurn(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	streamTurn(t, s, conversation, session, "t2", false)

	compactAll(t, s, conversation, 1000)
	methods := archiveMethods(t, s, conversation)
	live := 0
	for _, payload := range methods["message.delta"] {
		if strings.Contains(payload, `"providerTurnId":"t2"`) {
			live++
		}
	}
	if live != 3 {
		t.Fatalf("running turn kept %d of 3 message deltas", live)
	}
	candidates, err := s.ProviderEventCompactionCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v err=%v, want the conversation still pending", candidates, err)
	}

	if err := s.SettleTurn(ctx, conversation, "t2", domain.TurnStateInterrupted, "", histClock); err != nil {
		t.Fatal(err)
	}
	compactAll(t, s, conversation, 1000)
	for _, payload := range archiveMethods(t, s, conversation)["message.delta"] {
		if payload != "{}" {
			t.Fatalf("delta of the settled turn survived: %s", payload)
		}
	}
	candidates, err = s.ProviderEventCompactionCandidates(ctx)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v err=%v, want none once everything is compacted", candidates, err)
	}
}

func expireAll(t *testing.T, s *sqlite.Store, before time.Time, batch int64) (deleted, tombstoned int64) {
	t.Helper()
	ctx := context.Background()
	expired, err := s.ExpiredSessionsWithProviderEvents(ctx, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range expired {
		after := session.ExpiredThroughID
		for range 1000 {
			result, err := s.ExpireSessionProviderEvents(ctx, session.SessionID, after, batch, histClock)
			if err != nil {
				t.Fatal(err)
			}
			if result.Deleted+result.Tombstoned > batch {
				t.Fatalf("one window expired %d rows, want at most %d", result.Deleted+result.Tombstoned, batch)
			}
			deleted += result.Deleted
			tombstoned += result.Tombstoned
			after = result.Next
			if result.Done {
				break
			}
		}
	}
	return deleted, tombstoned
}

func terminate(t *testing.T, s *sqlite.Store, session domain.SessionID, at time.Time) {
	t.Helper()
	rec, _, err := s.GetSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	rec.UpdatedAt = at
	if err := s.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// Retention expires the provider events of a session terminated before the
// cutoff, in bounded windows: rows without a native identity are deleted, rows
// with one are kept as '{}' tombstones. Nothing of a live or recently ended
// session is touched, and the rendered history never changes.
func TestRetentionExpiresProviderEventsOfLongTerminatedSessions(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	now := time.Now().UTC()

	if expired, err := s.ExpiredSessionsWithProviderEvents(ctx, now.Add(-14*24*time.Hour), 10); err != nil || len(expired) != 0 {
		t.Fatalf("live session listed as expired: %v err=%v", expired, err)
	}
	terminate(t, s, session, now.Add(-3*24*time.Hour))
	if expired, err := s.ExpiredSessionsWithProviderEvents(ctx, now.Add(-14*24*time.Hour), 10); err != nil || len(expired) != 0 {
		t.Fatalf("session terminated 3 days ago listed as expired: %v err=%v", expired, err)
	}
	snapshotBefore, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}

	deleted, tombstoned := expireAll(t, s, now.Add(-24*time.Hour), 4)
	if deleted == 0 || tombstoned == 0 {
		t.Fatalf("retention deleted %d and tombstoned %d rows, want both", deleted, tombstoned)
	}
	for method, payloads := range archiveMethods(t, s, conversation) {
		for _, payload := range payloads {
			if payload != "{}" {
				t.Errorf("%s kept its payload after expiry: %s", method, payload)
			}
		}
	}
	if expired, err := s.ExpiredSessionsWithProviderEvents(ctx, now.Add(-24*time.Hour), 10); err != nil || len(expired) != 0 {
		t.Fatalf("fully expired session still listed: %v err=%v", expired, err)
	}
	snapshotAfter, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshotBefore, snapshotAfter) {
		t.Fatal("retention changed the rendered history of the terminated session")
	}
}

// A session restored after retention replays its native history with the same
// stable identities. The tombstones must still reject every one of them, or
// messages and activity text would be imported a second time. Newer events of
// the restored session are expired once it is past retention again.
func TestReplayAfterRetentionIsStillDeduplicated(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	now := time.Now().UTC()
	terminate(t, s, session, now.Add(-30*24*time.Hour))
	expireAll(t, s, now.Add(-14*24*time.Hour), 1000)

	// Restored: the replay re-sends every identity-bearing event of the turn.
	rec, _, err := s.GetSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = false
	rec.UpdatedAt = now
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	for _, replay := range []struct{ id, method, item, text string }{
		{"t1:delta:0", "message.delta", "msg-t1", "Hello"},
		{"t1:delta:2", "message.delta", "msg-t1", "world"},
	} {
		if projectArchived(t, s, conversation, session, replay.id, replay.method, deltaPayload("t1", replay.item, replay.text), now,
			func(txCtx context.Context) error {
				return s.AppendAssistantDelta(txCtx, conversation, replay.item, "t1", replay.text, "m-dup-"+replay.id, now)
			}) {
			t.Fatalf("replayed %s was projected again after retention", replay.id)
		}
	}
	for _, id := range []string{"t1:out:0", "t1:out:1"} {
		if projectArchived(t, s, conversation, session, id, "command.output.delta",
			`{"kind":"command.output.delta","providerTurnId":"t1","providerItemId":"cmd-t1","delta":"again\n"}`, now,
			func(txCtx context.Context) error {
				_, err := s.AppendCommandOutput(txCtx, conversation, "cmd-t1", "again\n", now)
				return err
			}) {
			t.Fatalf("replayed %s was projected again after retention", id)
		}
	}
	snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(snapshot.Messages); !reflect.DeepEqual(got, []string{"Hello, world"}) {
		t.Fatalf("messages after replay = %q", got)
	}
	for _, activity := range snapshot.Activities {
		if activity.Kind == domain.ActivityKindCommand && activity.CommandOutput != "ok  pkg/a\nok  pkg/b\n" {
			t.Fatalf("command output after replay = %q", activity.CommandOutput)
		}
	}

	// The restored session works on, ends again, and only its new event is
	// expired once it is past retention.
	projectArchived(t, s, conversation, session, "", "message.delta", deltaPayload("t2", "msg-t2", "new"), now, nil)
	terminate(t, s, session, now.Add(-20*24*time.Hour))
	expired, err := s.ExpiredSessionsWithProviderEvents(ctx, now.Add(-14*24*time.Hour), 10)
	if err != nil || len(expired) != 1 || expired[0].ExpiredThroughID == 0 {
		t.Fatalf("expired = %+v err=%v, want the session resuming at its cursor", expired, err)
	}
	if deleted, tombstoned := expireAll(t, s, now.Add(-14*24*time.Hour), 1000); deleted != 1 || tombstoned != 0 {
		t.Fatalf("second expiry deleted %d and tombstoned %d, want only the new row", deleted, tombstoned)
	}
}

// A heartbeat that re-sends an unchanged activity writes nothing: no revision
// bump and so no change_log row. Turning compaction off restores the rewrite.
func TestUnchangedActivityUpsertIsNotWritten(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	ctx := context.Background()
	activity := domain.ConversationActivity{
		ID: "a1", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning,
		Summary: "sleep 600", Detail: []byte(`{"command":"sleep 600"}`), ProviderItemID: "tool-1",
	}
	if err := s.UpsertActivity(ctx, conversation, "", activity, histClock); err != nil {
		t.Fatal(err)
	}
	revision := func() int64 {
		snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
		if err != nil || len(snapshot.Activities) != 1 {
			t.Fatalf("snapshot: %+v err=%v", snapshot, err)
		}
		return snapshot.Activities[0].Revision
	}
	seq := func() int64 {
		latest, err := s.LatestSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return latest
	}
	startRevision, startSeq := revision(), seq()
	for i := range 5 {
		activity.ID = fmt.Sprintf("heartbeat-%d", i)
		if err := s.UpsertActivity(ctx, conversation, "", activity, histClock.Add(time.Duration(i+1)*30*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if revision() != startRevision || seq() != startSeq {
		t.Fatalf("heartbeats wrote: revision %d -> %d, change_log %d -> %d", startRevision, revision(), startSeq, seq())
	}

	activity.Status = domain.ActivityStatusCompleted
	if err := s.UpsertActivity(ctx, conversation, "", activity, histClock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if revision() != startRevision+1 || seq() <= startSeq {
		t.Fatalf("a real change was not written: revision %d, change_log %d -> %d", revision(), startSeq, seq())
	}

	s.SetCoalesceWrites(false)
	changed := revision()
	if err := s.UpsertActivity(ctx, conversation, "", activity, histClock.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if revision() != changed+1 {
		t.Fatalf("with compaction off an unchanged upsert should rewrite: revision %d -> %d", changed, revision())
	}
}

// A new database is created with incremental auto-vacuum, so the janitor can
// return freed pages to the OS without a blocking full VACUUM.
func TestOptimizeStorageReturnsFreedPagesOnNewDatabase(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	big := strings.Repeat("x", 64*1024)
	for i := range 40 {
		projectArchived(t, s, conversation, session, "", "activity.completed",
			fmt.Sprintf(`{"kind":"activity.completed","providerItemId":"i%d","text":%q}`, i, big), histClock, nil)
	}
	terminate(t, s, session, time.Now().UTC().Add(-30*24*time.Hour))
	if deleted, _ := expireAll(t, s, time.Now().UTC().Add(-14*24*time.Hour), 100); deleted != 40 {
		t.Fatalf("expired %d rows, want the 40 written", deleted)
	}
	result, err := s.OptimizeStorage(ctx, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IncrementalVacuum || result.FreedPages < 40*16 {
		t.Fatalf("optimize = %+v, want incremental vacuum to free the deleted pages", result)
	}
}

func liveDeltas(t *testing.T, s *sqlite.Store, conversation, turn string) int {
	t.Helper()
	live := 0
	for _, payload := range archiveMethods(t, s, conversation)["message.delta"] {
		if strings.Contains(payload, fmt.Sprintf(`"providerTurnId":%q`, turn)) {
			live++
		}
	}
	return live
}

// More unattributed deltas than one window holds (a nested provider thread Tallpine
// has no turn row for), with the turn.completed that settles them arriving
// later, must not stall the conversation: the next pass compacts them and
// everything after them.
func TestCompactionPastMoreUnattributedDeltasThanOneWindow(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	const batch = 3
	for i := range 3*batch + 1 {
		id := ""
		if i%2 == 0 {
			id = fmt.Sprintf("nested:%d", i)
		}
		projectArchived(t, s, conversation, session, id, "message.delta",
			deltaPayload("nested-thread", "nested-msg", fmt.Sprintf("chunk %d ", i)), histClock, nil)
	}

	// Nothing has completed after them yet: they stay, and the conversation stays
	// a candidate.
	compactAll(t, s, conversation, batch)
	if live := liveDeltas(t, s, conversation, "nested-thread"); live != 3*batch+1 {
		t.Fatalf("unattributed deltas compacted before any completion: %d left", live)
	}
	if candidates, err := s.ProviderEventCompactionCandidates(ctx); err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}

	streamTurn(t, s, conversation, session, "t1", true)
	compactAll(t, s, conversation, batch)
	if live := liveDeltas(t, s, conversation, "nested-thread"); live != 0 {
		t.Fatalf("%d unattributed deltas survived a later turn.completed", live)
	}
	if live := liveDeltas(t, s, conversation, "t1"); live != 0 {
		t.Fatalf("%d deltas of the settled turn after them survived", live)
	}
	if candidates, err := s.ProviderEventCompactionCandidates(ctx); err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v err=%v, want the conversation fully compacted", candidates, err)
	}
}

// A delta held by a turn that is still running keeps the watermark in place,
// but later settled turns are compacted in the same pass, beyond any window.
func TestHeldDeltaDoesNotBlockLaterSettledTurns(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "running", false)
	streamTurn(t, s, conversation, session, "t2", true)
	streamTurn(t, s, conversation, session, "t3", true)

	compactAll(t, s, conversation, 3)
	if live := liveDeltas(t, s, conversation, "running"); live != 3 {
		t.Fatalf("running turn kept %d of 3 deltas", live)
	}
	for _, turn := range []string{"t2", "t3"} {
		if live := liveDeltas(t, s, conversation, turn); live != 0 {
			t.Fatalf("held delta blocked compaction of %s: %d deltas left", turn, live)
		}
	}
	candidates, err := s.ProviderEventCompactionCandidates(ctx)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v err=%v, want the held conversation pending", candidates, err)
	}
}

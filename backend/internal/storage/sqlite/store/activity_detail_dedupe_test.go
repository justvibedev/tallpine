package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// activityDetail returns the stored detail of the conversation's activity with
// the given provider item id, decoded, and the activity itself.
func activityDetail(t *testing.T, s *sqlite.Store, conversation, item string) (map[string]any, domain.ConversationActivity) {
	t.Helper()
	snapshot, err := s.LoadConversationSnapshot(context.Background(), conversation)
	if err != nil {
		t.Fatal(err)
	}
	for _, activity := range snapshot.Activities {
		if activity.ProviderItemID != item {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal(activity.Detail, &detail); err != nil {
			t.Fatalf("decode detail %s: %v", activity.Detail, err)
		}
		return detail, activity
	}
	t.Fatalf("no activity %s", item)
	return nil, domain.ConversationActivity{}
}

func detailedCommand(item, status string, detail map[string]any) domain.ConversationActivity {
	encoded, _ := json.Marshal(detail)
	return domain.ConversationActivity{
		ID: "activity-" + item, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatus(status),
		Summary: "go test ./...", ProviderItemID: item, Detail: encoded,
	}
}

// An ACP provider re-sends the whole tool call on completion, output included.
// Once the output streamed into command_output, the read path shows that
// column, so the snapshot's copy is not stored; a rawCommand that only repeats
// command is not either. Resending the same snapshot is still an unchanged
// repeat that does not bump the revision.
func TestCompletedCommandSnapshotStoresItsOutputOnce(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	s.SetCoalesceWrites(true)
	ctx := context.Background()
	running := map[string]any{"protocol": "acp", "command": "go test ./...", "rawCommand": "go test ./...", "output": ""}
	if err := s.UpsertActivity(ctx, conversation, "provider-turn-1", detailedCommand("exec-1", "running", running), histClock); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCommandOutput(ctx, conversation, "exec-1", "ok  pkg 0.1s\n", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	completed := map[string]any{
		"protocol": "acp", "command": "go test ./...", "rawCommand": "go test ./...",
		"output": "ok  pkg 0.1s\n", "outputTruncated": true, "exitCode": 0,
	}
	if err := s.UpsertActivity(ctx, conversation, "provider-turn-1", detailedCommand("exec-1", "completed", completed), histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	detail, activity := activityDetail(t, s, conversation, "exec-1")
	want := map[string]any{"protocol": "acp", "command": "go test ./...", "exitCode": float64(0)}
	if !reflect.DeepEqual(detail, want) || activity.CommandOutput != "ok  pkg 0.1s\n" {
		t.Fatalf("stored detail = %v, output %q; want %v and the streamed output", detail, activity.CommandOutput, want)
	}
	if err := s.UpsertActivity(ctx, conversation, "provider-turn-1", detailedCommand("exec-1", "completed", completed), histClock.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, again := activityDetail(t, s, conversation, "exec-1"); again.Revision != activity.Revision {
		t.Fatalf("unchanged resend bumped the revision %d -> %d", activity.Revision, again.Revision)
	}
}

// A snapshot stored with its output before any output streamed loses that copy
// when the first delta lands, since the read path shows the column from then on.
// Output that never streamed (a provider without deltas) is the only copy and
// stays, and a rawCommand that differs from command is the audit record of
// what ran and stays.
func TestFirstOutputDeltaDropsTheSnapshotCopy(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	ctx := context.Background()
	wrapped := map[string]any{"command": "ls", "rawCommand": "/bin/zsh -lc ls", "output": "a\nb\n", "outputSource": "aggregate"}
	for _, item := range []string{"exec-streams", "exec-aggregate"} {
		if err := s.UpsertActivity(ctx, conversation, "provider-turn-1", detailedCommand(item, "running", wrapped), histClock); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendCommandOutput(ctx, conversation, "exec-streams", "a\n", histClock.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	streamed, _ := activityDetail(t, s, conversation, "exec-streams")
	if want := map[string]any{"command": "ls", "rawCommand": "/bin/zsh -lc ls"}; !reflect.DeepEqual(streamed, want) {
		t.Fatalf("streamed detail = %v, want %v", streamed, want)
	}
	if aggregate, _ := activityDetail(t, s, conversation, "exec-aggregate"); !reflect.DeepEqual(aggregate, wrapped) {
		t.Fatalf("aggregate-only detail = %v, want it unchanged %v", aggregate, wrapped)
	}
}

// Rows written before compaction existed are rewritten by the paced backfill,
// window by window up to the rowid recorded when it was installed, without a
// revision bump; later rows and rows with nothing repeated are left alone.
func TestActivityDetailBackfillCompactsLegacyRows(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	ctx := context.Background()
	for _, item := range []string{"legacy-1", "legacy-2", "legacy-3", "wrapped", "late"} {
		if err := s.UpsertActivity(ctx, conversation, "provider-turn-1", detailedCommand(item, "completed", map[string]any{"command": "ls"}), histClock); err != nil {
			t.Fatal(err)
		}
	}
	legacy := `{"command":"ls","rawCommand":"ls","output":"a\n","outputTruncated":true,"exitCode":0}`
	wrapped := `{"command":"ls","rawCommand":"/bin/zsh -lc ls","output":"a\n"}`
	for _, stmt := range []string{
		`UPDATE conversation_activities SET detail_json = '` + legacy + `', command_output = 'a' || char(10)
		 WHERE provider_item_id IN ('legacy-1', 'legacy-2', 'legacy-3', 'late')`,
		`UPDATE conversation_activities SET detail_json = '` + wrapped + `' WHERE provider_item_id = 'wrapped'`,
		`UPDATE activity_detail_compaction SET compacted_through_rowid = 0,
		 through_rowid = (SELECT rowid FROM conversation_activities WHERE provider_item_id = 'wrapped')`,
	} {
		if err := s.ExecWriterForTest(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	_, before := activityDetail(t, s, conversation, "legacy-1")

	var compacted int64
	for i := 0; ; i++ {
		result, err := s.CompactActivityDetailsWindow(ctx, 2, histClock)
		if err != nil {
			t.Fatal(err)
		}
		compacted += result.Compacted
		if result.Done {
			break
		}
		if i > 10 {
			t.Fatal("backfill never finished")
		}
	}
	// Three legacy rows each drop output and rawCommand.
	if compacted != 6 {
		t.Fatalf("compacted = %d, want 6", compacted)
	}
	for _, item := range []string{"legacy-1", "legacy-2", "legacy-3"} {
		detail, activity := activityDetail(t, s, conversation, item)
		if want := map[string]any{"command": "ls", "exitCode": float64(0)}; !reflect.DeepEqual(detail, want) || activity.CommandOutput != "a\n" {
			t.Fatalf("%s = %v output %q, want %v", item, detail, activity.CommandOutput, want)
		}
		if item == "legacy-1" && activity.Revision != before.Revision {
			t.Fatalf("backfill bumped the revision %d -> %d", before.Revision, activity.Revision)
		}
	}
	if detail, _ := activityDetail(t, s, conversation, "wrapped"); detail["rawCommand"] != "/bin/zsh -lc ls" || detail["output"] != "a\n" {
		t.Fatalf("wrapped = %v, want rawCommand and the only output copy kept", detail)
	}
	if detail, _ := activityDetail(t, s, conversation, "late"); detail["rawCommand"] != "ls" {
		t.Fatalf("row after the backfill's end was touched: %v", detail)
	}
	if result, err := s.CompactActivityDetailsWindow(ctx, 2, histClock); err != nil || !result.Done || result.Compacted != 0 {
		t.Fatalf("finished backfill = %+v err=%v, want done", result, err)
	}
}

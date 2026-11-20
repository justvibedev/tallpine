package store_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// repackAll sweeps the whole archive in windows of batch rows.
func repackAll(t *testing.T, s *sqlite.Store, through, batch int64) int64 {
	t.Helper()
	var after, total int64
	for range 10000 {
		result, err := s.RepackProviderEvents(context.Background(), after, through, batch)
		if err != nil {
			t.Fatalf("repack after %d: %v", after, err)
		}
		total += result.Rewritten
		if result.Done {
			return total
		}
		if result.Next <= after {
			t.Fatalf("repack cursor did not move past %d", after)
		}
		after = result.Next
	}
	t.Fatal("repack never finished")
	return 0
}

func archiveDump(t *testing.T, s *sqlite.Store) []string {
	t.Helper()
	dump, err := s.ArchiveDumpForTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return dump
}

// Tombstoning shrinks rows inside their pages, which incremental vacuum cannot
// return; the repack must free those pages without changing a single row.
func TestRepackKeepsEveryArchiveRowAndReturnsTheSpaceInsideItsPages(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	payload := strings.Repeat("y", 1500)
	for i := range 600 {
		projectArchived(t, s, conversation, session, fmt.Sprintf("item-%d", i), "activity.started",
			fmt.Sprintf(`{"kind":"activity.started","providerItemId":"i%d","text":%q}`, i, payload), histClock, nil)
	}
	// A row without a branch must stay as it is: inserting it again would let
	// the branch trigger assign it the active branch.
	if err := s.ExecWriterForTest(ctx, `UPDATE conversation_provider_events SET branch_id = '' WHERE provider_event_id = 'item-7'`); err != nil {
		t.Fatal(err)
	}
	// Shrink every row in place, as tombstoning did before identity keys.
	if err := s.ExecWriterForTest(ctx, `UPDATE conversation_provider_events SET payload_json = '{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OptimizeStorage(ctx, 1<<20); err != nil {
		t.Fatal(err)
	}
	before, err := s.ProviderEventArchiveSlack(ctx)
	if err != nil {
		t.Fatalf("dbstat must be available to the production driver: %v", err)
	}
	if before.UnusedBytes*2 < before.Bytes || before.MaxID == 0 {
		t.Fatalf("slack before repack = %+v, want tombstones to leave most of the pages unused", before)
	}
	dump := archiveDump(t, s)

	if n := repackAll(t, s, before.MaxID, 50); n != 599 {
		t.Fatalf("repacked %d rows, want all 600 but the branchless one", n)
	}
	if got := archiveDump(t, s); !reflect.DeepEqual(got, dump) {
		t.Fatalf("repack changed the archive:\nbefore %v\nafter  %v", dump[:3], got[:3])
	}
	freed, err := s.OptimizeStorage(ctx, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ProviderEventArchiveSlack(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if freed.FreedPages == 0 || after.Bytes*4 > before.Bytes {
		t.Fatalf("repack freed %d pages, archive %d -> %d bytes; want most pages returned", freed.FreedPages, before.Bytes, after.Bytes)
	}

	// The repacked tombstones still reject a replay of their identity.
	if projectArchived(t, s, conversation, session, "item-3", "activity.started", `{"kind":"activity.started"}`, histClock, nil) {
		t.Fatal("a replayed identity was archived again after the repack")
	}
}

// One window copies at most repackWindowBytes (4 MiB) of payload, so a run of
// large rows still makes a short transaction.
func TestRepackWindowStopsAtItsByteBudget(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	big := strings.Repeat("z", 1<<20)
	for i := range 6 {
		projectArchived(t, s, conversation, session, fmt.Sprintf("big-%d", i), "activity.completed",
			fmt.Sprintf(`{"t":%q}`, big), histClock, nil)
	}
	slack, err := s.ProviderEventArchiveSlack(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dump := archiveDump(t, s)
	first, err := s.RepackProviderEvents(ctx, 0, slack.MaxID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if first.Rewritten != 3 || first.Done || first.Next >= slack.MaxID {
		t.Fatalf("first window = %+v, want 3 rows of about 1 MiB and more to come", first)
	}
	second, err := s.RepackProviderEvents(ctx, first.Next, slack.MaxID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if second.Rewritten != 3 || !second.Done || second.Next != slack.MaxID {
		t.Fatalf("second window = %+v, want the last 3 rows and done", second)
	}
	if got := archiveDump(t, s); !reflect.DeepEqual(got, dump) {
		t.Fatal("repack changed the archive")
	}
}

// A row whose conversation is gone (migrations run with foreign keys off) would
// fail its foreign key when inserted again; the repack leaves it in place
// instead of failing the window.
func TestRepackLeavesForeignKeyOrphansInPlace(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	for i := range 5 {
		projectArchived(t, s, conversation, session, fmt.Sprintf("row-%d", i), "activity.started", `{"kind":"activity.started"}`, histClock, nil)
	}
	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO conversation_provider_events (conversation_id, session_id, provider_event_id, method, payload_json, received_at, branch_id)
		 VALUES ('gone', '` + string(session) + `', 'orphan', 'activity.started', '{}', '2026-08-02 10:00:00', 'branch-gone')`,
		`PRAGMA foreign_keys = ON`,
	} {
		if err := s.ExecWriterForTest(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	projectArchived(t, s, conversation, session, "row-after", "activity.started", `{"kind":"activity.started"}`, histClock, nil)
	slack, err := s.ProviderEventArchiveSlack(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dump := archiveDump(t, s)
	if n := repackAll(t, s, slack.MaxID, 50); n != 6 {
		t.Fatalf("repacked %d rows, want the 6 with a conversation", n)
	}
	if got := archiveDump(t, s); !reflect.DeepEqual(got, dump) {
		t.Fatal("repack changed the archive")
	}
}

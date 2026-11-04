package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func waitQueued(t *testing.T, queued func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for queued() < want {
		if time.Now().After(deadline) {
			t.Fatalf("%d projections queued, want %d", queued(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// Projections that arrive while the writer is busy commit together: one
// transaction for the whole queue instead of one each.
func TestProjectionsQueuedBehindABusyWriterShareOneTransaction(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var first any
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.GroupCommitProjectionForTest(ctx, func(_ context.Context, tx any) (bool, error) {
			first = tx
			close(entered)
			<-release
			return true, nil
		})
		firstDone <- err
	}()
	<-entered

	const queued = 5
	txs := make([]any, queued)
	errs := make([]error, queued)
	var wg sync.WaitGroup
	for i := range queued {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.GroupCommitProjectionForTest(ctx, func(_ context.Context, tx any) (bool, error) {
				txs[i] = tx
				return true, nil
			})
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, queued)
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i := range queued {
		if errs[i] != nil {
			t.Fatalf("unit %d: %v", i, errs[i])
		}
		if txs[i] != txs[0] {
			t.Fatalf("queued unit %d committed in its own transaction, want one shared by the queue", i)
		}
	}
	if txs[0] == first {
		t.Fatal("queued units joined the transaction that was already running")
	}
}

// A unit that fails rolls back alone: the projections batched with it commit,
// and its own archive row is gone, exactly as if it had committed by itself.
func TestFailedProjectionRollsBackAloneInsideABatch(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	release, blockerDone := holdWriter(ctx, s)

	const units = 6
	const failing = 3
	boom := errors.New("projection failed")
	projected := make([]bool, units)
	errs := make([]error, units)
	var wg sync.WaitGroup
	for i := range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			projected[i], errs[i] = s.ProjectProviderEvent(ctx, conversation, session, "gen-1",
				fmt.Sprintf("batched-%d", i), "message.delta", `{"delta":"x"}`, histClock, func(context.Context) error {
					if i == failing {
						return boom
					}
					return nil
				})
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, units)
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	events, err := s.ProviderEventsSince(ctx, conversation, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	archived := map[string]bool{}
	for _, event := range events {
		archived[event.ProviderEventID] = true
	}
	for i := range units {
		id := fmt.Sprintf("batched-%d", i)
		if i == failing {
			if !errors.Is(errs[i], boom) || projected[i] {
				t.Fatalf("failing unit = (%v, %v), want its own error", projected[i], errs[i])
			}
			if archived[id] {
				t.Fatal("the failed unit's archive row survived its rollback")
			}
			continue
		}
		if errs[i] != nil || !projected[i] {
			t.Fatalf("unit %d = (%v, %v), want projected", i, projected[i], errs[i])
		}
		if !archived[id] {
			t.Fatalf("unit %d committed without its archive row", i)
		}
	}
}

// A panic in one session's projection must not wedge the shared batcher: the
// panicking unit fails alone, its batch peers commit, and later projections
// still run.
func TestPanickingProjectionFailsAloneAndReleasesTheBatcher(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	release, blockerDone := holdWriter(ctx, s)

	const units = 4
	const panicking = 1
	errs := make([]error, units)
	var wg sync.WaitGroup
	for i := range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.ProjectProviderEvent(ctx, conversation, session, "gen-1",
				fmt.Sprintf("panic-batch-%d", i), "message.delta", `{"delta":"x"}`, histClock, func(context.Context) error {
					if i == panicking {
						panic("projection bug")
					}
					return nil
				})
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, units)
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("a panicking projection left its batch waiting forever")
	}
	for i, err := range errs {
		if i == panicking {
			if err == nil {
				t.Fatal("the panicking unit reported success")
			}
			continue
		}
		if err != nil {
			t.Fatalf("unit %d failed with its batch peer's panic: %v", i, err)
		}
	}
	events, err := s.ProviderEventsSince(ctx, conversation, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ProviderEventID == fmt.Sprintf("panic-batch-%d", panicking) {
			t.Fatal("the panicking unit's archive row survived")
		}
	}
	// The batcher is still usable afterwards.
	done := make(chan error, 1)
	go func() {
		_, err := s.ProjectProviderEvent(ctx, conversation, session, "gen-1", "after-panic", "message.delta",
			`{"delta":"y"}`, histClock, func(context.Context) error { return nil })
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("projections after a panic never ran")
	}
}

// slowInsert writes long enough on SQLite to be interrupted mid-statement. A
// write, not a read: an interrupted write is what rolls back the whole
// transaction.
const slowInsert = `INSERT INTO slow_sink(x) SELECT x FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 500000) SELECT x FROM c)`

// slowSink creates the temp table slowInsert writes to, inside the batch.
const slowSink = `CREATE TEMP TABLE IF NOT EXISTS slow_sink(x INTEGER)`

// The driver answers a cancelled context with sqlite3_interrupt, which rolls
// back the whole transaction. A session that stops mid-batch must not take the
// other sessions' units down with it.
func TestCallerCancelledMidStatementDoesNotRollBackItsBatch(t *testing.T) {
	// The batch watchdog is not under test here: keep it out of the way even
	// when -race makes the slow insert take far longer.
	restore := store.SetProjectionBatchWatchdogForTest(time.Hour)
	defer restore()
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	release, blockerDone := holdWriter(ctx, s)

	victimCtx, cancelVictim := context.WithCancel(ctx)
	defer cancelVictim()
	victimDone := make(chan error, 1)
	go func() {
		_, err := s.GroupCommitSQLForTest(victimCtx, func() {
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancelVictim()
			}()
		}, slowSink+"; "+slowInsert)
		victimDone <- err
	}()
	waitQueued(t, s.QueuedProjectionsForTest, 1)

	const peers = 3
	errs := make([]error, peers)
	var wg sync.WaitGroup
	for i := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.ProjectProviderEvent(ctx, conversation, session, "gen-1",
				fmt.Sprintf("peer-%d", i), "message.delta", `{"delta":"x"}`, histClock, func(context.Context) error { return nil })
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, 1+peers)
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	victimErr := <-victimDone
	for i, err := range errs {
		if err != nil {
			t.Fatalf("peer %d failed because another caller cancelled: %v", i, err)
		}
	}
	events, err := s.ProviderEventsSince(ctx, conversation, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	archived := map[string]bool{}
	for _, event := range events {
		archived[event.ProviderEventID] = true
	}
	for i := range peers {
		if !archived[fmt.Sprintf("peer-%d", i)] {
			t.Fatalf("peer %d's archive row was rolled back by another caller's cancellation", i)
		}
	}
	if victimErr != nil {
		t.Fatalf("unit cancelled after it started = %v, want it to finish its statements", victimErr)
	}
}

// A batch stuck past the watchdog is interrupted and abandoned, and each of its
// units commits in a transaction of its own: one slow unit cannot hold the
// writer for every session.
func TestProjectionBatchWatchdogRetriesUnitsAlone(t *testing.T) {
	restore := store.SetProjectionBatchWatchdogForTest(200 * time.Millisecond)
	defer restore()
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	release, blockerDone := holdWriter(ctx, s)

	// Slow only on its first attempt, inside the shared batch.
	var attempts atomic.Int32
	slowDone := make(chan error, 1)
	go func() {
		_, err := s.GroupCommitProjectionForTest(ctx, func(ctx context.Context, tx any) (bool, error) {
			if attempts.Add(1) > 1 {
				return true, nil
			}
			return s.ExecOnProjectionTxForTest(ctx, tx, slowSink+"; "+slowInsert+"; "+slowInsert+"; "+slowInsert)
		})
		slowDone <- err
	}()
	waitQueued(t, s.QueuedProjectionsForTest, 1)
	const peers = 3
	errs := make([]error, peers)
	var wg sync.WaitGroup
	for i := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.ProjectProviderEvent(ctx, conversation, session, "gen-1",
				fmt.Sprintf("watchdog-%d", i), "message.delta", `{"delta":"x"}`, histClock, func(context.Context) error { return nil })
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, 1+peers)
	started := time.Now()
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := <-slowDone; err != nil {
		t.Fatalf("slow unit = %v, want its retry alone to commit", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("slow unit ran %d times, want the interrupted batch attempt and one retry", got)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("batch took %s, want it abandoned at the watchdog", elapsed)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("peer %d = %v, want its own retry to commit", i, err)
		}
	}
	events, err := s.ProviderEventsSince(ctx, conversation, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	archived := map[string]bool{}
	for _, event := range events {
		archived[event.ProviderEventID] = true
	}
	for i := range peers {
		if !archived[fmt.Sprintf("watchdog-%d", i)] {
			t.Fatalf("peer %d was not committed after the watchdog abandoned its batch", i)
		}
	}
}

// A caller that gave up before its unit ran fails alone and touches nothing.
func TestCallerCancelledBeforeItsTurnFailsAlone(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = s.GroupCommitProjectionForTest(ctx, func(context.Context, any) (bool, error) {
			close(entered)
			<-release
			return false, nil
		})
	}()
	<-entered
	gone, cancel := context.WithCancel(ctx)
	goneDone := make(chan error, 1)
	go func() {
		_, err := s.ProjectProviderEvent(gone, conversation, session, "gen-1", "gone", "message.delta",
			`{"delta":"x"}`, histClock, func(context.Context) error { return nil })
		goneDone <- err
	}()
	waitQueued(t, s.QueuedProjectionsForTest, 1)
	peerDone := make(chan error, 1)
	go func() {
		_, err := s.ProjectProviderEvent(ctx, conversation, session, "gen-1", "stays", "message.delta",
			`{"delta":"y"}`, histClock, func(context.Context) error { return nil })
		peerDone <- err
	}()
	waitQueued(t, s.QueuedProjectionsForTest, 2)
	cancel()
	close(release)
	if err := <-goneDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller = %v, want its own cancellation", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("peer = %v", err)
	}
	events, err := s.ProviderEventsSince(ctx, conversation, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, event := range events {
		ids[event.ProviderEventID] = true
	}
	if ids["gone"] || !ids["stays"] {
		t.Fatalf("archived ids = %v, want only the live caller's", ids)
	}
}

// A unit that stays slow on its retry too is retried after its peers, which
// are answered as soon as their own transactions commit, and its lone retry
// ends at the watchdog instead of holding the writer.
func TestPersistentlySlowUnitRetriesAfterItsPeers(t *testing.T) {
	restore := store.SetProjectionBatchWatchdogForTest(200 * time.Millisecond)
	defer restore()
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	release, blockerDone := holdWriter(ctx, s)

	var attempts atomic.Int32
	slow := strings.Repeat("; "+slowInsert, 20)
	slowDone := make(chan time.Time, 1)
	var slowErr error
	go func() {
		_, slowErr = s.GroupCommitProjectionForTest(ctx, func(ctx context.Context, tx any) (bool, error) {
			attempts.Add(1)
			return s.ExecOnProjectionTxForTest(ctx, tx, slowSink+slow)
		})
		slowDone <- time.Now()
	}()
	waitQueued(t, s.QueuedProjectionsForTest, 1)
	const peers = 3
	errs := make([]error, peers)
	peerDone := make([]time.Time, peers)
	var wg sync.WaitGroup
	for i := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.ProjectProviderEvent(ctx, conversation, session, "gen-1",
				fmt.Sprintf("stays-slow-%d", i), "message.delta", `{"delta":"x"}`, histClock, func(context.Context) error { return nil })
			peerDone[i] = time.Now()
		}()
	}
	waitQueued(t, s.QueuedProjectionsForTest, 1+peers)
	started := time.Now()
	close(release)
	if err := <-blockerDone; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	slowAt := <-slowDone
	for i, err := range errs {
		if err != nil {
			t.Fatalf("peer %d = %v, want it committed on its own", i, err)
		}
		if !peerDone[i].Before(slowAt) {
			t.Fatalf("peer %d was answered after the slow unit's retry, want before it", i)
		}
	}
	if slowErr == nil {
		t.Fatal("a unit that stays slow committed, want its lone retry stopped at the watchdog")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("slow unit ran %d times, want the batch attempt and one retry", got)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("a persistently slow unit held the writer for %s, want about two watchdog periods", elapsed)
	}
}

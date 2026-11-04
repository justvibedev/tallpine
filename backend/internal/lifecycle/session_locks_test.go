package lifecycle

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// stallingStore makes the first read of one session hang until released, the
// way a session read queues behind SQLite's single writer. Calls into the
// embedded fake are serialized because reducers now run concurrently.
type stallingStore struct {
	*fakeStore
	mu      sync.Mutex
	stallID domain.SessionID
	// stalling picks the first read of stallID. Later reads pass straight
	// through: a sync.Once would block them inside Do as well, which would make
	// a same-session serialization test pass without any session lock.
	stalling atomic.Bool
	stalled  chan struct{}
	release  chan struct{}
}

func newStallingStore(stallID domain.SessionID) *stallingStore {
	return &stallingStore{fakeStore: newFakeStore(), stallID: stallID, stalled: make(chan struct{}), release: make(chan struct{})}
}

func (s *stallingStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if id == s.stallID && s.stalling.CompareAndSwap(false, true) {
		close(s.stalled)
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.GetSession(ctx, id)
}

func (s *stallingStore) UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord, expected int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.UpdateSessionFromActivitySignal(ctx, rec, expected)
}

func (s *stallingStore) state(id domain.SessionID) domain.ActivityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id].Activity.State
}

func applyIdleInBackground(m *Manager, id domain.SessionID) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- m.ApplyActivitySignal(context.Background(), id, ports.ActivitySignal{Valid: true, State: domain.ActivityIdle})
	}()
	return done
}

// One session's slow store round trip must not hold every other session's
// activity behind it.
func TestActivitySignalDoesNotWaitOnAnotherSessionsStoreRead(t *testing.T) {
	st := newStallingStore("mer-1")
	st.sessions["mer-1"] = working("mer-1")
	st.sessions["mer-2"] = working("mer-2")
	m := New(st, nil)

	stalled := applyIdleInBackground(m, "mer-1")
	<-st.stalled
	select {
	case err := <-applyIdleInBackground(m, "mer-2"):
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mer-2's signal waited for mer-1's stalled store read")
	}
	if got := st.state("mer-2"); got != domain.ActivityIdle {
		t.Fatalf("mer-2 activity = %s, want idle", got)
	}
	close(st.release)
	if err := <-stalled; err != nil {
		t.Fatal(err)
	}
	if got := st.state("mer-1"); got != domain.ActivityIdle {
		t.Fatalf("mer-1 activity = %s, want idle", got)
	}
}

// Signals for the same session still reduce one at a time.
func TestActivitySignalsForOneSessionStaySerialized(t *testing.T) {
	st := newStallingStore("mer-1")
	st.sessions["mer-1"] = working("mer-1")
	m := New(st, nil)

	first := applyIdleInBackground(m, "mer-1")
	<-st.stalled
	second := make(chan error, 1)
	go func() {
		second <- m.ApplyActivitySignal(context.Background(), "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput})
	}()
	select {
	case err := <-second:
		t.Fatalf("second signal reduced while the first held the session (err=%v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(st.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if got := st.state("mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("activity = %s, want the second signal's waiting_input", got)
	}
	if held := m.sessionLocks.held(); held != 0 {
		t.Fatalf("%d session locks left after every reducer returned, want 0", held)
	}
}

// slowStore charges every session read and write a fixed latency, the way
// SQLite does while its single writer is busy with streaming projection.
// Writes also serialize on one writer, as they do in SQLite; reads do not.
type slowStore struct {
	*fakeStore
	mu      sync.Mutex
	writer  sync.Mutex
	latency time.Duration
}

func (s *slowStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	time.Sleep(s.latency)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.GetSession(ctx, id)
}

func (s *slowStore) UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord, expected int64) (bool, error) {
	s.writer.Lock()
	defer s.writer.Unlock()
	time.Sleep(s.latency)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.UpdateSessionFromActivitySignal(ctx, rec, expected)
}

// BenchmarkActivitySignalsAcross30Sessions: 30 sessions each report an
// active/idle pair per iteration, concurrently, against a 1 ms store.
func BenchmarkActivitySignalsAcross30Sessions(b *testing.B) {
	st := &slowStore{fakeStore: newFakeStore(), latency: time.Millisecond}
	ids := make([]domain.SessionID, 30)
	for i := range ids {
		ids[i] = domain.SessionID("mer-" + string(rune('a'+i)))
		st.sessions[ids[i]] = working(ids[i])
	}
	m := New(st, nil)
	states := []domain.ActivityState{domain.ActivityIdle, domain.ActivityActive}
	b.ResetTimer()
	for range b.N {
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, state := range states {
					if err := m.ApplyActivitySignal(context.Background(), id, ports.ActivitySignal{Valid: true, State: state}); err != nil {
						b.Error(err)
					}
				}
			}()
		}
		wg.Wait()
	}
}

// The ready-to-merge snapshot must not interleave with the same session's
// activity reducer, or the notification could race a waiting_input update.
func TestSCMNotificationSnapshotWaitsForTheSessionsReducer(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = working("mer-1")
	m := New(st, nil)
	release := m.sessionLocks.lock("mer-1")
	done := make(chan error, 1)
	go func() {
		_, err := m.notificationIntentForCurrentSCM(context.Background(), "mer-1", ports.SCMObservation{}, true)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("notification snapshot read the session while its reducer held it (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification snapshot did not proceed after the reducer released the session")
	}
}

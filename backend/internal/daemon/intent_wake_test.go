package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

type fakeWakeStore struct {
	due      []domain.OrchestratorIntent
	enabled  map[domain.ProjectID]bool
	claimed  map[string]bool
	released []string
	// stale makes the list ignore claims, as a sweep that listed before another claimed.
	stale bool
}

func (f *fakeWakeStore) SelectDueIntentWakes(context.Context, time.Time) ([]domain.OrchestratorIntent, error) {
	var out []domain.OrchestratorIntent
	for _, in := range f.due {
		if f.stale || !f.claimed[in.ID] {
			out = append(out, in)
		}
	}
	return out, nil
}

func (f *fakeWakeStore) ClaimIntentWake(_ context.Context, id string, _ time.Time) (bool, error) {
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

func (f *fakeWakeStore) ReleaseIntentWake(_ context.Context, id string) error {
	delete(f.claimed, id)
	f.released = append(f.released, id)
	return nil
}

func (f *fakeWakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{Config: domain.ProjectConfig{IntentWake: f.enabled[domain.ProjectID(id)]}}, true, nil
}

type fakeWaker struct {
	sent []string
	err  error
}

func (w *fakeWaker) send(_ context.Context, _ domain.ProjectID, _ domain.SessionID, msg string, byUser bool) error {
	if byUser {
		panic("an intent wake is a system message, not the owner's words")
	}
	if w.err != nil {
		return w.err
	}
	w.sent = append(w.sent, msg)
	return nil
}

func wakeFixture(project domain.ProjectID, enabled bool) *fakeWakeStore {
	at := time.Date(2026, 11, 14, 6, 20, 0, 0, time.UTC)
	return &fakeWakeStore{
		due:     []domain.OrchestratorIntent{{ID: "i1", ProjectID: project, Text: "check the Play listing", WakeAt: &at}},
		enabled: map[domain.ProjectID]bool{project: enabled}, claimed: map[string]bool{},
	}
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// With the switch on a due wake names the intent and is sent exactly once, even
// across repeated sweeps (and so across a restart, which only re-runs a sweep).
func TestSweepIntentWakesSendsOnce(t *testing.T) {
	store, waker := wakeFixture("p", true), &fakeWaker{}
	for range 3 {
		sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	}
	if len(waker.sent) != 1 || !strings.Contains(waker.sent[0], "i1") || !strings.Contains(waker.sent[0], "check the Play listing") {
		t.Fatalf("sent = %q", waker.sent)
	}
}

// The default (switch off) sends nothing and leaves the wake pending.
func TestSweepIntentWakesOffByDefault(t *testing.T) {
	store, waker := wakeFixture("p", false), &fakeWaker{}
	sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	if len(waker.sent) != 0 || len(store.claimed) != 0 {
		t.Fatalf("sent=%q claimed=%v", waker.sent, store.claimed)
	}
	store.enabled["p"] = true
	sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	if len(waker.sent) != 1 {
		t.Fatalf("a wake held back by the switch must fire once it is on; sent=%q", waker.sent)
	}
}

// A failed delivery (no live orchestrator) is retried by the next sweep.
func TestSweepIntentWakesRetriesAfterFailedDelivery(t *testing.T) {
	store, waker := wakeFixture("p", true), &fakeWaker{err: errors.New("no orchestrator")}
	sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	if len(store.released) != 1 || len(waker.sent) != 0 {
		t.Fatalf("released=%v sent=%q", store.released, waker.sent)
	}
	waker.err = nil
	sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	if len(waker.sent) != 1 {
		t.Fatalf("sent = %q", waker.sent)
	}
}

// Two overlapping sweeps that both listed the wake: only the one that wins the
// claim sends.
func TestSweepIntentWakesLostClaimSendsNothing(t *testing.T) {
	store, waker := wakeFixture("p", true), &fakeWaker{}
	store.stale = true
	store.claimed["i1"] = true
	sweepIntentWakes(context.Background(), store, waker, time.Now(), quietLog)
	if len(waker.sent) != 0 {
		t.Fatalf("sent = %q", waker.sent)
	}
}

// No reachable orchestrator is the expected retry case: the claim is released
// and nothing is logged at Warn.
func TestSweepIntentWakesNoOrchestratorReleasesQuietly(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store, waker := wakeFixture("p", true), &fakeWaker{err: backlogsvc.ErrNoOrchestrator}
	sweepIntentWakes(context.Background(), store, waker, time.Now(), log)
	if len(store.released) != 1 || buf.Len() != 0 {
		t.Fatalf("released=%v log=%q", store.released, buf.String())
	}
}

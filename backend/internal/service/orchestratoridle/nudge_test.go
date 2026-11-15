package orchestratoridle

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeStore struct {
	sessions []domain.SessionRecord
	cfg      domain.ProjectConfig
	intents  int
	asks     int
	runs     int
}

func (f *fakeStore) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}
func (f *fakeStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{Config: f.cfg}, true, nil
}
func (f *fakeStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}
func (f *fakeStore) ListRunningReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	return make([]domain.ReviewRun, f.runs), nil
}
func (f *fakeStore) SelectOrchestratorIntents(context.Context, domain.ProjectID, bool) ([]domain.OrchestratorIntent, error) {
	return make([]domain.OrchestratorIntent, f.intents), nil
}
func (f *fakeStore) ListOpenOwnerAsks(context.Context, domain.ProjectID) ([]domain.OwnerAsk, error) {
	return make([]domain.OwnerAsk, f.asks), nil
}

type harness struct {
	f    *fakeStore
	s    *Sweeper
	sent []string
	now  time.Time
	// refuse makes the guard refuse the nudge.
	refuse bool
}

func newHarness(on bool) *harness {
	h := &harness{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	orch := domain.SessionRecord{ID: "o1", ProjectID: "p", Kind: domain.KindOrchestrator}
	orch.Activity.State = domain.ActivityIdle
	orch.Activity.LastActivityAt = h.now.Add(-time.Hour)
	h.f = &fakeStore{sessions: []domain.SessionRecord{orch}, cfg: domain.ProjectConfig{OrchestratorIdleNudge: on}, intents: 2}
	h.s = New(Deps{Store: h.f, Now: func() time.Time { return h.now },
		Send: func(_ context.Context, _ domain.SessionID, msg string) (bool, error) {
			if h.refuse {
				return false, nil
			}
			h.sent = append(h.sent, msg)
			return true, nil
		}})
	return h
}

func (h *harness) sweep(t *testing.T) {
	t.Helper()
	if err := h.s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOffByDefault(t *testing.T) {
	h := newHarness(false)
	h.sweep(t)
	if len(h.sent) != 0 {
		t.Fatalf("nudged with switch off: %v", h.sent)
	}
}

func TestNudgesOncePerIdlePeriod(t *testing.T) {
	h := newHarness(true)
	h.sweep(t)
	h.sweep(t)
	if len(h.sent) != 1 {
		t.Fatalf("sent %d, want 1: %v", len(h.sent), h.sent)
	}
	// Work starts and ends: a new idle period nudges again.
	h.f.asks = 1
	h.sweep(t)
	h.f.asks = 0
	h.sweep(t)
	if len(h.sent) != 2 {
		t.Fatalf("sent %d after a new idle period, want 2", len(h.sent))
	}
}

func TestNoNudgeWhenNotQuiet(t *testing.T) {
	cases := map[string]func(*harness){
		"recently active":      func(h *harness) { h.f.sessions[0].Activity.LastActivityAt = h.now.Add(-time.Minute) },
		"no intents":           func(h *harness) { h.f.intents = 0 },
		"open ask":             func(h *harness) { h.f.asks = 1 },
		"busy orchestrator":    func(h *harness) { h.f.sessions[0].Activity.State = domain.ActivityActive },
		"orchestrator waiting": func(h *harness) { h.f.sessions[0].Activity.State = domain.ActivityWaitingInput },
		"running worker": func(h *harness) {
			w := domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker}
			w.Activity.State = domain.ActivityActive
			h.f.sessions = append(h.f.sessions, w)
		},
		"running review": func(h *harness) {
			h.f.sessions = append(h.f.sessions, domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker, IsTerminated: true})
			h.f.runs = 1
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(true)
			mod(h)
			h.sweep(t)
			if len(h.sent) != 0 {
				t.Fatalf("nudged: %v", h.sent)
			}
		})
	}
}

func TestIdleLiveWorkerDoesNotSuppress(t *testing.T) {
	h := newHarness(true)
	w := domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker}
	w.Activity.State = domain.ActivityIdle
	h.f.sessions = append(h.f.sessions, w)
	h.sweep(t)
	if len(h.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(h.sent))
	}
}

func TestRefusedNudgeRetriesAndIntentChangeRearms(t *testing.T) {
	h := newHarness(true)
	h.refuse = true
	h.sweep(t)
	h.refuse = false
	h.sweep(t)
	if len(h.sent) != 1 {
		t.Fatalf("sent %d after a refusal, want 1", len(h.sent))
	}
	h.f.intents = 3
	h.sweep(t)
	if len(h.sent) != 2 {
		t.Fatalf("sent %d after the intents changed, want 2", len(h.sent))
	}
}

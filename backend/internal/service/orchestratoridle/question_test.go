package orchestratoridle

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeQuestions struct {
	text  string
	at    time.Time
	lanes map[domain.SessionID]string
}

func (f *fakeQuestions) LastAssistantMessage(context.Context, domain.SessionID) (string, time.Time, bool, error) {
	return f.text, f.at, f.text != "", nil
}
func (f *fakeQuestions) ListSessionLanes(context.Context, domain.ProjectID) (map[domain.SessionID]string, error) {
	return f.lanes, nil
}

type fakeOwner struct {
	notified []ports.NotificationIntent
	resolved []ports.NotificationResolution
}

func (f *fakeOwner) Notify(_ context.Context, in ports.NotificationIntent) error {
	f.notified = append(f.notified, in)
	return nil
}
func (f *fakeOwner) Resolve(_ context.Context, r ports.NotificationResolution) error {
	f.resolved = append(f.resolved, r)
	return nil
}

// web-5 (B750): a worker finished, the orchestrator replied with a result that
// ended "Want me to land it?", filed no ask, and went idle with nothing in flight.
func questionHarness() (*harness, *fakeQuestions, *fakeOwner) {
	h := newHarness(false) // the B713 intent nudge stays off; this notice is always on
	idleAt := h.now.Add(-10 * time.Minute)
	h.f.sessions[0].Activity.LastActivityAt = idleAt
	h.f.sessions[0].Metadata.LatestUserPromptAt = idleAt.Add(-time.Hour)
	q := &fakeQuestions{text: "web-6 finished: PR #41 green, review approved.\n\nWant me to land it?", at: idleAt}
	o := &fakeOwner{}
	h.s.deps.Questions, h.s.deps.Owner = q, o
	return h, q, o
}

func TestUnansweredOwnerQuestionNotifiesOnce(t *testing.T) {
	h, _, o := questionHarness()
	h.sweep(t)
	h.sweep(t)
	if len(o.notified) != 1 {
		t.Fatalf("notified %d, want 1", len(o.notified))
	}
	n := o.notified[0]
	if n.Type != domain.NotificationPlanWaiting || n.SessionID != "o1" || !strings.HasPrefix(n.SourceKey, domain.OwnerQuestionSourcePrefix+"o1:") ||
		!strings.Contains(n.Body, "Want me to land it?") {
		t.Fatalf("intent = %+v", n)
	}
	if len(h.sent) != 0 {
		t.Fatalf("orchestrator nudged: %v", h.sent)
	}
	if !(domain.NotificationRecord{Type: n.Type, SourceKey: n.SourceKey}).IsOwnerQuestion() {
		t.Fatal("record not recognised as an owner question")
	}
}

func TestOwnerQuestionNotSentWhenNotWaiting(t *testing.T) {
	cases := map[string]func(*harness, *fakeQuestions){
		"plain status": func(_ *harness, q *fakeQuestions) { q.text = "web-6 finished; PR #41 merged." },
		"owner answered": func(h *harness, q *fakeQuestions) {
			h.f.sessions[0].Metadata.LatestUserPromptAt = q.at.Add(time.Second)
		},
		"just went idle":      func(h *harness, _ *fakeQuestions) { h.f.sessions[0].Activity.LastActivityAt = h.now.Add(-time.Minute) },
		"orchestrator busy":   func(h *harness, _ *fakeQuestions) { h.f.sessions[0].Activity.State = domain.ActivityActive },
		"ask filed":           func(h *harness, _ *fakeQuestions) { h.f.asks = 1 },
		"reply of older turn": func(_ *harness, q *fakeQuestions) { q.at = q.at.Add(-10 * time.Minute) },
		"worker in flight": func(h *harness, _ *fakeQuestions) {
			w := domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker}
			w.Activity.State = domain.ActivityActive
			h.f.sessions = append(h.f.sessions, w)
		},
		"review in flight": func(h *harness, _ *fakeQuestions) {
			h.f.sessions = append(h.f.sessions, domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker})
			h.f.runs = 1
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			h, q, o := questionHarness()
			mod(h, q)
			h.sweep(t)
			if len(o.notified) != 0 {
				t.Fatalf("notified: %+v", o.notified)
			}
		})
	}
}

func TestOwnerQuestionClearsWhenAnsweredAndUsesLanePrefix(t *testing.T) {
	h, q, o := questionHarness()
	q.lanes = map[domain.SessionID]string{"o1": "B12"}
	h.sweep(t)
	if len(o.notified) != 1 || !strings.HasPrefix(o.notified[0].SourceKey, domain.OwnerQuestionLaneSourcePrefix+"o1:") {
		t.Fatalf("notified = %+v", o.notified)
	}
	before := len(o.resolved) // the start-up clear of any older notice
	h.f.sessions[0].Metadata.LatestUserPromptAt = h.now
	h.sweep(t)
	if len(o.resolved) == before || o.resolved[len(o.resolved)-1].Type != domain.NotificationPlanWaiting {
		t.Fatalf("not resolved: %+v", o.resolved)
	}
	h.sweep(t)
	if n := len(o.resolved) - before; n != 2 {
		t.Fatalf("resolved %d times, want one pass over both prefixes", n)
	}
}

// A notice filed before a daemon restart is resolved when the owner answers
// after it, though this Sweeper never filed it; it is resolved once, not every sweep.
func TestAnsweredQuestionResolvesNoticeFromBeforeRestart(t *testing.T) {
	h, q, o := questionHarness() // fresh Sweeper: no notice in memory
	h.f.sessions[0].Metadata.LatestUserPromptAt = q.at.Add(time.Minute)
	h.sweep(t)
	h.sweep(t)
	if len(o.notified) != 0 || len(o.resolved) != 2 {
		t.Fatalf("notified %d, resolved %+v; want 0 and one pass over both prefixes", len(o.notified), o.resolved)
	}
	for i, prefix := range []string{domain.OwnerQuestionSourcePrefix, domain.OwnerQuestionLaneSourcePrefix} {
		if o.resolved[i].SourceKeyPrefix != prefix+"o1:" {
			t.Fatalf("resolution %d = %+v", i, o.resolved[i])
		}
	}
}

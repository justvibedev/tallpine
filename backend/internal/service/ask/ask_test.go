package ask

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// memStore is an in-memory Store with the same atomic contracts as SQLite's.
type memStore struct {
	mu       sync.Mutex
	asks     []domain.OwnerAsk
	sessions map[domain.SessionID]domain.SessionRecord
	lanes    map[domain.SessionID]string
	human    map[string]store.PlanMessageSource
	latest   string
	latestAt time.Time
}

func (m *memStore) BacklogPlanMessageSource(_ context.Context, id string) (store.PlanMessageSource, bool, error) {
	s, ok := m.human[id]
	return s, ok, nil
}
func (m *memStore) LatestHumanProjectMessage(context.Context, domain.ProjectID) (string, time.Time, bool, error) {
	return m.latest, m.latestAt, m.latest != "", nil
}
func (m *memStore) CreateOwnerAsk(_ context.Context, a domain.OwnerAsk, check func([]domain.OwnerAsk, *domain.OwnerAsk) error) (domain.OwnerAsk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := check(append([]domain.OwnerAsk(nil), m.asks...), &a); err != nil {
		return a, err
	}
	a.Ref = "Q" + strconv.Itoa(len(m.asks)+1)
	m.asks = append(m.asks, a)
	return a, nil
}
func (m *memStore) SetOwnerAskAnchor(_ context.Context, id, turn, act string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.asks {
		if m.asks[i].ID == id {
			m.asks[i].AnchorTurnID, m.asks[i].AnchorActivityID = turn, act
		}
	}
	return nil
}
func (m *memStore) GetOwnerAsk(_ context.Context, _ domain.ProjectID, ref string) (domain.OwnerAsk, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.asks {
		if a.Ref == ref {
			return a, true, nil
		}
	}
	return domain.OwnerAsk{}, false, nil
}
func (m *memStore) ListOwnerAsks(context.Context, domain.ProjectID) ([]domain.OwnerAsk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.OwnerAsk(nil), m.asks...), nil
}
func (m *memStore) ListOpenOwnerAsks(ctx context.Context, p domain.ProjectID) ([]domain.OwnerAsk, error) {
	all, _ := m.ListOwnerAsks(ctx, p)
	var out []domain.OwnerAsk
	for _, a := range all {
		if a.State == domain.AskOpen {
			out = append(out, a)
		}
	}
	return out, nil
}
func (m *memStore) SaveOwnerAskTransition(_ context.Context, a domain.OwnerAsk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.asks {
		if m.asks[i].ID == a.ID {
			if m.asks[i].State != domain.AskOpen {
				return domain.ErrAskNotOpen
			}
			a.Ref = m.asks[i].Ref
			m.asks[i] = a
		}
	}
	return nil
}
func (m *memStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := m.sessions[id]
	return r, ok, nil
}
func (m *memStore) GetBacklogItem(_ context.Context, id string) (domain.BacklogItem, bool, error) {
	return domain.BacklogItem{ID: id, Seq: 7}, true, nil
}
func (m *memStore) SessionLane(_ context.Context, id domain.SessionID) (domain.SessionLane, error) {
	return domain.SessionLane{LaneItemID: m.lanes[id]}, nil
}

type fakeAnchor struct {
	err   error
	calls int
}

func (f *fakeAnchor) RecordAsk(context.Context, domain.SessionID, domain.OwnerAsk) (string, string, error) {
	f.calls++
	if f.err != nil {
		return "", "", f.err
	}
	return "turn-1", "act-1", nil
}

type fakeRouter struct {
	sent []string
	err  error
}

func (f *fakeRouter) SendOwnerReply(_ context.Context, _ domain.ProjectID, lane, text string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, lane+"|"+text)
	return nil
}

type fakeNotifier struct{ opened, closed []string }

func (f *fakeNotifier) Opened(_ context.Context, a domain.OwnerAsk) {
	f.opened = append(f.opened, a.Ref)
}
func (f *fakeNotifier) Closed(_ context.Context, a domain.OwnerAsk) {
	f.closed = append(f.closed, a.Ref)
}

type rig struct {
	svc    *Service
	st     *memStore
	anchor *fakeAnchor
	router *fakeRouter
	note   *fakeNotifier
	now    time.Time
}

var (
	orch  = Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "orch-main"}
	laneO = Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "orch-lane"}
	owner = Actor{Kind: domain.BacklogOriginHuman}
)

func newRig() *rig {
	st := &memStore{
		sessions: map[domain.SessionID]domain.SessionRecord{
			"orch-main": {ID: "orch-main", ProjectID: "p1"},
			"orch-lane": {ID: "orch-lane", ProjectID: "p1"},
		},
		lanes: map[domain.SessionID]string{"orch-lane": "B7"},
		human: map[string]store.PlanMessageSource{},
	}
	r := &rig{st: st, anchor: &fakeAnchor{}, router: &fakeRouter{}, note: &fakeNotifier{}, now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	r.svc = New(Deps{Store: st, Anchor: r.anchor, Router: r.router, Notifier: r.note, Now: func() time.Time { return r.now }})
	return r
}

func in(text string) CreateInput {
	return CreateInput{Text: text, Options: []domain.PlanOption{{ID: "a", Label: "Yes"}, {ID: "b", Label: "No"}}, Recommended: "a"}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v; want an API error", err)
	}
	return ae.Code
}

func (r *rig) tick() { r.now = r.now.Add(time.Minute) }

func TestCreate_OrchestratorOnly(t *testing.T) {
	r := newRig()
	for _, k := range []domain.BacklogOriginKind{domain.BacklogOriginWorker, domain.BacklogOriginHuman, domain.BacklogOriginReviewer} {
		_, err := r.svc.Create(context.Background(), "p1", Actor{Kind: k, SessionID: "w1"}, in("Ship it?"))
		if code(t, err) != "ASK_ORCHESTRATOR_ONLY" {
			t.Fatalf("%s: err = %v", k, err)
		}
	}
}

func TestCreate_RefsLaneAnchorAndNotifyOnce(t *testing.T) {
	r := newRig()
	a, err := r.svc.Create(context.Background(), "p1", laneO, in("Ship it?"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Ref != "Q1" || a.Lane != "B7" || a.LaneRef != "B7" || a.State != domain.AskOpen || !a.Anchored() {
		t.Fatalf("ask = %+v", a)
	}
	if len(r.note.opened) != 1 {
		t.Fatalf("opened notifications = %v; want exactly one", r.note.opened)
	}
	// A refused duplicate must not notify again.
	if _, err := r.svc.Create(context.Background(), "p1", orch, in("ship it")); err == nil {
		t.Fatal("duplicate accepted")
	}
	if len(r.note.opened) != 1 {
		t.Fatalf("opened after refused duplicate = %v", r.note.opened)
	}
}

func TestCreate_NoTurnHasNoAnchor(t *testing.T) {
	r := newRig()
	r.anchor.err = errors.New("no active turn")
	a, err := r.svc.Create(context.Background(), "p1", orch, in("Ship it?"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Anchored() || a.AnchorTurnID != "" {
		t.Fatalf("ask = %+v; want no anchor", a)
	}
	got, _, _ := r.st.GetOwnerAsk(context.Background(), "p1", "Q1")
	if got.State != domain.AskOpen || got.Anchored() {
		t.Fatalf("stored = %+v", got)
	}
}

func TestCreate_DedupeRules(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	first, _ := r.svc.Create(ctx, "p1", orch, in("Use Postgres?"))
	// Open: refused by normalized text from any orchestrator.
	if _, err := r.svc.Create(ctx, "p1", laneO, in("use postgres")); code(t, err) != "ASK_ALREADY_OPEN" {
		t.Fatalf("err = %v", err)
	}
	reask := in("Use Postgres?")
	reask.Reask = "schema changed"
	if _, err := r.svc.Create(ctx, "p1", orch, reask); code(t, err) != "ASK_ALREADY_OPEN" {
		t.Fatalf("--reask on an open match: err = %v; want ASK_ALREADY_OPEN", err)
	}
	// About key matches an open ask with different text.
	withAbout := in("Another question entirely")
	withAbout.About = "B62"
	if _, err := r.svc.Create(ctx, "p1", orch, withAbout); err != nil {
		t.Fatal(err)
	}
	same := in("Third wording")
	same.About = "b62"
	if _, err := r.svc.Create(ctx, "p1", laneO, same); code(t, err) != "ASK_ALREADY_OPEN" {
		t.Fatalf("about match: err = %v", err)
	}

	// Answered: refused until --reask, then linked.
	r.tick()
	if _, _, err := r.svc.Answer(ctx, "p1", owner, first.Ref, AnswerInput{Option: "a"}); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.Create(ctx, "p1", orch, in("Use postgres?"))
	if code(t, err) != "ASK_ALREADY_ANSWERED" {
		t.Fatalf("err = %v", err)
	}
	var ae *apierr.Error
	errors.As(err, &ae)
	if ae.Details["answer"] != "Yes" || ae.Details["ref"] != "Q1" {
		t.Fatalf("details = %v; want the earlier ask and answer", ae.Details)
	}
	again, err := r.svc.Create(ctx, "p1", orch, reask)
	if err != nil || again.ReaskOf != "Q1" || again.ReaskReason != "schema changed" {
		t.Fatalf("reask = %+v err %v", again, err)
	}
}

func TestCreate_WithdrawnDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Use Postgres?"))
	if _, err := r.svc.Withdraw(ctx, "p1", orch, a.Ref, "moot"); err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Create(ctx, "p1", orch, in("Use Postgres?"))
	if err != nil || b.ReaskOf != "" {
		t.Fatalf("ask after withdraw = %+v err %v", b, err)
	}
}

func TestCreate_OpenCapPerLane(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	for i := 0; i < domain.MaxOpenAsks; i++ {
		if _, err := r.svc.Create(ctx, "p1", orch, in("Question number "+strconv.Itoa(i))); err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
	}
	if _, err := r.svc.Create(ctx, "p1", orch, in("The eleventh")); code(t, err) != "ASK_LIMIT" {
		t.Fatalf("err = %v; want ASK_LIMIT", err)
	}
	// Another lane has its own budget.
	if _, err := r.svc.Create(ctx, "p1", laneO, in("The eleventh")); err != nil {
		t.Fatalf("lane ask: %v", err)
	}
}

func TestAnswer_OwnerPostsToLaneChat(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", laneO, in("Ship it?"))
	got, _, err := r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Option: "b"})
	if err != nil || got.State != domain.AskAnswered || got.Answer.Text != "No" {
		t.Fatalf("answer = %+v err %v", got, err)
	}
	if len(r.router.sent) != 1 || r.router.sent[0] != "B7|Re Q1: No" {
		t.Fatalf("sent = %v", r.router.sent)
	}
	if len(r.note.closed) != 1 {
		t.Fatalf("closed = %v", r.note.closed)
	}
}

func TestAnswer_OtherTextAndValidation(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	for _, bad := range []AnswerInput{{}, {Option: "a", Other: "x"}, {Option: "zzz"}} {
		if _, _, err := r.svc.Answer(ctx, "p1", owner, a.Ref, bad); code(t, err) != "ASK_ANSWER_INVALID" {
			t.Fatalf("%+v: err = %v", bad, err)
		}
	}
	got, _, err := r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Other: "Only on Friday"})
	if err != nil || got.Answer.Other != "Only on Friday" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if r.router.sent[0] != "|Re Q1: Only on Friday" {
		t.Fatalf("sent = %v", r.router.sent)
	}
}

func TestAnswer_RelayNeedsRealFreshOwnerMessage(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	relay := func(id string) error {
		_, _, err := r.svc.Answer(ctx, "p1", orch, a.Ref, AnswerInput{Option: "a", OwnerMessage: id})
		return err
	}
	if code(t, relay("")) != "ASK_OWNER_MESSAGE_REQUIRED" {
		t.Fatal("missing --owner-message accepted")
	}
	if code(t, relay("last")) != "ASK_OWNER_MESSAGE_STALE" {
		t.Fatal("no owner message at all accepted")
	}
	r.st.human["old"] = store.PlanMessageSource{Human: true, ProjectID: "p1", At: a.CreatedAt.Add(-time.Hour)}
	r.st.human["agent"] = store.PlanMessageSource{Human: false, ProjectID: "p1", At: a.CreatedAt.Add(time.Hour)}
	r.st.human["foreign"] = store.PlanMessageSource{Human: true, ProjectID: "p2", At: a.CreatedAt.Add(time.Hour)}
	for id, want := range map[string]string{"old": "ASK_OWNER_MESSAGE_STALE", "agent": "ASK_OWNER_MESSAGE_INVALID", "foreign": "ASK_OWNER_MESSAGE_INVALID", "nope": "ASK_OWNER_MESSAGE_INVALID"} {
		if got := code(t, relay(id)); got != want {
			t.Fatalf("%s: code %s; want %s", id, got, want)
		}
	}
	r.st.human["fresh"] = store.PlanMessageSource{Human: true, ProjectID: "p1", At: a.CreatedAt.Add(time.Minute)}
	if err := relay("fresh"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := r.st.GetOwnerAsk(ctx, "p1", "Q1")
	if got.State != domain.AskAnswered || !got.Answer.Relayed || got.Answer.MessageID != "fresh" {
		t.Fatalf("stored = %+v", got)
	}
	if len(r.router.sent) != 0 {
		t.Fatalf("a relayed answer must not be posted back to chat: %v", r.router.sent)
	}
}

func TestAnswer_WorkerForbidden(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	_, _, err := r.svc.Answer(ctx, "p1", Actor{Kind: domain.BacklogOriginWorker, SessionID: "w"}, a.Ref, AnswerInput{Option: "a"})
	if code(t, err) != "ASK_ANSWER_FORBIDDEN" {
		t.Fatalf("err = %v", err)
	}
}

func TestAnswer_RaceOneWins(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, opt := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Option: opt})
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if code(t, err) != "ASK_NOT_OPEN" {
			t.Fatalf("loser err = %v", err)
		}
	}
	if wins != 1 || len(r.router.sent) != 1 {
		t.Fatalf("wins %d, sent %v; want one winner and one delivery", wins, r.router.sent)
	}
	// A late answer on a closed ask fails too.
	if _, _, err := r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Option: "a"}); code(t, err) != "ASK_NOT_OPEN" {
		t.Fatalf("err = %v", err)
	}
}

func TestWithdrawLane_OnlyThatLane(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	main, _ := r.svc.Create(ctx, "p1", orch, in("Main question"))
	lane, _ := r.svc.Create(ctx, "p1", laneO, in("Lane question"))
	if n := r.svc.WithdrawLane(ctx, "p1", "B7"); n != 1 {
		t.Fatalf("withdrew %d; want 1", n)
	}
	gl, _, _ := r.st.GetOwnerAsk(ctx, "p1", lane.Ref)
	gm, _, _ := r.st.GetOwnerAsk(ctx, "p1", main.Ref)
	if gl.State != domain.AskWithdrawn || gm.State != domain.AskOpen {
		t.Fatalf("lane %s, main %s", gl.State, gm.State)
	}
}

func TestNotFound(t *testing.T) {
	r := newRig()
	if _, _, err := r.svc.Answer(context.Background(), "p1", owner, "Q9", AnswerInput{Option: "a"}); code(t, err) != "ASK_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
}

// A card answer that cannot be posted is still recorded, and the caller is told
// so the owner can say it in chat; a delivered one reports no failure.
func TestAnswer_FailedDeliveryIsReportedNotLost(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	a, _ := r.svc.Create(ctx, "p1", orch, in("Ship it?"))
	r.router.err = errors.New("no live orchestrator")
	got, failed, err := r.svc.Answer(ctx, "p1", owner, a.Ref, AnswerInput{Option: "a"})
	if err != nil || !failed || got.State != domain.AskAnswered {
		t.Fatalf("got %+v failed %v err %v", got, failed, err)
	}
	stored, _, _ := r.st.GetOwnerAsk(ctx, "p1", a.Ref)
	if stored.State != domain.AskAnswered || stored.Answer.Text != "Yes" {
		t.Fatalf("answer lost: %+v", stored)
	}
	b, _ := r.svc.Create(ctx, "p1", orch, in("Another?"))
	r.router.err = nil
	if _, failed, err := r.svc.Answer(ctx, "p1", owner, b.Ref, AnswerInput{Option: "a"}); err != nil || failed {
		t.Fatalf("delivered answer: failed %v err %v", failed, err)
	}
}

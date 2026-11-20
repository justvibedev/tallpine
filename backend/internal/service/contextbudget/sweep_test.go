package contextbudget

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const proj = "p"

// fakeWork records the backlog item and worker a Yes asks for.
type fakeWork struct {
	mu      sync.Mutex
	items   []ItemInput
	workers []Item
	briefs  []string
	failErr error
}

func (w *fakeWork) FileItem(_ context.Context, _ domain.ProjectID, in ItemInput) (Item, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failErr != nil {
		return Item{}, w.failErr
	}
	w.items = append(w.items, in)
	return Item{ID: "item-1", Ref: "B1"}, nil
}

func (w *fakeWork) StartWorker(_ context.Context, _ domain.ProjectID, it Item, brief string) (domain.SessionID, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workers = append(w.workers, it)
	w.briefs = append(w.briefs, brief)
	return "w1", nil
}

// router records any message the ask service would send to an orchestrator.
type router struct{ sent []string }

func (r *router) SendOwnerReply(_ context.Context, _ domain.ProjectID, _, text string) error {
	r.sent = append(r.sent, text)
	return nil
}

// extra is a finder that reports n fixed findings, to exercise the card cap.
type extra struct{ n int }

func (e extra) Find(context.Context, domain.ProjectRecord, Measurement) ([]domain.ContextHealthFinding, error) {
	var out []domain.ContextHealthFinding
	for i := range e.n {
		fp := "extra-" + string(rune('a'+i))
		out = append(out, domain.ContextHealthFinding{Kind: domain.ContextInstructionBudget, Fingerprint: fp, MeasuredTokens: 2000 + 1000*i, BudgetTokens: 10, Evidence: "extra " + fp})
	}
	return out, nil
}

// rigStore is the real store with the usage read stubbed: filling the usage
// tables takes a full ingestion, which the store's own test covers.
type rigStore struct {
	*sqlite.Store
	firstTurn []store.FirstTurnInput
}

func (s *rigStore) ListFirstTurnInputs(context.Context, domain.ProjectID, time.Time, int) ([]store.FirstTurnInput, error) {
	return s.firstTurn, nil
}

type rig struct {
	t       *testing.T
	st      *rigStore
	asks    *asksvc.Service
	work    *fakeWork
	router  *router
	now     time.Time
	claude  string // CLAUDE.md content
	memory  *Memory
	prompt  string
	cfg     domain.ProjectConfig
	finder  []Finder
	root    string
	analyst *Analyst
}

// newRig has a 1000-token budget; CLAUDE.md of 3300 bytes is ~1100 tokens.
func newRig(t *testing.T, on bool) *rig {
	t.Helper()
	r := &rig{t: t, st: &rigStore{Store: sqlitetest.MustOpen(t)}, work: &fakeWork{}, router: &router{}, now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), claude: strings.Repeat("a", 3300), root: t.TempDir()}
	r.cfg = domain.ProjectConfig{ContextHealth: on, ContextBudgetTokens: 1000}
	r.upsertProject()
	h := &Handler{Store: r.st, Work: r.work, Now: func() time.Time { return r.now }}
	r.asks = asksvc.New(asksvc.Deps{Store: r.st, Router: r.router, Now: func() time.Time { return r.now },
		AnswerHandlers: map[string]func(context.Context, domain.OwnerAsk) error{domain.AskOriginContextHealth: h.Answered}})
	return r
}

func (r *rig) upsertProject() {
	r.t.Helper()
	if err := r.st.UpsertProject(context.Background(), domain.ProjectRecord{ID: proj, Path: "/tmp/p", RegisteredAt: r.now, Config: r.cfg}); err != nil {
		r.t.Fatal(err)
	}
}

// sweeper builds a new Sweeper over the same store, as after a daemon restart.
func (r *rig) sweeper() *Sweeper {
	d := Deps{Store: r.st, Asks: r.asks, Finders: r.finder, Now: func() time.Time { return r.now },
		ArtifactRoot: r.root, Analyst: r.analyst,
		Reader: func(context.Context, domain.ProjectRecord) (Source, error) {
			return Source{Read: func(_ context.Context, p string) ([]byte, bool, error) {
				if p == "CLAUDE.md" {
					return []byte(r.claude), true, nil
				}
				return nil, false, nil
			}}, nil
		},
		Memory: func(context.Context, domain.ProjectRecord) (*Memory, error) { return r.memory, nil },
	}
	if r.prompt != "" {
		d.Prompt = func(context.Context, domain.ProjectRecord, domain.SessionKind, domain.AgentHarness) (string, error) {
			return r.prompt, nil
		}
	}
	return New(d)
}

// sweep advances the clock by d and sweeps with a fresh Sweeper.
func (r *rig) sweep(d time.Duration) {
	r.t.Helper()
	r.now = r.now.Add(d)
	if err := r.sweeper().Sweep(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) asksList() []domain.OwnerAsk {
	r.t.Helper()
	l, err := r.asks.List(context.Background(), proj, false)
	if err != nil {
		r.t.Fatal(err)
	}
	return l
}

func (r *rig) findings() map[string]domain.ContextHealthFinding {
	r.t.Helper()
	l, err := r.st.ListFindings(context.Background(), proj)
	if err != nil {
		r.t.Fatal(err)
	}
	m := map[string]domain.ContextHealthFinding{}
	for _, f := range l {
		m[f.Fingerprint] = f
	}
	return m
}

func (r *rig) answer(ref, option, other string) {
	r.t.Helper()
	_, failed, err := r.asks.Answer(context.Background(), proj, asksvc.Actor{Kind: domain.BacklogOriginHuman}, ref, asksvc.AnswerInput{Option: option, Other: other})
	if err != nil || failed {
		r.t.Fatalf("answer %s: failed=%v err=%v", ref, failed, err)
	}
}

func TestOffByDefaultMeasuresAndAsksNothing(t *testing.T) {
	r := newRig(t, false)
	r.memory = &Memory{IndexTokens: 99999}
	r.sweep(0)
	if len(r.asksList()) != 0 || len(r.findings()) != 0 {
		t.Fatalf("asks=%v findings=%v, want none while contextHealth is off", r.asksList(), r.findings())
	}
	if _, ok, _ := r.st.GetRun(context.Background(), proj); ok {
		t.Fatal("a run was recorded for a project that did not opt in")
	}
}

func TestOverBudgetFilesOneDaemonCardAndRestartDoesNotRepeatIt(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	asks := r.asksList()
	if len(asks) != 1 {
		t.Fatalf("asks = %d, want 1", len(asks))
	}
	a := asks[0]
	if a.Origin != domain.AskOriginContextHealth || a.Lane != "" || a.SessionID != "" || a.State != domain.AskOpen || a.About != "context-health:instruction_budget" {
		t.Fatalf("ask = %+v", a)
	}
	f := r.findings()["instruction_budget"]
	if f.State != domain.ContextFindingOpen || f.AskID != a.ID || f.MeasuredTokens != 1100 {
		t.Fatalf("finding = %+v", f)
	}
	// The next days, and a new Sweeper (a daemon restart), file nothing more.
	r.sweep(25 * time.Hour)
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks after restarts = %d, want 1", got)
	}
}

func TestDailyGateSkipsASecondCheckTheSameDay(t *testing.T) {
	r := newRig(t, true)
	sw := r.sweeper()
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.claude = strings.Repeat("a", 30000) // grown, but not due yet
	r.now = r.now.Add(time.Hour)
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.findings()["instruction_budget"].MeasuredTokens; got != 1100 {
		t.Fatalf("measured = %d, want the first day's 1100", got)
	}
}

func TestYesFilesBacklogItemAndWorkerWithoutMessagingAnOrchestrator(t *testing.T) {
	r := newRig(t, true)
	r.prompt = strings.Repeat("p", 6000)
	r.sweep(0)
	r.answer("Q1", "yes", "")
	if len(r.work.items) != 1 || len(r.work.workers) != 1 || r.work.workers[0].ID != "item-1" {
		t.Fatalf("items=%v workers=%v", r.work.items, r.work.workers)
	}
	if !strings.Contains(r.work.briefs[0], "Never drop or weaken a behavioral rule") || !strings.Contains(r.work.briefs[0], "evidence.md") {
		t.Fatalf("brief = %q", r.work.briefs[0])
	}
	f := r.findings()["instruction_budget"]
	if f.State != domain.ContextFindingAccepted || f.BacklogItemID != "item-1" {
		t.Fatalf("finding = %+v", f)
	}
	if len(r.router.sent) != 0 {
		t.Fatalf("the router was called: %v", r.router.sent)
	}
	// An accepted finding that is still over budget files no second card.
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want 1", got)
	}
}

func TestNoSuppressesUntilTenPercentOrThirtyDays(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.answer("Q1", "no", "")
	if len(r.router.sent) != 0 || len(r.work.workers) != 0 {
		t.Fatalf("a No reached the router or a worker: %v %v", r.router.sent, r.work.workers)
	}
	if f := r.findings()["instruction_budget"]; f.State != domain.ContextFindingDeclined || f.DeclinedTokens != 1100 {
		t.Fatalf("finding = %+v", f)
	}
	r.claude = strings.Repeat("a", 3300+297) // +99 tokens: under +10% of the 1000 budget
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want still 1 (suppressed)", got)
	}
	r.claude = strings.Repeat("a", 3300+300) // +100 tokens: the regrowth step
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 2 {
		t.Fatalf("asks = %d, want a new card after +10%% growth", got)
	}
}

func TestNoLapsesAfterThirtyDays(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.answer("Q1", "no", "")
	r.sweep(29 * 24 * time.Hour)
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want 1 inside 30 days", got)
	}
	r.sweep(2 * 24 * time.Hour)
	if got := len(r.asksList()); got != 2 {
		t.Fatalf("asks = %d, want a new card after 30 days", got)
	}
}

func TestFreeTextAnswerCountsAsNoAndKeepsTheReason(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.answer("Q1", "", "we split it next sprint")
	f := r.findings()["instruction_budget"]
	if f.State != domain.ContextFindingDeclined || f.DeclineReason != "we split it next sprint" || len(r.work.workers) != 0 {
		t.Fatalf("finding = %+v workers=%v", f, r.work.workers)
	}
}

func TestThreeOpenCardsCapQueuesTheFourth(t *testing.T) {
	r := newRig(t, true)
	r.finder = []Finder{budgetFinder{}, extra{n: 3}} // plus the instruction finding: four
	r.sweep(0)
	open, queued := 0, 0
	for _, f := range r.findings() {
		switch f.State {
		case domain.ContextFindingOpen:
			open++
		case domain.ContextFindingQueued:
			queued++
		}
	}
	if open != 3 || queued != 1 || len(r.asksList()) != 3 {
		t.Fatalf("open=%d queued=%d asks=%d, want 3/1/3", open, queued, len(r.asksList()))
	}
	// Answering one frees a slot; the queued finding gets its card on the next check.
	r.answer("Q1", "no", "")
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 4 {
		t.Fatalf("asks = %d, want the queued card filed", got)
	}
}

func TestBackUnderBudgetWithdrawsTheCard(t *testing.T) {
	r := newRig(t, true)
	r.finder = []Finder{budgetFinder{}, extra{n: 3}}
	r.sweep(0)
	r.claude = "short"
	r.finder = []Finder{budgetFinder{}} // the extras are gone too
	r.sweep(25 * time.Hour)
	for _, a := range r.asksList() {
		if a.State != domain.AskWithdrawn || a.WithdrawReason == "" {
			t.Fatalf("ask %s = %s %q, want withdrawn with a reason", a.Ref, a.State, a.WithdrawReason)
		}
	}
	for fp, f := range r.findings() {
		if f.State != domain.ContextFindingResolved {
			t.Fatalf("finding %s = %s, want resolved", fp, f.State)
		}
	}
	if len(r.router.sent) != 0 {
		t.Fatalf("router called: %v", r.router.sent)
	}
}

func TestMemoryIndexOverBudgetFilesACard(t *testing.T) {
	r := newRig(t, true)
	r.claude = "short"
	r.memory = &Memory{Dir: "/m", IndexTokens: 9000, IndexBytes: 27000, Files: 40, DirTokens: 50000}
	r.sweep(0)
	f, ok := r.findings()["memory_budget"]
	if !ok || f.State != domain.ContextFindingOpen || f.MeasuredTokens != 9000 || f.BudgetTokens != domain.DefaultContextMemoryBudgetTokens {
		t.Fatalf("finding = %+v ok=%v", f, ok)
	}
	if _, bad := r.findings()["instruction_budget"]; bad || len(r.asksList()) != 1 {
		t.Fatalf("findings = %v asks = %d, want only the memory card", r.findings(), len(r.asksList()))
	}
	r.memory = &Memory{Dir: "/m", IndexTokens: 7000}
	r.sweep(25 * time.Hour)
	if a := r.asksList()[0]; a.State != domain.AskWithdrawn {
		t.Fatalf("memory card = %s after the index shrank", a.State)
	}
}

func TestEvidenceHasCalibrationAndPromptShareButThePromptGetsNoCard(t *testing.T) {
	r := newRig(t, true)
	r.prompt = strings.Repeat("p", 3300)
	r.st.firstTurn = []store.FirstTurnInput{{Harness: "claude-code", Kind: "worker", Tokens: 40000}}
	r.sweep(0)
	ev := r.findings()["instruction_budget"].Evidence
	for _, want := range []string{"## Tallpine system prompt", "50% of prompt plus instruction files", "## Calibration", "claude-code: median ~40.0K"} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence lacks %q:\n%s", want, ev)
		}
	}
	// A huge prompt with small instruction files is not a finding.
	r2 := newRig(t, true)
	r2.claude = "short"
	r2.prompt = strings.Repeat("p", 300000)
	r2.sweep(0)
	if len(r2.findings()) != 0 || len(r2.asksList()) != 0 {
		t.Fatalf("a Tallpine-prompt-only overrun made findings=%v asks=%d", r2.findings(), len(r2.asksList()))
	}
}

func TestOwnerWithdrawnCardIsReadAsNoAndFreesItsSlot(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	if _, err := r.asks.Withdraw(context.Background(), proj, asksvc.Actor{Kind: domain.BacklogOriginHuman}, "Q1", "not now"); err != nil {
		t.Fatal(err)
	}
	r.sweep(25 * time.Hour)
	f := r.findings()["instruction_budget"]
	if f.State != domain.ContextFindingDeclined || f.DeclinedTokens != 1100 {
		t.Fatalf("finding = %+v, want declined after the owner withdrew the card", f)
	}
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want no new card inside the suppression window", got)
	}
}

func TestAnsweredCardWhoseHandlerFailedIsQueuedForANewCard(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.work.failErr = errors.New("backlog down")
	if _, failed, err := r.asks.Answer(context.Background(), proj, asksvc.Actor{Kind: domain.BacklogOriginHuman}, "Q1", asksvc.AnswerInput{Option: "yes"}); err != nil || !failed {
		t.Fatalf("answer failed=%v err=%v, want the handler failure reported", failed, err)
	}
	if f := r.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen {
		t.Fatalf("finding = %s, want it still open before the next check", f.State)
	}
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 2 {
		t.Fatalf("asks = %d, want a fresh card after the failed Yes", got)
	}
	if f := r.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen || f.AskRef != "Q2" {
		t.Fatalf("finding = %+v, want open on Q2", f)
	}
}

func TestDippingUnderBudgetDoesNotLoseANo(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.answer("Q1", "no", "")
	r.claude = "short"
	r.sweep(25 * time.Hour) // under budget: resolved
	r.claude = strings.Repeat("a", 3300)
	r.sweep(25 * time.Hour) // back over inside the 30 days and +10%
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want the No kept across the dip", got)
	}
	if f := r.findings()["instruction_budget"]; f.State != domain.ContextFindingDeclined {
		t.Fatalf("finding = %s, want declined", f.State)
	}
	r.sweep(31 * 24 * time.Hour)
	if got := len(r.asksList()); got != 2 {
		t.Fatalf("asks = %d, want a new card once the 30 days passed", got)
	}
}

func TestACardFiledBeforeTheDaemonStoppedIsAdopted(t *testing.T) {
	r := newRig(t, true)
	orphan, err := r.asks.CreateSystem(context.Background(), proj, domain.AskOriginContextHealth, askInput(domain.ContextHealthFinding{
		Kind: domain.ContextInstructionBudget, Fingerprint: "instruction_budget", MeasuredTokens: 1100, BudgetTokens: 1000, Evidence: "ev"}))
	if err != nil {
		t.Fatal(err)
	}
	r.sweep(0)
	f := r.findings()["instruction_budget"]
	if f.State != domain.ContextFindingOpen || f.AskID != orphan.ID || len(r.asksList()) != 1 {
		t.Fatalf("finding = %+v asks=%d, want the existing card adopted", f, len(r.asksList()))
	}
	r.answer(orphan.Ref, "no", "")
	if r.findings()["instruction_budget"].State != domain.ContextFindingDeclined {
		t.Fatal("an answer to the adopted card did not reach its finding")
	}
}

func TestFullMainChatKeepsFindingsQueuedAndStillFinishesTheRun(t *testing.T) {
	r := newRig(t, true)
	for i := range domain.MaxOpenAsks {
		in := askInput(domain.ContextHealthFinding{Kind: domain.ContextInstructionBudget, MeasuredTokens: 5000 + 1000*i, BudgetTokens: 1000})
		in.About = "other:" + string(rune('a'+i))
		if _, err := r.asks.CreateSystem(context.Background(), proj, domain.AskOriginContextHealth, in); err != nil {
			t.Fatal(err)
		}
	}
	r.sweep(0)
	if f := r.findings()["instruction_budget"]; f.State != domain.ContextFindingQueued {
		t.Fatalf("finding = %s, want queued while the chat is full", f.State)
	}
	if _, ok, _ := r.st.GetRun(context.Background(), proj); !ok {
		t.Fatal("the run was not recorded")
	}
}

// routingStub reports one routing finding while on.
type routingStub struct{ on *bool }

func (s routingStub) Find(context.Context, domain.ProjectRecord, Measurement) ([]domain.ContextHealthFinding, error) {
	if !*s.on {
		return nil, nil
	}
	return []domain.ContextHealthFinding{{Kind: domain.ContextRoutingPointer, Fingerprint: DigestFingerprint, MeasuredTokens: 3, BudgetTokens: routingBudget, Evidence: "a.md read"}}, nil
}

func TestRoutingCardWithdrawnWhenEvidenceFadesSaysWhy(t *testing.T) {
	r := newRig(t, true)
	on := true
	r.finder = []Finder{routingStub{&on}}
	r.sweep(0)
	on = false
	r.sweep(25 * time.Hour)
	asks := r.asksList()
	if len(asks) != 1 || asks[0].State != domain.AskWithdrawn || !strings.Contains(asks[0].WithdrawReason, "No doc-pointer gaps left") {
		t.Fatalf("asks = %+v, want one withdrawn with the routing reason", asks)
	}
}

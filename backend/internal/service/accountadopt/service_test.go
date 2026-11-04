package accountadopt

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var ctx = context.Background()

func batchOf(t *testing.T, store *memAdoptions, id string) domain.AccountAdoption {
	t.Helper()
	b, _, _ := store.GetAccountAdoption(ctx, id)
	return b
}

// Three sessions, one busy, under wait: two move, the busy one is reported
// waiting and moves once its turn ends.
func TestWaitPolicyReportsTheBusySessionPendingThenMoved(t *testing.T) {
	svc, w, store := fixture(t)
	for i, id := range []string{"s1", "s2", "s3"} {
		w.session(id, domain.KindWorker, i, domain.ActivityIdle)
	}
	w.initial["s2"] = domain.SessionInterfaceTransitionDraining
	b, replay, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil || replay {
		t.Fatalf("adopt = %v replay=%v", err, replay)
	}
	eventually(t, "s1 and s3 started", func() bool { return len(w.callLog()) >= 4 })
	w.settle("s1", domain.SessionInterfaceTransitionCompleted)
	w.settle("s3", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "two moved, one waiting", func() bool {
		st := states(batchOf(t, store, b.ID))
		return st["s1"] == domain.AccountItemMoved && st["s3"] == domain.AccountItemMoved && st["s2"] == domain.AccountItemWaiting
	})
	if got := batchOf(t, store, b.ID).State; got != domain.AccountAdoptionRunning {
		t.Fatalf("batch with a waiting session = %q, want running", got)
	}
	w.settle("s2", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "batch completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	if st := states(batchOf(t, store, b.ID)); st["s2"] != domain.AccountItemMoved {
		t.Fatalf("busy session = %q after its turn ended", st["s2"])
	}
}

// A failed preflight leaves that session on its old account and the batch goes on.
func TestFailedPreflightDoesNotStopTheBatch(t *testing.T) {
	svc, w, store := fixture(t)
	for i, id := range []string{"s1", "s2", "s3", "s4"} {
		w.session(id, domain.KindWorker, i, domain.ActivityIdle)
		w.initial[domain.SessionID(id)] = domain.SessionInterfaceTransitionCompleted
	}
	w.startErr["s2"] = errors.New("preflight: login lost")    // refused to start
	w.initial["s3"] = domain.SessionInterfaceTransitionFailed // failed after it started
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "batch completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	got := batchOf(t, store, b.ID)
	st := states(got)
	if st["s1"] != domain.AccountItemMoved || st["s4"] != domain.AccountItemMoved || st["s2"] != domain.AccountItemFailed || st["s3"] != domain.AccountItemFailed {
		t.Fatalf("outcomes = %v", st)
	}
	for _, it := range got.Items {
		if it.State == domain.AccountItemFailed && (it.ErrorCode == "" || it.ErrorMessage == "") {
			t.Fatalf("failed item %s has no reason: %+v", it.SessionID, it)
		}
	}
}

// The same (account, key) twice, one after the other and at once, queues once.
func TestRepeatedRequestDoesNotEnqueueTwice(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityIdle)
	w.session("s2", domain.KindWorker, 1, domain.ActivityIdle)
	var wg sync.WaitGroup
	ids := make([]string, 4)
	replays := make([]bool, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, replay, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "same")
			if err != nil {
				t.Error(err)
			}
			ids[i], replays[i] = b.ID, replay
		}()
	}
	wg.Wait()
	first := 0
	for i := range ids {
		if ids[i] != ids[0] {
			t.Fatalf("batches differ: %v", ids)
		}
		if !replays[i] {
			first++
		}
	}
	if first != 1 {
		t.Fatalf("%d requests created a batch, want 1", first)
	}
	eventually(t, "both started", func() bool { return len(w.callLog()) >= 3 })
	w.settle("s1", domain.SessionInterfaceTransitionCompleted)
	w.settle("s2", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "completed", func() bool { return batchOf(t, store, ids[0]).State == domain.AccountAdoptionCompleted })
	if _, replay, _ := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "same"); !replay {
		t.Fatal("a repeat after completion was not a replay")
	}
	starts := 0
	for _, c := range w.callLog() {
		if c == "start:s1" || c == "start:s2" {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("moves started = %d, want 2 (calls %v)", starts, w.callLog())
	}
}

// Another key while a batch of the provider runs is refused naming that batch.
func TestSecondBatchWhileRunningIsRefused(t *testing.T) {
	svc, w, _ := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityIdle)
	first, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "one")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "two")
	var busy *InProgressError
	if !errors.As(err, &busy) || busy.Batch.ID != first.ID {
		t.Fatalf("second batch = %v, want InProgressError naming %s", err, first.ID)
	}
}

// A split-history (or signed-out) target is refused before anything changes.
func TestRefusedTargetChangesNothing(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityIdle)
	w.validate = &ports.SplitHistoryError{Home: "/tmp/acct-b", Dirs: []string{"projects"}}
	_, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatalf("adopt = %v, want split history", err)
	}
	if w.defaultID != "" || len(w.callLog()) != 0 {
		t.Fatalf("a refused switch changed things: default=%q calls=%v", w.defaultID, w.callLog())
	}
	if _, ok, _ := store.GetLatestAccountAdoption(ctx, "acct-b"); ok {
		t.Fatal("a refused switch stored a batch")
	}
	if _, err := svc.Preview(ctx, "acct-b"); !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatalf("preview = %v, want split history", err)
	}
}

// Orchestrators are moved after every worker has started and settled; a worker
// parked on a busy turn does not hold them back.
func TestOrchestratorsGoLast(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("orch", domain.KindOrchestrator, 0, domain.ActivityIdle) // oldest, still last
	w.session("w1", domain.KindWorker, 1, domain.ActivityIdle)
	w.session("w2", domain.KindWorker, 2, domain.ActivityIdle)
	w.session("w3", domain.KindWorker, 3, domain.ActivityIdle)
	w.initial["w1"] = domain.SessionInterfaceTransitionDraining // parked
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Items[len(b.Items)-1]; got.SessionID != "orch" || !got.IsOrchestrator {
		t.Fatalf("plan order = %+v", b.Items)
	}
	eventually(t, "w1 parked and w2 started", func() bool { return slices.Contains(w.callLog(), "start:w2") })
	if slices.Contains(w.callLog(), "start:orch") {
		t.Fatalf("orchestrator started while a worker was still moving: %v", w.callLog())
	}
	w.settle("w2", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "w3 started", func() bool { return slices.Contains(w.callLog(), "start:w3") })
	if slices.Contains(w.callLog(), "start:orch") {
		t.Fatalf("orchestrator started before the last worker settled: %v", w.callLog())
	}
	w.settle("w3", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "orchestrator started with w1 still parked", func() bool { return slices.Contains(w.callLog(), "start:orch") })
	if st := states(batchOf(t, store, b.ID)); st["w1"] != domain.AccountItemWaiting {
		t.Fatalf("w1 = %q", st["w1"])
	}
}

// At most two moves run at once, and a move parked on a busy turn frees its slot.
func TestSlotsCapMovesAndParkedMovesFreeThem(t *testing.T) {
	svc, w, store := fixture(t)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		w.session(id, domain.KindWorker, i, domain.ActivityIdle)
	}
	w.initial["a"] = domain.SessionInterfaceTransitionDraining
	w.initial["b"] = domain.SessionInterfaceTransitionDraining
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	// a and b park at once, so c and d take the two slots; e waits for one.
	eventually(t, "c and d started", func() bool {
		c := w.callLog()
		return slices.Contains(c, "start:c") && slices.Contains(c, "start:d")
	})
	if slices.Contains(w.callLog(), "start:e") {
		t.Fatalf("a third move started while two were running: %v", w.callLog())
	}
	w.settle("c", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "e started", func() bool { return slices.Contains(w.callLog(), "start:e") })
	for _, id := range []string{"a", "b", "d", "e"} {
		w.settle(id, domain.SessionInterfaceTransitionCompleted)
	}
	eventually(t, "completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	if w.maxFlight > 2 {
		t.Fatalf("up to %d moves ran at once, cap is 2", w.maxFlight)
	}
}

// Only live sessions of the provider that use another account are planned.
func TestPlanSkipsSessionsAlreadyOnTargetTerminatedOrOtherProvider(t *testing.T) {
	svc, w, _ := fixture(t)
	w.session("move", domain.KindWorker, 0, domain.ActivityIdle)
	w.session("on-target", domain.KindWorker, 1, domain.ActivityIdle)
	w.bound["on-target"] = "acct-b"
	w.session("dead", domain.KindWorker, 2, domain.ActivityIdle)
	w.recs[2].IsTerminated = true
	w.session("codex", domain.KindWorker, 3, domain.ActivityIdle)
	w.recs[3].Harness = domain.HarnessCodex
	p, err := svc.Preview(ctx, "acct-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sessions) != 1 || p.Sessions[0].SessionID != "move" {
		t.Fatalf("preview = %+v", p.Sessions)
	}
	if _, _, err := svc.Adopt(ctx, "codex-1", domain.AccountAdoptionWait, "k"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("codex adopt = %v", err)
	}
}

// The default changes before any move starts.
func TestDefaultIsSetBeforeTheFirstMove(t *testing.T) {
	svc, w, _ := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityIdle)
	if _, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "move started", func() bool { return len(w.callLog()) >= 2 })
	if got := w.callLog(); got[0] != "default" || got[1] != "start:s1" {
		t.Fatalf("order = %v", got)
	}
}

// A session whose agent has exited is only rebound: no agent is started.
func TestExitedSessionIsReboundNotLaunched(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("gone", domain.KindWorker, 0, domain.ActivityExited)
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	if st := states(batchOf(t, store, b.ID)); st["gone"] != domain.AccountItemRebound {
		t.Fatalf("exited session = %q", st["gone"])
	}
	if slices.Contains(w.callLog(), "start:gone") || !slices.Contains(w.callLog(), "commit:gone") {
		t.Fatalf("calls = %v", w.callLog())
	}
}

// After a restart Start continues the batch from the store: a started move is
// read, not started again, and queued sessions go on.
func TestStartResumesARunningBatchWithoutRestartingMoves(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityIdle)
	w.session("s2", domain.KindWorker, 1, domain.ActivityIdle)
	now := svc.cfg.Now()
	_ = store.CreateAccountAdoption(ctx, domain.AccountAdoption{
		ID: "old", AccountID: "acct-b", Provider: domain.AgentAccountProviderClaude, Policy: domain.AccountAdoptionWait, RequestKey: "k", CreatedAt: now,
		Items: []domain.AccountAdoptionItem{{SessionID: "s1", Position: 0}, {SessionID: "s2", Position: 1}},
	})
	w.phase["t-s1"] = domain.SessionInterfaceTransitionCompleted
	_ = store.UpdateAccountAdoptionItem(ctx, "old", domain.AccountAdoptionItem{SessionID: "s1", State: domain.AccountItemMoving, TransitionID: "t-s1", UpdatedAt: now})
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "s2 started", func() bool { return slices.Contains(w.callLog(), "start:s2") })
	w.settle("s2", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "completed", func() bool { return batchOf(t, store, "old").State == domain.AccountAdoptionCompleted })
	if slices.Contains(w.callLog(), "start:s1") {
		t.Fatalf("the started move was started again: %v", w.callLog())
	}
	if st := states(batchOf(t, store, "old")); st["s1"] != domain.AccountItemMoved {
		t.Fatalf("s1 = %q", st["s1"])
	}
}

// A free slot is not enough for an orchestrator: a worker still moving holds it back.
func TestOrchestratorWaitsForAMovingWorker(t *testing.T) {
	svc, w, _ := fixture(t)
	w.session("w1", domain.KindWorker, 0, domain.ActivityIdle)
	w.session("orch", domain.KindOrchestrator, 1, domain.ActivityIdle)
	if _, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "w1 started", func() bool { return slices.Contains(w.callLog(), "start:w1") })
	time.Sleep(40 * time.Millisecond) // many polls with a slot free
	if slices.Contains(w.callLog(), "start:orch") {
		t.Fatalf("orchestrator started while w1 was moving: %v", w.callLog())
	}
	w.settle("w1", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "orchestrator started", func() bool { return slices.Contains(w.callLog(), "start:orch") })
}

func TestDefaultSlotsAreTwo(t *testing.T) {
	if got := New(Config{}).cfg.Slots; got != 2 {
		t.Fatalf("default slots = %d, want 2", got)
	}
}

// Wait keeps busy turns alone; stopping them is only for the interrupt policy.
func TestPolicyReachesTheMove(t *testing.T) {
	for policy, want := range map[domain.AccountAdoptionPolicy]domain.SessionInterfaceTransitionPolicy{
		domain.AccountAdoptionWait:      domain.SessionInterfaceTransitionDrain,
		domain.AccountAdoptionInterrupt: domain.SessionInterfaceTransitionInterrupt,
	} {
		svc, w, _ := fixture(t)
		w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
		if _, _, err := svc.Adopt(ctx, "acct-b", policy, "k"); err != nil {
			t.Fatal(err)
		}
		eventually(t, "move started", func() bool { return slices.Contains(w.callLog(), "start:s1") })
		w.mu.Lock()
		got := w.policies["s1"]
		w.mu.Unlock()
		if got != want {
			t.Fatalf("policy %q started the move with %q, want %q", policy, got, want)
		}
	}
}

// A move the batch started before a crash is followed, not failed or restarted.
func TestResumeFollowsAMoveStartedBeforeTheCrash(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
	w.session("s2", domain.KindWorker, 1, domain.ActivityActive)
	now := svc.cfg.Now()
	_ = store.CreateAccountAdoption(ctx, domain.AccountAdoption{
		ID: "old", AccountID: "acct-b", Provider: domain.AgentAccountProviderClaude, Policy: domain.AccountAdoptionWait, RequestKey: "k", CreatedAt: now,
		Items: []domain.AccountAdoptionItem{{SessionID: "s1", Position: 0}, {SessionID: "s2", Position: 1}},
	})
	// s1: recorded as moving, id lost. s2: still queued, but its move was claimed.
	_ = store.UpdateAccountAdoptionItem(ctx, "old", domain.AccountAdoptionItem{SessionID: "s1", State: domain.AccountItemMoving, UpdatedAt: now})
	for _, id := range []domain.SessionID{"s1", "s2"} {
		w.active[id] = domain.SessionInterfaceTransition{ID: "t-" + string(id), SessionID: id, TargetAccountID: "acct-b"}
		w.phase["t-"+string(id)] = domain.SessionInterfaceTransitionDraining
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both followed as waiting", func() bool {
		st := states(batchOf(t, store, "old"))
		return st["s1"] == domain.AccountItemWaiting && st["s2"] == domain.AccountItemWaiting
	})
	w.settle("s1", domain.SessionInterfaceTransitionCompleted)
	w.settle("s2", domain.SessionInterfaceTransitionCompleted)
	eventually(t, "completed", func() bool { return batchOf(t, store, "old").State == domain.AccountAdoptionCompleted })
	if st := states(batchOf(t, store, "old")); st["s1"] != domain.AccountItemMoved || st["s2"] != domain.AccountItemMoved {
		t.Fatalf("outcomes = %v", st)
	}
	// s1 was followed from its recorded state, not asked for again.
	if slices.Contains(w.callLog(), "start:s1") {
		t.Fatalf("s1's move was started again: %v", w.callLog())
	}
}

// The item is recorded as moving before the move is asked for.
func TestItemIsMovingBeforeTheMoveStarts(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
	var seen domain.AccountAdoptionItemState
	w.onStart = func() {
		b, _, _ := store.GetLatestAccountAdoption(ctx, "acct-b")
		seen = b.Items[0].State
	}
	if _, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "move started", func() bool { return slices.Contains(w.callLog(), "start:s1") })
	w.mu.Lock()
	got := seen
	w.mu.Unlock()
	if got != domain.AccountItemMoving {
		t.Fatalf("item was %q when the move started, want moving", got)
	}
}

// A move to some other account is not ours to follow.
func TestActiveMoveToAnotherAccountFailsTheItem(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
	w.active["s1"] = domain.SessionInterfaceTransition{ID: "t-other", SessionID: "s1", TargetAccountID: "acct-z"}
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	got := batchOf(t, store, b.ID).Items[0]
	if got.State != domain.AccountItemFailed || got.ErrorCode != "SESSION_SWITCHING" {
		t.Fatalf("item = %+v", got)
	}
}

// A recovery row is final, but a move that committed before it counts as moved.
func TestRecoveryRowIsMovedOnlyWhenTheBindingChanged(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("committed", domain.KindWorker, 0, domain.ActivityActive)
	w.session("lost", domain.KindWorker, 1, domain.ActivityActive)
	w.initial["committed"] = domain.SessionInterfaceTransitionRecovery
	w.initial["lost"] = domain.SessionInterfaceTransitionRecovery
	w.commitOnStart["committed"] = true // the commit lands before the row closes
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	st := states(batchOf(t, store, b.ID))
	if st["committed"] != domain.AccountItemMoved || st["lost"] != domain.AccountItemFailed {
		t.Fatalf("outcomes = %v", st)
	}
}

// A session with a manual move running is not rebound under it.
func TestExitedSessionWithAnActiveMoveIsSkipped(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("gone", domain.KindWorker, 0, domain.ActivityExited)
	w.active["gone"] = domain.SessionInterfaceTransition{ID: "t-m", SessionID: "gone", TargetAccountID: "acct-z"}
	b, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "completed", func() bool { return batchOf(t, store, b.ID).State == domain.AccountAdoptionCompleted })
	if slices.Contains(w.callLog(), "commit:gone") {
		t.Fatalf("rebound under an active move: %v", w.callLog())
	}
	if got := batchOf(t, store, b.ID).Items[0]; got.State != domain.AccountItemSkipped || got.ErrorCode != "SESSION_SWITCHING" {
		t.Fatalf("item = %+v", got)
	}
}

// A batch that cannot be stored leaves the default where it was.
func TestFailedCreateDoesNotChangeTheDefault(t *testing.T) {
	svc, w, store := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
	store.createErr = errors.New("disk full")
	if _, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k"); err == nil {
		t.Fatal("adopt succeeded without a stored batch")
	}
	if w.defaultID != "" || slices.Contains(w.callLog(), "default") {
		t.Fatalf("default changed to %q", w.defaultID)
	}
}

// Wait returns only once the runners have stopped.
func TestWaitStopsAfterTheRunnersEnd(t *testing.T) {
	svc, w, _ := fixture(t)
	w.session("s1", domain.KindWorker, 0, domain.ActivityActive)
	runCtx, cancel := context.WithCancel(ctx)
	if err := svc.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Adopt(ctx, "acct-b", domain.AccountAdoptionWait, "k"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "move started", func() bool { return slices.Contains(w.callLog(), "start:s1") })
	cancel()
	svc.Wait(3 * time.Second)
	svc.mu.Lock()
	left := len(svc.running)
	svc.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d runners still going after Wait", left)
	}
}

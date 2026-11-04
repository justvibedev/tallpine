package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// These run against a real SQLite store rather than a mock, because the point is
// that provider events actually land as durable rows in the right order — which a
// mock store cannot demonstrate.

const (
	testProject = domain.ProjectID("p1")
	testSession = domain.SessionID("p1-1")
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dir)

	ctx := context.Background()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID:           string(testProject),
		Path:         dir,
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID:        testSession,
		ProjectID: testProject,
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return st
}

func fullSnapshotReader(st *sqlite.Store) chatsvc.SnapshotReader {
	return chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshot(ctx, conversationID)
		if err != nil {
			return chatsvc.ConversationRows{}, err
		}
		return chatsvc.ConversationRows{
			Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities,
			BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage,
		}, nil
	})
}

func boundedSnapshotReader(st *sqlite.Store) chatsvc.SnapshotPageReader {
	return chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, id string, before, limit int64) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
		return chatsvc.ConversationRows{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch,
			Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities}, err
	})
}

/* ---- a fake conversation the controller can drive ---------------------- */

type fakeConversation struct {
	// noNetwork makes SandboxAllowsNetwork report a sandbox with no network.
	noNetwork bool
	// networkMode, when set, is the one approval mode whose sandbox has network.
	networkMode            ports.PermissionMode
	events                 chan ports.ChatEvent
	providerConversationID string

	mu        sync.Mutex
	sent      []ports.ChatUserMessage
	caps      ports.ChatCapabilities
	resolved  map[string]ports.ChatDecision
	sendCalls int
	turnSeq   int
	sendErr   error
	// beforeSend runs at the start of SendTurn, outside the fake's lock.
	beforeSend         func()
	onSend             func(providerTurnID string)
	onClose            func()
	closeStarted       chan struct{}
	closeEventsRelease <-chan struct{}
	closeSignalOnce    sync.Once
	closeOnce          sync.Once
}

type deferredConversation struct {
	*fakeConversation
	start func(string) error
}

type stuckConversation struct {
	*fakeConversation
	closeErr error
}

func (s *stuckConversation) Close() error { return s.closeErr }

type terminatingConversation struct {
	*fakeConversation
	terminated atomic.Bool
}

func (c *terminatingConversation) Terminate() error {
	c.terminated.Store(true)
	return c.Close()
}

func (c *terminatingConversation) PreservesProviderOnClose() bool { return true }

type liveReconnectedConversation struct{ *nativeHistoryConversation }

func (c *liveReconnectedConversation) ReconnectedLive() bool { return true }

func (f *deferredConversation) StartDeferredTurn(providerTurnID string) error {
	return f.start(providerTurnID)
}

func (f *deferredConversation) DiscardDeferredTurn(string) {}

func newFakeConversation() *fakeConversation {
	return &fakeConversation{
		events:                 make(chan ports.ChatEvent, 64),
		providerConversationID: "thread-1",
		resolved:               map[string]ports.ChatDecision{},
	}
}

func (f *fakeConversation) ProviderConversationID() string { return f.providerConversationID }

// SandboxAllowsNetwork is the agent sandbox's network, as Codex reports it;
// noNetwork plays a Codex thread in accept-edits or auto.
func (f *fakeConversation) SandboxAllowsNetwork(mode ports.PermissionMode) bool {
	if f.networkMode != "" {
		return mode == f.networkMode
	}
	return !f.noNetwork
}
func (f *fakeConversation) Capabilities() ports.ChatCapabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.caps == nil {
		return productionCaps()
	}
	return maps.Clone(f.caps)
}
func (f *fakeConversation) setCapabilities(caps ports.ChatCapabilities) {
	f.mu.Lock()
	f.caps = maps.Clone(caps)
	f.mu.Unlock()
}
func (f *fakeConversation) Events() <-chan ports.ChatEvent { return f.events }

func (f *fakeConversation) SendTurn(_ context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	f.mu.Lock()
	if hook := f.beforeSend; hook != nil {
		f.mu.Unlock()
		hook()
		f.mu.Lock()
	}
	f.sendCalls++
	if f.sendErr != nil {
		f.mu.Unlock()
		return ports.ChatTurnRef{}, f.sendErr
	}
	f.sent = append(f.sent, msg)
	f.turnSeq++
	providerTurnID := fmt.Sprintf("provider-turn-%d", f.turnSeq)
	onSend := f.onSend
	f.mu.Unlock()
	if onSend != nil {
		onSend(providerTurnID)
	}
	return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
}

// sentTexts is what actually reached the provider, in order. Queuing is only real
// if a message the user typed mid-turn is absent from this until the turn ends.
func (f *fakeConversation) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	texts := make([]string, 0, len(f.sent))
	for _, msg := range f.sent {
		texts = append(texts, msg.Text)
	}
	return texts
}

func (f *fakeConversation) sentMessages() []ports.ChatUserMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.ChatUserMessage(nil), f.sent...)
}

func (f *fakeConversation) sendCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendCalls
}

func (f *fakeConversation) Interrupt(context.Context, string) error { return nil }

func (f *fakeConversation) ResolveRequest(_ context.Context, id string, d ports.ChatDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved[id] = d
	return nil
}

func (f *fakeConversation) Close() error {
	f.mu.Lock()
	onClose := f.onClose
	closeStarted := f.closeStarted
	closeEventsRelease := f.closeEventsRelease
	f.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	if closeStarted != nil {
		f.closeSignalOnce.Do(func() { close(closeStarted) })
	}
	closeEvents := func() {
		f.closeOnce.Do(func() { close(f.events) })
	}
	if closeEventsRelease != nil {
		go func() {
			<-closeEventsRelease
			closeEvents()
		}()
		return nil
	}
	closeEvents()
	return nil
}

func (f *fakeConversation) emit(events ...ports.ChatEvent) {
	for _, event := range events {
		f.events <- event
	}
}

func (f *fakeConversation) decisionFor(id string) (ports.ChatDecision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.resolved[id]
	return d, ok
}

// fakeDriver hands back whatever conversation double the test supplied, so a
// scenario can replace how the provider ANSWERS without reimplementing how it
// streams.
type fakeDriver struct {
	conv      ports.ChatConversation
	startCfg  *ports.ChatStartConfig
	resumeCfg *ports.ChatResumeConfig
	caps      ports.ChatCapabilities
	probe     func() error
	start     func(ports.ChatStartConfig) (ports.ChatConversation, error)
	resume    func(ports.ChatResumeConfig) (ports.ChatConversation, error)
	// supports, when set, is the agent's answer to which permission modes it runs in.
	supports func(ports.PermissionMode) bool
}

func (d fakeDriver) SupportsPermissionMode(mode ports.PermissionMode) bool {
	return d.supports == nil || d.supports(mode)
}

type sequenceDriver struct {
	mu            sync.Mutex
	conversations []ports.ChatConversation
}

func (d *sequenceDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }
func (d *sequenceDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return productionCaps(), nil
}
func (d *sequenceDriver) next() (ports.ChatConversation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.conversations) == 0 {
		return nil, errors.New("no conversation queued")
	}
	conversation := d.conversations[0]
	d.conversations = d.conversations[1:]
	return conversation, nil
}
func (d *sequenceDriver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	return d.next()
}
func (d *sequenceDriver) Resume(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error) {
	return d.next()
}

func (d fakeDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }
func (d fakeDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	if d.probe != nil {
		if err := d.probe(); err != nil {
			return nil, err
		}
	}
	if d.caps != nil {
		return d.caps, nil
	}
	return productionCaps(), nil
}
func (d fakeDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil && !conversationReconnectedLive(d.conv) {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	if d.startCfg != nil {
		*d.startCfg = cfg
	}
	if d.start != nil {
		return d.start(cfg)
	}
	return d.conv, nil
}
func (d fakeDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil && !conversationReconnectedLive(d.conv) {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	if d.resumeCfg != nil {
		*d.resumeCfg = cfg
	}
	if d.resume != nil {
		return d.resume(cfg)
	}
	return d.conv, nil
}

func conversationReconnectedLive(conversation ports.ChatConversation) bool {
	reconnected, ok := conversation.(ports.ChatLiveReconnector)
	return ok && reconnected.ReconnectedLive()
}

type fakeRegistry struct{ driver ports.ChatDriver }

func (r fakeRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) { return r.driver, nil }
func (r fakeRegistry) SupportsChat(domain.AgentHarness) bool                { return true }

func productionCaps() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}

type reviewerModelConversation struct{ *fakeConversation }

func (c reviewerModelConversation) ListModels(context.Context) ([]ports.ChatModel, error) {
	return []ports.ChatModel{{ID: "review-model", DisplayName: "Review model", Efforts: []string{"high"}}}, nil
}

func seedProjectConversationWithProviderHistory(
	t *testing.T,
	st *sqlite.Store,
	conversationID string,
	now time.Time,
) (domain.ConversationRecord, domain.ConversationBranch) {
	t.Helper()
	ctx := context.Background()
	record, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "source-provider-thread"
	record.Metadata.ControllerGeneration = "source-generation"
	if err := st.UpdateSession(ctx, record); err != nil {
		t.Fatalf("seed source provider owner: %v", err)
	}
	conversation, err := st.CreateConversation(ctx, conversationID,
		domain.ConversationScopeProject, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
		ID: "source-history", Kind: domain.ActivityKindSystem,
		Status: domain.ActivityStatusCompleted, Summary: "prior provider history",
		ProviderItemID: "source-provider-history",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("seed provider history: %v", err)
	}
	conversation, err = st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	active, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatalf("ConversationBranch: %v", err)
	}
	return conversation, active
}

type harness struct {
	svc *chatsvc.Service
	// logs captures the service's log output.
	logs      *lockedBuffer
	st        *sqlite.Store
	conv      *fakeConversation
	ctrl      *chatsvc.Controller
	activity  *recordingActivity
	hostStops atomic.Int32
	// accountChecks counts requests to read the session's account again.
	accountChecks atomic.Int32
	renders       *attachmentstore.Store
	rendersDir    string
	// reconciled lists the sessions whose output type a save asked to update;
	// reconcileErr is what that update returns.
	reconciled   []domain.SessionID
	reconcileErr error

	clockMu sync.Mutex
	clock   time.Time
}

// advance moves the injected clock. Needed where an ordering rule is expressed in
// timestamps — the queue cancellation cutoff — rather than in call order.
func (h *harness) advance(d time.Duration) {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	h.clock = h.clock.Add(d)
}

func (h *harness) now() time.Time {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	return h.clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithConversation(t, nil)
}

// newHarnessWithConversation lets a test supply its own provider double, for the
// cases where the interesting behavior is how the provider answers rather than
// what it streams. A nil conv gets the plain fake.
func newHarnessWithConversation(t *testing.T, conv ports.ChatConversation) *harness {
	return newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store { return st })
}

func newHarnessWithConversationAndStore(
	t *testing.T,
	conv ports.ChatConversation,
	wrapStore func(*sqlite.Store) chatsvc.Store,
) *harness {
	return newHarnessWithConversationAndStoreForHarness(t, conv, wrapStore, domain.HarnessCodex)
}

func newHarnessForHarness(t *testing.T, agentHarness domain.AgentHarness) *harness {
	t.Helper()
	return newHarnessWithConversationAndStoreForHarness(t, nil, func(st *sqlite.Store) chatsvc.Store { return st }, agentHarness)
}

func newHarnessWithConversationAndStoreForHarness(
	t *testing.T,
	conv ports.ChatConversation,
	wrapStore func(*sqlite.Store) chatsvc.Store,
	agentHarness domain.AgentHarness,
) *harness {
	t.Helper()
	st := openStore(t)
	if agentHarness != domain.HarnessCodex {
		record, found, err := st.GetSession(context.Background(), testSession)
		if err != nil || !found {
			t.Fatalf("read seeded session: found=%v, err=%v", found, err)
		}
		record.Harness = agentHarness
		if err := st.UpdateSession(context.Background(), record); err != nil {
			t.Fatalf("set seeded harness: %v", err)
		}
	}
	base := newFakeConversation()
	if conv == nil {
		conv = base
	} else if recorder, ok := conv.(*interruptRecorder); ok {
		base = recorder.fakeConversation
	} else if recorder, ok := conv.(*historyRecorder); ok {
		base = recorder.fakeConversation
	}
	h := &harness{
		st:       st,
		conv:     base,
		activity: &recordingActivity{},
		clock:    time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
	}
	h.logs = &lockedBuffer{}
	h.rendersDir = t.TempDir()
	h.renders = attachmentstore.New(h.rendersDir)

	// Guarded because the id factory is called from both the projection goroutine and
	// whichever goroutine a test drives commands from, and an unsynchronized counter
	// is a data race the -race build fails on rather than a harmless test detail.
	var (
		counterMu sync.Mutex
		counter   int
	)
	chatStore := wrapStore(st)
	svc := chatsvc.New(chatsvc.Options{
		Store:      chatStore,
		Reader:     fullSnapshotReader(st),
		PageReader: boundedSnapshotReader(st),
		Sessions:   st,
		StopProviderHost: func(context.Context, domain.SessionID) error {
			h.hostStops.Add(1)
			return nil
		},
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: h.activity,
		Log:      slog.New(slog.NewTextHandler(h.logs, nil)),
		OnAccountChanged: func(domain.SessionID, string, domain.AgentHarness) {
			h.accountChecks.Add(1)
		},
		NewID: func() string {
			counterMu.Lock()
			defer counterMu.Unlock()
			counter++
			return fmt.Sprintf("id-%03d", counter)
		},
		Now:     h.now,
		Renders: h.renders,
		DataDir: h.rendersDir,
		ReconcileOutputType: func(_ context.Context, id domain.SessionID) error {
			h.reconciled = append(h.reconciled, id)
			return h.reconcileErr
		},
	})

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID:     testSession,
		ProjectID:     testProject,
		Harness:       agentHarness,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	h.svc, h.ctrl = svc, ctrl
	return h
}

// awaitSnapshot polls until pred holds, so a test does not race the projector.
func (h *harness) awaitSnapshot(t *testing.T, pred func(store.ConversationSnapshot) bool) store.ConversationSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last store.ConversationSnapshot
	for time.Now().Before(deadline) {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		last = snapshot
		if pred(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("snapshot never satisfied the condition; last had %d messages, %d activities, %d turns",
		len(last.Messages), len(last.Activities), len(last.Turns))
	return last
}

// configOptionConversation models a provider that owns its own session config
// (e.g. Claude Code's model picker), so SetConfigOption has a real surface to
// drive.
type configOptionConversation struct {
	*fakeConversation
	mu      sync.Mutex
	applied []ports.ChatConfigOptionValue
}

func (c *configOptionConversation) ListConfigOptions(context.Context) ([]ports.ChatConfigOption, error) {
	return c.catalog(), nil
}

func (c *configOptionConversation) SetConfigOption(_ context.Context, _ string, value ports.ChatConfigOptionValue) ([]ports.ChatConfigOption, error) {
	c.mu.Lock()
	c.applied = append(c.applied, value)
	c.mu.Unlock()
	return c.catalog(), nil
}

// catalog reflects the most recently applied model select, the way a provider
// reports the post-change state back.
func (c *configOptionConversation) catalog() []ports.ChatConfigOption {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := ""
	if len(c.applied) > 0 {
		current = c.applied[len(c.applied)-1].Select
	}
	return []ports.ChatConfigOption{{
		ID: "model", Category: "model",
		Type:    ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: current},
	}}
}

type failConversationReadStore struct {
	*sqlite.Store
	reads int
}

func (s *failConversationReadStore) ConversationForSession(
	ctx context.Context,
	session domain.SessionID,
) (domain.ConversationRecord, error) {
	s.reads++
	return domain.ConversationRecord{}, errors.New("injected post-commit conversation read failure")
}

// turnStateByText is how the queue tests read the timeline: a turn matters here
// only as the fate of one message the user typed.
func turnStateByText(t *testing.T, s store.ConversationSnapshot) map[string]domain.TurnState {
	t.Helper()
	turns := map[string]domain.ConversationTurn{}
	for _, turn := range s.Turns {
		turns[turn.ID] = turn
	}
	states := map[string]domain.TurnState{}
	for _, msg := range s.Messages {
		if msg.Role != domain.MessageRoleUser {
			continue
		}
		if turn, ok := turns[msg.TurnID]; ok {
			states[msg.Text] = turn.State
		}
	}
	return states
}

type completionOnInterruptConversation struct{ *fakeConversation }

func (c *completionOnInterruptConversation) Interrupt(_ context.Context, turn string) error {
	// ACP completes its prompt goroutine from the same cancellation handshake;
	// this event can therefore be queued before session/cancel returns.
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn,
		TurnState: domain.TurnStateInterrupted,
	})
	return nil
}

type failFirstQueueCancellationStore struct {
	*sqlite.Store

	mu    sync.Mutex
	calls int
}

func (s *failFirstQueueCancellationStore) CancelAllQueuedTurns(
	ctx context.Context,
	conversationID string,
	now time.Time,
) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		return errors.New("injected queue cancellation failure")
	}
	return s.Store.CancelAllQueuedTurns(ctx, conversationID, now)
}

func (s *failFirstQueueCancellationStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func errorsIs(err, target error) bool {
	for err != nil {
		if errors.Is(err, target) {
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

// interruptRecorder answers turn/interrupt the way the provider does: it refuses
// any turn it has not been told to consider active.
type interruptRecorder struct {
	*fakeConversation
	activeMu sync.Mutex
	active   map[string]bool
	attempts []string
}

// blockingDispatchConversation exposes the interval in which Send owns sendMu
// but has not yet published pendingTurnID. Stop must wait for dispatch and then
// retain the normal provider-ack grace period.
type blockingDispatchConversation struct {
	*interruptRecorder
	sendStarted chan struct{}
	releaseSend chan struct{}
	attempted   chan struct{}
	startOnce   sync.Once
	attemptOnce sync.Once
}

func newBlockingDispatchConversation() *blockingDispatchConversation {
	return &blockingDispatchConversation{
		interruptRecorder: newInterruptRecorder(),
		sendStarted:       make(chan struct{}),
		releaseSend:       make(chan struct{}),
		attempted:         make(chan struct{}),
	}
}

func (c *blockingDispatchConversation) SendTurn(
	ctx context.Context,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	c.startOnce.Do(func() { close(c.sendStarted) })
	select {
	case <-c.releaseSend:
		return c.fakeConversation.SendTurn(ctx, msg)
	case <-ctx.Done():
		return ports.ChatTurnRef{}, ctx.Err()
	}
}

func (c *blockingDispatchConversation) Interrupt(ctx context.Context, turn string) error {
	c.attemptOnce.Do(func() { close(c.attempted) })
	return c.interruptRecorder.Interrupt(ctx, turn)
}

// blockingInterruptRefusalConversation holds the provider refusal open so a
// message can arrive after Stop began but before reconciliation runs.
type blockingInterruptRefusalConversation struct {
	*fakeConversation
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newBlockingInterruptRefusalConversation() *blockingInterruptRefusalConversation {
	return &blockingInterruptRefusalConversation{
		fakeConversation: newFakeConversation(),
		started:          make(chan struct{}),
		release:          make(chan struct{}),
	}
}

func (c *blockingInterruptRefusalConversation) Interrupt(ctx context.Context, _ string) error {
	c.startedOnce.Do(func() { close(c.started) })
	select {
	case <-c.release:
		return ports.ErrChatNoActiveTurn
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *blockingInterruptRefusalConversation) unblock() {
	c.releaseOnce.Do(func() { close(c.release) })
}

// completionBeforeRefusalConversation lets the committed completion win the
// controller's send lock before the provider's stale refusal reaches Interrupt.
type completionBeforeRefusalConversation struct {
	*fakeConversation
	emitted     chan struct{}
	release     chan struct{}
	emittedOnce sync.Once
	releaseOnce sync.Once
}

func newCompletionBeforeRefusalConversation() *completionBeforeRefusalConversation {
	return &completionBeforeRefusalConversation{
		fakeConversation: newFakeConversation(),
		emitted:          make(chan struct{}),
		release:          make(chan struct{}),
	}
}

func (c *completionBeforeRefusalConversation) Interrupt(ctx context.Context, turn string) error {
	c.emittedOnce.Do(func() {
		c.emit(ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn,
			TurnState: domain.TurnStateCompleted,
		})
		close(c.emitted)
	})
	select {
	case <-c.release:
		return ports.ErrChatNoActiveTurn
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *completionBeforeRefusalConversation) unblock() {
	c.releaseOnce.Do(func() { close(c.release) })
}

func newInterruptRecorder() *interruptRecorder {
	return &interruptRecorder{fakeConversation: newFakeConversation(), active: map[string]bool{}}
}

func (r *interruptRecorder) markActive(turn string) {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	r.active[turn] = true
}

func (r *interruptRecorder) Interrupt(_ context.Context, turn string) error {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	r.attempts = append(r.attempts, turn)
	if !r.active[turn] {
		return ports.ErrChatNoActiveTurn
	}
	return nil
}

func (r *interruptRecorder) attemptCount() int {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	return len(r.attempts)
}

func hasActivitySignal(signals []ports.ActivitySignal, state domain.ActivityState, event string) bool {
	for _, s := range signals {
		if s.State == state && s.Event == event {
			return true
		}
	}
	return false
}

func countActivitySignals(signals []ports.ActivitySignal, state domain.ActivityState, event string) int {
	count := 0
	for _, signal := range signals {
		if signal.State == state && signal.Event == event {
			count++
		}
	}
	return count
}

func testStartSettlesWorkLeftByAKilledController(t *testing.T, queuedOnly bool) {
	h := newHarness(t)
	ctx := context.Background()

	// A turn in flight and a message queued behind it, exactly as a crash leaves them.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "running", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", ClientMessageID: "c2"}); err != nil {
		t.Fatalf("mid-turn Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderItemID: "9", RequestID: "9", ActivityKind: domain.ActivityKindCommand,
		ActivityStatus: domain.ActivityStatusPending, Summary: "Run something",
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 1 })
	if queuedOnly {
		if err := h.st.SettleTurn(ctx, h.ctrl.ConversationID(), "provider-turn-1", domain.TurnStateFailed, "crash", h.now()); err != nil {
			t.Fatal(err)
		}
	}

	// A killed daemon leaves the rows mid-flight and takes its service with it, so
	// the next controller comes up in a NEW service over the SAME store. Building
	// it that way rather than reusing this one is the point: nothing in the old
	// process gets a chance to clean up.
	restarts := 0
	restart := func() (*chatsvc.Service, *fakeConversation) {
		conv := newFakeConversation()
		restarts++
		conv.turnSeq = 100 * restarts // a fresh provider process issues fresh turn ids
		svc := chatsvc.New(chatsvc.Options{
			Store:    h.st,
			Sessions: h.st,
			Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
			Log:      slog.New(slog.DiscardHandler),
			NewID:    func() string { return "next-" + fmt.Sprint(time.Now().UnixNano()) },
			Now:      h.now,
		})
		t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
		return svc, conv
	}
	start := func(svc *chatsvc.Service) {
		t.Helper()
		if _, err := svc.Start(ctx, chatsvc.StartConfig{
			SessionID:              testSession,
			ProjectID:              testProject,
			Harness:                domain.HarnessCodex,
			WorkspacePath:          t.TempDir(),
			ProviderConversationID: "thread-1",
		}); err != nil {
			t.Fatalf("Start after crash: %v", err)
		}
	}
	next, nextConv := restart()
	// Retry moves an interrupted async start back to provisioning. That state
	// must not hide the running turn left by its previous controller.
	if !queuedOnly {
		if _, err := h.st.SetSessionProvisionState(ctx, testSession, domain.SessionProvisionProvisioning, "", h.now()); err != nil {
			t.Fatal(err)
		}
	}

	start(next)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && turnStateByText(t, s)["running"].Terminal()
	})
	states := turnStateByText(t, snapshot)
	if states["running"] != domain.TurnStateFailed {
		t.Errorf("turn abandoned mid-flight = %q, want failed", states["running"])
	}
	if states["queued"] != domain.TurnStateQueued {
		t.Errorf("message queued behind the lost turn = %q, want still queued for delivery", states["queued"])
	}
	if got := snapshot.Activities[0].Status; got == domain.ActivityStatusPending {
		t.Error("approval left pending by a dead controller; the user can never answer it")
	}

	// Resume drains the queue; a repeated drain must not send it again.
	for range 2 {
		if err := next.DrainQueued(ctx, testSession); err != nil {
			t.Fatalf("DrainQueued: %v", err)
		}
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued"] == domain.TurnStateRunning
	})
	if got := nextConv.sentTexts(); len(got) != 1 || got[0] != "queued" {
		t.Fatalf("provider received %v after resume, want the queued message once", got)
	}

	// A second crash loses that turn too; the next controller settles it and
	// must not deliver the message again.
	third, thirdConv := restart()
	start(third)
	if err := third.DrainQueued(ctx, testSession); err != nil {
		t.Fatalf("DrainQueued after second restart: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued"] == domain.TurnStateFailed
	})
	if got := thirdConv.sentTexts(); len(got) != 0 {
		t.Fatalf("provider received %v after a second restart, want nothing replayed", got)
	}
}

// compactingConversation is a provider that can reclaim context. The plain fake
// deliberately cannot, so the unsupported path stays exercised by every other test.
type compactingConversation struct {
	*fakeConversation

	compactMu sync.Mutex
	calls     int
}

func newCompactingConversation() *compactingConversation {
	conv := &compactingConversation{fakeConversation: newFakeConversation()}
	caps := productionCaps()
	caps[ports.ChatCapabilityCompaction] = true
	conv.setCapabilities(caps)
	return conv
}

func (c *compactingConversation) Compact(context.Context) (ports.ChatCompactionResult, error) {
	c.compactMu.Lock()
	defer c.compactMu.Unlock()
	c.calls++
	// What is about to be reclaimed, not what was: the real provider accepts the
	// request and does the work as its own turn afterwards.
	return ports.ChatCompactionResult{TokensBefore: 15650}, nil
}

func (c *compactingConversation) compactCalls() int {
	c.compactMu.Lock()
	defer c.compactMu.Unlock()
	return c.calls
}

// failingProjectStore injects a projection failure for one event kind,
// simulating a SettleTurn error or tx.Commit failure without corrupting SQLite.
type failingProjectStore struct {
	chatsvc.Store
	failMethod string
	failed     chan struct{}
	failOnce   sync.Once
}

type recordingCleanupStore struct {
	chatsvc.Store
	called chan struct{}
}

func (s *recordingCleanupStore) CleanupOwnedControllerWork(
	ctx context.Context,
	session domain.SessionID,
	conversationID, generation string,
	now time.Time,
) (bool, error) {
	s.called <- struct{}{}
	return s.Store.CleanupOwnedControllerWork(ctx, session, conversationID, generation, now)
}

func (s *failingProjectStore) ProjectProviderEvent(
	ctx context.Context, conversationID string, session domain.SessionID,
	generation, providerEventID, method, payloadJSON string, now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if method != s.failMethod {
		return s.Store.ProjectProviderEvent(ctx, conversationID, session, generation,
			providerEventID, method, payloadJSON, now, project)
	}
	projected, err := s.Store.ProjectProviderEvent(ctx, conversationID, session, generation,
		providerEventID, method, payloadJSON, now, func(txCtx context.Context) error {
			if err := project(txCtx); err != nil {
				return err
			}
			// Fail after the projection callback so its SQLite writes roll back. This
			// is the exact boundary that used to leak volatile state out of the tx.
			return errors.New("injected projection failure")
		})
	s.failOnce.Do(func() { close(s.failed) })
	return projected, err
}

// awaitStoreSnapshot is awaitSnapshot for tests that build their own service
// rather than using the harness.
func awaitStoreSnapshot(t *testing.T, st *sqlite.Store, conversationID string,
	pred func(store.ConversationSnapshot) bool) store.ConversationSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last store.ConversationSnapshot
	for time.Now().Before(deadline) {
		snapshot, err := st.LoadConversationSnapshot(context.Background(), conversationID)
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		last = snapshot
		if pred(snapshot) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("snapshot never satisfied the condition; last had %d messages, %d activities, %d turns",
		len(last.Messages), len(last.Activities), len(last.Turns))
	return last
}

// Package chat owns the daemon side of a Chat-mode session: one controller per
// session, the projection of provider events into durable conversation rows, and
// the typed commands a client can issue against it.
//
// Ownership rules this package enforces:
//
//   - exactly one live controller per session, and it is the only writer to that
//     session's provider conversation;
//   - provider events are archived and projected together, so the raw record can
//     never disagree with the timeline derived from it;
//   - an event carrying a stale controller generation is dropped, so a controller
//     that is dying cannot mutate the session that replaced it;
//   - a turn left in flight when its provider ends settles as failed. Deliberate
//     daemon detach from a persistent provider is not provider termination.
package chat

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

const (
	nativeHistorySettlePoll    = 100 * time.Millisecond
	nativeHistorySettlePollMax = 2 * time.Second
	nativeHistorySettleLimit   = 45 * time.Second
	branchHandoffReportLimit   = 5 * time.Second
	retryClientMessagePrefix   = "retry-attempt/"
)

// nativeHistoryLoadAttemptLimit bounds one provider re-observation inside the
// settle budget. An ACP refresh is a full session/load; without this bound a
// single stalled load consumes the whole nativeHistorySettleLimit before the
// loop can classify anything. A variable so tests can shorten it.
var nativeHistoryLoadAttemptLimit = 30 * time.Second

var errNativeHistoryLoadAttemptTimeout = errors.New("native history load attempt exceeded its time limit")

// Store is the durable conversation surface the controller needs. Implemented by
// the SQLite store.
type Store interface {
	CreateConversation(ctx context.Context, id string, scope domain.ConversationScope, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error)
	CreateReviewConversation(ctx context.Context, id, reviewID string, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error)
	OpenNativeConversation(ctx context.Context, id string, scope domain.ConversationScope, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error)
	CreateProjectConversationWithContextReset(ctx context.Context, id string, project domain.ProjectID, session domain.SessionID, reset domain.ConversationActivity, now time.Time) (domain.ConversationRecord, error)
	CreateProjectConversationAdoptingQueue(ctx context.Context, id string, project domain.ProjectID, session domain.SessionID, reset domain.ConversationActivity, now time.Time) (domain.ConversationRecord, error)
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
	ClaimChatControllerGeneration(ctx context.Context, session domain.SessionID, generation string) error
	ClaimReviewChatController(ctx context.Context, reviewID, providerID, generation string, now time.Time) (bool, error)
	ConversationBranch(ctx context.Context, conversationID, branchID string) (domain.ConversationBranch, error)
	CommitProviderSwitch(ctx context.Context, rec domain.SessionRecord, branch domain.ConversationBranch, generation string, settings domain.ConversationSettings, activity domain.ConversationActivity) error
	ListTerminalTurns(ctx context.Context, id domain.SessionID) ([]domain.HandoffTurn, error)
	ConversationEditAnchor(ctx context.Context, conversationID, replacedTurnID string) (domain.ConversationEditAnchor, error)
	RepairIncompleteConversationEdit(ctx context.Context, sessionID domain.SessionID, conversationID string, now time.Time) (domain.ConversationBranch, bool, error)
	CreateAndActivateConversationBranch(ctx context.Context, sessionID domain.SessionID, branch domain.ConversationBranch, generation string, now time.Time) error
	CreateAndActivateReviewConversationBranch(ctx context.Context, reviewID string, branch domain.ConversationBranch, generation string, now time.Time) error
	ActivateConversationBranch(ctx context.Context, sessionID domain.SessionID, conversationID, branchID, providerConversationID, generation string, now time.Time) error
	UpdateConversationBranchReplacement(ctx context.Context, branchID, replacementTurnID string) error

	AdoptProviderTurn(ctx context.Context, conversationID string, session domain.SessionID, generation, turnID, providerTurnID string, now time.Time) error
	AdoptReviewProviderTurn(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation, turnID, providerTurnID string, now time.Time) error
	AppendImportedUserMessage(ctx context.Context, conversationID, providerTurnID string, msg domain.ConversationMessage, now time.Time) error

	AppendUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error)
	AppendReviewUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error)
	ConversationMessageByClientID(ctx context.Context, conversationID, clientMessageID string) (domain.ConversationMessage, bool, error)
	ConversationMessages(ctx context.Context, conversationID string) ([]domain.ConversationMessage, error)
	AppendRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error)
	AppendReviewRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error)
	MarkTurnDispatching(ctx context.Context, turnID string) error
	RecordProviderInput(ctx context.Context, turnID, text string) error
	BindTurnToProvider(ctx context.Context, turnID, providerTurnID string, now time.Time) error
	BindPrimaryChatTurnToProvider(ctx context.Context, turnID, providerTurnID, generation string, now time.Time) error
	RecordActiveChatTurnFallback(ctx context.Context, conversationID, providerTurnID string) error
	SupersedeCompletedTurnFallbacks(ctx context.Context, session domain.SessionID) error
	SettleTurn(ctx context.Context, conversationID, providerTurnID string, state domain.TurnState, errMessage string, now time.Time) error
	RecordChatTurnFallback(ctx context.Context, conversationID, providerTurnID, excerpt string, at time.Time) error
	SettleTurnByID(ctx context.Context, turnID string, state domain.TurnState, errMessage string, now time.Time) error
	SettleOrphanedTurns(ctx context.Context, session domain.SessionID, now time.Time) error
	SettleOrphanedRunningTurns(ctx context.Context, session domain.SessionID, now time.Time) error
	SettleUnboundRunningTurn(ctx context.Context, conversationID string, session domain.SessionID, turnID string, now time.Time) error
	CleanupOwnedControllerWork(ctx context.Context, session domain.SessionID, conversationID, generation string, now time.Time) (bool, error)
	CleanupOwnedReviewControllerWork(ctx context.Context, reviewID, conversationID, generation string, now time.Time) (bool, error)
	ListVisibleRunningTurnProviderIDs(ctx context.Context, conversationID string) ([]string, error)

	SetConversationSettings(ctx context.Context, conversationID string, settings domain.ConversationSettings, now time.Time) error

	// Usage and rate limits are current state, not timeline entries: each write
	// replaces the last. The provider reports usage after every tool call, so an
	// append-per-report is what buried the conversation the first time round.
	RecordUsage(ctx context.Context, conversationID string, usage domain.ConversationUsage) error
	RecordRateLimits(ctx context.Context, conversationID string, limits domain.ConversationRateLimits) error
	// AddModelUsage accumulates one turn's per-model spend into running totals.
	AddModelUsage(ctx context.Context, conversationID string, turn []domain.ModelUsage, now time.Time) error

	NextQueuedTurn(ctx context.Context, conversationID string) (domain.QueuedTurn, error)
	ListQueuedBatch(ctx context.Context, conversationID string, limit int) ([]domain.QueuedTurn, error)
	ClaimQueuedBatch(ctx context.Context, conversationID string, dispatch bool, limit int, now time.Time) ([]domain.QueuedTurn, error)
	CompleteQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, providerTurnID, mode string, now time.Time) error
	ReleaseQueuedBatch(ctx context.Context, conversationID string, turnIDs []string) error
	FailQueuedBatch(ctx context.Context, conversationID string, turnIDs []string, reason string, now time.Time) error
	FailReservedQueuedBatch(ctx context.Context, conversationID string, now time.Time) error
	ReserveQueuedTurnForPromotion(ctx context.Context, conversationID, turnID string, now time.Time) (domain.QueuedTurn, error)
	ReleaseQueuedTurnPromotion(ctx context.Context, conversationID, turnID string) error
	CompleteQueuedTurnPromotion(ctx context.Context, conversationID, sourceTurnID, providerTurnID string, activity domain.ConversationActivity, now time.Time) error
	SteerDelivery(ctx context.Context, conversationID, clientMessageID string) (domain.ConversationSteerDelivery, bool, error)
	ReserveSteerDelivery(ctx context.Context, conversationID, clientMessageID, requestJSON string, now time.Time) (domain.ConversationSteerDelivery, bool, error)
	CompleteSteerDelivery(ctx context.Context, conversationID, clientMessageID, providerTurnID string, activity domain.ConversationActivity, now time.Time) error
	CompleteSteerBatchDelivery(ctx context.Context, conversationID, clientMessageID, providerTurnID string, activity domain.ConversationActivity, turnIDs []string, now time.Time) error
	RejectSteerDelivery(ctx context.Context, conversationID, clientMessageID string, kind domain.ConversationSteerRejectionKind, message string, now time.Time) error
	EditDelivery(ctx context.Context, conversationID, clientMessageID string) (domain.ConversationEditDelivery, bool, error)
	ReserveEditDelivery(ctx context.Context, conversationID, clientMessageID, requestJSON string, now time.Time) (domain.ConversationEditDelivery, bool, error)
	BeginEditProviderWork(ctx context.Context, conversationID, clientMessageID, generation string) error
	RecoverCompletedEditDelivery(ctx context.Context, conversationID, clientMessageID string, now time.Time) error
	CompleteEditDelivery(ctx context.Context, conversationID, clientMessageID, sourceBranchID, activeBranchID string, turn domain.ConversationTurn, now time.Time) error
	RejectEditDelivery(ctx context.Context, conversationID, clientMessageID string, kind domain.ConversationEditRejectionKind, message string, now time.Time) error
	CancelQueuedTurns(ctx context.Context, conversationID string, cutoff, now time.Time) error
	CancelAllQueuedTurns(ctx context.Context, conversationID string, now time.Time) error
	CancelQueuedTurnByID(ctx context.Context, conversationID, turnID string, now time.Time) error
	QueuedTurnMessage(ctx context.Context, conversationID, turnID string) (domain.ConversationMessage, error)
	QueuedEditDelivery(ctx context.Context, conversationID, clientMessageID string) (string, bool, error)
	UpdateQueuedTurnMessage(ctx context.Context, conversationID, turnID, text, contentJSON string, revision int64, now time.Time, delivery domain.ConversationQueuedEditDelivery) error
	CommitQueuedTurnReports(ctx context.Context, conversationID, turnID string, message domain.ConversationMessage, projectID domain.ProjectID, claimToken string, count int, now time.Time) error
	QueuedMessages(ctx context.Context, conversationID string, limit int) ([]domain.ConversationMessage, error)
	QueuedDeliveryMessage(ctx context.Context, conversationID, turnID string) (domain.ConversationMessage, error)
	CombineQueuedMessages(ctx context.Context, conversationID string, messages []domain.ConversationMessage, text string, now time.Time) error
	ReorderQueuedTurns(ctx context.Context, conversationID string, turnIDs []string) error

	RetryPrompt(ctx context.Context, conversationID, turnID string) (domain.RetryPrompt, error)
	RetryTurnIDForSource(ctx context.Context, conversationID, sourceTurnID string) (string, bool, error)

	TurnByID(ctx context.Context, turnID string) (domain.ConversationTurn, error)
	TurnByProviderID(ctx context.Context, conversationID, providerTurnID string) (domain.ConversationTurn, error)
	RollbackTurns(ctx context.Context, conversationID, turnID string, now time.Time) (int, error)

	SetProviderTitle(ctx context.Context, conversationID, title string, now time.Time) error
	ApplyProviderTitle(ctx context.Context, conversationID string, session domain.SessionID, title string, now time.Time) (bool, error)

	AppendAssistantDelta(ctx context.Context, conversationID, providerItemID, providerTurnID, delta, messageID string, now time.Time) error
	SettleAssistantMessage(ctx context.Context, conversationID, providerItemID, providerTurnID, text, messageID string, now time.Time) error

	AppendCommandOutput(ctx context.Context, conversationID, providerItemID, delta string, now time.Time) (bool, error)
	SetTurnDiff(ctx context.Context, conversationID, providerTurnID string, diff domain.ConversationTurnDiff, now time.Time) (bool, error)

	// Streamed provider prose for an activity: reasoning summaries, terminal
	// keystrokes, tool progress. Appended many times and then replaced by the
	// settled form, which is why it is two calls rather than one upsert.
	AppendActivityStreamedText(ctx context.Context, conversationID, providerItemID, delta string, now time.Time) (bool, error)
	SettleActivityStreamedText(ctx context.Context, conversationID, providerItemID, text string, now time.Time) (bool, error)

	SetTurnPlan(ctx context.Context, conversationID, providerTurnID string, plan domain.ConversationPlan) (bool, error)

	// Latest-wins provider state that belongs to the conversation rather than to a
	// turn. Each write replaces the last.
	RecordModelReroute(ctx context.Context, conversationID string, reroute domain.ConversationModelReroute) error
	ConversationAccount(ctx context.Context, conversationID string) (*domain.ConversationAccount, error)
	RecordAccount(ctx context.Context, conversationID string, account domain.ConversationAccount, now time.Time) error
	ReconcileConversationAuthentication(ctx context.Context, conversationID, generation, providerTurnID string, now time.Time) (*domain.ConversationAccount, error)
	RecordThreadState(ctx context.Context, conversationID string, state domain.ConversationThreadState) error
	RecordMCPServers(ctx context.Context, conversationID string, servers []domain.ConversationMCPServer) error

	UpsertActivity(ctx context.Context, conversationID, providerTurnID string, activity domain.ConversationActivity, now time.Time) error
	MarkCompacted(ctx context.Context, conversationID string, at time.Time) error
	ResolveApproval(ctx context.Context, conversationID, requestID, detailJSON string, now time.Time) error
	HasPendingConversationInteractions(ctx context.Context, conversationID string) (bool, error)
	FailPendingApprovals(ctx context.Context, conversationID string, now time.Time) error
	FailPendingInputs(ctx context.Context, conversationID string, now time.Time) error

	ProjectProviderEvent(ctx context.Context, conversationID string, session domain.SessionID, generation, providerEventID, method, payloadJSON string, now time.Time, project func(context.Context) error) (bool, error)
	ProjectReviewProviderEvent(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation, providerEventID, method, payloadJSON string, now time.Time, project func(context.Context) error) (bool, error)
}

// ActivityRecorder feeds derived session status.
//
// Chat reports activity through the SAME lifecycle reduction terminal sessions
// use, rather than persisting a second display status. Without it a chat session
// reads as idle while the agent is working, because the hook and terminal signals
// that normally drive activity never fire for a chat controller.
type ActivityRecorder interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// IDFactory mints the identifiers Tallpine assigns. Injected so tests get stable ids.
type IDFactory func() string

// Clock is injected so tests do not depend on wall time.
type Clock func() time.Time

// controllerHandoff is the controller's dispatch gate. Keeping the lifecycle in
// one state avoids invalid combinations such as "handoff=true, drain=false"
// meaning either an interrupt transition or an idle branch mutation.
type controllerHandoff uint8

const (
	controllerHandoffNone controllerHandoff = iota
	controllerHandoffInterfaceDrain
	controllerHandoffInterfaceInterrupt
	controllerHandoffIdleBranch
	controllerHandoffHibernate
)

func interfaceHandoff(policy domain.SessionInterfaceTransitionPolicy) controllerHandoff {
	if policy == domain.SessionInterfaceTransitionDrain {
		return controllerHandoffInterfaceDrain
	}
	return controllerHandoffInterfaceInterrupt
}

// Controller drives one Chat session.
type Controller struct {
	sessionID    domain.SessionID
	reviewID     string
	conversation domain.ConversationRecord
	generation   string
	harness      domain.AgentHarness

	conv                   ports.ChatConversation
	store                  Store
	continuationReader     SnapshotReader
	activity               ActivityRecorder
	log                    *slog.Logger
	newID                  IDFactory
	now                    Clock
	onAccountChanged       func(domain.SessionID, string, domain.AgentHarness)
	recheckAccount         func(domain.SessionID, domain.AgentHarness)
	onUsageLimitPaused     func(domain.SessionID, domain.AgentHarness) // asks the service to fail over
	pauseWake              *time.Timer                                 // guarded by mu; re-drains the queue after a usage-limit pause
	transientRetry         transientRetryState                         // guarded by mu; the pending automatic retry (B682)
	onAsyncTask            func(domain.SessionID, ports.ChatAsyncTask)
	onAsyncTasksGone       func(domain.SessionID)
	onCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation)
	reportCoordinator      func() *reportsvc.Coordinator
	prepareWorkerQueue     func(context.Context, domain.QueuedTurn) (domain.QueuedTurn, error)

	// sendMu serializes command dispatch so only one operation mutates the
	// provider conversation at a time.
	sendMu sync.Mutex
	// A current-context copy fences dispatch, but Stop cancels it before waiting.
	forkCancel context.CancelFunc
	forkDone   chan struct{}
	interrupts int
	// finalAssistant is the root conversation's latest assistant message, kept
	// for the turn-completed lifecycle signal. Only the projection goroutine
	// touches it.
	finalAssistant finalAssistantText
	// autonomousTurns are the open harness-started turn tasks (guarded by mu).
	autonomousTurns map[string]bool
	// reauthCleared carries "this turn completion cleared a durable reauth
	// demand" from the projection transaction to afterProject, which then asks
	// for the account to be read again. An ordinary successful turn leaves it
	// false so it never costs a CLI probe.
	reauthCleared atomic.Bool
	// parkCut is set when a deliberate quit interrupts this controller, so the
	// interrupted turns it settles are labeled as an app restart, not a user stop.
	parkCut atomic.Bool
	// configMu serializes live provider setting changes. Each response replaces
	// the complete option catalog and updates durable next-turn settings, so
	// concurrent writes could otherwise persist an older catalog last.
	configMu sync.Mutex
	// compactLive is the compaction window, in tokens, the running Claude Code
	// agent uses: what it launched with, then what a live change set. 0 is the
	// agent's own default, -1 unknown (a provider process Tallpine reattached to).
	// Guarded by mu.
	compactLive int
	// compactBaseline is the window the project's own environment sets, which
	// Auto means: 0 when it sets none. Fixed for the controller's life.
	compactBaseline int
	research        researchState // orchestrator research mode (B413); see researchState
	// kind is the session kind; an orchestrator has its own compaction default.
	kind domain.SessionKind

	mu sync.Mutex
	// suppressStoppedActivity marks a deliberate branch-controller retirement.
	// The replacement generation owns lifecycle state; publishing an exited fact
	// from the source would make that replacement unable to report active work.
	suppressStoppedActivity bool
	// activeTurn maps a provider turn id to Tallpine's turn id for the turn currently
	// in flight, so a completion can be attributed without a round trip.
	pendingTurnID string
	// dispatchedApproval is the approval mode the latest turn was sent with,
	// the one its sandbox runs under; settings hold the next turn's. Unset
	// until this controller sends a turn.
	dispatchedApproval    ports.PermissionMode
	hasDispatchedApproval bool
	// dispatchingTurnID is Tallpine's durable turn row while SendTurn is in flight.
	// Eager providers can emit turn/started before SendTurn returns with the
	// provider id; that event must bind this row instead of adopting a duplicate.
	dispatchingTurnID string
	// sendingTurnID is the last turn Send decided to dispatch rather than queue. Its
	// row is written queued before dispatch moves it on, so a snapshot read in that
	// gap would show an idle agent's message as waiting in the queue. It is kept after
	// the send returns: a snapshot can load the row while it is still queued and read
	// this marker afterward, and only a queued row is ever overlaid.
	sendingTurnID string
	// A compact request can be accepted before its provider turn starts.
	compactionPending bool
	// openInputs are the provider's structured questions (in-chat forms) still
	// waiting for an answer: request id to the provider turn that asked. A
	// running turn blocked on one never ends by itself, so a message queued
	// behind it would never be delivered. Keyed by turn so a question left by a
	// turn that ended without a matching completion (Stop, a failed deferred
	// start, a side turn) never marks a later turn as waiting. In memory only:
	// after a daemon restart or controller replacement a still-open form is not
	// known here, and sends fall back to queueing.
	openInputs map[string]string
	// Provider calls keep automatic hibernation out without blocking explicit Kill.
	operations int
	// ackedTurnID is the turn the PROVIDER has confirmed it started, which lags
	// pendingTurnID by the round trip between turn/start returning and the
	// turn-started notification arriving. Interrupt needs the distinction: a
	// provider refuses to cancel a turn it has not acknowledged yet.
	ackedTurnID string
	// artifactsShown holds the artifact paths the thread already shows in
	// provider turn artifactsShownTurn: pages reported there, and renders
	// kept as artifacts. A new turn replaces the set.
	artifactsShownTurn string
	artifactsShown     map[string]bool
	state              ports.ChatControllerState
	// settings are the provider choices applied to the next dispatch. Held here as
	// well as on disk so a dispatch does not need a read, and updated together with
	// the row so the two cannot drift.
	settings domain.ConversationSettings
	// cancelQueuedAt is set when the user interrupts, and is the cutoff for the
	// queue that interrupt cancels. Zero means nothing is being cancelled.
	cancelQueuedAt time.Time
	// handoff closes source-controller intake while ownership is being changed.
	// Interface drain keeps dispatching accepted rows; interface interrupt and an
	// idle branch mutation stop dispatch entirely. The target controller is not
	// started until this controller reports quiescent and is closed.
	handoff controllerHandoff
	// handoffInterrupted is the provider turn an interrupt-policy handoff
	// stopped, so the move can say on that exact turn why it ended.
	handoffInterrupted string
	branchHandoffDone  chan struct{}
	// preserveProviderOnStop is set before deliberate daemon detach. It prevents
	// the closing controller from projecting a false provider exit or failing work
	// that the detached host continues to run.
	preserveProviderOnStop bool
	transportInterrupted   bool
	explicitClose          bool

	// account, threadState and mcpServers are merged here before being written,
	// because the provider reports each of them in pieces: account/updated carries
	// auth mode and plan but never a credential demand, a thread-status report says
	// nothing about archiving, and MCP servers are announced one at a time. Writing
	// each report straight through would make every field blank the one before it.
	threadState domain.ConversationThreadState
	// usage is merged in memory because providers may split context occupancy and
	// cumulative accounting across separate events. Writing either half as a full
	// replacement erases the other half and turns a percentage meter into a bare
	// token count.
	usage domain.ConversationUsage
	// mcpServers is keyed by name; mcpServerOrder preserves first-seen order so the
	// list a client renders does not reshuffle on every turn.
	mcpServers            map[string]domain.ConversationMCPServer
	mcpServerOrder        []string
	mcpServerRevision     uint64
	mcpServerSeenRevision map[string]uint64
	// mcpPersistMu orders durable MCP replacements with individual notification
	// writes. Reload runs outside the event pump, so c.mu alone cannot prevent an
	// older notification write from landing after an authoritative replacement.
	mcpPersistMu sync.Mutex

	// flushRequests asks the projector to write the deltas it is batching; it
	// closes the sent channel once that write has finished, whether or not it
	// succeeded.
	flushRequests chan chan struct{}
	// coalesce is the per-item memory storage compaction uses to skip and trim
	// repeated activity snapshots.
	coalesce activityCoalescer

	stopped  chan struct{}
	once     sync.Once
	closeErr error
}

func (c *Controller) beginOperation() func() {
	c.mu.Lock()
	c.operations++
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.operations--
		c.mu.Unlock()
	}
}

// ErrNoActiveTurn reports an interrupt with nothing to cancel.
var ErrNoActiveTurn = errors.New("no active turn")

// ErrControllerHandoff reports a send attempted after a controller handoff has
// closed source intake. The client should retain the draft and retry after the
// session_updated event announces the target mode.
var ErrControllerHandoff = errors.New("chat controller is switching interfaces")

// ErrTurnNotRetryable reports a turn that cannot be retried: it is not part of
// this conversation, not failed, was rolled back, or carries no durable prompt
// the user authored. A retry is only ever offered for an eligible failed human
// turn.
var ErrTurnNotRetryable = errors.New("turn is not retryable")

// ErrRetryStaleBranch reports a durable failed turn that is no longer visible
// on the conversation's active branch. Retrying it would inject abandoned
// history into the provider thread the user is currently viewing.
var ErrRetryStaleBranch = errors.New("turn is not on the active conversation branch")

// ErrRetryDeliveryUncertain reports a failed turn whose dispatch was never
// acknowledged by the provider. Settling it failed was Tallpine's safe guess rather
// than a fact about delivery: the provider may have accepted the work. Because
// a retry re-sends the prompt as a new turn, an uncertain source could execute
// it twice, so these turns are refused until delivery is confirmed.
var ErrRetryDeliveryUncertain = errors.New("turn was never confirmed as delivered to the provider")

// ErrRetryContentInvalid reports durable structured prompt content that can no
// longer be reconstructed safely. Retrying must fail clearly rather than drop
// an attachment or hand corrupt data to the provider.
var ErrRetryContentInvalid = errors.New("stored retry content is invalid")

// ErrRetryUnsupported reports structured prompt content the current provider
// cannot accept. This can happen after an in-place agent switch: the failed
// prompt remains visible, but the new provider may negotiate fewer capabilities.
var ErrRetryUnsupported = errors.New("current agent cannot retry this prompt content")

func newController(
	sessionID domain.SessionID,
	owner domain.ConversationOwner,
	conversation domain.ConversationRecord,
	generation string,
	harness domain.AgentHarness,
	conv ports.ChatConversation,
	store Store,
	activity ActivityRecorder,
	log *slog.Logger,
	newID IDFactory,
	now Clock,
	onAccountChanged func(domain.SessionID, string, domain.AgentHarness),
	recheckAccount func(domain.SessionID, domain.AgentHarness),
	onCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation),
	reader SnapshotReader,
) *Controller {
	c := &Controller{
		sessionID:              sessionID,
		reviewID:               reviewOwnerID(owner),
		conversation:           conversation,
		generation:             generation,
		harness:                harness,
		conv:                   conv,
		store:                  store,
		continuationReader:     reader,
		activity:               activity,
		log:                    log,
		newID:                  newID,
		now:                    now,
		onAccountChanged:       onAccountChanged,
		recheckAccount:         recheckAccount,
		onCodexCapacityChanged: onCodexCapacityChanged,
		state:                  ports.ChatControllerReady,
		settings:               conversation.Settings,
		mcpServers:             map[string]domain.ConversationMCPServer{},
		mcpServerSeenRevision:  map[string]uint64{},
		flushRequests:          make(chan chan struct{}),
		stopped:                make(chan struct{}),
	}
	if conversation.ThreadState != nil {
		c.threadState = *conversation.ThreadState
	}
	if conversation.Usage != nil {
		c.usage = *conversation.Usage
	}
	for _, server := range conversation.MCPServers {
		if _, seen := c.mcpServers[server.Name]; !seen {
			c.mcpServerOrder = append(c.mcpServerOrder, server.Name)
		}
		c.mcpServers[server.Name] = server
	}
	return c
}

// start begins live provider consumption after any durable native history has
// been imported. Keeping construction and consumption separate prevents a resume
// notification from racing ahead of the older turns it follows.
func (c *Controller) start() {
	go c.project(deltaFlushInterval)
	if c.harness != domain.HarnessCodex {
		go c.readRateLimits()
	}
}

// Wait blocks until the controller's event stream has ended.
func (c *Controller) Wait() { <-c.stopped }

func (c *Controller) handoffActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handoff != controllerHandoffNone
}

// asyncTasksGone tells the daemon this controller's provider process ended, unless
// a persistent host keeps it (and its tasks) alive for the replacement.
func (c *Controller) asyncTasksGone() {
	c.mu.Lock()
	preserved := c.preserveProviderOnStop
	c.mu.Unlock()
	if !preserved && c.onAsyncTasksGone != nil && c.reviewID == "" {
		c.onAsyncTasksGone(c.sessionID)
	}
}

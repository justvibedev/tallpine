package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// eventBuffer bounds the normalized event stream. Deltas are dropped when a
// consumer falls this far behind; lifecycle events are not.
const eventBuffer = 4096

// approvalWait bounds how long the provider is left blocked on an unanswered
// request. Codex holds its turn until the client replies, so an approval nobody
// resolves would hang the session indefinitely. On expiry Tallpine refuses rather than
// deciding on the user's behalf.
const approvalWait = 30 * time.Minute

// errConversationClosed reports a decision arriving after the controller ended.
var errConversationClosed = errors.New("conversation closed")

// approvalMethods are the server->client requests that represent a decision the
// user must make. Anything else the provider asks is refused: answering a request
// Tallpine does not model risks consenting to something on the user's behalf.
var approvalMethods = map[string]domain.ActivityKind{
	codexproto.MethodItemCommandExecutionRequestApproval: domain.ActivityKindCommand,
	codexproto.MethodItemFileChangeRequestApproval:       domain.ActivityKindFileChange,
	codexproto.MethodItemPermissionsRequestApproval:      domain.ActivityKindApproval,
	codexproto.MethodItemToolRequestUserInput:            domain.ActivityKindApproval,
}

// conversation is one live Codex thread. It is the only writer to that thread.
type conversation struct {
	conn *conn
	proc *process
	log  *slog.Logger

	threadMu sync.RWMutex
	threadID string
	// freshThread is the thread/start request that opens a replacement thread
	// with this conversation's launch settings (ClearContext).
	freshThread     map[string]any
	historyParentID string
	providerScopeID string
	readOnly        bool
	disableNetwork  bool
	// launchMode is the permission mode the thread started or resumed with;
	// a turn without its own approval setting runs under it.
	launchMode ports.PermissionMode
	// policy is the conversation's frozen agent policy decision.
	policy agentpolicy.Decision
	// workspace is the conversation's cwd, used to find a linked worktree's git dirs.
	workspace string
	events    chan ports.ChatEvent
	// Effective defaults returned when Codex opened or resumed this thread.
	threadModel, threadEffort string
	// Guarded by sendMu: whether the last turn put the thread on the fast tier
	// or in plan mode, and each model's fast tier id as last listed.
	fastTier, planMode bool
	// A reconnected thread may be on any tier and in either collaboration mode.
	tierUnknown, planUnknown bool
	fastTiers                map[string]string

	mu      sync.Mutex
	pending map[string]*parkedRequest
	closed  bool

	// sendMu serializes turn dispatch so only one operation mutates the provider
	// conversation at a time.
	sendMu sync.Mutex

	// activeTurn is the most recent provider turn id, used when a caller asks to
	// interrupt without naming one.
	activeTurn string

	// contextTokens is the conversation's latest position in the model's context,
	// and contextWindow the size of that context. Both come from the provider's
	// token-usage reports; zero means it has not reported one yet.
	contextTokens int64
	contextWindow int64
	// contextAtTurnStart is contextTokens as it stood when the current provider turn
	// began. It is the "before" half of a compaction reclaim: a compaction runs as
	// its own provider turn, so the figure captured at that turn's start is the
	// context position the compaction is about to shrink.
	contextAtTurnStart int64
	// compactedTurn is the last turn a compaction was reported for, so a provider
	// build that emits both the notification and the item reports one compaction
	// once.
	compactedTurn string

	pumpDone     chan struct{}
	eventsMu     sync.RWMutex
	eventsClosed bool
	closeOnce    sync.Once
	closeErr     error
}

var _ ports.ChatConversation = (*conversation)(nil)

var _ ports.ChatProviderHibernator = (*conversation)(nil)

// Asserted here so a refactor cannot silently drop model listing: the service
// feature-detects this interface, and a missed method would just mean "no models"
// with nothing to notice.
var _ ports.ChatModelLister = (*conversation)(nil)

// Same reasoning for the quota read: a dropped method would silently become "this
// account has no limits to report", which is the wrong answer to show a user
// whose turns are about to start failing.
var _ ports.ChatUsageReporter = (*conversation)(nil)

// Same reason, for compaction. Losing this method does not break a build; it just
// makes the control disappear and long conversations start failing again.
var _ ports.ChatCompactor = (*conversation)(nil)

// Same reason, for the MCP reload: a dropped method makes the affordance vanish and
// leaves a session with a dead tool server no way back.
var _ ports.ChatMCPReloader = (*conversation)(nil)

func newConversation(proc *process, log *slog.Logger, providerScopeID string) *conversation {
	c := &conversation{
		proc:            proc,
		log:             log,
		events:          make(chan ports.ChatEvent, eventBuffer),
		pending:         make(map[string]*parkedRequest),
		pumpDone:        make(chan struct{}),
		providerScopeID: providerScopeID,
	}
	c.conn = newConnAt(proc.stdin, proc.stdout, log, c.handleServerRequest, proc.nextRequestID)
	return c
}

// start records the opened thread and begins translating notifications. It is
// called once, after the thread is open, so no event is emitted for a
// conversation the caller does not yet have a handle to.
func (c *conversation) start(threadID, model, effort string) {
	c.threadMu.Lock()
	c.threadID = threadID
	c.threadMu.Unlock()
	c.threadModel = model
	c.threadEffort = effort
	go c.pump()
}

// ProviderConversationID is the Codex thread id Tallpine persists for resume.
func (c *conversation) ProviderConversationID() string { return c.thread() }

// PreservesProviderOnClose tells the daemon controller that Close only detaches
// from a host; it must not project provider death or fail in-flight work.
func (c *conversation) PreservesProviderOnClose() bool { return c.proc.terminate != nil }

// ReconnectedLive distinguishes attachment to the same initialized process from
// native-history resume in a replacement process.
func (c *conversation) ReconnectedLive() bool { return c.proc.reconnected }

// Capabilities reports what this conversation can do.
func (c *conversation) Capabilities() ports.ChatCapabilities { return capabilities() }

// Events is the normalized stream. It closes when the conversation ends.
func (c *conversation) Events() <-chan ports.ChatEvent { return c.events }

// pump translates provider notifications into neutral events until the
// connection ends, then reports why and closes the stream.
func (c *conversation) pump() {
	defer close(c.pumpDone)
	defer func() {
		c.eventsMu.Lock()
		defer c.eventsMu.Unlock()
		c.eventsClosed = true
		close(c.events)
	}()
	retries := make(map[string]ports.ChatEvent)
	citations := newCitationFormatter()
	var usageLimits usageLimitTracker

	for n := range c.conn.notifs() {
		// Before normalizing, because a token-usage report is the only place the
		// context position is stated and a compaction event that arrives in the same
		// batch has to be able to read it.
		c.trackContext(n)
		now := time.Now()
		usageLimits.observe(n, c.thread(), now)
		// Some normalized output events omit their thread ID. Read the native
		// envelope so child-thread recovery cannot settle the root's retry.
		var scope struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(n.Params, &scope)
		citations.observeNotification(n, c.thread())

		// The clock is passed in rather than read inside: a rate-limit reset arrives
		// as an absolute instant and has to become a remaining duration, and a
		// normalizer that reads the clock itself cannot be tested deterministically.
		for _, ev := range normalizeNotification(n, now) {
			ev = usageLimits.decorate(ev, now)
			threadID := ev.ProviderConversationID
			if threadID == "" {
				threadID = scope.ThreadID
			}
			if threadID == "" {
				threadID = c.thread()
			}
			var visible bool
			ev, visible = citations.formatEvent(threadID, ev)
			if !visible {
				continue
			}
			key := threadID + ":" + ev.ProviderTurnID
			if ev.Kind == ports.ChatEventError {
				// Turn settlement will settle the running retry along with the failure.
				delete(retries, key)
			}
			isRetry := ev.Kind == ports.ChatEventActivityStarted && strings.HasPrefix(ev.ProviderItemID, "codex-retry:")
			if isRetry {
				if active, ok := retries[key]; ok {
					ev.ProviderItemID = active.ProviderItemID
				} else {
					// Live Codex errors have no item/replay ID. Keep attempts on one
					// row, but never overwrite a recovered episode later in the turn.
					ev.ProviderItemID += ":" + uuid.NewString()
				}
				retries[key] = ev
			}
			completed := ev.Kind == ports.ChatEventTurnCompleted
			recovered := !isRetry && (ev.Kind == ports.ChatEventMessageDelta || ev.Kind == ports.ChatEventMessageCompleted ||
				ev.Kind == ports.ChatEventActivityStarted || ev.Kind == ports.ChatEventReasoningDelta || ev.Kind == ports.ChatEventPlanUpdated)
			if completed || recovered {
				if retry, ok := retries[key]; ok {
					retry.Kind = ports.ChatEventActivityCompleted
					retry.ActivityStatus = domain.ActivityStatusCompleted
					if !completed || (ev.TurnState != domain.TurnStateFailed && ev.Err == nil) {
						c.emit(retry)
					}
					delete(retries, key)
				}
			}
			rootConversation := ev.ProviderConversationID == "" || ev.ProviderConversationID == c.thread()
			if ev.Kind == ports.ChatEventTurnStarted && ev.ProviderTurnID != "" && rootConversation {
				c.mu.Lock()
				c.activeTurn = ev.ProviderTurnID
				// Snapshot the context position this turn starts from. Cheap on every
				// turn, and the only way to know what a compaction reclaimed.
				c.contextAtTurnStart = c.contextTokens
				c.mu.Unlock()
			}
			if ev.Kind == ports.ChatEventTurnCompleted && ev.ProviderTurnID != "" && rootConversation {
				c.mu.Lock()
				if c.activeTurn == ev.ProviderTurnID {
					c.activeTurn = ""
				}
				c.mu.Unlock()
			}
			if ev.Kind == ports.ChatEventCompacted {
				settled, ok := c.settleCompaction(ev)
				if !ok {
					continue
				}
				ev = settled
			}
			if ev.Kind == ports.ChatEventApprovalResolved {
				// The provider resolved it (possibly via another client), so any
				// card Tallpine is still showing is stale.
				c.discardPending(ev.RequestID)
			}
			c.emit(ev)
		}
	}
	// The connection ended. Say so explicitly rather than letting the stream go
	// quiet: a silent channel close is indistinguishable from an idle agent.
	state := ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped}
	if err := c.conn.err(); err != nil {
		state.Err = err
	}
	c.emit(state)
	c.failPendingApprovals()
}

// emit delivers an event, preferring to drop a delta over blocking the reader. A
// lifecycle event is never dropped silently.
func (c *conversation) emit(ev ports.ChatEvent) {
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()
	if c.eventsClosed {
		return
	}
	ev = c.scopedEvent(ev)
	select {
	case c.events <- ev:
		return
	default:
	}

	switch ev.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta:
		// The settled text arrives on item/completed for both of these — a message's
		// as `text`, a reasoning item's as `summary` — so a dropped delta costs
		// smoothness, not correctness.
		//
		// The other streams are deliberately NOT droppable. Command output, terminal
		// keystrokes and tool progress have no settled restatement: the delta is the
		// only account of them, so losing one loses the record.
		c.log.Warn("dropped chat delta: consumer behind",
			"kind", ev.Kind, "item", ev.ProviderItemID)
		return
	}

	select {
	case c.events <- ev:
	case <-time.After(5 * time.Second):
		c.log.Error("dropped chat lifecycle event: consumer stalled", "kind", ev.Kind)
	}
}

// configuredServiceTier is the service_tier the user's Codex config sets, or
// "" for none or when it cannot be read.
func (c *conversation) configuredServiceTier(ctx context.Context) string {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var resp struct {
		Config struct {
			ServiceTier string `json:"service_tier"`
		} `json:"config"`
	}
	if err := c.conn.request(readCtx, "config/read", map[string]any{}, &resp); err != nil {
		return ""
	}
	return resp.Config.ServiceTier
}

// codexFastServiceTier is the fast tier's id before a model list is read.
const codexFastServiceTier = "priority"

// thread is the Codex thread the conversation currently drives. It changes only
// when ClearContext moves the conversation onto a replacement thread.
func (c *conversation) thread() string {
	c.threadMu.RLock()
	defer c.threadMu.RUnlock()
	return c.threadID
}

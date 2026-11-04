package acp

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The conversation's outbound events wait in two places: a small channel the
// consumer reads, preallocated per conversation, and a backlog slice that only
// exists while the consumer is behind. Together they hold at most eventBuffer
// events, the same limit a single preallocated channel used to impose, but an
// idle or keeping-up conversation no longer pins eventBuffer × sizeof(ChatEvent)
// (about 1.7 MB) for its whole life.
//
// Order is preserved: once anything is in the backlog, later events queue
// behind it rather than racing to the channel. A pump goroutine moves the
// backlog into the channel and exits when it is empty.
type eventBacklog struct {
	events  []ports.ChatEvent
	pumping bool
	// room is closed (and cleared) whenever the pump frees space, waking every
	// emitter waiting for it.
	room chan struct{}
	// closeWhenDrained: the conversation stopped while events were still
	// queued; the pump closes the channel after delivering them.
	closeWhenDrained bool
	// spilled: this backlog has grown past eventBuffer (logged once).
	spilled bool
	// bytes estimates what the queued events hold (eventSize), so the spill
	// is bounded by memory, not only by entry count.
	bytes int
}

const (
	// eventChannelBuffer is the part of eventBuffer allocated up front.
	eventChannelBuffer = 64
	// closedDrainWait is how long, once the conversation is closing, the pump
	// waits for a reader to take each queued event before giving up.
	closedDrainWait = 5 * time.Second
	// eventSpillBytes bounds what an unjournaled conversation's queue holds
	// once it has grown past eventBuffer; past it the emitter waits for room
	// instead. It is counted in bytes, not entries: a tool snapshot carries up
	// to 64 KB of output and never merges. eventOverhead, the fixed cost every
	// entry is charged, also bounds the entry count (~32K small events).
	eventSpillBytes = 16 << 20
	eventOverhead   = 512
	// maxMergedDelta caps one merged delta, so merging stays linear in the text.
	maxMergedDelta = 64 << 10
)

// offerEvent queues event without blocking. When the queue is full it returns
// false and a channel that is closed once space may be available.
func (c *conversation) offerEvent(event ports.ChatEvent) (bool, <-chan struct{}) {
	c.backlogMu.Lock()
	defer c.backlogMu.Unlock()
	b := &c.backlog
	if len(b.events) == 0 && !b.pumping {
		select {
		case c.events <- event:
			return true, nil
		default:
		}
	}
	if len(c.events)+len(b.events) >= eventBuffer {
		if b.room == nil {
			b.room = make(chan struct{})
		}
		return false, b.room
	}
	b.events = append(b.events, event)
	b.bytes += eventSize(event)
	if !b.pumping {
		b.pumping = true
		go c.pumpEvents()
	}
	return true, nil
}

// eventSize estimates the memory one queued event holds.
func eventSize(e ports.ChatEvent) int {
	return eventOverhead + len(e.Delta) + len(e.Text) + len(e.Summary) + len(e.Title) + len(e.Detail)
}

// spillEvent queues an unjournaled event the eventBuffer limit refused. A
// stream delta merges into the queued delta just before it when that delta
// belongs to the same item, so a stalled consumer costs memory proportional to
// the text, not to the number of chunks; anything else is appended. Nothing is
// dropped. Past eventSpillBytes it refuses, with a room signal, and the emitter
// applies backpressure instead.
func (c *conversation) spillEvent(event ports.ChatEvent) (bool, <-chan struct{}) {
	c.backlogMu.Lock()
	defer c.backlogMu.Unlock()
	b := &c.backlog
	full := b.bytes >= eventSpillBytes
	// The head may be in the pump's hands; merge only into a later entry.
	if n := len(b.events); !full && n >= 2 && mergeStreamDelta(&b.events[n-1], event) {
		b.bytes += len(event.Delta)
		return true, nil
	}
	if full {
		if b.room == nil {
			b.room = make(chan struct{})
		}
		return false, b.room
	}
	if !b.spilled {
		b.spilled = true
		c.log.Warn("ACP chat consumer behind; holding events past the buffer", "queued", len(b.events))
	}
	b.events = append(b.events, event)
	b.bytes += eventSize(event)
	if !b.pumping {
		b.pumping = true
		go c.pumpEvents()
	}
	return true, nil
}

// mergeStreamDelta appends src's text to dst when both are unjournaled deltas
// of the same kind for the same item and otherwise identical.
func mergeStreamDelta(dst *ports.ChatEvent, src ports.ChatEvent) bool {
	switch src.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta, ports.ChatEventCommandOutputDelta:
	default:
		return false
	}
	if !plainDelta(*dst) || !plainDelta(src) || len(dst.Delta)+len(src.Delta) > maxMergedDelta ||
		deltaIdentityOf(*dst) != deltaIdentityOf(src) {
		return false
	}
	dst.Delta += src.Delta
	return true
}

// deltaIdentity is every comparable ChatEvent field except Delta.
type deltaIdentity struct {
	kind                                         ports.ChatEventKind
	nativeUserMessageID, nativeTurnID            string
	providerTurnID, providerConversationID, item string
	clientMessageID, text, summary, requestID    string
	title                                        string
	turnState                                    domain.TurnState
	activityKind                                 domain.ActivityKind
	activityStatus                               domain.ActivityStatus
	controllerState                              ports.ChatControllerState
}

func deltaIdentityOf(e ports.ChatEvent) deltaIdentity {
	return deltaIdentity{
		kind: e.Kind, nativeUserMessageID: e.NativeUserMessageID, nativeTurnID: e.NativeTurnID,
		providerTurnID: e.ProviderTurnID, providerConversationID: e.ProviderConversationID, item: e.ProviderItemID,
		clientMessageID: e.ClientMessageID, text: e.Text, summary: e.Summary, requestID: e.RequestID,
		title: e.Title, turnState: e.TurnState, activityKind: e.ActivityKind, activityStatus: e.ActivityStatus,
		controllerState: e.ControllerState,
	}
}

// plainDelta reports a delta that carries nothing a merge could lose: no
// journal identity and none of the structured payload fields.
func plainDelta(e ports.ChatEvent) bool {
	return e.ProviderEventID == "" && len(e.ProviderItemAliases) == 0 && len(e.Detail) == 0 &&
		len(e.Decisions) == 0 && e.Input == nil && e.Usage == nil && e.RateLimits == nil &&
		e.Diff == nil && e.Plan == nil && e.Reroute == nil && e.Account == nil &&
		e.ThreadState == nil && len(e.MCPServers) == 0 && e.AsyncTask == nil && e.Err == nil
}

// pumpEvents delivers the backlog in order. Queued events outlive Close the way
// a buffered channel's contents do, so a consumer still draining receives them;
// but a consumer that stopped reading (a failed projector) must not strand this
// goroutine, so after closing it gives up once no reader takes an event in time.
func (c *conversation) pumpEvents() {
	for {
		c.backlogMu.Lock()
		b := &c.backlog
		if len(b.events) == 0 {
			closeEvents := b.closeWhenDrained
			*b = eventBacklog{}
			c.backlogMu.Unlock()
			if closeEvents {
				close(c.events)
			}
			return
		}
		// The head stays queued (and counted against eventBuffer) until the
		// channel takes it; emitters only ever append.
		event := b.events[0]
		c.backlogMu.Unlock()
		delivered := c.deliverQueued(event)
		c.backlogMu.Lock()
		if !delivered {
			// Closing, and nobody is reading: drop what is left.
			closeEvents := b.closeWhenDrained
			if b.room != nil {
				close(b.room)
			}
			*b = eventBacklog{}
			c.backlogMu.Unlock()
			if closeEvents {
				close(c.events)
			}
			return
		}
		b.bytes -= eventSize(b.events[0])
		b.events[0] = ports.ChatEvent{}
		b.events = b.events[1:]
		if b.room != nil {
			close(b.room)
			b.room = nil
		}
		c.backlogMu.Unlock()
	}
}

func (c *conversation) deliverQueued(event ports.ChatEvent) bool {
	select {
	case c.events <- event:
		return true
	case <-c.closing:
	}
	timer := time.NewTimer(closedDrainWait)
	defer timer.Stop()
	select {
	case c.events <- event:
		return true
	case <-timer.C:
		return false
	}
}

// finishEvents delivers the final event after everything already queued and
// then closes the channel. The caller holds eventMu exclusively and has marked
// the events closed, so no emitter can queue behind it.
func (c *conversation) finishEvents(event ports.ChatEvent) {
	c.backlogMu.Lock()
	if c.backlog.pumping {
		c.backlog.events = append(c.backlog.events, event)
		c.backlog.bytes += eventSize(event)
		c.backlog.closeWhenDrained = true
		c.backlogMu.Unlock()
		return
	}
	c.backlogMu.Unlock()
	select {
	case c.events <- event:
	case <-c.closing:
	}
	close(c.events)
}

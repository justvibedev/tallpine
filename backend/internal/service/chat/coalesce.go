package chat

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// activityFlushInterval is the longest a run of streamed activity text (command
// output, reasoning, tool progress) waits for its write while the stream keeps
// going, when storage compaction is on. Assistant message deltas keep
// deltaFlushInterval: that is the text a person watches arrive. A var so tests
// can change it.
var activityFlushInterval = 250 * time.Millisecond

// maxCoalescedItems bounds the per-item memory below. Losing it only means the
// next snapshot of an item is stored in full once more.
const maxCoalescedItems = 4096

// writeCoalescer is the store's view of the storage-compaction setting.
type writeCoalescer interface {
	CoalescesWrites() bool
}

// coalescing reports whether the storage-compaction setting is on for this
// controller's store.
func (c *Controller) coalescing() bool {
	store, ok := c.store.(writeCoalescer)
	return ok && store.CoalescesWrites()
}

// deltaPolicy is how the event pump batches streamed deltas.
type deltaPolicy struct {
	// interval paces runs of assistant message deltas.
	interval time.Duration
	// activityInterval paces runs of streamed activity text.
	activityInterval time.Duration
	// joinSnapshots lets an activity.started snapshot of the item a run of
	// activity text belongs to join that run instead of ending it.
	joinSnapshots bool
}

func (c *Controller) deltaPolicy(interval time.Duration) func() deltaPolicy {
	return func() deltaPolicy {
		if !c.coalescing() || interval <= 0 {
			// A zero interval turns batching off altogether.
			return deltaPolicy{interval: interval, activityInterval: interval}
		}
		return deltaPolicy{
			interval:         interval,
			activityInterval: max(interval, activityFlushInterval),
			joinSnapshots:    true,
		}
	}
}

// activityCoalescer remembers, per provider item, what was last archived and
// whether the item's command output is streaming into its own column. The zero
// value is ready to use.
type activityCoalescer struct {
	mu sync.Mutex
	// archived is the fingerprint of the last activity.started archived for an
	// item, without its provider event identity.
	archived map[string][32]byte
	// streaming marks items whose command output deltas have landed in the
	// activity's command_output column.
	streaming map[string]bool
}

// repeat reports whether fingerprint matches the item's last archived snapshot.
func (a *activityCoalescer) repeat(item string, fingerprint [32]byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	last, ok := a.archived[item]
	return ok && last == fingerprint
}

func (a *activityCoalescer) archivedSnapshot(item string, fingerprint [32]byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.archived == nil || len(a.archived) >= maxCoalescedItems {
		a.archived = make(map[string][32]byte)
	}
	a.archived[item] = fingerprint
}

func (a *activityCoalescer) outputStreaming(item string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streaming[item]
}

func (a *activityCoalescer) markOutputStreaming(item string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.streaming == nil || len(a.streaming) >= maxCoalescedItems {
		a.streaming = make(map[string]bool)
	}
	a.streaming[item] = true
}

// settled forgets one item once it completed.
func (a *activityCoalescer) settled(item string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.archived, item)
	delete(a.streaming, item)
}

// reset forgets every item, at a turn boundary.
func (a *activityCoalescer) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archived, a.streaming = nil, nil
}

// observe updates the per-item memory for an event whose projection committed.
func (a *activityCoalescer) observe(event ports.ChatEvent) {
	switch event.Kind {
	case ports.ChatEventActivityCompleted:
		a.settled(event.ProviderItemID)
	case ports.ChatEventTurnCompleted:
		a.reset()
	}
}

// coalesceSnapshot trims a running command snapshot whose output streams into
// the activity's command_output column.
//
// ACP providers re-send the whole tool call on every update, so each output
// chunk also arrives as a snapshot carrying all output so far in
// detail.output. Stored, that rewrites the full output into the activity row and
// the archive once per chunk, and makes every snapshot differ from the last.
// The read path already prefers the streamed column over detail.output
// (activityDetailPayload), so dropping it from running snapshots hides nothing,
// and the snapshot becomes an unchanged repeat that is neither rewritten nor
// archived in full. The completing snapshot keeps its output.
//
// streamingInUnit reports that an output delta for the item precedes the
// snapshot in the same unit. projectRunWithSnapshots applies the snapshot before
// the unit's text, so that text lands in command_output in the same commit.
func (c *Controller) coalesceSnapshot(event ports.ChatEvent, streamingInUnit bool) ports.ChatEvent {
	if event.Kind != ports.ChatEventActivityStarted || event.ActivityKind != domain.ActivityKindCommand ||
		event.ProviderItemID == "" || len(event.Detail) == 0 ||
		(!streamingInUnit && !c.coalesce.outputStreaming(event.ProviderItemID)) {
		return event
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(event.Detail, &detail); err != nil {
		return event
	}
	_, hasOutput := detail["output"]
	_, hasTruncated := detail["outputTruncated"]
	if !hasOutput && !hasTruncated {
		return event
	}
	delete(detail, "output")
	delete(detail, "outputTruncated")
	trimmed, err := json.Marshal(detail)
	if err != nil {
		return event
	}
	event.Detail = trimmed
	return event
}

// snapshotArchivePayload returns the archive payload for an activity.started
// record. A snapshot identical to the last one archived for its item, apart
// from its provider event identity, is stored as a marker that keeps the
// identity and the arrival time (received_at) instead of the whole payload. It
// reports the snapshot's fingerprint for the caller to remember once archived.
func snapshotArchivePayload(event ports.ChatEvent, record map[string]any, coalescer *activityCoalescer) ([]byte, [32]byte, error) {
	identity, hasIdentity := record["providerEventId"]
	delete(record, "providerEventId")
	body, err := json.Marshal(record)
	if hasIdentity {
		record["providerEventId"] = identity
	}
	if err != nil {
		return nil, [32]byte{}, err
	}
	fingerprint := sha256.Sum256(body)
	if coalescer.repeat(event.ProviderItemID, fingerprint) {
		marker := map[string]any{
			"kind":            event.Kind,
			"providerEventId": event.ProviderEventID,
			"providerItemId":  event.ProviderItemID,
			"providerTurnId":  event.ProviderTurnID,
			"unchanged":       true,
		}
		dropEmptyArchiveFields(marker)
		payload, err := json.Marshal(marker)
		return payload, fingerprint, err
	}
	payload, err := json.Marshal(record)
	return payload, fingerprint, err
}

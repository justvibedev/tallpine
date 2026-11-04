package acp

import (
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func newQueueTestConversation() *conversation {
	return &conversation{
		events: make(chan ports.ChatEvent, eventChannelBuffer),
		log:    slog.New(slog.DiscardHandler),
	}
}

func queuedDelta(i int) ports.ChatEvent {
	return ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "item", Delta: fmt.Sprint(i)}
}

// A conversation allocates only the small channel up front; the rest of
// eventBuffer is allocated while, and only while, its consumer is behind.
func TestEventQueuePreallocatesOnlyTheChannel(t *testing.T) {
	daemon, host := net.Pipe()
	defer host.Close()
	conv := newConversation(&process{stdin: daemon, stdout: daemon, stop: daemon.Close}, slog.New(slog.DiscardHandler), "", nil, nil)
	defer conv.Close()
	if got := cap(conv.events); got != eventChannelBuffer {
		t.Fatalf("preallocated %d event slots, want %d", got, eventChannelBuffer)
	}
}

// Events that spill past the channel keep their order, and the backlog is
// released once the consumer catches up.
func TestEventQueueKeepsOrderAcrossTheBacklog(t *testing.T) {
	conv := newQueueTestConversation()
	const n = 1000
	for i := range n {
		conv.emit(queuedDelta(i))
	}
	for i := range n {
		select {
		case event := <-conv.Events():
			if event.Delta != fmt.Sprint(i) {
				t.Fatalf("event %d = %q, out of order", i, event.Delta)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		conv.backlogMu.Lock()
		released := !conv.backlog.pumping && conv.backlog.events == nil
		conv.backlogMu.Unlock()
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backlog was not released after draining")
		}
		time.Sleep(time.Millisecond)
	}
	// Once drained, events go straight to the channel again.
	conv.emit(queuedDelta(n))
	if event := <-conv.Events(); event.Delta != fmt.Sprint(n) {
		t.Fatalf("post-drain event = %q", event.Delta)
	}
}

// The queue's limit is still eventBuffer: an unjournaled conversation whose
// consumer is stalled holds that many events before it refuses more.
func TestEventQueueHoldsEventBuffer(t *testing.T) {
	conv := newQueueTestConversation()
	for i := range eventBuffer {
		if queued, _ := conv.offerEvent(queuedDelta(i)); !queued {
			t.Fatalf("refused event %d below the limit", i)
		}
	}
	queued, room := conv.offerEvent(queuedDelta(eventBuffer))
	if queued || room == nil {
		t.Fatalf("accepted an event past the limit (queued=%v)", queued)
	}
	<-conv.Events()
	select {
	case <-room:
	case <-time.After(time.Second):
		t.Fatal("draining one event did not signal room")
	}
}

// A journaled emitter waits at the limit instead of dropping, and its event
// lands after everything queued before it.
func TestEventQueueJournaledEmitWaitsForRoom(t *testing.T) {
	conv, _ := newPipeConversation(t)
	defer conv.Close()
	for i := range eventBuffer {
		if queued, _ := conv.offerEvent(queuedDelta(i)); !queued {
			t.Fatalf("refused event %d below the limit", i)
		}
	}
	emitted := make(chan struct{})
	go func() {
		conv.emit(queuedDelta(eventBuffer))
		close(emitted)
	}()
	select {
	case <-emitted:
		t.Fatal("journaled emit returned while the queue was full")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i <= eventBuffer; i++ {
		select {
		case event := <-conv.Events():
			if event.Delta != fmt.Sprint(i) {
				t.Fatalf("event %d = %q, out of order", i, event.Delta)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %d never arrived", i)
		}
	}
	<-emitted
}

// An unjournaled conversation has no replay, so a consumer that stalls (a busy
// SQLite writer) must not cost streamed text: deltas past eventBuffer merge
// into the queued delta for their item, lifecycle events queue behind them in
// order, and the provider's reader never blocks.
func TestEventQueueUnjournaledStallLosesNothing(t *testing.T) {
	conv := newQueueTestConversation()
	type key struct {
		kind ports.ChatEventKind
		item string
	}
	want := map[key]string{}
	var script []ports.ChatEvent
	stream := func(kind ports.ChatEventKind, item string, n int) {
		for i := range n {
			text := fmt.Sprintf("%s%d;", item, i)
			want[key{kind, item}] += text
			script = append(script, ports.ChatEvent{Kind: kind, ProviderItemID: item, ProviderTurnID: "turn", Delta: text})
		}
	}
	stream(ports.ChatEventMessageDelta, "m1", eventBuffer+2000)
	script = append(script, ports.ChatEvent{Kind: ports.ChatEventUsage, ProviderTurnID: "turn"})
	stream(ports.ChatEventReasoningDelta, "r1", 3000)
	stream(ports.ChatEventMessageDelta, "m2", 3000)
	script = append(script, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn"})

	emitted := make(chan struct{})
	go func() {
		for _, event := range script {
			conv.emit(event)
		}
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("unjournaled emit blocked on a stalled consumer")
	}

	got := map[key]string{}
	var order []string
	received := 0
	for {
		var event ports.ChatEvent
		select {
		case event = <-conv.Events():
		case <-time.After(time.Second):
			t.Fatalf("stream ended early after %d events", received)
		}
		received++
		switch event.Kind {
		case ports.ChatEventUsage, ports.ChatEventTurnCompleted:
			order = append(order, string(event.Kind))
		default:
			k := key{event.Kind, event.ProviderItemID}
			if got[k] == "" {
				order = append(order, k.item)
			}
			got[k] += event.Delta
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	for k, text := range want {
		if got[k] != text {
			t.Fatalf("%s %s lost text: got %d bytes, want %d", k.kind, k.item, len(got[k]), len(text))
		}
	}
	if fmt.Sprint(order) != "[m1 usage r1 m2 turn.completed]" {
		t.Fatalf("event order = %v", order)
	}
	if received >= len(script) {
		t.Fatalf("received %d events for %d emitted; nothing was merged", received, len(script))
	}
}

// Past eventSpillBytes an unjournaled emitter waits for room rather than
// dropping, and its event still arrives in order.
func TestEventQueueUnjournaledSpillLimitWaits(t *testing.T) {
	conv := newQueueTestConversation()
	full := func() bool {
		conv.backlogMu.Lock()
		defer conv.backlogMu.Unlock()
		return conv.backlog.bytes >= eventSpillBytes
	}
	sent := 0
	for ; !full(); sent++ {
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Delta: fmt.Sprint(sent)})
	}
	emitted := make(chan struct{})
	go func() {
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Delta: "last"})
		close(emitted)
	}()
	select {
	case <-emitted:
		t.Fatal("emit past eventSpillBytes did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i <= sent; i++ {
		event := <-conv.Events()
		want := fmt.Sprint(i)
		if i == sent {
			want = "last"
		}
		if event.Delta != want {
			t.Fatalf("event %d = %q, want %q", i, event.Delta, want)
		}
	}
	<-emitted
}

// Only plain deltas of the same kind for the same item merge; a journaled
// event keeps its own identity for acknowledgement.
func TestMergeStreamDeltaRules(t *testing.T) {
	base := ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m", ProviderTurnID: "t", Delta: "a"}
	for _, tc := range []struct {
		name string
		src  ports.ChatEvent
		want bool
	}{
		{"same item", ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m", ProviderTurnID: "t", Delta: "b"}, true},
		{"other item", ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "n", ProviderTurnID: "t", Delta: "b"}, false},
		{"other kind", ports.ChatEvent{Kind: ports.ChatEventReasoningDelta, ProviderItemID: "m", ProviderTurnID: "t", Delta: "b"}, false},
		{"other turn", ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m", ProviderTurnID: "u", Delta: "b"}, false},
		{"journaled", ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m", ProviderTurnID: "t", Delta: "b", ProviderEventID: "e:0"}, false},
		{"not a delta", ports.ChatEvent{Kind: ports.ChatEventUsage, ProviderItemID: "m", ProviderTurnID: "t"}, false},
		{"too large", ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m", ProviderTurnID: "t", Delta: string(make([]byte, maxMergedDelta))}, false},
	} {
		dst := base
		if got := mergeStreamDelta(&dst, tc.src); got != tc.want {
			t.Errorf("%s: merged=%v, want %v", tc.name, got, tc.want)
		}
		if tc.want && dst.Delta != "ab" {
			t.Errorf("%s: merged delta = %q", tc.name, dst.Delta)
		}
		if !tc.want && dst.Delta != "a" {
			t.Errorf("%s: refused merge still changed dst: %q", tc.name, dst.Delta)
		}
	}
}

// ACP's ToolCallUpdate emits a command-output delta and then a full tool
// snapshot (up to 64 KB of output) for the same item, so those deltas never
// merge. A stalled consumer must still bound the unjournaled queue by bytes,
// backpressure past it, and lose nothing once it resumes.
func TestEventQueueUnjournaledSnapshotsAreByteBounded(t *testing.T) {
	conv := newQueueTestConversation()
	for i := range eventBuffer {
		if queued, _ := conv.offerEvent(ports.ChatEvent{Kind: ports.ChatEventUsage, Delta: fmt.Sprint(i)}); !queued {
			t.Fatalf("refused event %d below the limit", i)
		}
	}
	detail := []byte(strings.Repeat("o", 64<<10))
	snapshot := func(i int) ports.ChatEvent {
		return ports.ChatEvent{Kind: ports.ChatEventActivityStarted, ProviderItemID: "tool", ProviderTurnID: "t",
			ActivityKind: domain.ActivityKindCommand, Detail: detail, Summary: fmt.Sprint(i)}
	}
	const updates = 1000 // ~64 MB of snapshots if nothing bounded them
	var progressed atomic.Int32
	done := make(chan struct{})
	go func() {
		for i := range updates {
			conv.emit(ports.ChatEvent{Kind: ports.ChatEventCommandOutputDelta, ProviderItemID: "tool", ProviderTurnID: "t", Delta: fmt.Sprintf("line %d\n", i)})
			conv.emit(snapshot(i))
			progressed.Store(int32(i + 1))
		}
		close(done)
	}()
	last := int32(-1)
	for stable := 0; stable < 5; {
		time.Sleep(20 * time.Millisecond)
		select {
		case <-done:
			t.Fatal("queued every snapshot without backpressure")
		default:
		}
		if p := progressed.Load(); p == last {
			stable++
		} else {
			last, stable = p, 0
		}
	}
	conv.backlogMu.Lock()
	queuedBytes, queued := conv.backlog.bytes, len(conv.backlog.events)
	conv.backlogMu.Unlock()
	if limit := eventSpillBytes + eventSize(snapshot(0)); queuedBytes > limit {
		t.Fatalf("stalled queue holds %d bytes, want at most %d", queuedBytes, limit)
	}
	t.Logf("backpressure after %d of %d updates: %d entries, %.1f MiB queued", last, updates, queued, float64(queuedBytes)/(1<<20))

	next := func() ports.ChatEvent {
		t.Helper()
		select {
		case event := <-conv.Events():
			return event
		case <-time.After(5 * time.Second):
			t.Fatal("stream stopped")
			return ports.ChatEvent{}
		}
	}
	for i := range eventBuffer {
		if event := next(); event.Delta != fmt.Sprint(i) {
			t.Fatalf("prefill %d = %q", i, event.Delta)
		}
	}
	for i := range updates {
		if event := next(); event.Kind != ports.ChatEventCommandOutputDelta || event.Delta != fmt.Sprintf("line %d\n", i) {
			t.Fatalf("update %d delta = %s %q", i, event.Kind, event.Delta)
		}
		if event := next(); event.Kind != ports.ChatEventActivityStarted || event.Summary != fmt.Sprint(i) || len(event.Detail) != len(detail) {
			t.Fatalf("update %d snapshot = %s %q", i, event.Kind, event.Summary)
		}
	}
	<-done
	conv.backlogMu.Lock()
	defer conv.backlogMu.Unlock()
	if conv.backlog.bytes != 0 {
		t.Fatalf("drained queue still accounts %d bytes", conv.backlog.bytes)
	}
}

// mergeStreamDelta must see every ChatEvent field: deltaIdentity compares it or
// plainDelta requires it empty. A new field missing from both would be
// silently overwritten by a merge.
func TestMergeStreamDeltaClassifiesEveryChatEventField(t *testing.T) {
	classified := map[string]bool{"Delta": true}
	for _, name := range []string{ // deltaIdentity
		"Kind", "NativeUserMessageID", "NativeTurnID", "ProviderTurnID", "ProviderConversationID",
		"ProviderItemID", "ClientMessageID", "Text", "Summary", "RequestID", "Title", "TurnState",
		"ActivityKind", "ActivityStatus", "ControllerState",
	} {
		classified[name] = true
	}
	for _, name := range []string{ // plainDelta
		"ProviderEventID", "ProviderItemAliases", "Detail", "Decisions", "Input", "Usage", "RateLimits",
		"Diff", "Plan", "Reroute", "Account", "ThreadState", "MCPServers", "AsyncTask", "Err",
	} {
		classified[name] = true
	}
	fields := reflect.TypeFor[ports.ChatEvent]()
	for i := range fields.NumField() {
		if name := fields.Field(i).Name; !classified[name] {
			t.Errorf("ChatEvent.%s is in neither deltaIdentity nor plainDelta; a merge would overwrite it", name)
		}
	}
}

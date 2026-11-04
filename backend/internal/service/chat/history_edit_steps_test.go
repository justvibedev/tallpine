package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestEditDeliveryIsSettled(t *testing.T) {
	t.Parallel()
	const req = `{"a":1}`
	cases := []struct {
		name     string
		delivery domain.ConversationEditDelivery
		want     bool
	}{
		{"fresh reservation of same request is re-run", domain.ConversationEditDelivery{
			RequestJSON: req, State: domain.ConversationEditReserved}, false},
		{"different request is replayed", domain.ConversationEditDelivery{
			RequestJSON: `{"a":2}`, State: domain.ConversationEditReserved}, true},
		{"provider work in flight is replayed", domain.ConversationEditDelivery{
			RequestJSON: req, State: domain.ConversationEditReserved, ProviderWorkStarted: true}, true},
		{"finished delivery is replayed", domain.ConversationEditDelivery{
			RequestJSON: req, State: domain.ConversationEditDeliveryState("done")}, true},
	}
	for _, tc := range cases {
		if got := editDeliveryIsSettled(tc.delivery, req); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEditRunRestoreWithoutStoppedSourceKeepsFence(t *testing.T) {
	t.Parallel()
	r := &editRun{abortSource: true}
	cause := errors.New("boom")
	if got := r.restore(context.Background(), cause); !errors.Is(got, cause) {
		t.Fatalf("restore changed the error: %v", got)
	}
	if !r.abortSource {
		t.Fatal("source fence must still abort when the source was never closed")
	}
}

func TestEditRunNewBranch(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	s := &Service{now: func() time.Time { return now }}
	anchor := domain.ConversationEditAnchor{SourceBranchID: "src", ReplacedTurnID: "t1", ForkAfterSequence: 7}
	r := &editRun{
		s: s, id: "sess", anchor: anchor, providerConversationID: "prov",
		source:       &Controller{conversation: domain.ConversationRecord{ID: "conv"}},
		sourceBranch: domain.ConversationBranch{HandoffNote: "note", ProviderIDsScoped: true},
	}
	b := r.newBranch("b1")
	if b.Strategy != domain.ConversationBranchStrategyNative || b.ParentBranchID != "src" ||
		b.ForkAfterSequence != 7 || b.HandoffNote != "note" || !b.ProviderIDsScoped || b.ReplayCutoffSequence != 0 {
		t.Fatalf("native branch = %#v", b)
	}
	r.replayContent = ports.ChatContent{Type: "text"}
	r.replayTruncated = true
	r.providerScopeID = "scope"
	b = r.newBranch("b2")
	if b.Strategy != domain.ConversationBranchStrategyApproximateContext ||
		b.ReplayCutoffSequence != 7 || !b.ReplayTruncated || b.ProviderScopeID != "scope" {
		t.Fatalf("replay branch = %#v", b)
	}
}

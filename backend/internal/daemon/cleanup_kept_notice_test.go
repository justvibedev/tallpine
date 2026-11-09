package daemon

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
)

type recordingOwner struct{ got []ports.NotificationIntent }

func (r *recordingOwner) Notify(_ context.Context, i ports.NotificationIntent) error {
	r.got = append(r.got, i)
	return nil
}

func TestKeptWorktreeNotifierKeysPerSessionAndReason(t *testing.T) {
	owner := &recordingOwner{}
	notify := keptWorktreeNotifier(owner)
	notify(context.Background(), cleanupsvc.KeptNotice{SessionID: "s1", Path: "/w/s1", Reason: "uncommitted changes; salvaged work: /d/salvage/s1", ReasonKey: "uncommitted changes", SalvagePath: "/d/salvage/s1"})
	if len(owner.got) != 1 {
		t.Fatalf("got %d intents", len(owner.got))
	}
	in := owner.got[0]
	if in.Type != domain.NotificationWorktreeKept || in.SourceKey != "worktree-kept:s1:uncommitted changes" ||
		!strings.Contains(in.Body, "/d/salvage/s1") || !strings.Contains(in.Body, "/w/s1") {
		t.Fatalf("intent = %+v", in)
	}
	if keptWorktreeNotifier(nil) != nil {
		t.Fatal("a nil owner must give no notifier")
	}
}

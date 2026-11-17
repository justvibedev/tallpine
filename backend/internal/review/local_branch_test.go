package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// newRootRepo is a worktree recorded on tp/mer-1/root (no commits beyond
// main) that the worker left for checkedOut, which carries one commit.
func newRootRepo(t *testing.T, checkedOut string) (localRepo, domain.SessionRecord) {
	t.Helper()
	r := newLocalRepo(t, 0)
	r.git("branch", "-q", "-m", "feat", "tp/mer-1/root")
	r.git("checkout", "-q", "-b", checkedOut)
	r.commit("topic.txt", "topic\n")
	w := r.worker()
	w.Metadata.Branch = "tp/mer-1/root"
	return r, w
}

// B748: a worker that committed on a self-made sibling of its /root branch is
// reviewed on that branch, not refused with "nothing to review" on root.
func TestTriggerFollowsSelfMadeSessionBranch(t *testing.T) {
	r, w := newRootRepo(t, "tp/mer-1/topic")
	if got := LocalBranch(context.Background(), w); got != "tp/mer-1/topic" {
		t.Fatalf("LocalBranch = %q", got)
	}
	store := &fakeStore{}
	eng := newEngineForTest(store, fakeSessions{rec: w, ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{handle: "h"})
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatal(err)
	}
	if len(store.runs) != 1 || store.runs[0].PRURL != "local:tp/mer-1/topic" || store.runs[0].TargetSHA != r.git("rev-parse", "tp/mer-1/topic") {
		t.Fatalf("runs %+v", store.runs)
	}
}

// A checked-out branch outside the session's namespace (another session's, or
// an unrelated one) is never adopted: the recorded branch stays the target.
func TestLocalBranchIgnoresForeignCheckout(t *testing.T) {
	for _, foreign := range []string{"tp/mer-2/topic", "feature-x", "tp/mer-10/root"} {
		_, w := newRootRepo(t, foreign)
		if got := LocalBranch(context.Background(), w); got != "tp/mer-1/root" {
			t.Fatalf("%s: LocalBranch = %q", foreign, got)
		}
		if _, ok, err := LocalTarget(context.Background(), w); ok || !errors.Is(err, ErrNoLocalCommits) {
			t.Fatalf("%s: ok=%v err=%v", foreign, ok, err)
		}
	}
}

func TestLocalBranchDetachedHeadKeepsRecorded(t *testing.T) {
	r, w := newRootRepo(t, "tp/mer-1/topic")
	r.git("checkout", "-q", "--detach")
	if got := LocalBranch(context.Background(), w); got != "tp/mer-1/root" {
		t.Fatalf("LocalBranch = %q", got)
	}
}

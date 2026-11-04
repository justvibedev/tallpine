package lifecycle

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type recordingMergeNotifier struct{ got []string }

func (r *recordingMergeNotifier) NotifyMerge(_ context.Context, p domain.ProjectID, url string) {
	r.got = append(r.got, string(p)+" "+url)
}

// A merged PR observation announces the merge (project + URL); an open one does not.
func TestPRObservation_MergedAnnouncesToMergeNotifier(t *testing.T) {
	m, st, _ := newManager()
	n := &recordingMergeNotifier{}
	m.SetMergeNotifier(n)
	rec := working("mer-1")
	rec.ProjectID = "mer"
	st.sessions["mer-1"] = rec
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1"}}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1"}); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 0 {
		t.Fatalf("open PR announced: %v", n.got)
	}
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(n.got) != 1 || n.got[0] != "mer pr1" {
		t.Fatalf("announced %v, want [mer pr1]", n.got)
	}
}

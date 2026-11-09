package review

import (
	stdctx "context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeItems struct {
	item domain.BacklogItem
	ok   bool
	err  error
}

func (f fakeItems) LinkedItemForWorker(stdctx.Context, domain.SessionID) (domain.BacklogItem, bool, error) {
	return f.item, f.ok, f.err
}

// The reviewer sees the linked item's current done-when and the spawn brief,
// not the backlog section captured when the worker started (B601).
func TestReviewLaunchSpecUsesCurrentBacklogItem(t *testing.T) {
	ctx := stdctx.Background()
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}
	worker.Metadata.Prompt = "BRIEF only do the API part\n\n" + domain.BacklogPromptHeader + "Backlog item B593: T\n\nDone when:\nOLD criteria"
	item := domain.BacklogItem{Seq: 593, Title: "T", DoneWhen: "NEW criteria", AgentNotes: "NOTES"}

	spec := (&Engine{projects: fakeProjects{}, items: fakeItems{item: item, ok: true}}).
		reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "")
	for _, want := range []string{"BRIEF only do the API part", "NEW criteria", "NOTES"} {
		if !strings.Contains(spec.TaskContext, want) {
			t.Fatalf("task context lacks %q: %q", want, spec.TaskContext)
		}
	}
	if strings.Contains(spec.TaskContext, "OLD") {
		t.Fatalf("stale done-when kept: %q", spec.TaskContext)
	}

	noMarker := worker
	noMarker.Metadata.Prompt = "plain OLD brief"
	for name, c := range map[string]struct {
		src ItemSource
		w   domain.SessionRecord
	}{
		"nil": {nil, worker}, "none": {fakeItems{}, worker},
		"error": {fakeItems{err: errors.New("x")}, worker}, "no marker": {fakeItems{item: item, ok: true}, noMarker},
	} {
		spec := (&Engine{projects: fakeProjects{}, items: c.src}).
			reviewLaunchSpec(ctx, c.w, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "")
		if !strings.Contains(spec.TaskContext, "OLD") {
			t.Fatalf("%s: want stored prompt fallback, got %q", name, spec.TaskContext)
		}
	}
}

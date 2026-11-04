package backlog

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPublishingBriefLocalScopeSaysCommitAndReportReady(t *testing.T) {
	got := publishingBrief(domain.BacklogScopeLocal)
	for _, want := range []string{"Commit your work on your branch", "do not push", "do not open a PR", "report ready"} {
		if !strings.Contains(got, want) {
			t.Fatalf("local brief %q lacks %q", got, want)
		}
	}
}

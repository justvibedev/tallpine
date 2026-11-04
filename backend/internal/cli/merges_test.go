package cli

import (
	"bytes"
	"strings"
	"testing"

	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
)

func TestWriteMergeDigestListsMergedInFlightAndOverlaps(t *testing.T) {
	d := mergessvc.Digest{
		RecentlyMerged: []mergessvc.MergeEntry{{Number: 4, Title: "done", Lane: "B3", Paths: []string{"a.go"}}},
		InFlight:       []mergessvc.MergeEntry{{Number: 7, Title: "wip", State: "open", Lane: "B9", Paths: []string{"a.go", "b.go", "c.go", "d.go"}}, {Number: 8, Title: "bare", State: "draft"}},
		Overlaps:       []mergessvc.MergeOverlap{{Number: 7, With: 4, CrossLane: true, Advice: "#4 merged touching the same files: rebase #7"}},
	}
	var out bytes.Buffer
	if err := writeMergeDigest(&out, d); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#4 [B3] done - a.go", "#7 [B9] wip [open] - a.go, b.go, c.go, +1 more", "#8 bare [draft] - files unknown", "! #4 merged touching the same files: rebase #7 [cross-lane]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestOverlapRefusalNamesOnlyTheWaitingInFlightPR(t *testing.T) {
	d := mergessvc.Digest{Overlaps: []mergessvc.MergeOverlap{
		{Number: 9, With: 7, Advice: "merge #7 first, then rebase #9"},
		{Number: 12, With: 4, WithMerged: true, Advice: "#4 merged touching the same files"},
	}}
	if got := overlapRefusal(d, 9); got != "merge #7 first, then rebase #9" {
		t.Errorf("pr 9: got %q", got)
	}
	for _, pr := range []int{7, 12, 99} {
		if got := overlapRefusal(d, pr); got != "" {
			t.Errorf("pr %d should not be refused, got %q", pr, got)
		}
	}
}

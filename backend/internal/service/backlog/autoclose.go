package backlog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// AutoClose is the lifecycle rule's verdict on one live item: whether a merge
// closes it now, and in words why not when it does not. Reconcile acts on it,
// logs it, and `tallpine backlog show` prints Reason for an item that is still open
// although a linked PR merged.
type AutoClose struct {
	Close bool
	// Merged lists the merged PR numbers behind the decision, ascending.
	Merged []int64
	// Reason is the closed reason when Close is set, else why the item stays open.
	Reason string
}

// DecideAutoClose applies the "every linked PR merged" rule to an item and its
// facts; an item with a done-when also needs a merged PR whose title says
// "closes <ref>".
func DecideAutoClose(item domain.BacklogItem, facts store.BacklogFacts) AutoClose {
	var merged, open, closed []int64
	for _, pr := range facts.PRs {
		switch pr.State {
		case "merged":
			merged = append(merged, pr.Number)
		case "closed":
			closed = append(closed, pr.Number)
		default:
			open = append(open, pr.Number)
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	out := AutoClose{Merged: merged}
	switch {
	case item.Scope == domain.BacklogScopeLocal:
		out.Reason = "scope is local: a merge never closes it (multi-PR work)"
	case len(facts.PRs) == 0 && len(facts.UntrackedPRs) == 0:
		out.Reason = "no PR is linked to it"
		if n := len(facts.AckedPRs); n > 0 {
			nums := make([]int64, n)
			for i, pr := range facts.AckedPRs {
				nums[i] = pr.Number
			}
			out.Reason = "reopened after " + prList("merged", nums) + "; waiting for a new merge"
		} else if n := len(facts.EarlierPRs); n > 0 {
			out.Reason = fmt.Sprintf("only %d PR(s) the linked worker opened before it was linked, which do not count", n)
		}
	case len(facts.UntrackedPRs) > 0:
		out.Reason = "linked PR " + strings.Join(facts.UntrackedPRs, ", ") + " is not tracked, so its merge cannot be verified"
	case len(open) > 0:
		out.Reason = "waiting for " + prList("open", open)
	case len(closed) > 0:
		out.Reason = prList("closed without merge", closed)
	case strings.TrimSpace(item.DoneWhen) != "" && !claimsDoneWhen(item.Ref(), facts.PRs):
		out.Reason = prList("merged", merged) + " does not claim the done-when: put \"closes " + item.Ref() + "\" in the PR title to close it, or close the item by hand"
	default:
		out.Close = true
		out.Reason = domain.BacklogReasonMerged(merged)
	}
	return out
}

// A qualifier next to a claim cancels it: before ("partly closes B12") or after
// ("closes B12 part 1", "(closes B12, partial)").
var (
	claimQualifiedBefore = regexp.MustCompile(`(?i)\b(?:partly|partially|partial|not|never|part\s+of)\s+(?:\w+\s+)?$`)
	claimQualifiedAfter  = regexp.MustCompile(`(?i)\b(?:part\s*\d+|part\s+of|partly|partially|partial|wip|step\s*\d+)\b`)
)

// claimsDoneWhen reports whether a merged PR says in its title that it fully
// delivers the item: the exact word "closes" before the item ref ("closes B12").
// Other verbs do not count, and a qualifier before it ("partly closes B12",
// "does not closes B12") or after it ("closes B12 part 1") cancels it, so a partial PR never closes the item.
func claimsDoneWhen(ref string, prs []store.BacklogPRFact) bool {
	if ref == "" {
		return false
	}
	claim := regexp.MustCompile(`(?i)\bcloses\s+` + regexp.QuoteMeta(ref) + `\b`)
	for _, pr := range prs {
		if pr.State != "merged" {
			continue
		}
		for _, loc := range claim.FindAllStringIndex(pr.Title, -1) {
			if !claimQualifiedBefore.MatchString(pr.Title[:loc[0]]) && !claimQualifiedAfter.MatchString(pr.Title[loc[1]:]) {
				return true
			}
		}
	}
	return false
}

func prList(what string, numbers []int64) string {
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return "PR " + strings.Join(parts, ", ") + " " + what
}

// Explain is the one-line status for `tallpine backlog show`.
func (a AutoClose) Explain() string {
	if a.Close {
		return "closing: " + a.Reason
	}
	return "stays open: " + a.Reason
}

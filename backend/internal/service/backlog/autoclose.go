package backlog

import (
	"fmt"
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

// DecideAutoClose applies the "every linked PR merged" rule to an item and its facts.
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
	default:
		out.Close = true
		out.Reason = domain.BacklogReasonMerged(merged)
	}
	return out
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

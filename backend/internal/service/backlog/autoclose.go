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

// A qualifier next to a claim cancels it: just before it ("partly closes B12"),
// as a label ("Part 1: closes B12", "(partial) closes B12") or leading the
// title, after an optional "type(scope):" prefix ("WIP: ...", "fix(x): partial fix, ..."), or after it ("closes B12 part 1"). A qualifier word inside
// the description ("keep partial drafts (closes B12)") does not.
var (
	claimQualifiedBefore = regexp.MustCompile(`(?i)(?:\b(?:partly|partially|partial|not|never|part\s+of)\s+(?:\w+\s+)?|\b(?:wip|partly|partially|partial|(?:part|step)\s*\d+(?:\s*(?:/|of)\s*\d+)?)\W*)$|^\W*(?:\w+(?:\([^)]*\))?!?:\s*)?(?:wip|partly|partially|partial|part\s*\d+|step\s*\d+)\b`)
	claimQualifiedAfter  = regexp.MustCompile(`(?i)\b(?:part\s*\d+|part\s+of|partly|partially|partial|wip|step\s*\d+)\b`)
)

// claimsDoneWhen reports whether a merged PR says in its title that it fully
// delivers the item: the exact word "closes" before the item ref ("closes B12").
// Other verbs do not count, and a qualifier before it ("partly closes B12",
// "Part 1: closes B12", "does not closes B12") or in its own clause after it
// ("closes B12 part 1") cancels it, so a partial PR never closes the item. When a
// later item ref follows, the claim's text ends at the earliest comma, semicolon
// or that ref (a "part of" leading into the ref is dropped), so "closes B401,
// part of B600" and "closes B401, partly fixes B600" still claim B401. With no
// later ref the whole remainder counts, so "closes B1, partial" is cancelled.
func claimsDoneWhen(ref string, prs []store.BacklogPRFact) bool {
	for _, pr := range prs {
		if pr.State == "merged" && TitleClaims(pr.Title, ref) {
			return true
		}
	}
	return false
}

var (
	// claimNextRef is the next item ref after a claim.
	claimNextRef = regexp.MustCompile(`(?i)\bB\d+\b`)
	// claimClauseEnd is a comma or semicolon.
	claimClauseEnd = regexp.MustCompile(`[,;]`)
	// otherItemLead is a qualifier that points at that next ref ("part of B600").
	otherItemLead = regexp.MustCompile(`(?i)\bpart\s+of\s*$`)
)

// TitleClaims reports whether a PR title carries an unqualified "closes <ref>".
func TitleClaims(title, ref string) bool {
	if ref == "" {
		return false
	}
	claim := regexp.MustCompile(`(?i)\bcloses\s+` + regexp.QuoteMeta(ref) + `\b`)
	for _, loc := range claim.FindAllStringIndex(title, -1) {
		after := title[loc[1]:]
		if next := claimNextRef.FindStringIndex(after); next != nil {
			after = after[:next[0]]
			if end := claimClauseEnd.FindStringIndex(after); end != nil {
				after = after[:end[0]]
			}
			after = otherItemLead.ReplaceAllString(after, "")
		}
		if !claimQualifiedBefore.MatchString(title[:loc[0]]) && !claimQualifiedAfter.MatchString(after) {
			return true
		}
	}
	return false
}

var claimedRef = regexp.MustCompile(`(?i)\bcloses\s+(B\d+)\b`)

// ClaimedRefs lists the item refs a PR title claims with an unqualified "closes <ref>".
func ClaimedRefs(title string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range claimedRef.FindAllStringSubmatch(title, -1) {
		ref := strings.ToUpper(m[1])
		if !seen[ref] && TitleClaims(title, ref) {
			seen[ref] = true
			out = append(out, ref)
		}
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

package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FinishedWorkerGrace is how long a finished worker stays open after it last
// reported or acted, so someone can still read its result or reply to it.
const FinishedWorkerGrace = 30 * time.Minute

// FinishedWorkerFacts are the session, report and PR facts
// FinishedWorkerKeepReason decides on. Reports are in creation order.
type FinishedWorkerFacts struct {
	Session SessionRecord
	Reports []ReportRecord
	PRs     []PullRequest
	Now     time.Time
}

// WorkerBranchWork is what a worker's git state says about unfinished work.
type WorkerBranchWork struct {
	// Unknown explains why the state could not be read; non-empty means keep.
	Unknown string
	// Unmerged names session-owned refs with commits not on the base.
	Unmerged []string
}

// KeepReason returns why this branch state keeps the worker, or "".
func (w WorkerBranchWork) KeepReason() string {
	if w.Unknown != "" {
		return "branch state unknown: " + w.Unknown
	}
	if len(w.Unmerged) > 0 {
		return "unmerged commits on " + strings.Join(w.Unmerged, ", ")
	}
	return ""
}

var (
	// reportedPRURL matches change-request URLs from any host, including ones
	// with no SCM adapter here (Bitbucket's /pull-requests/N).
	reportedPRURL = regexp.MustCompile(`(?i)https?://[^\s<>()'"]+/(?:pull|pulls|pull-requests|merge_requests)/\d+`)
	// reportedPRNumber matches "PR #2", "PR-2", "MR !7", "pull request 12".
	reportedPRNumber = regexp.MustCompile(`(?i)\b(?:PR|MR|pull[ -]?request|merge[ -]?request)[\s:-]*[#!]?\s*(\d+)\b`)
	trailingNumber   = regexp.MustCompile(`(\d+)/?$`)
)

// reportWaitPhrases mark a done report that is still waiting on someone. The
// match is deliberately broad: a false keep costs a sidebar row, a false close
// loses work.
var reportWaitPhrases = []string{
	"waiting on", "waiting for", "awaiting", "needs input", "need input",
	"needs your", "need your", "blocked on", "blocked by", "please confirm",
	"please review", "let me know", "your call", "needs a decision",
	"need a decision", "decision needed", "?", "should i", "do you want",
	"want me to", "would you like", "which option", "shall i",
}

// FinishedWorkerKeepReason returns why a worker with no merged PR must stay
// open, or "" when its reports and PRs show it is finished. It covers only
// durable facts; git state and live work are checked by the caller. Unknown
// always keeps: an empty PR list is not evidence of no PR, because hosts with
// no SCM adapter (Bitbucket) never populate it.
func FinishedWorkerKeepReason(f FinishedWorkerFacts) string {
	rec := f.Session
	if rec.Kind != KindWorker {
		return "not a worker"
	}
	if rec.IsPinned {
		return "pinned"
	}
	if len(f.Reports) == 0 {
		return "no finished report"
	}
	last := f.Reports[len(f.Reports)-1]
	if last.State != ReportDone {
		return fmt.Sprintf("last report is %q, not done", last.State)
	}
	if phrase := reportWaitPhrase(last); phrase != "" {
		return fmt.Sprintf("last report says %q", phrase)
	}
	for _, r := range f.Reports {
		if r.DeliveryState != ReportAcknowledged {
			return "a report is not yet delivered"
		}
	}
	meta := rec.Metadata
	if meta.LatestInteractionAt.After(last.CreatedAt) || meta.LatestUserPromptAt.After(last.CreatedAt) {
		return "directed after its last report"
	}
	// Restored from an earlier end (CleanupGeneration bumps only then): someone
	// brought it back, and restore has no durable timestamp to compare.
	if rec.CleanupGeneration > 0 {
		return "restored after being ended"
	}
	quietSince := last.CreatedAt
	if rec.Activity.LastActivityAt.After(quietSince) {
		quietSince = rec.Activity.LastActivityAt
	}
	if f.Now.Sub(quietSince) < FinishedWorkerGrace {
		return "finished within the grace period"
	}
	for _, pr := range f.PRs {
		if !pr.Merged && !pr.Closed {
			return "open PR " + pr.URL
		}
	}
	return unresolvedPRReason(f.Reports, f.PRs)
}

func reportWaitPhrase(r ReportRecord) string {
	text := strings.ToLower(r.Note + " " + r.Message)
	for _, phrase := range reportWaitPhrases {
		if strings.Contains(text, phrase) {
			return phrase
		}
	}
	return ""
}

// unresolvedPRReason finds a PR named in the report history whose state nothing
// here can confirm: a created-PR output or a note mention that is neither a
// linked PR (all merged or closed by now) nor a PR the worker only reviewed.
func unresolvedPRReason(reports []ReportRecord, prs []PullRequest) string {
	known := prRefSet{urls: map[string]bool{}, numbers: map[string]bool{}}
	for _, pr := range prs {
		known.add(pr.URL)
		known.add(pr.HTMLURL)
	}
	linkedOnly := known.clone()
	for _, r := range reports {
		for _, o := range r.Outputs {
			if o.Kind == ReportOutputPRReviewed {
				known.add(o.Reference)
			}
		}
	}
	for _, r := range reports {
		for _, o := range r.Outputs {
			if o.Kind == ReportOutputPRCreated && !linkedOnly.hasURL(o.Reference) {
				return "reported PR with no observed state: " + o.Reference
			}
		}
		text := r.Note + " " + r.Message
		for _, url := range reportedPRURL.FindAllString(text, -1) {
			if !known.hasURL(url) {
				return "report names PR with no observed state: " + url
			}
		}
		for _, m := range reportedPRNumber.FindAllStringSubmatch(text, -1) {
			if !known.numbers[m[1]] {
				return "report names PR with no observed state: " + m[0]
			}
		}
	}
	return ""
}

// prRefSet is the PR URLs and numbers the worker's history can account for.
type prRefSet struct {
	urls    map[string]bool
	numbers map[string]bool
}

func (s prRefSet) add(ref string) {
	key := normalizePRRef(ref)
	if key == "" {
		return
	}
	s.urls[key] = true
	if m := trailingNumber.FindStringSubmatch(key); m != nil {
		s.numbers[m[1]] = true
	}
}

func (s prRefSet) hasURL(ref string) bool { return s.urls[normalizePRRef(ref)] }

func (s prRefSet) clone() prRefSet {
	out := prRefSet{urls: map[string]bool{}, numbers: map[string]bool{}}
	for k := range s.urls {
		out.urls[k] = true
	}
	for k := range s.numbers {
		out.numbers[k] = true
	}
	return out
}

func normalizePRRef(ref string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(ref)), "/")
}

package gitlab

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func mergeabilityFromMR(mr *restMR, ciState, reviewDecision string) ports.SCMMergeabilityObservation {
	ms := effectiveMergeStatus(mr)
	var blockers []string

	switch ms {
	// Mergeable (current + legacy aliases). GitLab reports these when the
	// branch can merge cleanly; Tallpine still layers CI/review/draft blockers on top.
	case "mergeable", "can_be_merged":
		mergeable := true
		if ciState == string(domain.CIFailing) {
			blockers = append(blockers, "ci_failing")
			mergeable = false
		}
		if reviewDecision == string(domain.ReviewRequired) {
			blockers = append(blockers, "review_required")
			mergeable = false
		}
		if mr.Draft {
			blockers = append(blockers, "draft")
			mergeable = false
		}
		if mergeable {
			return ports.SCMMergeabilityObservation{
				State:     string(domain.MergeMergeable),
				Mergeable: true,
			}
		}
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: blockers,
		}

	// Conflicting (current + legacy aliases).
	case "conflict", "cannot_be_merged", "cannot_be_merged_recheck":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeConflicting),
			Conflict: true,
			Blockers: []string{"conflicts"},
		}

	// Unknown — mergeability is being computed or the diff is being prepared.
	case "checking", "preparing", "unchecked", "approvals_syncing", "":
		return ports.SCMMergeabilityObservation{
			State: string(domain.MergeUnknown),
		}

	// Need rebase — head is behind base; a fast-forward merge is not possible.
	case "need_rebase":
		return ports.SCMMergeabilityObservation{
			State:      string(domain.MergeBlocked),
			BehindBase: true,
			Blockers:   []string{"behind_base"},
		}

	// CI blockers (current + legacy).
	case "ci_must_pass", "ci_still_running":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"ci_failing"},
		}

	case "discussions_not_resolved":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"discussions_unresolved"},
		}

	case "draft_status":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"draft"},
		}

	// Review blockers.
	case "requested_changes":
		// A reviewer explicitly requested changes — the direct equivalent
		// of GitHub's CHANGES_REQUESTED. The blocker label differs from
		// not_approved so downstream status logic can distinguish "someone
		// asked for changes" from "nobody has approved yet".
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"changes_requested"},
		}

	case "not_approved":
		// Approvals are required and have not been granted. The approvals
		// endpoint is authoritative for this case.
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"review_required"},
		}

	// Provider-blocked / non-mergeable states without a more specific cause.
	case "not_open", "merge_request_blocked", "merge_time", "commits_status",
		"jira_association_missing", "status_checks_must_pass",
		"security_policy_pipeline_check", "security_policy_violations",
		"locked_paths", "locked_lfs_files", "title_regex":
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"blocked_by_provider"},
		}

	default:
		return ports.SCMMergeabilityObservation{
			State:    string(domain.MergeBlocked),
			Blockers: []string{"blocked_by_provider"},
		}
	}
}

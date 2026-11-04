package gitlab

import (
	"strconv"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type restMR struct {
	ID                  int64  `json:"id"`
	IID                 int    `json:"iid"`
	Title               string `json:"title"`
	State               string `json:"state"`
	Draft               bool   `json:"draft"`
	WebURL              string `json:"web_url"`
	SourceBranch        string `json:"source_branch"`
	TargetBranch        string `json:"target_branch"`
	SHA                 string `json:"sha"`
	MergeStatus         string `json:"merge_status"`
	DetailedMergeStatus string `json:"detailed_merge_status"`

	Author struct {
		Username string `json:"username"`
	} `json:"author"`

	SourceProjectID int `json:"source_project_id"`
	TargetProjectID int `json:"target_project_id"`

	// DiffRefs is populated from the nested diff_refs object in the MR
	// response. GitLab returns the same shape from both the list and detail
	// endpoints, so the cached restMR from ListPRsByRepo carries the base
	// SHA without any manual second-pass unmarshal.
	DiffRefs       restDiffRefs `json:"diff_refs"`
	MergeCommitSHA string       `json:"merge_commit_sha"`

	CreatedAt *time.Time `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
	ClosedAt  *time.Time `json:"closed_at"`

	BlockingDiscussionsResolved bool `json:"blocking_discussions_resolved"`
}

// restProject is the subset of the GitLab "GET /projects/:id" response that
// the provider needs: path_with_namespace is used to resolve a fork MR's head
// repository (review Item 4).
type restProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

type restDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

func mrToSCMPRObservation(repo ports.SCMRepo, mr *restMR) ports.SCMPRObservation {
	state := normalizeMRState(mr.State, mr.Draft)
	merged := mr.State == "merged"
	closed := mr.State == "closed" || mr.State == "locked"
	return ports.SCMPRObservation{
		ProviderID:               providerID(mr.ID),
		URL:                      mr.WebURL,
		HTMLURL:                  mr.WebURL,
		Number:                   mr.IID,
		State:                    string(state),
		Draft:                    mr.Draft,
		Merged:                   merged,
		Closed:                   closed,
		SourceBranch:             mr.SourceBranch,
		HeadRepo:                 repo.Repo,
		TargetBranch:             mr.TargetBranch,
		HeadSHA:                  mr.SHA,
		Title:                    mr.Title,
		Author:                   mr.Author.Username,
		ProviderState:            mr.State,
		ProviderMergeable:        mr.MergeStatus,
		ProviderMergeStateStatus: effectiveMergeStatus(mr),
		CreatedAtProvider:        safeTime(mr.CreatedAt),
		UpdatedAtProvider:        safeTime(mr.UpdatedAt),
		MergedAtProvider:         safeTime(mr.MergedAt),
		ClosedAtProvider:         safeTime(mr.ClosedAt),
	}
}

func providerID(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func normalizeMRState(state string, draft bool) domain.PRState {
	switch state {
	case "opened":
		if draft {
			return domain.PRStateDraft
		}
		return domain.PRStateOpen
	case "merged":
		return domain.PRStateMerged
	case "closed", "locked":
		return domain.PRStateClosed
	default:
		return domain.PRStateClosed
	}
}

func effectiveMergeStatus(mr *restMR) string {
	if mr.DetailedMergeStatus != "" {
		return mr.DetailedMergeStatus
	}
	return mr.MergeStatus
}

// changesRequested reports whether the MR's effective merge status is
// "requested_changes" — GitLab's signal that a reviewer explicitly asked for
// changes (the direct equivalent of GitHub's CHANGES_REQUESTED). Both
// fetchSingleMR and FetchReviewThreads use this to override the approvals-
// based review decision.
func changesRequested(mr *restMR) bool {
	return effectiveMergeStatus(mr) == "requested_changes"
}

func safeTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

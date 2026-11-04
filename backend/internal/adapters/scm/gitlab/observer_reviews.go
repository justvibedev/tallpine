package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (p *Provider) fetchApprovalDecision(ctx context.Context, repo ports.SCMRepo, mrIID int) (domain.ReviewDecision, error) {
	approvals, err := p.fetchApprovalsCached(ctx, repo, mrIID)
	if err != nil {
		return domain.ReviewNone, err
	}
	return approvalDecision(*approvals), nil
}

// fetchApprovalsCached returns the approvals state for (repo, mrIID),
// consulting the provider-level approvals cache first (keyed by
// host+repo+IID, TTL 60s). On a cache hit, returns the cached *restApprovals
// without an HTTP call — eliminating the duplicate
// GET /merge_requests/:iid/approvals round-trip that would otherwise occur
// when both fetchApprovalDecision (inside FetchPullRequests) and the approvals
// fetch in FetchReviewThreads run for the same MR in the same observer cycle.
//
// On a miss (entry expired, or not yet fetched this cycle), fetches via doGET,
// caches the result, and returns it. This is the single highest-impact
// deduplication: one eliminated HTTP round-trip per active MR per
// review-refresh cycle. The 304 from the ETag cache (which still counts
// against rate limits) is avoided entirely — the second call is a memory
// lookup with zero network I/O.
//
// Maximum staleness: ~60s. The approvals state within a single cycle is
// already implicitly stale (the observer fetches it once per cycle), so
// serving a cached copy within the same cycle introduces no new staleness
// relative to the observer's existing behavior.
func (p *Provider) fetchApprovalsCached(ctx context.Context, repo ports.SCMRepo, mrIID int) (*restApprovals, error) {
	if cached, ok := p.cache.getApprovals(repo.Host, repo.Repo, mrIID); ok {
		return cached, nil
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/approvals", projectPath(repo.Owner, repo.Name), mrIID)
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, err
	}

	resp, err := hc.doGET(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch approvals: %w", err)
	}

	var approvals restApprovals
	if err := json.Unmarshal(resp.Body, &approvals); err != nil {
		return nil, fmt.Errorf("unmarshal approvals: %w", err)
	}
	p.cache.setApprovals(repo.Host, repo.Repo, mrIID, &approvals)
	return &approvals, nil
}

// approvalDecision derives the review decision from parsed approval state.
// It trusts the `approved` bool rather than comparing len(approved_by) with
// approvals_required, because approved_by can contain approvals that do not
// satisfy the applicable rules.
func approvalDecision(a restApprovals) domain.ReviewDecision {
	if a.Approved {
		// GitLab reports approved=true even when approvals_required=0 and
		// nobody has actually approved. Treat that as no review signal,
		// matching GitHub's behavior where APPROVED only fires when a real
		// approval review is submitted.
		if a.ApprovalsRequired > 0 || len(a.ApprovedBy) > 0 {
			return domain.ReviewApproved
		}
		return domain.ReviewNone
	}
	if a.ApprovalsRequired > 0 {
		return domain.ReviewRequired
	}
	return domain.ReviewNone
}

type restApprovals struct {
	Approved          bool `json:"approved"`
	ApprovalsRequired int  `json:"approvals_required"`
	ApprovedBy        []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		ApprovedAt *time.Time `json:"approved_at"`
	} `json:"approved_by"`
}

// ---------------------------------------------------------------------------
// FetchReviewThreads
// ---------------------------------------------------------------------------

// FetchReviewThreads fetches discussion threads and approval state for a merge
// request.
func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	repo := ref.Repo

	// Fetch discussions, following Link rel=next to fetch ALL pages (review
	// finding #8: previously only the first 100 discussions were fetched).
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/discussions", projectPath(repo.Owner, repo.Name), ref.Number)
	q := url.Values{"per_page": {strconv.Itoa(reviewDiscussionPageSize)}}
	var discussions []restDiscussion
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}

	truncated, err := hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var page []restDiscussion
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("gitlab scm: unmarshal discussions: %w", err)
		}
		discussions = append(discussions, page...)
		return nil
	})
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}

	var threads []ports.SCMReviewThreadObservation
	for _, d := range discussions {
		if d.IndividualNote {
			continue
		}
		if len(d.Notes) == 0 {
			continue
		}
		firstNote := d.Notes[0]
		if firstNote.System {
			continue
		}

		resolved := true
		allBot := true
		var comments []ports.SCMReviewCommentObservation
		filePath := ""
		line := 0

		for j, n := range d.Notes {
			if n.System {
				continue
			}
			isBot := isBotAuthor(n.Author.Username)
			if !isBot {
				allBot = false
			}
			if !n.Resolved && n.Resolvable {
				resolved = false
			}
			if j == 0 && n.Position != nil {
				filePath = n.Position.NewPath
				line = n.Position.NewLine
			}
			if j < reviewCommentLimitPerThread {
				comments = append(comments, ports.SCMReviewCommentObservation{
					ID:     strconv.Itoa(n.ID),
					Author: n.Author.Username,
					Body:   n.Body,
					URL:    n.noteURL(ref),
					IsBot:  isBot,
				})
			}
		}

		threads = append(threads, ports.SCMReviewThreadObservation{
			ID:       d.ID,
			Path:     filePath,
			Line:     line,
			Resolved: resolved,
			IsBot:    allBot,
			Comments: comments,
		})
	}

	// Fetch approvals via the shared cache (ticket 04). When FetchPullRequests
	// has already run for this MR in the same cycle, fetchApprovalDecision
	// populated the approvals cache and this call is a memory lookup with zero
	// network I/O — eliminating the duplicate /merge_requests/:iid/approvals
	// round-trip. The cached *restApprovals is reused for both the review
	// decision and the review summaries.
	approvalsPtr, err := p.fetchApprovalsCached(ctx, repo, ref.Number)
	if err != nil {
		return ports.SCMReviewObservation{}, fmt.Errorf("gitlab scm: %w", err)
	}
	approvals := *approvalsPtr
	decision := approvalDecision(approvals)

	// Override the review decision when the cached MR detail has
	// requested_changes — see changesRequested for rationale. When the cache
	// is cold (e.g. FetchReviewThreads runs before FetchPullRequests in a
	// cycle), the override is skipped and the approvals-based decision stands
	// — no crash, no false override.
	if cached, ok := p.cache.getMRDetail(repo.Host, repo.Repo, ref.Number); ok {
		if changesRequested(cached) {
			decision = domain.ReviewChangesRequest
		}
	}

	// Build review summaries with stable, unique IDs so multiple approvers
	// don't overwrite each other in persistence
	var reviews []ports.SCMReviewSummaryObservation
	for _, ab := range approvals.ApprovedBy {
		username := ab.User.Username
		reviews = append(reviews, ports.SCMReviewSummaryObservation{
			ID:          "approval:" + username,
			Author:      username,
			State:       string(domain.ReviewApproved),
			IsBot:       isBotAuthor(username),
			SubmittedAt: safeTime(ab.ApprovedAt),
		})
	}

	return ports.SCMReviewObservation{
		Decision: string(decision),
		Reviews:  reviews,
		Threads:  threads,
		Partial:  truncated,
	}, nil
}

type restDiscussion struct {
	ID             string     `json:"id"`
	IndividualNote bool       `json:"individual_note"`
	Notes          []restNote `json:"notes"`
}

type restNote struct {
	ID         int    `json:"id"`
	Body       string `json:"body"`
	System     bool   `json:"system"`
	Resolvable bool   `json:"resolvable"`
	Resolved   bool   `json:"resolved"`
	Author     struct {
		Username string `json:"username"`
	} `json:"author"`
	Position *restNotePosition `json:"position"`
}

type restNotePosition struct {
	NewPath string `json:"new_path"`
	NewLine int    `json:"new_line"`
}

func (n *restNote) noteURL(ref ports.SCMPRRef) string {
	return fmt.Sprintf("%s#note_%d", ref.URL, n.ID)
}

var botUsernameRe = regexp.MustCompile(`^project_\d+_bot`)

func isBotAuthor(username string) bool {
	if strings.HasSuffix(username, "[bot]") || strings.HasSuffix(username, "-bot") {
		return true
	}
	switch username {
	case "gitlab-bot", "ghost", "dependabot[bot]", "renovate[bot]",
		"sast-bot", "codeclimate[bot]", "sonarcloud[bot]", "snyk-bot":
		return true
	}
	return botUsernameRe.MatchString(username)
}

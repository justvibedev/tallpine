package github

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (p *Provider) fetchReviewThreadPage(ctx context.Context, ref ports.SCMPRRef, beforeCursor string, includeReviews bool) ([]ports.SCMReviewThreadObservation, []ports.SCMReviewSummaryObservation, string, map[string]any, error) {
	query := buildReviewThreadsQuery(ref, beforeCursor, includeReviews)
	data, err := p.clientFor(ref.Repo.Host).doGraphQL(ctx, query, nil)
	if err != nil {
		return nil, nil, "", nil, err
	}
	repoData, _ := data["repo"].(map[string]any)
	pr, _ := repoData["pullRequest"].(map[string]any)
	if pr == nil {
		return nil, nil, "", nil, fmt.Errorf("%w: pull request not found in review response", ErrNotFound)
	}
	decision := string(reviewDecisionFromGraphQL(pr))
	reviewSummaries, _ := pr["reviewSummaries"].(map[string]any)
	reviews := make([]ports.SCMReviewSummaryObservation, 0, len(nodes(reviewSummaries["nodes"])))
	for _, review := range nodes(reviewSummaries["nodes"]) {
		summary := scmReviewSummaryFromGraphQL(review)
		if summary.ID == "" && summary.URL == "" {
			continue
		}
		reviews = append(reviews, summary)
	}
	threads, _ := pr["reviewThreads"].(map[string]any)
	out := make([]ports.SCMReviewThreadObservation, 0, len(nodes(threads["nodes"])))
	for _, th := range nodes(threads["nodes"]) {
		out = append(out, scmThreadFromGraphQL(th))
	}
	pi, _ := threads["pageInfo"].(map[string]any)
	return out, reviews, decision, pi, nil
}

func buildReviewThreadsQuery(ref ports.SCMPRRef, beforeCursor string, includeReviews bool) string {
	before := "null"
	if beforeCursor != "" {
		before = graphQLString(beforeCursor)
	}
	reviewSelection := ""
	if includeReviews {
		reviewSelection = fmt.Sprintf(" reviewSummaries: reviews(last:%d, states:[APPROVED,CHANGES_REQUESTED,COMMENTED]){ nodes{ id state url submittedAt body commit{ oid } author{ login __typename } } }", githubReviewSummaryLimit)
	}
	return fmt.Sprintf(`query{
repo: repository(owner:%s,name:%s){ pullRequest(number:%d){ reviewDecision%s reviewThreads(last:%d, before:%s){ nodes{
  id isResolved isOutdated path line
  comments(first:%d){ nodes{ id body url pullRequestReview{ databaseId } author{ login __typename } } }
} pageInfo{ hasPreviousPage startCursor } } } }
}`, graphQLString(ref.Repo.Owner), graphQLString(ref.Repo.Name), ref.Number, reviewSelection, githubReviewThreadPageSize, before, githubReviewCommentLimitPerThread)
}

func scmReviewSummaryFromGraphQL(review map[string]any) ports.SCMReviewSummaryObservation {
	author, _ := review["author"].(map[string]any)
	commit, _ := review["commit"].(map[string]any)
	return ports.SCMReviewSummaryObservation{
		ID:          str(review["id"]),
		Author:      str(author["login"]),
		State:       string(reviewStateFromGraphQL(review["state"])),
		URL:         str(review["url"]),
		Body:        str(review["body"]),
		TargetSHA:   str(commit["oid"]),
		IsBot:       isBotAuthor(author),
		SubmittedAt: parseGitHubTime(str(review["submittedAt"])),
	}
}

func reviewStateFromGraphQL(state any) domain.ReviewDecision {
	switch strings.ToUpper(strings.TrimSpace(str(state))) {
	case "APPROVED":
		return domain.ReviewApproved
	case "CHANGES_REQUESTED":
		return domain.ReviewChangesRequest
	case "REVIEW_REQUIRED":
		return domain.ReviewRequired
	default:
		return domain.ReviewNone
	}
}

func scmThreadFromGraphQL(th map[string]any) ports.SCMReviewThreadObservation {
	out := ports.SCMReviewThreadObservation{
		ID:       str(th["id"]),
		Path:     str(th["path"]),
		Line:     int(num(th["line"])),
		Resolved: boolv(th["isResolved"]),
		Outdated: boolv(th["isOutdated"]),
	}
	comments, _ := th["comments"].(map[string]any)
	commentNodes := nodes(comments["nodes"])
	allCommentsBot := len(commentNodes) > 0
	for _, cn := range commentNodes {
		author, _ := cn["author"].(map[string]any)
		parentReview, _ := cn["pullRequestReview"].(map[string]any)
		isBot := isBotAuthor(author)
		if !isBot {
			allCommentsBot = false
		}
		out.Comments = append(out.Comments, ports.SCMReviewCommentObservation{
			ID:       str(cn["id"]),
			ReviewID: decimalID(parentReview["databaseId"]),
			Author:   str(author["login"]),
			Body:     str(cn["body"]),
			URL:      str(cn["url"]),
			IsBot:    isBot,
		})
	}
	out.IsBot = allCommentsBot
	return out
}

package github

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

type restListPull struct {
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
	NodeID  string `json:"node_id"`
	Number  int    `json:"number"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	Title   string `json:"title"`
	Head    struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	User struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func restListPullToSCM(pull restListPull) ports.SCMPRObservation {
	closed := strings.EqualFold(pull.State, "closed")
	return ports.SCMPRObservation{
		ProviderID:        pull.NodeID,
		URL:               textutil.FirstNonBlank(pull.HTMLURL, pull.URL),
		Number:            pull.Number,
		State:             string(domain.PRLifecycleState(false, closed, pull.Draft)),
		Draft:             pull.Draft,
		Closed:            closed,
		SourceBranch:      pull.Head.Ref,
		HeadRepo:          pull.Head.Repo.FullName,
		BaseRepo:          pull.Base.Repo.FullName,
		TargetBranch:      pull.Base.Ref,
		HeadSHA:           pull.Head.SHA,
		Title:             pull.Title,
		Author:            pull.User.Login,
		AuthorAvatarURL:   pull.User.AvatarURL,
		BaseSHA:           pull.Base.SHA,
		ProviderState:     pull.State,
		HTMLURL:           pull.HTMLURL,
		CreatedAtProvider: parseGitHubTime(pull.CreatedAt),
		UpdatedAtProvider: parseGitHubTime(pull.UpdatedAt),
	}
}

func buildSCMBatchQuery(refs []ports.SCMPRRef) (string, []string) {
	aliases := make([]string, len(refs))
	var b strings.Builder
	b.WriteString("query{\n")
	for i, ref := range refs {
		alias := fmt.Sprintf("pr%d", i)
		aliases[i] = alias
		_, _ = fmt.Fprintf(&b, "%s: repository(owner:%s,name:%s){ pullRequest(number:%d){ %s } }\n",
			alias, graphQLString(ref.Repo.Owner), graphQLString(ref.Repo.Name), ref.Number, scmPRFields())
	}
	b.WriteString("}")
	return b.String(), aliases
}

func scmPRFields() string {
	return strings.ReplaceAll(strings.ReplaceAll(`
number id url state isDraft merged closed title additions deletions changedFiles
mergeable mergeStateStatus reviewDecision headRefName headRefOid baseRefName baseRefOid
createdAt updatedAt mergedAt closedAt
author{ login avatarUrl }
mergeCommit{ oid }
files(first:PATH_LIMIT){ nodes{ path } }
commits(last:1){ nodes{ commit{ oid statusCheckRollup{ state contexts(first:CONTEXT_LIMIT){ nodes{
  __typename
  ... on CheckRun { name status conclusion detailsUrl url databaseId }
  ... on StatusContext { context state targetUrl }
} pageInfo{ hasNextPage endCursor } } } } } }
`, "CONTEXT_LIMIT", strconv.Itoa(scmBatchCheckContextLimit)), "PATH_LIMIT", strconv.Itoa(scmBatchPathLimit))
}

func scmObservationFromGraphQL(ref ports.SCMPRRef, pr map[string]any) ports.SCMObservation {
	checks := scmChecksFromGraphQL(pr)
	failed := failedSCMChecks(checks)
	ci := string(ciSummaryFromRollupState(pr))
	prURL := textutil.FirstNonBlank(str(pr["url"]), ref.URL)
	review := string(reviewDecisionFromGraphQL(pr))
	providerMergeable := str(pr["mergeable"])
	providerMergeState := str(pr["mergeStateStatus"])
	merged := boolv(pr["merged"])
	closed := boolv(pr["closed"]) && !merged
	draft := boolv(pr["isDraft"])
	canonicalRepo := repoFullName(ref.Repo)
	if owner, repoName, _, err := parsePRURL(prURL); err == nil {
		canonicalRepo = owner + "/" + repoName
	}
	urlAlias := strings.TrimSpace(ref.URL)
	if urlAlias == strings.TrimSpace(prURL) {
		urlAlias = ""
	}
	obs := ports.SCMObservation{
		Fetched:  true,
		Provider: ref.Repo.Provider,
		Host:     ref.Repo.Host,
		Repo:     canonicalRepo,
		PR: ports.SCMPRObservation{
			ProviderID:               str(pr["id"]),
			URL:                      prURL,
			URLAlias:                 urlAlias,
			Number:                   int(num(pr["number"])),
			State:                    string(domain.PRLifecycleState(merged, closed, draft)),
			Draft:                    draft,
			Merged:                   merged,
			Closed:                   closed,
			SourceBranch:             str(pr["headRefName"]),
			TargetBranch:             str(pr["baseRefName"]),
			HeadSHA:                  textutil.FirstNonBlank(str(pr["headRefOid"]), latestCommitOID(pr)),
			Title:                    str(pr["title"]),
			Additions:                int(num(pr["additions"])),
			Deletions:                int(num(pr["deletions"])),
			ChangedFiles:             int(num(pr["changedFiles"])),
			Paths:                    changedPaths(pr),
			Author:                   authorLogin(pr["author"]),
			AuthorAvatarURL:          authorAvatarURL(pr["author"]),
			BaseSHA:                  str(pr["baseRefOid"]),
			MergeCommitSHA:           mergeCommitOID(pr),
			ProviderState:            str(pr["state"]),
			ProviderMergeable:        providerMergeable,
			ProviderMergeStateStatus: providerMergeState,
			HTMLURL:                  str(pr["url"]),
			CreatedAtProvider:        parseGitHubTime(str(pr["createdAt"])),
			UpdatedAtProvider:        parseGitHubTime(str(pr["updatedAt"])),
			MergedAtProvider:         parseGitHubTime(str(pr["mergedAt"])),
			ClosedAtProvider:         parseGitHubTime(str(pr["closedAt"])),
		},
		CI: ports.SCMCIObservation{
			Summary:           ci,
			HeadSHA:           textutil.FirstNonBlank(str(pr["headRefOid"]), latestCommitOID(pr)),
			Checks:            checks,
			FailedChecks:      failed,
			FailedFingerprint: githubFailedFingerprint(textutil.FirstNonBlank(str(pr["headRefOid"]), latestCommitOID(pr)), failed),
			NoChecks:          githubHeadHasNoChecks(pr),
		},
		Review: ports.SCMReviewObservation{Decision: review},
	}
	obs.Mergeability = mergeabilityObservation(providerMergeable, providerMergeState, ci, review, draft)
	return obs
}

func mergeabilityObservation(providerMergeable, providerMergeState, ci, review string, draft bool) ports.SCMMergeabilityObservation {
	state := strings.ToUpper(strings.TrimSpace(providerMergeState))
	mergeable := strings.ToUpper(strings.TrimSpace(providerMergeable))
	out := ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	addBlocker := func(b string) { out.Blockers = append(out.Blockers, b) }
	if state == "DIRTY" || mergeable == "CONFLICTING" {
		out.State = string(domain.MergeConflicting)
		out.Conflict = true
		addBlocker("conflicts")
		return out
	}
	if state == "BEHIND" || state == "BEHIND_BASE" {
		out.BehindBase = true
		addBlocker("behind_base")
	}
	if state == "BLOCKED" {
		out.State = string(domain.MergeBlocked)
		addBlocker("blocked_by_provider")
	}
	// UNSTABLE outranks draft / CI / review (doc.go rule 3, and the
	// mergeabilityFromGraphQL sibling): GitHub reports UNSTABLE when the PR
	// *is* mergeable but a non-required check failed or is pending. Such a PR
	// also has a FAILURE rollup (-> ci == failing), so leaving this below the
	// blockers downgraded every genuinely-mergeable UNSTABLE PR to blocked.
	if state == "UNSTABLE" {
		out.State = string(domain.MergeUnstable)
		return out
	}
	if draft {
		out.State = string(domain.MergeBlocked)
		addBlocker("draft")
	}
	if ci == string(domain.CIFailing) {
		out.State = string(domain.MergeBlocked)
		addBlocker("ci_failing")
	}
	switch review {
	case string(domain.ReviewChangesRequest):
		out.State = string(domain.MergeBlocked)
		addBlocker("changes_requested")
	case string(domain.ReviewRequired):
		out.State = string(domain.MergeBlocked)
		addBlocker("review_required")
	}
	if out.State == string(domain.MergeBlocked) {
		return out
	}
	if mergeable == "MERGEABLE" && (state == "CLEAN" || state == "HAS_HOOKS" || state == "") &&
		(review == "" || review == string(domain.ReviewApproved) || review == string(domain.ReviewNone)) && !draft {
		out.State = string(domain.MergeMergeable)
		out.Mergeable = true
		return out
	}
	return out
}

func parseGitHubTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func authorLogin(v any) string {
	author, _ := v.(map[string]any)
	return str(author["login"])
}

func authorAvatarURL(v any) string {
	author, _ := v.(map[string]any)
	return str(author["avatarUrl"])
}

func mergeCommitOID(pr map[string]any) string {
	mc, _ := pr["mergeCommit"].(map[string]any)
	return str(mc["oid"])
}

// changedPaths returns the PR's changed file paths, nil when the response
// carried no files connection (so stored paths are kept, not cleared).
func changedPaths(pr map[string]any) []string {
	files, ok := pr["files"].(map[string]any)
	if !ok {
		return nil
	}
	paths := []string{}
	for _, n := range nodes(files["nodes"]) {
		if p := str(n["path"]); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func latestCommitOID(pr map[string]any) string {
	commits, _ := pr["commits"].(map[string]any)
	for _, n := range nodes(commits["nodes"]) {
		commit, _ := n["commit"].(map[string]any)
		if oid := str(commit["oid"]); oid != "" {
			return oid
		}
	}
	return ""
}

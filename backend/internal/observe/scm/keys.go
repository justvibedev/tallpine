package scm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func metadataSemanticHash(obs ports.SCMObservation) string {
	return stableHash(map[string]any{"provider": obs.Provider, "host": obs.Host, "repo": obs.Repo, "pr": obs.PR, "mergeability": obs.Mergeability})
}

func ciSemanticHash(ci ports.SCMCIObservation) string {
	h := stableHash(map[string]any{"summary": ci.Summary, "head": ci.HeadSHA, "checks": ci.Checks, "failed": ci.FailedChecks, "tail": ci.FailureLogTail})
	switch {
	case ci.NoCIWaived:
		h += domain.NoCIWaivedHashSuffix
	case ci.NoCIAwaitingApproval:
		h += domain.NoCIAwaitingApprovalHashSuffix
	}
	if ci.FailedFingerprint != "" {
		return ci.FailedFingerprint + ":" + h
	}
	return h
}

func reviewSemanticHash(review ports.SCMReviewObservation) string {
	type reviewHashPayload struct {
		Decision string
		Reviews  []ports.SCMReviewSummaryObservation
		Threads  []ports.SCMReviewThreadObservation
		Partial  bool `json:",omitempty"`
	}
	return stableHash(reviewHashPayload{Decision: review.Decision, Reviews: review.Reviews, Threads: review.Threads, Partial: review.Partial})
}

func threadSemanticHash(th ports.SCMReviewThreadObservation) string {
	return stableHash(th)
}

func stableHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%#v", v))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PR identity has one owner: the provider-native id (provider, host,
// provider_id), which survives repository renames and org transfers. Repo
// name, number, and URL are mutable display coordinates. Subjects and
// discovery dedupe key on identityPRKey when a provider id is known; the
// legacy name-based prKey remains only as a self-retiring fallback for rows
// that predate provider ids (their first successful fetch stamps one).
// Fetched observations are never re-keyed from their content at all — they
// are attributed positionally to the ref that requested them (see the
// Provider.FetchPullRequests contract). Repo-level state — list ETags, sync
// cursors, commit-check guards — deliberately stays name-keyed: listing
// genuinely is a per-name operation.
func identityPRKey(provider, host, providerID string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" +
		strings.ToLower(strings.TrimSpace(host)) + "@" + strings.TrimSpace(providerID)
}

// keyForTrackedPR returns the dispatch key a tracked PR registers under: the
// provider-native identity when the row carries one, else the legacy
// name-based key derived from the resolved repo.
func keyForTrackedPR(pr domain.PullRequest, repo ports.SCMRepo) string {
	if pr.Provider != "" && pr.Host != "" && pr.ProviderID != "" {
		return identityPRKey(pr.Provider, pr.Host, pr.ProviderID)
	}
	return prKey(repo, pr.Number)
}

func prKey(repo ports.SCMRepo, number int) string {
	base := repo.Provider + ":" + repo.Host + ":" + repoFullName(repo)
	if number <= 0 {
		return base
	}
	return base + "#" + fmt.Sprint(number)
}

func commitKey(repo ports.SCMRepo, sha string) string { return prKey(repo, 0) + "@" + sha }

func repoFullName(repo ports.SCMRepo) string {
	if repo.Repo != "" {
		return repo.Repo
	}
	return repo.Owner + "/" + repo.Name
}

func ownerOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

func nameOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return full
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

// SessionOwnsBranch reports whether head is a branch the session owns under the
// observer's attribution rules: its current or temporary (pre-rename) branch,
// a "/" child, the "/root" namespace, and, for workspace sessions only, the
// generated numeric hyphen suffix. Used to gate explicit PR attachment.
func SessionOwnsBranch(sess domain.SessionRecord, workspace bool, head string) bool {
	head = strings.TrimSpace(head)
	if head == "" {
		return false
	}
	sr := sessionRepo{session: sess, branch: sess.Metadata.Branch, workspace: workspace}
	_, ok, _ := matchSessionDetail([]sessionRepo{sr}, head)
	return ok
}

// DiscoverableByIdentity reports whether a listed open PR may be attached to a
// session without an explicit claim: the authenticated human account opened it
// and its head lives in the listed repository (no fork heads). It is the
// author and head-repo part of the observer's discovery rules, shared with the
// review trigger's on-demand discovery.
func DiscoverableByIdentity(pr ports.SCMPRObservation, repo ports.SCMRepo, id ports.SCMIdentity) bool {
	login := strings.TrimSpace(id.Login)
	if !id.Human || login == "" || !strings.EqualFold(strings.TrimSpace(pr.Author), login) {
		return false
	}
	head := strings.TrimSpace(pr.HeadRepo)
	if head == "" {
		return false
	}
	return strings.EqualFold(head, repoFullName(repo)) || (pr.BaseRepo != "" && strings.EqualFold(head, pr.BaseRepo))
}

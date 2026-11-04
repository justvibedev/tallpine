package scm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestTrackedPRsForSessionRetainsTerminalPRsOnlyForAutoTerminationRetry(t *testing.T) {
	prs := []domain.PullRequest{
		{Number: 1},
		{Number: 2, Merged: true},
		{Number: 3, Closed: true},
	}
	if got := trackedPRsForSession(domain.SessionRecord{}, prs); len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("default tracked PRs = %+v, want only open PR", got)
	}
	if got := trackedPRsForSession(domain.SessionRecord{TerminateOnPRMerge: true}, prs); len(got) != 3 {
		t.Fatalf("auto-termination tracked PRs = %+v, want open and terminal PRs", got)
	}
}
func TestDomainFromObservationPreservesStableIdentityAndAliases(t *testing.T) {
	oldURL := "https://github.com/old-owner/repo/pull/7"
	currentURL := "https://github.com/new-owner/repo/pull/7"
	obs := ports.SCMObservation{
		Fetched:  true,
		Provider: "github",
		Host:     "github.com",
		Repo:     "new-owner/repo",
		PR: ports.SCMPRObservation{
			ProviderID: "PR_stable_7",
			URL:        currentURL,
			URLAlias:   oldURL,
			Number:     7,
		},
	}

	pr, _, _, _, _ := domainFromObservation("repo-1", domain.SessionRecord{}, obs, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	if pr.ProviderID != "PR_stable_7" {
		t.Fatalf("ProviderID = %q, want PR_stable_7", pr.ProviderID)
	}
	if pr.URLAlias != oldURL {
		t.Fatalf("URLAlias = %q, want %s", pr.URLAlias, oldURL)
	}
}

func TestDomainFromObservationSnapshotsReviewInjectionPolicy(t *testing.T) {
	obs := testObs(1)
	obs.Review = ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Reviews:  []ports.SCMReviewSummaryObservation{{ID: "r1", Author: "alice", State: string(domain.ReviewChangesRequest)}},
		Threads: []ports.SCMReviewThreadObservation{{
			ID: "t1", Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "alice", Body: "fix"}},
		}},
	}
	_, _, reviews, _, comments := domainFromObservation(
		"p-1",
		domain.SessionRecord{AutoInjectReview: false},
		obs,
		domain.PullRequest{},
		persistenceOptions{},
		time.Unix(1, 0).UTC(),
	)
	if len(reviews) != 1 || reviews[0].AutoInjectReview {
		t.Fatalf("reviews = %+v, want one review snapshotted as not injected", reviews)
	}
	if len(comments) != 1 || comments[0].AutoInjectReview {
		t.Fatalf("comments = %+v, want one comment snapshotted as not injected", comments)
	}
}

func TestRepoForTrackedPRMatchesLegacyRepoOnlyRows(t *testing.T) {
	pr := knownPR(1)
	pr.Provider = ""
	pr.Host = ""
	pr.Repo = "o/r"
	repo, ok := repoForTrackedPR(pr, []ports.SCMRepo{testRepo})
	if !ok {
		t.Fatal("legacy repo-only row should match candidate repo")
	}
	if repoFullName(repo) != "o/r" {
		t.Fatalf("matched repo = %q, want o/r", repoFullName(repo))
	}
}

func TestRepoForTrackedPRUsesPersistedRepoWhenCurrentScanDropsUpstream(t *testing.T) {
	pr := knownPR(42)
	pr.Provider = "github"
	pr.Host = "github.com"
	pr.Repo = "upstream/api"
	repo, ok := repoForTrackedPR(pr, []ports.SCMRepo{testAPIRepo})
	if !ok {
		t.Fatal("persisted tracked PR repo should refresh even when current remotes no longer include it")
	}
	if repo.Provider != "github" || repo.Host != "github.com" || repo.Repo != "upstream/api" {
		t.Fatalf("repo = %#v, want persisted upstream/api tuple", repo)
	}
	if repo.Owner != "upstream" || repo.Name != "api" {
		t.Fatalf("repo owner/name = %q/%q, want upstream/api", repo.Owner, repo.Name)
	}
}

func TestPoll_RepoETag304SkipsListPRs(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1", NotModified: true}}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 0 {
		t.Fatalf("ListOpenPRsByRepo called on 304: %d", provider.listCalls)
	}
}

func TestPoll_NoOpenPRsCommitsRepoETag(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Fatalf("ListOpenPRsByRepo calls = %d, want 1", provider.listCalls)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "v2" {
		t.Fatalf("repo ETag after empty listing = %q, want v2", got)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("empty listing should not fetch PR batch: %#v", provider.fetchBatches)
	}
}

func TestPoll_RepoETag200DiscoversPRAndRefreshesSamePoll(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{ProviderID: "PR_stable_1", URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Fatalf("ListOpenPRsByRepo calls = %d, want 1", provider.listCalls)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("new PR not refreshed in same poll: %#v", provider.fetchBatches)
	}
	if len(store.writes) < 1 || len(lc.observed) != 1 {
		t.Fatalf("write/lifecycle missing: writes=%d lifecycle=%d", len(store.writes), len(lc.observed))
	}
	if store.writes[0].pr.ProviderID != "PR_stable_1" {
		t.Fatalf("discovery ProviderID = %q, want PR_stable_1", store.writes[0].pr.ProviderID)
	}
	if got := store.writes[0].pr.Author; got != "alice" {
		t.Fatalf("discovered author = %q, want alice", got)
	}
}

func TestPoll_DiscoversOnlyPRsFromAuthenticatedHuman(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Author: "other"},
			{URL: "https://github.com/o/r/pull/2", Number: 2, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha2", Author: " ALICE "},
			{URL: "https://github.com/o/r/pull/3", Number: 3, SourceBranch: "feat", HeadRepo: "o/r", Author: ""},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 2): testObs(2)},
	}
	identity := &fakeIdentityResolver{identity: ports.SCMIdentity{Login: " alice ", Human: true}}
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:                   time.Hour,
		Logger:                 quietSlog(),
		CacheMax:               128,
		ScopedIdentityResolver: identity,
	})
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if identity.calls != 1 {
		t.Fatalf("identity resolver calls = %d, want 1", identity.calls)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 2 {
		t.Fatalf("fetched PRs = %#v, want only authenticated author's PR #2", provider.fetchBatches)
	}
	for _, write := range store.writes {
		if write.pr.Number != 2 {
			t.Fatal("foreign author's PR was persisted")
		}
	}
}

// A session whose branch is the prefix of two open PRs (its root plus a stacked
// child on branch "feat/child") picks up both PRs in a single poll.
func TestPoll_DiscoversStackedChildByBranchPrefix(t *testing.T) {
	store := testStoreWithSession()
	childObs := testObs(2)
	childObs.PR.SourceBranch = "feat/child"
	childObs.PR.TargetBranch = "feat"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
			{URL: "https://github.com/o/r/pull/2", Number: 2, SourceBranch: "feat/child", HeadRepo: "o/r", TargetBranch: "feat", HeadSHA: "sha2"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1), prKey(testRepo, 2): childObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	fetched := map[int]bool{}
	for _, batch := range provider.fetchBatches {
		for _, ref := range batch {
			fetched[ref.Number] = true
		}
	}
	if !fetched[1] || !fetched[2] {
		t.Fatalf("expected both root and stacked child fetched, got %#v", fetched)
	}
}

func TestPoll_DiscoversSiblingUnderRootSessionNamespace(t *testing.T) {
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "tp/p-1/root"
	prObs := testObs(1)
	prObs.PR.SourceBranch = "tp/p-1/fix"
	prObs.PR.TargetBranch = "main"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "tp/p-1/fix", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected discovered PR write")
	}
	if got := store.writes[0].pr.SourceBranch; got != "tp/p-1/fix" {
		t.Fatalf("source branch = %q, want tp/p-1/fix", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle observations = %d, want 1", len(lc.observed))
	}
}

// A worker renamed after launch keeps owning branches under its temporary
// name, which the agent may have made or remembered before the rename landed.
func TestMatchSession_RenamedTaskKeepsTemporaryNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, branch, temporary, source string
		workspace                       bool
	}{
		{"single-repo sibling", "tp/fix-login/root", "tp/p-1/root", "tp/p-1/fix", false},
		{"pushed temporary root", "tp/fix-login/root", "tp/p-1/root", "tp/p-1/root", false},
		{"dev namespace sibling", "tp/dev/fix-login/root", "tp/dev/p-1/root", "tp/dev/p-1/fix", false},
		{"workspace hyphen sibling", "tp/fix-login/root", "tp/p-1", "tp/p-1-fix", true},
	} {
		sr := sessionRepo{session: domain.SessionRecord{ID: "p-1", TemporaryBranch: tc.temporary}, branch: tc.branch, workspace: tc.workspace}
		if got, ok := matchSession([]sessionRepo{sr}, tc.source); !ok || got.session.ID != "p-1" {
			t.Errorf("%s: %q not attributed after rename", tc.name, tc.source)
		}
	}
	// A later task named after the old temporary branch owns it outright.
	renamed := sessionRepo{session: domain.SessionRecord{ID: "p-1", TemporaryBranch: "tp/p-1/root"}, branch: "tp/login/root"}
	named := sessionRepo{session: domain.SessionRecord{ID: "p-9"}, branch: "tp/p-1/root"}
	for _, source := range []string{"tp/p-1/root", "tp/p-1/fix"} {
		if got, ok := matchSession([]sessionRepo{renamed, named}, source); !ok || got.session.ID != "p-9" {
			t.Errorf("%q = %q, %v; want the task currently on it", source, got.session.ID, ok)
		}
	}
	never := sessionRepo{session: domain.SessionRecord{ID: "p-1"}, branch: "tp/login/root"}
	if _, ok := matchSession([]sessionRepo{never}, "tp/p-1/fix"); ok {
		t.Error("a session that was never renamed claimed its ID namespace")
	}
}

func TestMatchSession_PrefersExactBranchOverNamespaceMatch(t *testing.T) {
	exact := sessionRepo{session: domain.SessionRecord{ID: "exact"}, branch: "tp/p-1"}
	namespace := sessionRepo{session: domain.SessionRecord{ID: "namespace"}, branch: "tp/p-1/root"}
	got, ok := matchSession([]sessionRepo{namespace, exact}, "tp/p-1")
	if !ok {
		t.Fatal("expected a matching session")
	}
	if got.session.ID != exact.session.ID {
		t.Fatalf("matched session = %q, want exact branch owner %q", got.session.ID, exact.session.ID)
	}
}

func TestPoll_DiscoversWorkspaceChildRepoPR(t *testing.T) {
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "tp/p-1/root"
	store.projects["p"] = domain.ProjectRecord{
		ID:            "p",
		RepoOriginURL: "https://github.com/o/r.git",
		Kind:          domain.ProjectKindWorkspace,
	}
	store.workspaceRepos = map[string][]domain.WorkspaceRepoRecord{
		"p": {{ProjectID: "p", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/o/api.git"}},
	}
	prObs := testObs(12)
	prObs.Repo = "o/api"
	prObs.PR.URL = "https://github.com/o/api/pull/12"
	prObs.PR.HTMLURL = "https://github.com/o/api/pull/12"
	prObs.PR.Number = 12
	prObs.PR.SourceBranch = "tp/p-1/api-billing"
	prObs.PR.TargetBranch = "main"
	prObs.PR.HeadSHA = "api-sha"
	prObs.CI.HeadSHA = "api-sha"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(testRepo, 0):    {ETag: "root-v2"},
			prKey(testAPIRepo, 0): {ETag: "api-v2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testAPIRepo, 0): {
				{URL: "https://github.com/o/api/pull/12", Number: 12, SourceBranch: "tp/p-1/api-billing", HeadRepo: "o/api", TargetBranch: "main", HeadSHA: "api-sha"},
			},
		},
		observations: map[string]ports.SCMObservation{prKey(testAPIRepo, 12): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 {
		t.Fatalf("child repo PR not refreshed: %#v", provider.fetchBatches)
	}
	ref := provider.fetchBatches[0][0]
	if ref.Repo.Repo != "o/api" || ref.Number != 12 {
		t.Fatalf("fetched ref = %#v, want o/api#12", ref)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected child repo PR write")
	}
	if got := store.writes[0].pr.Repo; got != "o/api" {
		t.Fatalf("persisted repo = %q, want o/api", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle observations = %d, want 1", len(lc.observed))
	}
}

func TestPoll_DiscoversWorkspaceChildRepoUpstreamPR(t *testing.T) {
	oldRemoteURLs := gitRemoteURLsFunc
	gitRemoteURLsFunc = func(_ context.Context, path string) []string {
		if strings.HasSuffix(filepath.ToSlash(path), "/api") {
			return []string{"https://github.com/o/api.git", "https://github.com/upstream/api.git"}
		}
		return nil
	}
	defer func() { gitRemoteURLsFunc = oldRemoteURLs }()

	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "tp/p-1/api-billing"
	store.projects["p"] = domain.ProjectRecord{
		ID:            "p",
		Path:          "/repo/workspace",
		RepoOriginURL: "https://github.com/o/root.git",
		Kind:          domain.ProjectKindWorkspace,
	}
	store.workspaceRepos = map[string][]domain.WorkspaceRepoRecord{
		"p": {{ProjectID: "p", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/o/api.git"}},
	}
	upstreamRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "upstream", Name: "api", Repo: "upstream/api"}
	prObs := testObs(44)
	prObs.Repo = "upstream/api"
	prObs.PR.URL = "https://github.com/upstream/api/pull/44"
	prObs.PR.HTMLURL = "https://github.com/upstream/api/pull/44"
	prObs.PR.Number = 44
	prObs.PR.SourceBranch = "tp/p-1/api-billing"
	prObs.PR.HeadRepo = "o/api"
	prObs.PR.TargetBranch = "main"
	prObs.PR.HeadSHA = "api-sha"
	prObs.CI.HeadSHA = "api-sha"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(upstreamRepo, 0): {ETag: "upstream-v1"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstreamRepo, 0): {
				{URL: "https://github.com/upstream/api/pull/44", Number: 44, SourceBranch: "tp/p-1/api-billing", HeadRepo: "o/api", TargetBranch: "main", HeadSHA: "api-sha"},
			},
		},
		observations: map[string]ports.SCMObservation{prKey(upstreamRepo, 44): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected discovered upstream child PR write")
	}
	if got := store.writes[0].pr.Repo; got != "upstream/api" {
		t.Fatalf("written repo = %q, want upstream/api", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
}

// A PR whose head branch matches a session branch but lives in a fork (its head
// repo differs from the project repo) must not be auto-attributed: its commits
// are not the session's work. It is neither fetched nor persisted.
func TestPoll_IgnoresForkPRWithMatchingBranch(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/forker/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "forker/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("fork PR must not be fetched, got %#v", provider.fetchBatches)
	}
	if len(store.writes) != 0 {
		t.Fatalf("fork PR must not be persisted, got %d writes", len(store.writes))
	}
}

func TestPoll_DiscoversSameRepoPRAfterRename(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/neworg/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "NewOrg/r", BaseRepo: "NewOrg/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("renamed-repo PR must be attributed to p-1, got %#v", store.writes)
	}
}

// A PR opened from the fork push origin against an upstream base repo (the
// standard fork -> upstream contribution flow) must be discovered and attributed
// by scanning every remote in the project checkout, not just origin. Its head
// lives in origin (o/r) while its base lives in upstream (up/r); the persisted
// row records the upstream base repo so refresh refetches against it.
func TestPoll_DiscoversCrossForkPRFromUpstreamRemote(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, "init", dir)
	mustGit(t, "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git")
	mustGit(t, "-C", dir, "remote", "add", "upstream", "https://github.com/up/r.git")

	upstream := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "up", Name: "r", Repo: "up/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "tp/p-1/root"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	crossObs := ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "up/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/up/r/pull/1", Number: 1, State: "open", SourceBranch: "tp/p-1/feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha1"},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "origin"}, prKey(upstream, 0): {ETag: "up"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstream, 0): {{URL: "https://github.com/up/r/pull/1", Number: 1, SourceBranch: "tp/p-1/feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}},
		},
		observations: map[string]ports.SCMObservation{prKey(upstream, 1): crossObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected cross-fork PR write")
	}
	got := store.writes[0].pr
	if got.SessionID != "p-1" {
		t.Fatalf("session id = %q, want p-1", got.SessionID)
	}
	if got.Repo != "up/r" {
		t.Fatalf("pr repo = %q, want up/r (upstream base)", got.Repo)
	}
	if got.SourceBranch != "tp/p-1/feat" {
		t.Fatalf("source branch = %q, want tp/p-1/feat", got.SourceBranch)
	}
	fetched := false
	for _, batch := range provider.fetchBatches {
		for _, ref := range batch {
			if ref.Repo.Repo == "up/r" && ref.Number == 1 {
				fetched = true
			}
		}
	}
	if !fetched {
		t.Fatalf("cross-fork PR must be refreshed against upstream, batches=%#v", provider.fetchBatches)
	}
}

// A PR on a scanned upstream remote whose head lives in some third-party fork
// (not this project's origin) must never be attributed, even though its branch
// name matches a session. Scanning extra remotes stays safe.
func TestPoll_IgnoresUpstreamPRFromForeignHead(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, "init", dir)
	mustGit(t, "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git")
	mustGit(t, "-C", dir, "remote", "add", "upstream", "https://github.com/up/r.git")

	upstream := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "up", Name: "r", Repo: "up/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "tp/p-1/root"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "origin"}, prKey(upstream, 0): {ETag: "up"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstream, 0): {{URL: "https://github.com/up/r/pull/9", Number: 9, SourceBranch: "tp/p-1/feat", HeadRepo: "stranger/r", TargetBranch: "main", HeadSHA: "sha9"}},
		},
		observations: map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("foreign-head upstream PR must not be fetched, got %#v", provider.fetchBatches)
	}
	if len(store.writes) != 0 {
		t.Fatalf("foreign-head upstream PR must not be persisted, got %d writes", len(store.writes))
	}
}

// A newly discovered open PR is persisted as a baseline row during discovery,
// before the refresh/lifecycle pass. This is what lets a same-poll terminal
// observation for a sibling PR see the open PR in the store and avoid completing
// the session prematurely. The persist holds even when the refresh fetch yields
// no observation for the new PR.
func TestPoll_DiscoveredPRPersistedAsBaselineBeforeRefresh(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{}, // refresh returns nothing
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	var baseline *domain.PullRequest
	for i := range store.writes {
		if store.writes[i].pr.Number == 1 {
			baseline = &store.writes[i].pr
			break
		}
	}
	if baseline == nil {
		t.Fatalf("discovered PR #1 not persisted as a baseline row; writes=%#v", store.writes)
		return
	}
	if baseline.Merged || baseline.Closed {
		t.Fatalf("baseline row must be open, got merged=%v closed=%v", baseline.Merged, baseline.Closed)
	}
}

// TestDiscoverSubjects_BackfillsRepoOriginURL asserts that a project row with
// an empty RepoOriginURL but a real on-disk repo gets its origin filled in
// during discovery and persisted, so the same project becomes observable
// without re-running project add.
func TestDiscoverSubjects_BackfillsRepoOriginURL(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}

	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir}}, // empty RepoOriginURL
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(0, 0).UTC())

	if _, _, err := obs.discoverSubjects(context.Background()); err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if got := store.projects["p"].RepoOriginURL; got != "https://github.com/o/r.git" {
		t.Fatalf("RepoOriginURL after backfill = %q, want https://github.com/o/r.git", got)
	}
}

// TestDiscoverSubjects_RefreshesRenamedRepoOriginURL asserts a stored origin
// that no longer matches the checkout's live origin (GitHub repo rename) is
// replaced and persisted.
func TestDiscoverSubjects_RefreshesRenamedRepoOriginURL(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/o/new.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/old.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	obs := newTestObserver(store, &fakeProvider{}, &fakeLifecycle{}, time.Unix(0, 0).UTC())
	if _, _, err := obs.discoverSubjects(context.Background()); err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if got := store.projects["p"].RepoOriginURL; got != "https://github.com/o/new.git" {
		t.Fatalf("RepoOriginURL = %q, want renamed origin", got)
	}
}

// TestDiscoverSubjects_DoesNotAdoptDifferentHostOrigin asserts an origin on a
// different host is not adopted as the project's trusted origin.
func TestDiscoverSubjects_DoesNotAdoptDifferentHostOrigin(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://gitlab.com/o/new.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/old.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	obs := newTestObserver(store, &fakeProvider{}, &fakeLifecycle{}, time.Unix(0, 0).UTC())
	if _, _, err := obs.discoverSubjects(context.Background()); err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if got := store.projects["p"].RepoOriginURL; got != "https://github.com/o/old.git" {
		t.Fatalf("RepoOriginURL = %q, want stored origin kept", got)
	}
}

// TestDiscoverSubjects_NonGitPathDoesNotBackfill confirms the backfill is
// best-effort: a non-git project path leaves RepoOriginURL empty without
// erroring or persisting a stub, so the project is simply skipped.
func TestDiscoverSubjects_NonGitPathDoesNotBackfill(t *testing.T) {
	dir := t.TempDir()
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	obs := newTestObserver(store, &fakeProvider{}, &fakeLifecycle{}, time.Unix(0, 0).UTC())
	subjects, sessionRepos, err := obs.discoverSubjects(context.Background())
	if err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if len(subjects) != 0 || len(sessionRepos) != 0 {
		t.Fatalf("non-git project should be skipped, got %d subjects %d sessionRepos", len(subjects), len(sessionRepos))
	}
	if got := store.projects["p"].RepoOriginURL; got != "" {
		t.Fatalf("RepoOriginURL = %q, want empty (no persist on failed backfill)", got)
	}
}

// TestPoll_IncrementalDiscovery_OnlyUpdatedMRsRefreshed verifies that after
// the first poll establishes a sync cursor, only MRs in the updated set are
// refreshed on subsequent polls — not every tracked MR in the repo (review
// finding #2).
func TestPoll_IncrementalDiscovery_OnlyUpdatedMRsRefreshed(t *testing.T) {
	store := testStoreWithSession()
	// Three tracked PRs with durable hashes so missingLocalState does not
	// force a refresh; the listedPRs filter is the deciding factor.
	pr1, pr2, pr3 := knownPR(1), knownPR(2), knownPR(3)
	for _, p := range []*domain.PullRequest{&pr1, &pr2, &pr3} {
		p.MetadataHash = "durable-meta"
		p.CIHash = "durable-ci"
		p.ReviewHash = "durable-review"
	}
	store.prs["p-1"] = []domain.PullRequest{pr1, pr2, pr3}

	// Poll 1: full listing (no cursor), all 3 PRs returned.
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo1"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testRepo, 0): {
				{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"},
				{Number: 2, State: "open", SourceBranch: "feat", HeadRepo: "o/r"},
				{Number: 3, State: "open", SourceBranch: "feat", HeadRepo: "o/r"},
			},
		},
		observations: map[string]ports.SCMObservation{
			prKey(testRepo, 1): testObs(1),
			prKey(testRepo, 2): testObs(2),
			prKey(testRepo, 3): testObs(3),
		},
	}
	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Fatalf("poll 1: listCalls = %d, want 1", provider.listCalls)
	}
	if obs.Cache.LastSyncCursor[prKey(testRepo, 0)].IsZero() {
		t.Fatal("poll 1: cursor not set after successful poll")
	}
	cursor := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]

	// Poll 2: ETag changed (repo2), but only PR 2 is in the updated set.
	// PRs #1 and #3 are tracked open but absent from the listing, so they
	// trigger terminal-state reconciliation fetches (Item 2). With durable
	// hashes matching their observations, the reconciled fetches are no-op
	// persists (no writes, no ETag/cursor advance) — but they still issue
	// detail fetches.
	provider.mu.Lock()
	provider.repoGuards[prKey(testRepo, 0)] = ports.SCMGuardResult{ETag: "repo2"}
	provider.openPRs[prKey(testRepo, 0)] = []ports.SCMPRObservation{
		{Number: 2, State: "open", SourceBranch: "feat", HeadRepo: "o/r"},
	}
	// Match durable hashes so the reconciliation results for PRs #1 and #3
	// are no-op persists (still-open, no terminal transition).
	o1, o3 := testObs(1), testObs(3)
	pr1.MetadataHash = metadataSemanticHash(o1)
	pr1.CIHash = ciSemanticHash(o1.CI)
	pr1.ReviewHash = reviewSemanticHash(o1.Review)
	pr3.MetadataHash = metadataSemanticHash(o3)
	pr3.CIHash = ciSemanticHash(o3.CI)
	pr3.ReviewHash = reviewSemanticHash(o3.Review)
	store.prs["p-1"] = []domain.PullRequest{pr1, pr2, pr3}
	provider.fetchBatches = nil
	provider.mu.Unlock()

	now2 := now.Add(time.Minute)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Cursor must advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; !got.After(cursor) {
		t.Fatalf("poll 2: cursor did not advance: got %v, want > %v", got, cursor)
	}
	// The incremental-discovery filter selects only PR 2 (in the updated
	// set) for normal refresh. PRs #1 and #3 are fetched via the
	// reconciliation pass. So we expect 2 fetch batches: one for PR 2
	// (normal refresh) and one for PRs #1 and #3 (reconciliation).
	if len(provider.fetchBatches) != 2 {
		t.Fatalf("poll 2: fetchBatches = %d, want 2 (normal + reconciliation)", len(provider.fetchBatches))
	}
	// Collect all fetched PR numbers.
	fetched := map[int]bool{}
	for _, batch := range provider.fetchBatches {
		for _, ref := range batch {
			fetched[ref.Number] = true
		}
	}
	// All three PRs are fetched: PR 2 via normal refresh, PRs 1 and 3 via
	// reconciliation. The incremental-discovery filter correctly prevents
	// PRs 1 and 3 from being in the normal refresh batch — they are only
	// fetched via reconciliation.
	if !fetched[1] || !fetched[2] || !fetched[3] {
		t.Fatalf("poll 2: expected PRs 1, 2, 3 fetched (2 via normal, 1+3 via reconciliation); got %v", fetched)
	}
}

// TestPoll_IncrementalDiscovery_CursorNotAdvancedOnFailure verifies that when
// FetchPullRequests fails, the sync cursor is NOT advanced so the next poll
// re-fetches MRs updated during the failed poll
func TestPoll_IncrementalDiscovery_CursorNotAdvancedOnFailure(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo1"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testRepo, 0): {{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}},
		},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	cursor := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]
	if cursor.IsZero() {
		t.Fatal("poll 1: cursor not set")
	}

	// Poll 2: FetchPullRequests fails — cursor must not advance.
	provider.mu.Lock()
	provider.repoGuards[prKey(testRepo, 0)] = ports.SCMGuardResult{ETag: "repo2"}
	provider.fetchErr = errors.New("transient fetch failure")
	provider.mu.Unlock()
	now2 := now.Add(time.Minute)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; got != cursor {
		t.Fatalf("poll 2: cursor advanced after failure: got %v, want %v (unchanged)", got, cursor)
	}
}

func TestPoll_RenamedRepoObservationPersistsUnderTrackedRef(t *testing.T) {
	store, provider, _ := renamedRepoFixture()
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want 1", len(provider.fetchBatches))
	}
	if len(store.writes) == 0 {
		t.Fatal("renamed-repo observation was dropped: no writes")
	}
	final := store.writes[len(store.writes)-1].pr
	if final.Repo != "new/r" || final.Title != "PR" || final.CI != domain.CIPassing || final.Mergeability != domain.MergeBlocked {
		t.Fatalf("persisted PR = repo %q title %q ci %q mergeability %q; want canonical new/r metadata", final.Repo, final.Title, final.CI, final.Mergeability)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle notifications = %d, want 1", len(lc.observed))
	}
}

func TestPoll_RenamedRepoReconciliationPersistsUnderTrackedRef(t *testing.T) {
	store, provider, oldRepo := renamedRepoFixture()
	// A fully-observed local row keeps the PR out of the normal refresh
	// candidate set; the empty incremental listing then routes it through
	// terminal reconciliation, which must survive the rename the same way.
	local := &store.prs["p-1"][0]
	local.MetadataHash = "stale-meta"
	local.CIHash = "stale-ci"
	local.ReviewHash = "stale-review"
	local.Review = domain.ReviewRequired
	local.ReviewObservedAt = time.Unix(60, 0).UTC()
	local.HeadSHA = "sha0"
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(100, 0).UTC())
	obs.Cache.LastSyncCursor[prKey(oldRepo, 0)] = time.Unix(50, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("renamed-repo reconciliation observation was dropped: no writes")
	}
	final := store.writes[len(store.writes)-1].pr
	if final.Repo != "new/r" || final.CI != domain.CIPassing {
		t.Fatalf("persisted PR = repo %q ci %q; want canonical new/r with passing CI", final.Repo, final.CI)
	}
}

func TestKeyForTrackedPR(t *testing.T) {
	repo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
	withID := domain.PullRequest{Provider: "GitHub", Host: "GitHub.com", ProviderID: "PR_x", Number: 7}
	if got, want := keyForTrackedPR(withID, repo), "github:github.com@PR_x"; got != want {
		t.Fatalf("identity key = %q, want %q", got, want)
	}
	withoutID := domain.PullRequest{Number: 7}
	if got, want := keyForTrackedPR(withoutID, repo), prKey(repo, 7); got != want {
		t.Fatalf("legacy key = %q, want %q", got, want)
	}
}

// The discovery clobber (issue #4089, mechanism 2): a second scan name for the
// same repository (stale upstream remote after a transfer) lists the same PRs.
// A tracked PR recognized by its provider identity must NOT be re-discovered —
// the baseline write would overwrite the row's CI/mergeability facts and wipe
// its semantic hashes, flapping the PR panel back to "Checking merge
// readiness" on every listing change.
func TestPoll_SecondScanNameDoesNotRebaselineTrackedPR(t *testing.T) {
	oldRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "old", Name: "r", Repo: "old/r"}
	newRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "new", Name: "r", Repo: "new/r"}
	restoreRemotes := gitRemoteURLsFunc
	gitRemoteURLsFunc = func(context.Context, string) []string {
		return []string{"https://github.com/new/r.git", "https://github.com/old/r.git"}
	}
	defer func() { gitRemoteURLsFunc = restoreRemotes }()

	listing := []ports.SCMPRObservation{{
		ProviderID: "PR_stable_1", URL: "https://github.com/new/r/pull/1", Number: 1,
		SourceBranch: "feat", HeadRepo: "new/r", TargetBranch: "main", HeadSHA: "sha1",
	}}
	for _, tt := range []struct {
		name       string
		providerID string
	}{
		{name: "provider identity already stored", providerID: "PR_stable_1"},
		{name: "legacy row associated by listing identity"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tracked := domain.PullRequest{
				URL: "https://github.com/new/r/pull/1", SessionID: "p-1", Number: 1,
				Provider: "github", Host: "github.com", Repo: "old/r", ProviderID: tt.providerID,
				SourceBranch: "feat", HeadSHA: "sha1", CI: domain.CIPassing, Mergeability: domain.MergeBlocked,
				MetadataHash: "m", CIHash: "c", ReviewHash: "r", Review: domain.ReviewRequired,
				ObservedAt: time.Unix(50, 0).UTC(), CIObservedAt: time.Unix(50, 0).UTC(), ReviewObservedAt: time.Unix(50, 0).UTC(),
			}
			store := &fakeStore{
				sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
				projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: "/proj", RepoOriginURL: "https://github.com/new/r.git"}},
				prs:      map[domain.SessionID][]domain.PullRequest{"p-1": {tracked}},
				checks:   map[string][]domain.PullRequestCheck{},
			}
			provider := &fakeProvider{
				repoGuards: map[string]ports.SCMGuardResult{
					prKey(newRepo, 0): {ETag: "vN"},
					prKey(oldRepo, 0): {ETag: "vO"},
				},
				// Both scan names list the same PR: the canonical listing and the
				// stale-remote listing served through GitHub's rename redirect.
				openPRs: map[string][]ports.SCMPRObservation{
					prKey(newRepo, 0): listing,
					prKey(oldRepo, 0): listing,
				},
				observations: map[string]ports.SCMObservation{},
			}
			obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(100, 0).UTC())
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, w := range store.writes {
				if w.pr.Number == 1 && w.pr.CI == domain.CIUnknown {
					t.Fatalf("tracked PR was re-baselined by a second scan name: %+v", w.pr)
				}
			}
		})
	}
}

// Identity-first dispatch: when the tracked row carries a provider id, a
// canonical observation for a renamed repo is delivered by identity — no URL
// fallback involved — and the persisted row heals to the canonical repo.
func TestPoll_IdentityKeyedDispatchSurvivesRename(t *testing.T) {
	oldRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "old", Name: "r", Repo: "old/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/old/r.git"}},
		prs: map[domain.SessionID][]domain.PullRequest{"p-1": {{
			// URL intentionally differs from the canonical URL so the legacy
			// URL fallback cannot be what delivers this observation.
			URL: "https://github.com/old/r/pull/1", SessionID: "p-1", Number: 1,
			Provider: "github", Host: "github.com", Repo: "old/r", ProviderID: "PR_stable_1",
			SourceBranch: "feat",
		}}},
		checks: map[string][]domain.PullRequestCheck{},
	}
	canonical := ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "new/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/new/r/pull/1", Number: 1, ProviderID: "PR_stable_1", State: "open", SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha1", Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha1"},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewRequired)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"review_required"}},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(oldRepo, 0): {ETag: "v1"}},
		openPRs:      map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{prKey(oldRepo, 1): canonical},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("identity-keyed observation was dropped")
	}
	final := store.writes[len(store.writes)-1].pr
	if final.Repo != "new/r" || final.CI != domain.CIPassing || final.ProviderID != "PR_stable_1" {
		t.Fatalf("persisted PR = repo %q ci %q providerID %q; want canonical new/r, passing, PR_stable_1", final.Repo, final.CI, final.ProviderID)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle notifications = %d, want 1", len(lc.observed))
	}
}

// A permanently unresolvable tracked PR (provider returns an ErrSCMNotFound
// placeholder every tick) must stay quiet at Warn level — Debug only — while
// still pinning the repo ETag/cursor and never entering a rate-limit
// cooldown. Guards the ~2.9k-warnings/day regression flagged in review.
func TestPoll_NotFoundObservationLogsDebugAndPinsRepo(t *testing.T) {
	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	provider := &fakeProvider{
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:        map[string][]ports.SCMPRObservation{},
		observations:   map[string]ports.SCMObservation{},
		fetchObsErrors: map[string]error{prKey(testRepo, 1): fmt.Errorf("%w: pull request o/r#1 not in batch response", ports.ErrSCMNotFound)},
		identity:       ports.SCMIdentity{Login: "alice", Human: true},
	}
	var logs bytes.Buffer
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:                   time.Hour,
		Logger:                 slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		CacheMax:               128,
		ScopedIdentityResolver: provider,
	})
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "provider fetch failed") || strings.Contains(logs.String(), "rate-limited") {
		t.Fatalf("not-found placeholder logged at warn: %s", logs.String())
	}
	if obs.inRateLimitCooldown(time.Unix(1, 0).UTC(), "github") {
		t.Fatal("not-found placeholder must not trigger a rate-limit cooldown")
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "v1" {
		t.Fatalf("repo ETag advanced past a not-found ref: %q, want pinned v1", got)
	}
	if len(store.writes) != 0 {
		t.Fatalf("not-found placeholder was persisted: %#v", store.writes)
	}
}

func TestDomainFromObservationCarriesChangedPaths(t *testing.T) {
	obs := ports.SCMObservation{PR: ports.SCMPRObservation{URL: "https://github.com/o/r/pull/1", Number: 1, Paths: []string{"a.go", "b.go"}}}
	pr, _, _, _, _ := domainFromObservation("p-1", domain.SessionRecord{}, obs, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	if len(pr.Paths) != 2 || pr.Paths[0] != "a.go" {
		t.Fatalf("paths = %v, want the observed changed paths", pr.Paths)
	}
}

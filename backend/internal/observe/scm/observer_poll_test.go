package scm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestDispatchOrderIsDeterministic(t *testing.T) {
	obs := map[string]ports.SCMObservation{
		"a#9": {PR: ports.SCMPRObservation{Number: 9}},
		"a#7": {PR: ports.SCMPRObservation{Number: 7}},
		"b#3": {PR: ports.SCMPRObservation{Number: 3}},
	}
	subjects := map[string]*subject{
		"a#9": {session: domain.SessionRecord{ID: "sess-a"}},
		"a#7": {session: domain.SessionRecord{ID: "sess-a"}},
		"b#3": {session: domain.SessionRecord{ID: "sess-b"}},
	}
	want := []string{"a#7", "a#9", "b#3"}
	for run := 0; run < 8; run++ {
		got := dispatchOrder(obs, subjects)
		if len(got) != len(want) {
			t.Fatalf("run %d: got %d keys, want %d (%v)", run, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("run %d: order[%d]=%q, want %q (full %v)", run, i, got[i], want[i], got)
			}
		}
	}
}

func TestStartAsyncPerformsImmediatePollAndStopsOnCancel(t *testing.T) {
	store := testStoreWithSession()
	store.listEntered = make(chan struct{})
	store.listRelease = make(chan struct{})
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	ctx, cancel := context.WithCancel(context.Background())
	done := obs.Start(ctx)
	select {
	case <-store.listEntered:
	case <-time.After(time.Second):
		t.Fatal("initial poll did not start asynchronously")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not exit after context cancellation")
	}
}

func TestPoll_SkipsProviderWhenCredentialsUnavailable(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		credentialOK:   false,
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.credentialChecks != 2 {
		t.Fatalf("credential checks = %d, want one per poll while unavailable", provider.credentialChecks)
	}
	if provider.repoGuardCalls != 0 || provider.listCalls != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("provider API calls should be skipped without credentials: guards=%d lists=%d batches=%d",
			provider.repoGuardCalls, provider.listCalls, len(provider.fetchBatches))
	}
}

func TestPoll_ReconcilesBranchStatesWithoutCredentials(t *testing.T) {
	provider := &fakeProvider{credentialGate: true, credentialOK: false, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(testStoreWithSession(), provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	branchStates := &countingBranchStates{}
	obs.branchStates = branchStates
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if branchStates.calls != 1 {
		t.Fatalf("branch state reconciles = %d, want one per tick even without credentials", branchStates.calls)
	}
}

func TestPoll_LogsStartAndCompletionTiming(t *testing.T) {
	store := &fakeStore{
		sessions: nil,
		projects: map[string]domain.ProjectRecord{},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	var logs bytes.Buffer
	obs := New(&fakeProvider{}, store, &fakeLifecycle{}, Config{
		Tick:   time.Hour,
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	logged := logs.String()
	for _, field := range []string{"scm observer: poll started", "scm observer: poll completed", "started_at=", "completed_at=", "duration_ms="} {
		if !strings.Contains(logged, field) {
			t.Fatalf("missing %q in poll timing log:\n%s", field, logged)
		}
	}
}

func TestLogProviderRequest_WarnsForRequestLongerThanPollCadence(t *testing.T) {
	var logs bytes.Buffer
	obs := &Observer{
		tick:   30 * time.Second,
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	obs.logProviderRequest("repo_pr_list_guard", "acme/widgets", time.Now().Add(-31*time.Second), nil)

	logged := logs.String()
	for _, field := range []string{"slow provider request completed", "stage=repo_pr_list_guard", "repository=acme/widgets", "started_at=", "completed_at=", "duration_ms="} {
		if !strings.Contains(logged, field) {
			t.Fatalf("missing %q in provider timing log:\n%s", field, logged)
		}
	}
}

func TestPoll_ResumesAfterCredentialsBecomeAvailable(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	var logs bytes.Buffer
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.logger = slog.New(slog.NewTextHandler(&logs, nil))

	for i := 0; i < 2; i++ {
		if err := obs.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if provider.repoGuardCalls != 0 {
		t.Fatalf("provider API called before authentication: guards=%d", provider.repoGuardCalls)
	}

	provider.mu.Lock()
	provider.credentialOK = true
	provider.mu.Unlock()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if obs.disabled || provider.credentialChecks != 3 || provider.repoGuardCalls != 1 {
		t.Fatalf("observer did not resume after authentication: disabled=%v credential checks=%d guards=%d",
			obs.disabled, provider.credentialChecks, provider.repoGuardCalls)
	}
	if got := strings.Count(logs.String(), "scm observer disabled: provider credentials unavailable"); got != 1 {
		t.Fatalf("unavailable credentials warning count = %d, want one: %s", got, logs.String())
	}
}

func TestPoll_RetriesTransientCredentialErrors(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		credentialErr:  errors.New("gh auth token failed transiently"),
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if obs.credentialsChecked || obs.disabled {
		t.Fatalf("transient credential error should not commit checked/disabled: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 1 || provider.repoGuardCalls != 0 {
		t.Fatalf("first poll should check credentials only: checks=%d repoGuards=%d", provider.credentialChecks, provider.repoGuardCalls)
	}

	provider.mu.Lock()
	provider.credentialErr = nil
	provider.credentialOK = true
	provider.mu.Unlock()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !obs.credentialsChecked || obs.disabled {
		t.Fatalf("successful retry should commit checked without disabling: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 2 || provider.repoGuardCalls != 1 {
		t.Fatalf("second poll should retry credentials and continue: checks=%d repoGuards=%d", provider.credentialChecks, provider.repoGuardCalls)
	}
}

// TestStart_LogsDisabledWarningWhenNoTokenAndNoSubjects exercises the bug-7
// regression: on a fresh daemon with no tracked sessions/PRs, discoverSubjects
// returns empty and Poll short-circuits before reaching the credential gate.
// The "scm observer disabled: provider credentials unavailable" warn line must
// still fire exactly once from the observer loop's pre-Poll credential check.
func TestStart_LogsDisabledWarningWhenNoTokenAndNoSubjects(t *testing.T) {
	store := &fakeStore{
		sessions: nil, // no sessions → discoverSubjects returns empty
		projects: map[string]domain.ProjectRecord{},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{
		credentialGate: true,
		credentialOK:   false,
		repoGuards:     map[string]ports.SCMGuardResult{},
		observations:   map[string]ports.SCMObservation{},
	}

	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:    func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:     time.Hour,
		Logger:   logger,
		CacheMax: 128,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := obs.Start(ctx)
	// Wait until the loop has emitted the expected warn line, or fail on timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "scm observer disabled: provider credentials unavailable") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not exit after context cancellation")
	}

	logged := buf.String()
	if !strings.Contains(logged, "scm observer disabled: provider credentials unavailable") {
		t.Fatalf("expected disabled-credentials warn line in logs; got:\n%s", logged)
	}
	if got := strings.Count(logged, "scm observer disabled: provider credentials unavailable"); got != 1 {
		t.Fatalf("warn line should fire exactly once, got %d occurrences:\n%s", got, logged)
	}
	if !obs.credentialsChecked || !obs.disabled {
		t.Fatalf("observer state after pre-poll credential check: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 1 {
		t.Fatalf("credential checks = %d, want exactly one pre-poll check", provider.credentialChecks)
	}
	if provider.repoGuardCalls != 0 || provider.listCalls != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("no provider API calls expected when disabled: guards=%d lists=%d batches=%d",
			provider.repoGuardCalls, provider.listCalls, len(provider.fetchBatches))
	}
}

func TestPoll_DisablesAutomaticDiscoveryWithoutHumanIdentity(t *testing.T) {
	tests := []struct {
		name        string
		identity    ports.SCMIdentity
		identityErr error
		noResolver  bool
	}{
		{name: "missing resolver", noResolver: true},
		{name: "empty login", identity: ports.SCMIdentity{Login: "  ", Human: true}},
		{name: "lookup error", identityErr: errors.New("identity unavailable")},
		{name: "bot account", identity: ports.SCMIdentity{Login: "tallpine-bot", Human: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStoreWithSession()
			provider := &fakeProvider{
				repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
				openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Author: "other"}}},
				observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
				identity:     tt.identity,
				identityErr:  tt.identityErr,
			}
			var resolver ports.ScopedIdentityResolver = provider
			if tt.noResolver {
				resolver = nil
			}
			obs := New(provider, store, &fakeLifecycle{}, Config{Logger: quietSlog(), ScopedIdentityResolver: resolver})
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(provider.fetchBatches) != 0 || len(store.writes) != 0 {
				t.Fatalf("automatic discovery must fail closed: batches=%#v writes=%#v", provider.fetchBatches, store.writes)
			}
		})
	}
}

func TestPoll_GraphQLBatchChunksAt25(t *testing.T) {
	store := &fakeStore{projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}}, prs: map[domain.SessionID][]domain.PullRequest{}, checks: map[string][]domain.PullRequestCheck{}}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{}}
	for i := 1; i <= 26; i++ {
		id := domain.SessionID("p-" + fmt.Sprint(i))
		store.sessions = append(store.sessions, domain.SessionRecord{ID: id, ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "b" + fmt.Sprint(i)}})
		pr := knownPR(i)
		pr.SessionID = id
		pr.MetadataHash = "" // force candidate
		store.prs[id] = []domain.PullRequest{pr}
		provider.observations[prKey(testRepo, i)] = testObs(i)
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 2 || len(provider.fetchBatches[0]) != 25 || len(provider.fetchBatches[1]) != 1 {
		t.Fatalf("batch sizes = %#v", provider.fetchBatches)
	}
}

func TestPoll_RealPassingChecksAreNotRefetchedByNoCIRule(t *testing.T) {
	local := knownPR(1) // provider-reported passing, no waiver marker
	obs, _, provider := quietNoCIPoll(t, false, local, nil, testObs(1), nil)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("fetch batches = %d, want none for a PR with real checks", len(provider.fetchBatches))
	}
}

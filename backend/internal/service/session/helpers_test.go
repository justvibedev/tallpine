package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type fakeAgentReadiness struct {
	snapshot                domain.AgentReadinessSnapshot
	err                     error
	calls                   int
	agentID                 string
	purpose                 domain.AgentReadinessPurpose
	installationInvalidated []string
	authInvalidated         []string
	rechecks                []string
}

func (f *fakeAgentReadiness) EnsureAgentReadiness(_ context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	f.calls++
	f.agentID = agentID
	f.purpose = purpose
	return f.snapshot, f.err
}

func (f *fakeAgentReadiness) InvalidateAgentInstallation(agentID string) {
	f.installationInvalidated = append(f.installationInvalidated, agentID)
}

func (f *fakeAgentReadiness) InvalidateAgentAuthentication(agentID string) {
	f.authInvalidated = append(f.authInvalidated, agentID)
}

func (f *fakeAgentReadiness) RecheckAgent(agentID string) {
	f.rechecks = append(f.rechecks, agentID)
}

type fakeStore struct {
	lanes               map[domain.SessionID]domain.SessionLane
	allListCalls        int
	liveListCalls       int
	cleanupFacts        map[domain.SessionID]domain.SessionCleanupRecord
	sessions            map[domain.SessionID]domain.SessionRecord
	getSessionErr       error
	pr                  map[domain.SessionID]domain.PRFacts
	prFacts             map[domain.SessionID][]domain.PRFacts
	prs                 map[domain.SessionID][]domain.PullRequest
	reportedPRURLs      map[domain.SessionID][]string
	projects            map[string]domain.ProjectRecord
	worktrees           map[domain.SessionID][]domain.SessionWorktreeRecord
	checks              map[string][]domain.PullRequestCheck
	reviews             map[string][]domain.PullRequestReview
	reviewsErr          error
	threads             map[string][]domain.PullRequestReviewThread
	comments            map[string][]domain.PullRequestComment
	commentsErr         error
	reviewRuns          map[domain.SessionID][]domain.CurrentHeadReviewRun
	interrupted         map[domain.SessionID]bool
	listPRFactsCalls    int
	listReviewRunsCalls int
	num                 int
	conversations       map[domain.SessionID]domain.ConversationRecord
	activities          []domain.ConversationActivity

	// getCleanupFactsCalls counts single-session reads; listCleanupFactsCalls
	// counts batched ones, and cleanupFactsRequested records the ids they asked for.
	getCleanupFactsCalls  int
	listCleanupFactsCalls int
	cleanupFactsRequested []domain.SessionID
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:       map[domain.SessionID]domain.SessionRecord{},
		pr:             map[domain.SessionID]domain.PRFacts{},
		prFacts:        map[domain.SessionID][]domain.PRFacts{},
		prs:            map[domain.SessionID][]domain.PullRequest{},
		reportedPRURLs: map[domain.SessionID][]string{},
		projects:       map[string]domain.ProjectRecord{},
		worktrees:      map[domain.SessionID][]domain.SessionWorktreeRecord{},
		checks:         map[string][]domain.PullRequestCheck{},
		reviews:        map[string][]domain.PullRequestReview{},
		threads:        map[string][]domain.PullRequestReviewThread{},
		comments:       map[string][]domain.PullRequestComment{},
		reviewRuns:     map[domain.SessionID][]domain.CurrentHeadReviewRun{},
	}
}

func (f *fakeStore) ListWorkspaceRepos(context.Context, string) ([]domain.WorkspaceRepoRecord, error) {
	return nil, nil
}

func (f *fakeStore) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	conversation, ok := f.conversations[id]
	if !ok {
		return domain.ConversationRecord{}, domain.ErrNoConversation
	}
	return conversation, nil
}

func (f *fakeStore) SessionForActivityProviderItem(_ context.Context, providerItemID string) (domain.SessionID, bool, error) {
	for _, activity := range f.activities {
		if activity.ProviderItemID != providerItemID {
			continue
		}
		for sessionID, conversation := range f.conversations {
			if conversation.ID == activity.ConversationID {
				return sessionID, true, nil
			}
		}
	}
	return "", false, nil
}

func (f *fakeStore) ActivityStatusByProviderItem(_ context.Context, conversationID, providerItemID string) (domain.ActivityStatus, bool, error) {
	for _, existing := range f.activities {
		if existing.ConversationID == conversationID && existing.ProviderItemID == providerItemID {
			return existing.Status, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeStore) UpsertActivity(_ context.Context, conversationID, _ string, activity domain.ConversationActivity, now time.Time) error {
	if activity.ProviderItemID != "" {
		for i, existing := range f.activities {
			if existing.ConversationID == conversationID && existing.ProviderItemID == activity.ProviderItemID {
				if activity.Kind == domain.ActivityKindTallpineAction &&
					activity.Status == domain.ActivityStatusRunning &&
					(existing.Status == domain.ActivityStatusCompleted ||
						existing.Status == domain.ActivityStatusFailed ||
						existing.Status == domain.ActivityStatusCancelled ||
						existing.Status == domain.ActivityStatusResolved) {
					return nil
				}
				activity.ID = existing.ID
				activity.ConversationID = conversationID
				activity.Sequence = existing.Sequence
				activity.Revision = existing.Revision + 1
				activity.UpdatedAt = now
				f.activities[i] = activity
				return nil
			}
		}
	}
	activity.ConversationID = conversationID
	activity.Sequence = int64(len(f.activities) + 1)
	activity.Revision = 1
	activity.CreatedAt = now
	activity.UpdatedAt = now
	f.activities = append(f.activities, activity)
	return nil
}

func newWorkspaceRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "tallpine@example.com")
	runGit(t, dir, "config", "user.name", "Tallpine Tests")
	writeWorkspaceFile(t, dir, ".gitignore", "node_modules/\n")
	writeWorkspaceFile(t, dir, "README.md", "hello\n")
	writeWorkspaceFile(t, dir, "src/app.go", "package main\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeWorkspaceFile(t testing.TB, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fixtureCommit is one commit for importCommits: its message and the files it
// writes.
type fixtureCommit struct {
	message string
	files   map[string]string
}

// importCommits appends commits to branch on top of parent in one git
// fast-import, far faster than a git commit per commit for the hundreds a
// capped commit list needs, and returns their SHAs oldest first.
func importCommits(t *testing.T, repo, branch, parent string, commits []fixtureCommit) []string {
	t.Helper()
	var stream strings.Builder
	for i, commit := range commits {
		fmt.Fprintf(&stream, "commit refs/heads/%s\ncommitter Tallpine Tests <tallpine@example.com> %d +0000\ndata %d\n%s\n", branch, 1700000000+i, len(commit.message), commit.message)
		if i == 0 {
			fmt.Fprintf(&stream, "from %s\n", parent)
		}
		for path, content := range commit.files {
			fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n%s\n", path, len(content), content)
		}
	}
	cmd := exec.Command("git", "-C", repo, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(stream.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	shas := strings.Fields(runGit(t, repo, "rev-list", "--reverse", parent+".."+branch))
	if len(shas) != len(commits) {
		t.Fatalf("imported %d commits, want %d", len(shas), len(commits))
	}
	return shas
}

func linkWorkspaceDir(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("creating symlink: %v", err)
	} else {
		cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
		if out, junctionErr := cmd.CombinedOutput(); junctionErr != nil {
			t.Skipf("creating symlink or junction: symlink: %v; junction: %v\n%s", err, junctionErr, out)
		}
	}
}

func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, f.num))
	f.sessions[rec.ID] = rec
	return rec, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.getSessionErr != nil {
		return domain.SessionRecord{}, false, f.getSessionErr
	}
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) GetSessionByClientRequestID(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	for _, rec := range f.sessions {
		if id != "" && rec.ClientRequestID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakeStore) ListSessions(_ context.Context, p domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if r.ProjectID == p {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	f.allListCalls++
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) ListLiveSessions(_ context.Context) ([]domain.SessionRecord, error) {
	f.liveListCalls++
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		if !r.IsTerminated {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) CountArchivedSessions(_ context.Context) ([]domain.ArchivedSessionCount, error) {
	type key struct {
		project domain.ProjectID
		kind    domain.SessionKind
	}
	counts := map[key]int{}
	for _, r := range f.sessions {
		if r.IsTerminated && !r.IsTaskPreparation {
			counts[key{r.ProjectID, r.Kind}]++
		}
	}
	out := make([]domain.ArchivedSessionCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, domain.ArchivedSessionCount{ProjectID: k.project, Kind: k.kind, Count: n})
	}
	return out, nil
}

func (f *fakeStore) RenameSession(_ context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) RenameSessionIfDisplayName(_ context.Context, id domain.SessionID, currentDisplayName, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok || r.DisplayName != currentDisplayName {
		return false, nil
	}
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionPinned(_ context.Context, id domain.SessionID, isPinned bool, pinnedAt *time.Time, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.IsPinned = isPinned
	r.PinnedAt = pinnedAt
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionPreviewURL(_ context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.Metadata.PreviewURL = previewURL
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionTerminateOnPRMerge(_ context.Context, id domain.SessionID, terminate bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.TerminateOnPRMerge = terminate
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionAutoInjectReview(_ context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.AutoInjectReview = autoInject
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionAutoInjectCI(_ context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.AutoInjectCI = autoInject
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionReviewerConfig(_ context.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.ReviewerHarness = harness
	r.ReviewerConfig = config
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionAutoReview(_ context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.AutoReviewEnabled = enabled
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	pr, ok := f.pr[id]
	return pr, ok, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	if prs, ok := f.prs[id]; ok {
		return append([]domain.PullRequest(nil), prs...), nil
	}
	pr, ok := f.pr[id]
	if !ok {
		return nil, nil
	}
	return []domain.PullRequest{{URL: pr.URL, SessionID: id, Number: pr.Number, Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, UpdatedAt: pr.UpdatedAt, TargetBranch: pr.TargetBranch}}, nil
}

func (f *fakeStore) ListReportedPRURLs(_ context.Context, id domain.SessionID) ([]string, error) {
	return append([]string(nil), f.reportedPRURLs[id]...), nil
}

func (f *fakeStore) ListPRFactsForSession(_ context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	f.listPRFactsCalls++
	if prs, ok := f.prFacts[id]; ok {
		return append([]domain.PRFacts(nil), prs...), nil
	}
	pr, ok := f.pr[id]
	if !ok {
		return nil, nil
	}
	return []domain.PRFacts{pr}, nil
}

func (f *fakeStore) ListPRFactsForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error) {
	f.listPRFactsCalls++
	out := make(map[domain.SessionID][]domain.PRFacts, len(ids))
	for _, id := range ids {
		if prs, ok := f.prFacts[id]; ok {
			out[id] = append([]domain.PRFacts(nil), prs...)
			continue
		}
		pr, ok := f.pr[id]
		if ok {
			out[id] = []domain.PRFacts{pr}
			continue
		}
		out[id] = []domain.PRFacts{}
	}
	return out, nil
}

func (f *fakeStore) ListCurrentHeadReviewRunsForSession(_ context.Context, id domain.SessionID) ([]domain.CurrentHeadReviewRun, error) {
	f.listReviewRunsCalls++
	return f.reviewRuns[id], nil
}

func (f *fakeStore) ListCurrentHeadReviewRunsForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.CurrentHeadReviewRun, error) {
	f.listReviewRunsCalls++
	out := make(map[domain.SessionID][]domain.CurrentHeadReviewRun, len(ids))
	for _, id := range ids {
		out[id] = append([]domain.CurrentHeadReviewRun(nil), f.reviewRuns[id]...)
	}
	return out, nil
}

func (f *fakeStore) ListInterruptedSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID]bool, error) {
	out := make(map[domain.SessionID]bool, len(ids))
	for _, id := range ids {
		if f.interrupted[id] {
			out[id] = true
		}
	}
	return out, nil
}

func (f *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	return append([]domain.PullRequestCheck(nil), f.checks[prURL]...), nil
}

func (f *fakeStore) ListPRReviews(_ context.Context, prURL string) ([]domain.PullRequestReview, error) {
	if f.reviewsErr != nil {
		return nil, f.reviewsErr
	}
	return append([]domain.PullRequestReview(nil), f.reviews[prURL]...), nil
}

func (f *fakeStore) ListPRReviewThreads(_ context.Context, prURL string) ([]domain.PullRequestReviewThread, error) {
	return append([]domain.PullRequestReviewThread(nil), f.threads[prURL]...), nil
}

func (f *fakeStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	if f.commentsErr != nil {
		return nil, f.commentsErr
	}
	return append([]domain.PullRequestComment(nil), f.comments[prURL]...), nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f.projects[id]
	return p, ok, nil
}

func (f *fakeStore) ListSessionWorktrees(_ context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error) {
	return append([]domain.SessionWorktreeRecord(nil), f.worktrees[id]...), nil
}

// pngBytes is a byte sequence that reads as binary (it carries a NUL) and
// carries a PNG signature, standing in for a real image in workspace tests.
func pngBytes(tail string) string {
	return "\x89PNG\r\n\x1a\n\x00" + tail
}

// newDivergentForcePushRepo builds a repo shaped like a target-branch
// force-push: the session's stale tracking ref (B) and the PR's
// provider-observed replacement base (X) descend from a common ancestor but
// neither is an ancestor of the other, while the session branch HEAD has
// incorporated both — B through its own fork point, X through a later merge.
func newDivergentForcePushRepo(t *testing.T) (repo, head, staleRef, replacementBase string) {
	t.Helper()
	repo = newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")

	// B: main's tip before the force-push. The session's tracking ref is
	// fetched here and never refreshed.
	writeWorkspaceFile(t, repo, "old-main.go", "package main\n")
	runGit(t, repo, "add", "old-main.go")
	runGit(t, repo, "commit", "-m", "old main line")
	b := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", b)

	// The session forks from B and makes its own change.
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	// X: main is force-pushed to a replacement line rooted at the same
	// initial commit as B, not descended from B.
	runGit(t, repo, "switch", "main")
	initial := strings.TrimSpace(runGit(t, repo, "rev-list", "--max-parents=0", "HEAD"))
	runGit(t, repo, "reset", "--hard", initial)
	writeWorkspaceFile(t, repo, "new-main.go", "package main\n")
	runGit(t, repo, "add", "new-main.go")
	runGit(t, repo, "commit", "-m", "replacement main line (force-pushed)")
	x := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	// The session merges the force-pushed main in, so HEAD now retains B
	// (through its own fork point) and X (through this merge) without B and
	// X being ancestors of each other.
	runGit(t, repo, "switch", "tp/work")
	runGit(t, repo, "merge", "main", "-m", "merge force-pushed main")
	h := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	return repo, h, b, x
}

func workspaceSectionPaths(files []WorkspaceFileSummary) map[string]bool {
	out := map[string]bool{}
	for _, file := range files {
		out[file.Path] = true
	}
	return out
}

// fakeCommander records Kill/Spawn calls so a test can assert the
// clean-orchestrator ordering without wiring a real session engine.
type fakeCommander struct {
	killed            []domain.SessionID
	retired           []domain.SessionID
	exited            []domain.SessionID
	resumed           []domain.SessionID
	ready             []domain.SessionID
	sent              []domain.SessionID
	sentMessages      []string
	cleanupProjects   []domain.ProjectID
	killErr           error
	retireErr         error
	retireFunc        func(context.Context, domain.SessionID) error
	sendErr           error
	sendFunc          func(domain.SessionID, string) error
	cleanupErr        error
	spawnErr          error
	spawnWaitsForCtx  bool
	spawnHook         func(context.Context)
	spawnRecord       domain.SessionRecord
	requestKillResult *sessionmanager.KillResult
	spawnFunc         func(ports.SpawnConfig) domain.SessionRecord
	spawnCalls        int
	spawned           bool
	spawnedCfg        ports.SpawnConfig
	spawnCtx          context.Context
	killsAtSpawn      int
	restoreErr        error
	restoreResult     sessionmanager.RestoreResult
	readyErr          error
	backgroundResult  string
	backgroundErr     error
	backgroundFunc    func(backgroundTaskCall) (string, error)
	backgroundCalls   []backgroundTaskCall
	killFunc          func(domain.SessionID)
	killMu            sync.Mutex
}

type backgroundTaskCall struct {
	ctx          context.Context
	id           domain.SessionID
	systemPrompt string
	prompt       string
}

func (f *fakeCommander) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	f.spawnCtx = ctx
	if f.spawnHook != nil {
		f.spawnHook(ctx)
	}
	if f.spawnWaitsForCtx {
		<-ctx.Done()
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", ctx.Err())
	}
	if f.spawnErr != nil {
		return domain.SessionRecord{}, 0, 0, f.spawnErr
	}
	f.spawned = true
	f.spawnCalls++
	f.spawnedCfg = cfg
	f.killsAtSpawn = len(f.retired)
	if f.spawnFunc != nil {
		return f.spawnFunc(cfg), len(cfg.Prompt), 0, nil
	}
	if f.spawnRecord.ID != "" {
		return f.spawnRecord, len(cfg.Prompt), 0, nil
	}
	return domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness}, len(cfg.Prompt), 0, nil
}
func (*fakeCommander) PrepareTaskWorkspace(context.Context, domain.ProjectRecord) (domain.TaskPreparationToken, error) {
	return "", nil
}
func (*fakeCommander) CancelTaskPreparation(context.Context, domain.TaskPreparationToken) error {
	return nil
}
func (f *fakeCommander) RestoreWithMode(context.Context, domain.SessionID) (sessionmanager.RestoreResult, error) {
	if f.restoreErr != nil {
		return sessionmanager.RestoreResult{}, f.restoreErr
	}
	return f.restoreResult, nil
}
func (f *fakeCommander) ResumeAgentWithMode(_ context.Context, id domain.SessionID) (sessionmanager.RestoreResult, error) {
	f.resumed = append(f.resumed, id)
	if f.restoreErr != nil {
		return sessionmanager.RestoreResult{}, f.restoreErr
	}
	return f.restoreResult, nil
}
func (f *fakeCommander) ExitAgent(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	f.exited = append(f.exited, id)
	if f.restoreErr != nil {
		return domain.SessionRecord{}, f.restoreErr
	}
	rec := f.restoreResult.Session
	rec.Activity.State = domain.ActivityExited
	return rec, nil
}
func (f *fakeCommander) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	if f.killErr != nil {
		return false, f.killErr
	}
	f.killMu.Lock()
	f.killed = append(f.killed, id)
	f.killMu.Unlock()
	if f.killFunc != nil {
		f.killFunc(id)
	}
	return true, nil
}
func (f *fakeCommander) RetireForReplacement(ctx context.Context, id domain.SessionID) error {
	if f.retireFunc != nil {
		return f.retireFunc(ctx, id)
	}
	if f.retireErr != nil {
		return f.retireErr
	}
	f.retired = append(f.retired, id)
	return nil
}
func (f *fakeCommander) WaitForMessageDeliveryReady(_ context.Context, id domain.SessionID) error {
	f.ready = append(f.ready, id)
	return f.readyErr
}
func (f *fakeCommander) Send(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	if f.sendFunc != nil {
		return f.sendFunc(id, message)
	}
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, id)
	f.sentMessages = append(f.sentMessages, message)
	return nil
}
func (f *fakeCommander) SendWithOptions(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment, _ ports.MessageDeliveryOptions) error {
	return f.Send(context.Background(), id, message, nil)
}
func (f *fakeCommander) RunBackgroundTask(ctx context.Context, id domain.SessionID, systemPrompt, prompt string) (string, error) {
	call := backgroundTaskCall{
		ctx: ctx, id: id, systemPrompt: systemPrompt, prompt: prompt,
	}
	f.backgroundCalls = append(f.backgroundCalls, call)
	if f.backgroundFunc != nil {
		return f.backgroundFunc(call)
	}
	if f.backgroundErr != nil {
		return "", f.backgroundErr
	}
	if f.backgroundResult != "" {
		return f.backgroundResult, nil
	}
	return "Generated task", nil
}
func (f *fakeCommander) Cleanup(_ context.Context, project domain.ProjectID) (sessionmanager.CleanupResult, error) {
	f.cleanupProjects = append(f.cleanupProjects, project)
	if f.cleanupErr != nil {
		return sessionmanager.CleanupResult{}, f.cleanupErr
	}
	return sessionmanager.CleanupResult{
		Cleaned: []domain.SessionID{"mer-1"},
		Skipped: []sessionmanager.CleanupSkip{{SessionID: "mer-2", Reason: "workspace has uncommitted changes"}},
	}, nil
}
func (f *fakeCommander) RollbackSpawn(context.Context, domain.SessionID) (bool, bool, error) {
	return false, false, nil
}

func (f *fakeCommander) StageAttachments(
	context.Context,
	domain.SessionID,
	[]ports.SpawnAttachment,
) ([]string, error) {
	return nil, nil
}

type fakeTracker struct {
	issue domain.Issue
	err   error
	ids   []domain.TrackerID
}

func (f *fakeTracker) Get(_ context.Context, id domain.TrackerID) (domain.Issue, error) {
	f.ids = append(f.ids, id)
	if f.err != nil {
		return domain.Issue{}, f.err
	}
	return f.issue, nil
}

func (f *fakeTracker) List(context.Context, domain.TrackerRepo, domain.ListFilter) ([]domain.Issue, error) {
	return nil, nil
}

func (f *fakeTracker) Preflight(context.Context) error { return nil }

type fakePRClaimer struct {
	out        errorFreeClaimOutcome
	err        error
	gotPR      domain.PullRequest
	gotMode    ports.ReviewWriteMode
	gotThreads []domain.PullRequestReviewThread
	called     bool
}

type errorFreeClaimOutcome struct {
	ports.ClaimOutcome
}

func (f *fakePRClaimer) ClaimPR(_ context.Context, pr domain.PullRequest, _ []domain.PullRequestCheck, _ []domain.PullRequestReview, threads []domain.PullRequestReviewThread, _ []domain.PullRequestComment, mode ports.ReviewWriteMode, _ bool) (ports.ClaimOutcome, error) {
	f.gotPR = pr
	f.gotMode = mode
	f.gotThreads = threads
	f.called = true
	return f.out.ClaimOutcome, f.err
}

type fakeSCM struct {
	obs       ports.SCMObservation
	review    ports.SCMReviewObservation
	fetchErr  error
	reviewErr error
}

func (f fakeSCM) ParseRepository(remote string) (ports.SCMRepo, bool) {
	host, owner, repo, err := repoFromURL(remote)
	if err != nil {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: domain.RepositoryProvider(host), Host: host, Owner: owner, Name: repo, Repo: owner + "/" + repo}, true
}

func (f fakeSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	out := make([]ports.SCMObservation, len(refs))
	if !f.obs.Fetched && f.obs.PR.URL == "" && f.obs.PR.Number == 0 {
		for i := range out {
			out[i].Error = ports.ErrSCMNotFound
		}
		return out, nil
	}
	for i := range out {
		out[i] = f.obs
	}
	return out, nil
}

func (f fakeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return f.review, f.reviewErr
}

type fakeOutputTypeReconciler struct {
	reconciled []domain.SessionID
	err        error
}

func (f *fakeOutputTypeReconciler) ReconcileSessionOutputType(_ context.Context, id domain.SessionID) error {
	f.reconciled = append(f.reconciled, id)
	return f.err
}

func intPtrForTest(v int) *int { return &v }

type multiPRFakeStore struct {
	*fakeStore
	prs []domain.PullRequest
}

func (f *multiPRFakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs, nil
}

func containsString(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}

func failingCheckNames(checks []PRFailingCheck) []string {
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		out = append(out, check.Name)
	}
	return out
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}

func (f *fakeStore) GetSessionCleanupFacts(_ context.Context, id domain.SessionID) (domain.SessionCleanupRecord, bool, error) {
	f.getCleanupFactsCalls++
	rec, ok := f.cleanupFacts[id]
	return rec, ok, nil
}

func (f *fakeStore) ListSessionCleanupFactsForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error) {
	f.listCleanupFactsCalls++
	f.cleanupFactsRequested = append(f.cleanupFactsRequested, ids...)
	out := make(map[domain.SessionID]domain.SessionCleanupRecord, len(ids))
	for _, id := range ids {
		if rec, ok := f.cleanupFacts[id]; ok {
			out[id] = rec
		}
	}
	return out, nil
}

func (f *fakeCommander) RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error) {
	if f.requestKillResult != nil {
		return *f.requestKillResult, nil
	}
	freed, err := f.Kill(ctx, id)
	return sessionmanager.KillResult{Freed: freed}, err
}

type fakeArtifactDeleter struct {
	deleted []domain.SessionID
	err     error
}

func (f *fakeArtifactDeleter) DeleteSession(_ context.Context, id domain.SessionID) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

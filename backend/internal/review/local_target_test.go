package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// localRepo is a real git repo on branch feat with one commit beyond main.
type localRepo struct {
	t   *testing.T
	dir string
}

func (r localRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r localRepo) commit(name, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "add "+name)
}

func newLocalRepo(t *testing.T, commits int) localRepo {
	r := localRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.commit("a.txt", "a\n")
	r.git("checkout", "-q", "-b", "feat")
	for i := 0; i < commits; i++ {
		r.commit("f"+string(rune('0'+i))+".txt", "feature\n")
	}
	return r
}

func (r localRepo) worker() domain.SessionRecord {
	w := liveWorker()
	w.DisplayName = "Add feature"
	w.Metadata.WorkspacePath = r.dir
	w.Metadata.Branch = "feat"
	w.Metadata.DiffBaseRef = "main"
	return w
}

func TestLocalTargetUnpushedBranch(t *testing.T) {
	r := newLocalRepo(t, 1)
	pr, ok, err := LocalTarget(context.Background(), r.worker())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if pr.URL != "local:feat" || !IsLocalTarget(pr.URL) || pr.URL != LocalTargetURL("feat") {
		t.Fatalf("url %q", pr.URL)
	}
	if pr.HeadSHA != r.git("rev-parse", "feat") || pr.BaseSHA != r.git("rev-parse", "main") {
		t.Fatalf("head %q base %q", pr.HeadSHA, pr.BaseSHA)
	}
}

func TestLocalTargetNothingToReview(t *testing.T) {
	r := newLocalRepo(t, 0)
	_, ok, err := LocalTarget(context.Background(), r.worker())
	if ok || !errors.Is(err, ErrNoLocalCommits) {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	prs, err := WithLocalTarget(context.Background(), r.worker(), nil)
	if err != nil || len(prs) != 0 {
		t.Fatalf("WithLocalTarget prs=%v err=%v", prs, err)
	}
	store := &fakeStore{}
	eng := newEngineForTest(store, fakeSessions{rec: r.worker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{handle: "h"})
	_, err = trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err == nil || !strings.Contains(err.Error(), "nothing to review") || !errors.Is(err, ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestLocalTargetPushedBranchKeepsNoPRError(t *testing.T) {
	r := newLocalRepo(t, 1)
	// A remote-tracking ref for the branch means it was pushed.
	r.git("update-ref", "refs/remotes/origin/feat", "feat")
	_, ok, err := LocalTarget(context.Background(), r.worker())
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: r.worker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{handle: "h"})
	_, err = trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{})
	if err == nil || !strings.Contains(err.Error(), "not tracking a PR") {
		t.Fatalf("err %v", err)
	}
}

func TestTriggerRecordsRunUnderLocalKey(t *testing.T) {
	r := newLocalRepo(t, 1)
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "h"}
	eng := newEngineForTest(store, fakeSessions{rec: r.worker(), ok: true}, fakePRs{}, fakeProjects{}, launcher)
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatal(err)
	}
	if len(store.runs) != 1 || store.runs[0].PRURL != "local:feat" || store.runs[0].TargetSHA != r.git("rev-parse", "feat") {
		t.Fatalf("runs %+v", store.runs)
	}
	if !launcher.spawned {
		t.Fatal("reviewer not launched")
	}
	list, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Reviews) != 1 || list.Reviews[0].PRURL != "local:feat" || list.Reviews[0].Status != ReviewStateRunning {
		t.Fatalf("reviews %+v", list.Reviews)
	}
}

func TestTrackedPRIsNotLocal(t *testing.T) {
	r := newLocalRepo(t, 1)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", Number: 1, HeadSHA: "abc"}
	prs, err := WithLocalTarget(context.Background(), r.worker(), []domain.PullRequest{pr})
	if err != nil || len(prs) != 1 || prs[0].URL != pr.URL {
		t.Fatalf("prs=%v err=%v", prs, err)
	}
	prompt, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: pr.URL, TargetSHA: "abc", RunID: "r1"})
	if !strings.Contains(prompt, "gh api") || !strings.Contains(system, "gh pr diff") {
		t.Fatal("PR prompt must keep the GitHub path")
	}
}

var ghCommand = regexp.MustCompile(`\bgh `)

func TestLocalReviewPromptHasNoGHAndNamesRange(t *testing.T) {
	spec := LaunchSpec{WorkerID: "mer-1", PRURL: "local:feat", TargetSHA: "head1", RunID: "r1",
		ReviewQueue: []ports.ReviewTask{{RunID: "r1", PRURL: "local:feat", TargetSHA: "head1", BaseRef: "base1"}}}
	prompt, system := reviewTexts(spec)
	for _, text := range []string{prompt, system} {
		if ghCommand.MatchString(text) {
			t.Fatalf("local prompt mentions gh: %q", text)
		}
	}
	if !strings.Contains(prompt, "base1...head1") || !strings.Contains(prompt, "path:line - finding") {
		t.Fatalf("prompt %q", prompt)
	}
}

func TestIdenticalPatchRebaseCarriesLocalApproval(t *testing.T) {
	r := newLocalRepo(t, 1)
	old := r.git("rev-parse", "feat")
	prior := domain.ReviewRun{ID: "r-old", ReviewID: "rv1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
		PRURL: "local:feat", TargetSHA: old, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: time.Now().Add(-time.Hour)}
	// main moves, the branch is rebased: same patch, new head.
	r.git("checkout", "-q", "main")
	r.commit("b.txt", "b\n")
	r.git("checkout", "-q", "feat")
	r.git("rebase", "-q", "main")
	newHead := r.git("rev-parse", "feat")
	if newHead == old {
		t.Fatal("rebase did not move the head")
	}
	store := &fakeStore{runs: []domain.ReviewRun{prior}}
	launcher := &fakeLauncher{handle: "h"}
	eng := newEngineForTest(store, fakeSessions{rec: r.worker(), ok: true}, fakePRs{}, fakeProjects{cfg: domain.ProjectConfig{ApproveIdenticalPatch: true}}, launcher)
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatal(err)
	}
	var carried bool
	for _, run := range store.runs {
		if run.TargetSHA == newHead && run.Verdict == domain.VerdictApproved && run.PRURL == "local:feat" {
			carried = true
		}
	}
	if !carried || launcher.spawned {
		t.Fatalf("carried=%v spawned=%v runs=%+v", carried, launcher.spawned, store.runs)
	}
}

func TestListAndCancelSurviveUnresolvableLocalBase(t *testing.T) {
	r := newLocalRepo(t, 1)
	w := r.worker()
	w.Metadata.DiffBaseRef = "no-such-ref"
	store := &fakeStore{}
	eng := newEngineForTest(store, fakeSessions{rec: w, ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{handle: "h"})
	if _, err := eng.List(context.Background(), "mer-1"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := eng.Cancel(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err == nil {
		t.Fatal("trigger must keep the hard error for an unresolvable base")
	}
}

func TestIdleReviewerSystemPromptCoversLocalTasks(t *testing.T) {
	l := &agentLauncher{dataDir: t.TempDir()}
	inv, err := l.prepareIdleInvocation(context.Background(), LaunchSpec{WorkerID: "mer-1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(inv.SystemPromptFile)
	if err != nil || !strings.Contains(string(b), "local:<branch>") {
		t.Fatalf("idle system prompt lacks the local flow note: %v", err)
	}
}

// A pane that started on a local branch and later gets a PR task must be told
// the PR task's own GitHub steps apply (#961 review).
func TestLocalReviewerSystemPromptLetsLaterPRTasksUseTheirGitHubSteps(t *testing.T) {
	_, system := reviewTexts(LaunchSpec{WorkerID: "mer-1", PRURL: "local:feat", TargetSHA: "head1", RunID: "r1"})
	if !strings.Contains(system, "names a pull request URL") || !strings.Contains(system, "its GitHub CLI and inline-comment steps") {
		t.Fatalf("local system prompt lacks the PR-task note: %q", system)
	}
}

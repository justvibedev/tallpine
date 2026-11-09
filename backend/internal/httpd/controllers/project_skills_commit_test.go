package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/ports"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// skillsCommitRouter serves a project whose checkout is a clone of a bare
// origin holding AGENTS.md, resolved with the production default-branch
// resolver.
func skillsCommitRouter(t *testing.T, defaultBranch string) (r chi.Router, checkout, origin string) {
	t.Helper()
	return skillsCommitRouterWith(t, defaultBranch, nil)
}

func skillsCommitRouterWith(t *testing.T, defaultBranch string, credentials ports.ProjectGitCredentials) (r chi.Router, checkout, origin string) {
	t.Helper()
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	gitT(t, base, "init", "-q", "--bare", "-b", "main", origin)
	checkout = filepath.Join(base, "checkout")
	gitT(t, base, "clone", "-q", origin, checkout)
	gitT(t, checkout, "checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(checkout, "AGENTS.md"), []byte("rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, checkout, "add", "AGENTS.md")
	gitT(t, checkout, "commit", "-q", "-m", "init")
	gitT(t, checkout, "push", "-q", "-u", "origin", "main")
	home := t.TempDir()
	cfg := &domain.ProjectConfig{DefaultBranch: defaultBranch, Env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""}}
	r = chi.NewRouter()
	(&controllers.ProjectSkillsController{
		Projects:       skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: checkout, Config: cfg}}},
		DefaultBranch:  gitworktree.NewDefaultBranchResolver(""),
		GitCredentials: credentials,
	}).Register(r)
	return r, checkout, origin
}

func TestProjectSkillsAPI_CommitPushesCreatedFilesToSessionBase(t *testing.T) {
	r, checkout, origin := skillsCommitRouter(t, "")
	get := func() controllers.ProjectSkillsResponse {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
		var got controllers.ProjectSkillsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if ct := get().CommitTarget; ct == nil || ct.Remote != "origin" || ct.Branch != "main" || !ct.NeedsPush {
		t.Fatalf("commitTarget = %+v, want origin/main needing a push", ct)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/offers/claude-md/apply", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	if err := os.WriteFile(filepath.Join(checkout, "unrelated.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A path outside the pending list is refused before anything is written.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/commit", strings.NewReader(`{"paths":["CLAUDE.md","unrelated.txt"]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unlisted path: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/commit", strings.NewReader(`{"paths":["CLAUDE.md"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body)
	}
	var res controllers.CommitProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || !res.NeedsPush || res.Branch != "main" || res.Remote != "origin" {
		t.Fatalf("commit response = %+v", res)
	}
	if got := gitT(t, origin, "rev-parse", "main"); got != res.SHA {
		t.Fatalf("origin main = %s, want %s", got, res.SHA)
	}
	if got := gitT(t, origin, "show", "main:CLAUDE.md"); got != "@AGENTS.md" {
		t.Fatalf("CLAUDE.md on origin = %q", got)
	}
	if got := gitT(t, checkout, "diff-tree", "--no-commit-id", "--name-only", "-r", res.SHA); got != "CLAUDE.md" {
		t.Fatalf("commit touched %q", got)
	}
	if after := get(); len(after.PendingCommit) != 0 {
		t.Fatalf("still pending: %v", after.PendingCommit)
	}
}

func TestProjectSkillsAPI_CommitRefreshesStaleTrackingRefBeforePushing(t *testing.T) {
	// An explicit default branch skips the auto-detect path, which would fetch by itself.
	r, checkout, origin := skillsCommitRouter(t, "main")
	// Someone else lands a commit on origin/main; the checkout's tracking ref is now stale.
	other := filepath.Join(filepath.Dir(checkout), "other")
	gitT(t, filepath.Dir(checkout), "clone", "-q", origin, other)
	if err := os.WriteFile(filepath.Join(other, "later.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, other, "add", "later.txt")
	gitT(t, other, "commit", "-q", "-m", "later")
	gitT(t, other, "push", "-q", "origin", "HEAD:main")
	landed := gitT(t, origin, "rev-parse", "main")
	if stale := gitT(t, checkout, "rev-parse", "origin/main"); stale == landed {
		t.Fatal("setup: tracking ref is not stale")
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/offers/claude-md/apply", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/commit", strings.NewReader(`{"paths":["CLAUDE.md"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body)
	}
	var res controllers.CommitProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.PushError != "" {
		t.Fatalf("stale tracking ref made the push fail: %+v", res)
	}
	if got := gitT(t, origin, "rev-parse", "main^"); got != landed {
		t.Fatalf("commit not built on the current remote head: parent %s, want %s", got, landed)
	}
	if got := gitT(t, origin, "show", "main:later.txt"); got != "x" {
		t.Fatal("the other commit was lost")
	}
}

func TestProjectSkillsAPI_RemovingACommittedLinkReportsItIsLocalOnly(t *testing.T) {
	r, checkout, _ := skillsCommitRouter(t, "main")
	src := filepath.Join(checkout, ".agents", "skills", "qa")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: qa\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(checkout, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/qa", filepath.Join(checkout, ".claude", "skills", "qa")); err != nil {
		t.Fatal(err)
	}
	remove := func(path string) controllers.RemoveProjectSkillResponse {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/remove", strings.NewReader(`{"path":"`+path+`"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("remove: %d %s", rec.Code, rec.Body)
		}
		var out controllers.RemoveProjectSkillResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Uncommitted link: removing it is complete.
	if got := remove(".claude/skills/qa"); got.LocalOnly {
		t.Fatalf("uncommitted link reported local-only: %+v", got)
	}
	// The same link committed and pushed to the session base: sessions keep it.
	if err := os.Symlink("../../.agents/skills/qa", filepath.Join(checkout, ".claude", "skills", "qa")); err != nil {
		t.Fatal(err)
	}
	gitT(t, checkout, "add", ".agents", ".claude")
	gitT(t, checkout, "commit", "-q", "-m", "skills")
	gitT(t, checkout, "push", "-q", "origin", "main")
	if got := remove(".claude/skills/qa"); !got.LocalOnly || got.Branch != "main" {
		t.Fatalf("committed link not reported local-only: %+v", got)
	}
	if _, err := os.Lstat(filepath.Join(checkout, ".claude", "skills", "qa")); !os.IsNotExist(err) {
		t.Fatal("link not removed")
	}
}

func TestProjectSkillsAPI_DefaultBranchCheckSuggestsAheadAndNewerBranch(t *testing.T) {
	t.Setenv("GIT_AUTHOR_DATE", "2022-12-14T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2022-12-14T00:00:00Z")
	r, checkout, _ := skillsCommitRouter(t, "master")
	// The seed repo only has main; make master the 2022 default and main newer.
	gitT(t, checkout, "branch", "master", "main~0")
	gitT(t, checkout, "update-ref", "refs/remotes/origin/master", "main")
	t.Setenv("GIT_AUTHOR_DATE", "2026-10-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-10-01T00:00:00Z")
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(checkout, "n"+string(rune('a'+i))), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, checkout, "add", "-A")
		gitT(t, checkout, "commit", "-q", "-m", "new")
	}
	// Sessions would start from origin/main after a switch, so it must be there.
	gitT(t, checkout, "push", "-q", "origin", "main")
	get := func() controllers.ProjectDefaultBranchCheckResponse {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/default-branch-check", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var out controllers.ProjectDefaultBranchCheckResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := get()
	if got.DefaultBranch != "master" || got.Suggestion == nil || got.Suggestion.Branch != "main" || got.Suggestion.Ahead != 3 || !got.Suggestion.CheckedOut {
		t.Fatalf("check = %+v %+v", got, got.Suggestion)
	}
}

type fakeGitCredentials struct {
	env      []string
	projects []domain.ProjectID
}

func (f *fakeGitCredentials) ProjectGitEnv(_ context.Context, project domain.ProjectID) []string {
	f.projects = append(f.projects, project)
	return f.env
}

// Committing a skill refreshes and pushes as the project's account: the fetch
// and the push both see GIT_TRACE from the credential env.
func TestProjectSkillsAPI_CommitFetchesAndPushesAsProjectAccount(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	creds := &fakeGitCredentials{env: []string{"GIT_TRACE=" + trace}}
	r, _, _ := skillsCommitRouterWith(t, "main", creds)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/offers/claude-md/apply", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/commit", strings.NewReader(`{"paths":["CLAUDE.md"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body)
	}
	b, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fetch", "push"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("git %s ran without the project's account; trace: %s", want, b)
		}
	}
	if len(creds.projects) == 0 || creds.projects[0] != "p" {
		t.Fatalf("credentials asked for %v, want p", creds.projects)
	}
}

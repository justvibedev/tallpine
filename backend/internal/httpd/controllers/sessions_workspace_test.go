package controllers_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func TestSessionsAPI_ListWorkspaceTree(t *testing.T) {
	t.Run("lists the requested directory", func(t *testing.T) {
		svc := newFakeSessionService()
		svc.workspaceTree = sessionsvc.WorkspaceTree{
			SessionID: "tp-1",
			Path:      "src",
			Entries: []sessionsvc.WorkspaceTreeEntry{{
				Name: "main.go", Path: "src/main.go", Type: sessionsvc.WorkspaceTreeFile,
			}},
		}
		srv := newSessionTestServer(t, svc)
		body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/tree?path=src", "")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", status, body)
		}
		if svc.workspaceTreePath != "src" {
			t.Fatalf("service path = %q, want src", svc.workspaceTreePath)
		}
		var got controllers.ListWorkspaceTreeResponse
		mustJSON(t, body, &got)
		if got.Path != "src" || len(got.Entries) != 1 || got.Entries[0].Path != "src/main.go" {
			t.Fatalf("response = %+v", got)
		}
	})

	t.Run("returns not found for an unknown session", func(t *testing.T) {
		srv := newSessionTestServer(t, newFakeSessionService())
		body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/missing/workspace/tree", "")
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", status, body)
		}
	})

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid path", err: apierr.Invalid("INVALID_WORKSPACE_PATH", "invalid workspace path", nil), want: http.StatusBadRequest},
		{name: "service failure", err: errors.New("tree unavailable"), want: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			svc.workspaceErr = tc.err
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/tree?path=src", "")
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.want, body)
			}
		})
	}
}

func TestSessionsAPI_ListWorkspaceFiles(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceFiles = sessionsvc.WorkspaceFiles{
		SessionID:      "tp-1",
		CompareBaseSHA: "base-sha",
		CompareBaseRef: "main",
		CompareMode:    sessionsvc.WorkspaceCompareBase,
		Files: []sessionsvc.WorkspaceFileSummary{
			{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 2, Deletions: 1, Size: 48, Editable: true},
			{Path: "notes.txt", PreviousPath: "old-notes.txt", Status: sessionsvc.WorkspaceFileRenamed, Additions: 1, Size: 11},
		},
		CommitsTruncated: true,
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/files", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace files = %d, want 200; body=%s", status, body)
	}
	var got struct {
		SessionID      string `json:"sessionId"`
		CompareBaseSHA string `json:"compareBaseSha"`
		CompareBaseRef string `json:"compareBaseRef"`
		CompareMode    string `json:"compareMode"`
		Files          []struct {
			Path         string `json:"path"`
			PreviousPath string `json:"previousPath"`
			Status       string `json:"status"`
			Additions    int    `json:"additions"`
			Deletions    int    `json:"deletions"`
			Size         int64  `json:"size"`
			Editable     bool   `json:"editable"`
		} `json:"files"`
		CommitsTruncated bool `json:"commitsTruncated"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "tp-1" || len(got.Files) != 2 || !got.CommitsTruncated {
		t.Fatalf("response = %#v", got)
	}
	if got.CompareMode != "base" || got.CompareBaseSHA != "base-sha" || got.CompareBaseRef != "main" {
		t.Fatalf("compare metadata = mode:%q sha:%q ref:%q", got.CompareMode, got.CompareBaseSHA, got.CompareBaseRef)
	}
	if got.Files[0].Path != "README.md" || got.Files[0].Status != "modified" || got.Files[0].Additions != 2 || got.Files[0].Deletions != 1 {
		t.Fatalf("first file = %#v", got.Files[0])
	}
	if !got.Files[0].Editable {
		t.Fatal("first file editable = false, want true")
	}
	if got.Files[1].Path != "notes.txt" || got.Files[1].PreviousPath != "old-notes.txt" || got.Files[1].Status != "renamed" {
		t.Fatalf("second file = %#v", got.Files[1])
	}
}

func TestSessionsAPI_GetWorkspaceManifestOmitsUnchangedInventory(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceFiles = sessionsvc.WorkspaceFiles{
		SessionID:        "tp-1",
		WorkspaceVersion: "version-1",
		CompareBaseSHA:   "base-sha",
		CompareBaseRef:   "main",
		CompareMode:      sessionsvc.WorkspaceCompareBase,
		Files: []sessionsvc.WorkspaceFileSummary{
			{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 2, Deletions: 1},
			{Path: "src/app.go", Status: sessionsvc.WorkspaceFileUnmodified},
		},
		Sections: sessionsvc.WorkspaceFileSections{
			Uncommitted: []sessionsvc.WorkspaceFileSummary{{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 2, Deletions: 1}},
			Unstaged:    []sessionsvc.WorkspaceFileSummary{{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 2, Deletions: 1}},
		},
		Summary: sessionsvc.WorkspaceSummary{Files: 1, Additions: 2, Deletions: 1},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/manifest", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace manifest = %d, want 200; body=%s", status, body)
	}
	var got controllers.WorkspaceManifestResponse
	mustJSON(t, body, &got)
	if got.WorkspaceVersion != "version-1" || got.CompareBaseSHA != "base-sha" || got.Summary.Files != 1 {
		t.Fatalf("manifest metadata = %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "README.md" {
		t.Fatalf("manifest files = %+v, want only changed README.md", got.Files)
	}
	if len(got.Sections.Uncommitted) != 1 || got.Sections.Uncommitted[0].Path != "README.md" {
		t.Fatalf("manifest uncommitted section = %+v, want README.md", got.Sections.Uncommitted)
	}
	// Empty sections still serialize as arrays, so a missing uncommitted key
	// can only mean an older daemon.
	if !strings.Contains(string(body), `"staged":[]`) {
		t.Fatalf("manifest body = %s, want empty sections as arrays", body)
	}
}

func TestSessionsAPI_GetWorkspaceHistoryForBranchView(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceViewHistory = &sessionsvc.WorkspaceHistory{
		SessionID: "tp-1",
		View: sessionsvc.WorkspaceViewInfo{
			Kind: sessionsvc.WorkspaceViewBranch, Name: "feature/login", TipSHA: "tip-sha", BaseSHA: "base-sha", BaseRef: "origin/main",
			RangeMode: sessionsvc.WorkspaceRangeSinceBase,
		},
		Commits:      []sessionsvc.CommitSummary{{SHA: "tip-sha", Subject: "add login"}},
		RangeSummary: &sessionsvc.WorkspaceSummary{Files: 2, Additions: 5, Deletions: 1},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/history?branch=feature%2Flogin", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET branch history = %d, want 200; body=%s", status, body)
	}
	if svc.workspaceHistoryView != (sessionsvc.WorkspaceView{Branch: "feature/login"}) {
		t.Fatalf("view = %+v, want branch feature/login", svc.workspaceHistoryView)
	}
	var got controllers.WorkspaceHistoryResponse
	mustJSON(t, body, &got)
	want := controllers.WorkspaceViewResponse{Kind: "branch", Name: "feature/login", TipSHA: "tip-sha", BaseSHA: "base-sha", BaseRef: "origin/main", RangeMode: "since_base"}
	if got.View != want {
		t.Fatalf("view = %+v, want %+v", got.View, want)
	}
	if got.RangeSummary == nil || *got.RangeSummary != (controllers.WorkspaceSummary{Files: 2, Additions: 5, Deletions: 1}) {
		t.Fatalf("rangeSummary = %+v", got.RangeSummary)
	}
	if len(got.Commits) != 1 || got.Commits[0].SHA != "tip-sha" || got.Ahead != nil || got.Behind != nil {
		t.Fatalf("history = %+v, want one commit and no ahead/behind", got)
	}

	_, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/history?prNumber=42&sourceUrl=https%3A%2F%2Fgithub.com%2Facme%2Frepo%2Fpull%2F42", "")
	if status != http.StatusOK {
		t.Fatalf("GET PR history = %d, want 200", status)
	}
	if svc.workspaceHistoryView != (sessionsvc.WorkspaceView{PRNumber: 42, SourceURL: "https://github.com/acme/repo/pull/42"}) {
		t.Fatalf("view = %+v, want PR 42 with its source URL", svc.workspaceHistoryView)
	}
	_, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/history", "")
	if status != http.StatusOK || svc.workspaceHistoryView != (sessionsvc.WorkspaceView{}) {
		t.Fatalf("GET history = %d with view %+v, want 200 and the checked-out view", status, svc.workspaceHistoryView)
	}

	for _, tc := range []struct{ query, code string }{
		{"branch=", "INVALID_WORKSPACE_BRANCH"},
		{"prNumber=abc", "INVALID_WORKSPACE_VIEW"},
		{"prNumber=0", "INVALID_WORKSPACE_VIEW"},
	} {
		body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/history?"+tc.query, "")
		assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
	}
}

func TestSessionsAPI_ListWorkspaceBranches(t *testing.T) {
	svc := newFakeSessionService()
	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc.workspaceBranches = sessionsvc.WorkspaceBranches{
		Supported: true, DefaultBranch: "main", Truncated: true,
		Branches: []sessionsvc.WorkspaceBranch{
			{Kind: sessionsvc.WorkspaceBranchLocal, Name: "session", TipSHA: "tip-sha", UpdatedAt: updated, CheckedOut: true, PRNumber: 7, PRURL: "https://github.com/acme/repo/pull/7", PRTitle: "Add login"},
			{Kind: sessionsvc.WorkspaceBranchLocal, Name: "main", TipSHA: "main-sha", IsDefault: true},
			{Kind: sessionsvc.WorkspaceBranchPullRequest, Name: "fork-fix", TipSHA: "pr-sha", UpdatedAt: updated, PRNumber: 9, PRURL: "https://github.com/acme/repo/pull/9"},
		},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/branches", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET branches = %d, want 200; body=%s", status, body)
	}
	var got controllers.WorkspaceBranchesResponse
	mustJSON(t, body, &got)
	if got.SessionID != "tp-1" || !got.Supported || got.DefaultBranch != "main" || !got.Truncated || len(got.Branches) != 3 {
		t.Fatalf("branches = %+v", got)
	}
	checkedOut := got.Branches[0]
	if checkedOut.Kind != "local" || !checkedOut.CheckedOut || checkedOut.UpdatedAt == nil || !checkedOut.UpdatedAt.Equal(updated) || checkedOut.PRNumber != 7 || checkedOut.PRURL != "https://github.com/acme/repo/pull/7" || checkedOut.PRTitle != "Add login" {
		t.Fatalf("checked-out row = %+v", checkedOut)
	}
	if main := got.Branches[1]; !main.IsDefault || main.UpdatedAt != nil || main.CheckedOut {
		t.Fatalf("main row = %+v, want the default branch with no time", main)
	}
	if pr := got.Branches[2]; pr.Kind != "pull_request" || pr.Name != "fork-fix" || pr.PRNumber != 9 || pr.TipSHA != "pr-sha" {
		t.Fatalf("pull request row = %+v", pr)
	}

	svc.workspaceBranches = sessionsvc.WorkspaceBranches{}
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/branches", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"branches":[]`) || !strings.Contains(string(body), `"supported":false`) {
		t.Fatalf("GET unsupported branches = %d %s, want supported false and an empty array", status, body)
	}

	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/missing/workspace/branches", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_ListPRFiles(t *testing.T) {
	svc := newFakeSessionService()
	svc.prFiles = sessionsvc.PRFiles{
		SessionID: "tp-1",
		Files:     []sessionsvc.WorkspaceFileSummary{{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 1}},
		Commits: []sessionsvc.CommitSummary{{
			SHA:     "abc123",
			Subject: "docs: update readme",
			Author:  "Ada",
			Files:   []sessionsvc.WorkspaceFileSummary{{Path: "README.md", Status: sessionsvc.WorkspaceFileModified, Additions: 1}},
		}},
		CommitsTruncated: true,
	}
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/42/files", "")
	if status != http.StatusOK {
		t.Fatalf("GET PR files = %d, want 200; body=%s", status, body)
	}
	var got controllers.ListPRFilesResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "tp-1" {
		t.Fatalf("response = %+v", got)
	}
	if len(got.Commits) != 1 || got.Commits[0].SHA != "abc123" || len(got.Commits[0].Files) != 1 || got.Commits[0].Files[0].Path != "README.md" {
		t.Fatalf("commits = %+v, want abc123 changing README.md", got.Commits)
	}
	if !got.CommitsTruncated {
		t.Fatal("commitsTruncated = false, want the service's truncated commit list flagged")
	}
}

func TestSessionsAPI_GetPRFileAtCommit(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/42/file?path=README.md", "")
	if status != http.StatusOK || svc.prFileCommitSHA != "" {
		t.Fatalf("GET PR file = %d, commitSha %q; want 200 and the whole-PR read; body=%s", status, svc.prFileCommitSHA, body)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/42/file?path=README.md&commitSha=abc123", "")
	if status != http.StatusOK {
		t.Fatalf("GET PR commit file = %d, want 200; body=%s", status, body)
	}
	if svc.prFileCommitSHA != "abc123" {
		t.Fatalf("commitSha = %q, want abc123", svc.prFileCommitSHA)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/42/file/revision?path=README.md&side=before&commitSha=abc123", "")
	if status != http.StatusOK {
		t.Fatalf("GET PR commit revision = %d, want 200; body=%s", status, body)
	}
	if svc.prRevisionCommitSHA != "abc123" || svc.workspaceRevisionSide != sessionsvc.WorkspaceBlobBefore {
		t.Fatalf("commit revision args = sha:%q side:%q", svc.prRevisionCommitSHA, svc.workspaceRevisionSide)
	}
}

func TestSessionsAPI_ListPRFilesRejectsInvalidNumber(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	_, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/pr/nope/files", "")
	if status != http.StatusBadRequest {
		t.Fatalf("GET PR files = %d, want 400", status)
	}
}

func TestSessionsAPI_GetWorkspaceFile(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceFile = sessionsvc.WorkspaceFileDetail{
		SessionID:      "tp-1",
		Path:           "README.md",
		PreviousPath:   "README.old.md",
		Status:         sessionsvc.WorkspaceFileModified,
		Additions:      1,
		Deletions:      1,
		Size:           14,
		Content:        "hello\nupdated\n",
		Diff:           "@@ -1 +1 @@\n-hello\n+updated\n",
		CompareBaseSHA: "base-sha",
		CompareBaseRef: "main",
		CompareMode:    sessionsvc.WorkspaceCompareBase,
		Editable:       true,
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/file?path="+url.QueryEscape("README.md"), "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace file = %d, want 200; body=%s", status, body)
	}
	var got struct {
		SessionID      string `json:"sessionId"`
		Path           string `json:"path"`
		PreviousPath   string `json:"previousPath"`
		Content        string `json:"content"`
		Diff           string `json:"diff"`
		CompareBaseSHA string `json:"compareBaseSha"`
		CompareBaseRef string `json:"compareBaseRef"`
		CompareMode    string `json:"compareMode"`
		Editable       bool   `json:"editable"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "tp-1" || got.Path != "README.md" || got.Content == "" || got.Diff == "" {
		t.Fatalf("response = %#v", got)
	}
	if got.PreviousPath != "README.old.md" || got.CompareMode != "base" || got.CompareBaseSHA != "base-sha" || got.CompareBaseRef != "main" {
		t.Fatalf("workspace file metadata = %#v", got)
	}
	if !got.Editable {
		t.Fatal("editable = false, want true")
	}
}

// TestSessionsAPI_GetWorkspaceFileSection verifies the section query param
// reaches the service unchanged, so a staged-section request and an
// unstaged-section request for the same path resolve to different diffs
// instead of colliding on one combined base..worktree diff.
func TestSessionsAPI_GetWorkspaceFileSection(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	for _, section := range []sessionsvc.WorkspaceFileSection{sessionsvc.WorkspaceFileSectionStaged, sessionsvc.WorkspaceFileSectionUnstaged} {
		body, status, _ := doRequest(t, srv, "GET",
			"/api/v1/sessions/tp-1/workspace/file?path="+url.QueryEscape("README.md")+"&section="+string(section), "")
		if status != http.StatusOK {
			t.Fatalf("section=%s: GET workspace file = %d, want 200; body=%s", section, status, body)
		}
		if svc.workspaceFileSection != section {
			t.Fatalf("section=%s: service received section %q", section, svc.workspaceFileSection)
		}
	}
}

func TestSessionsAPI_GetWorkspaceFileAtCommit(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/file?path=README.md&section=committed&commitSha=abc123", "")
	if status != http.StatusOK {
		t.Fatalf("GET workspace commit file = %d, want 200; body=%s", status, body)
	}
	if svc.workspaceFileCommitSHA != "abc123" {
		t.Fatalf("commitSha = %q, want abc123", svc.workspaceFileCommitSHA)
	}
}

func TestSessionsAPI_GetWorkspaceFileRequiresPath(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/file", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WORKSPACE_PATH_REQUIRED")
}

func TestSessionsAPI_UpdateWorkspaceFile(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceFile = sessionsvc.WorkspaceFileDetail{Status: sessionsvc.WorkspaceFileModified, FileFingerprint: "next-fingerprint"}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodPut, "/api/v1/sessions/tp-1/workspace/file", `{"path":"README.md","content":"updated\n","expectedFileFingerprint":"file-1"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("PUT workspace file = %d, want 200; body=%s", status, body)
	}
	if svc.workspaceFileUpdate.Path != "README.md" || svc.workspaceFileUpdate.Content != "updated\n" || svc.workspaceFileUpdate.ExpectedFileFingerprint != "file-1" {
		t.Fatalf("update input = %#v", svc.workspaceFileUpdate)
	}
	var got controllers.WorkspaceFileResponse
	mustJSON(t, body, &got)
	if got.Path != "README.md" || got.Content != "updated\n" || got.FileFingerprint != "next-fingerprint" {
		t.Fatalf("response = %#v", got)
	}
}

func TestSessionsAPI_UpdateWorkspaceFileRequiresPath(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())
	body, status, headers := doRequest(t, srv, http.MethodPut, "/api/v1/sessions/tp-1/workspace/file", `{"content":"updated"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WORKSPACE_PATH_REQUIRED")
}

func TestSessionsAPI_GetWorkspaceDiffs(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceDiffs = sessionsvc.WorkspaceDiffs{
		SessionID: "tp-1", WorkspaceVersion: "version-1",
		Groups: []sessionsvc.WorkspaceDiffGroup{{Repository: "frontend", Patch: "diff --git a/App.tsx b/App.tsx\n", IncludedPaths: []string{"frontend/App.tsx"}}},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/diffs", `{"scope":"staged","paths":["frontend/App.tsx"],"contextLines":5,"ignoreWhitespace":true,"workspaceVersion":"version-1"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("POST workspace diffs = %d body=%s", status, body)
	}
	if svc.workspaceDiffInput.Scope != sessionsvc.WorkspaceDiffStaged || len(svc.workspaceDiffInput.Paths) != 1 || svc.workspaceDiffInput.ContextLines != 5 || !svc.workspaceDiffInput.IgnoreWhitespace {
		t.Fatalf("service input = %#v", svc.workspaceDiffInput)
	}
	var got controllers.WorkspaceDiffsResponse
	mustJSON(t, body, &got)
	if got.WorkspaceVersion != "version-1" || len(got.Groups) != 1 || got.Groups[0].Repository != "frontend" {
		t.Fatalf("response = %#v", got)
	}
}

func TestSessionsAPI_GetWorkspaceDiffsAtCommit(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/diffs", `{"scope":"committed","paths":["README.md"],"contextLines":3,"commitSha":"abc123"}`)
	if status != http.StatusOK {
		t.Fatalf("POST workspace commit diffs = %d body=%s", status, body)
	}
	if svc.workspaceDiffInput.CommitSHA != "abc123" || svc.workspaceDiffInput.Scope != sessionsvc.WorkspaceDiffCommitted {
		t.Fatalf("commit diff input = %#v", svc.workspaceDiffInput)
	}
}

func TestSessionsAPI_GetWorkspaceFileRevision(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceRevision = sessionsvc.WorkspaceFileRevision{SessionID: "tp-1", Path: "README.md", Side: sessionsvc.WorkspaceBlobBefore, Revision: "rev-1", WorkspaceVersion: "version-1", Exists: true, Encoding: "utf-8", Content: "hello\n", Size: 6}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file/revision?path=README.md&scope=unstaged&side=before&workspaceVersion=version-1&expectedRevision=rev-1", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace revision = %d body=%s", status, body)
	}
	if headers.Get("Cache-Control") != "no-store" || headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers = %#v", headers)
	}
	if svc.workspaceRevisionPath != "README.md" || svc.workspaceRevisionScope != sessionsvc.WorkspaceDiffUnstaged || svc.workspaceRevisionSide != sessionsvc.WorkspaceBlobBefore || svc.workspaceRevisionVersion != "version-1" || svc.workspaceExpectedRevision != "rev-1" {
		t.Fatalf("service args = path:%q scope:%q side:%q version:%q expected:%q", svc.workspaceRevisionPath, svc.workspaceRevisionScope, svc.workspaceRevisionSide, svc.workspaceRevisionVersion, svc.workspaceExpectedRevision)
	}
}

func TestSessionsAPI_GetWorkspaceFileRevisionAtCommit(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file/revision?path=README.md&scope=committed&side=after&commitSha=abc123", "")
	if status != http.StatusOK {
		t.Fatalf("GET workspace commit revision = %d body=%s", status, body)
	}
	if got := svc.workspaceSelectionRevision; got.CommitSHA != "abc123" || got.Scope != sessionsvc.WorkspaceDiffCommitted || got.CommitSHAs != nil {
		t.Fatalf("commit revision input = %+v", got)
	}
}

func TestSessionsAPI_RevisionParsesCommaSeparatedCommitShas(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file/revision?path=README.md&scope=committed&side=before&commitShas=aaa,%20bbb,,ccc&branch=feature%2Flogin&expectedTipSha=tip&expectedBaseSha=base&workspaceVersion=v1", "")
	if status != http.StatusOK {
		t.Fatalf("GET workspace selection revision = %d body=%s", status, body)
	}
	if headers.Get("Cache-Control") != "no-store" || headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers = %#v", headers)
	}
	want := sessionsvc.WorkspaceSelectionRevisionInput{
		Path: "README.md", Side: sessionsvc.WorkspaceBlobBefore, Scope: sessionsvc.WorkspaceDiffCommitted, WorkspaceVersion: "v1",
		View:       sessionsvc.WorkspaceView{Branch: "feature/login", ExpectedTipSHA: "tip", ExpectedBaseSHA: "base"},
		CommitSHAs: []string{"aaa", "bbb", "ccc"},
	}
	got := svc.workspaceSelectionRevision
	if got.Path != want.Path || got.Side != want.Side || got.Scope != want.Scope || got.View != want.View || got.WorkspaceVersion != want.WorkspaceVersion || !slices.Equal(got.CommitSHAs, want.CommitSHAs) {
		t.Fatalf("selection revision input = %+v, want %+v", got, want)
	}

	// A PR view alone, without commits, still reads the selection.
	if _, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file/revision?path=README.md&scope=committed&prNumber=42", ""); status != http.StatusOK || svc.workspaceSelectionRevision.View.PRNumber != 42 {
		t.Fatalf("GET PR view revision = %d, input %+v", status, svc.workspaceSelectionRevision)
	}
	body, status, _ = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file/revision?path=README.md&prNumber=abc", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_WORKSPACE_VIEW")
}

func TestSessionsAPI_GetWorkspaceDiffsWithCommitShas(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/diffs", `{"scope":"committed","paths":["README.md"],"contextLines":3,"commitShas":["aaa","bbb"],"prNumber":42,"sourceUrl":" https://github.com/acme/repo/pull/42 ","expectedTipSha":"tip","expectedBaseSha":"base"}`)
	if status != http.StatusOK {
		t.Fatalf("POST workspace selection diffs = %d body=%s", status, body)
	}
	in := svc.workspaceDiffInput
	wantView := sessionsvc.WorkspaceView{PRNumber: 42, SourceURL: "https://github.com/acme/repo/pull/42", ExpectedTipSHA: "tip", ExpectedBaseSHA: "base"}
	if in.Scope != sessionsvc.WorkspaceDiffCommitted || !slices.Equal(in.CommitSHAs, []string{"aaa", "bbb"}) || in.View != wantView || in.CommitSHA != "" {
		t.Fatalf("selection diff input = %+v", in)
	}

	svc.workspaceErr = apierr.Conflict("WORKSPACE_COMMITS_NOT_COMBINABLE", "The selected commits cannot be combined", map[string]any{"commitSha": "bbb", "paths": []string{"README.md"}, "reason": "conflict"})
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/diffs", `{"scope":"committed","paths":["README.md"],"contextLines":3,"commitShas":["aaa","bbb"]}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WORKSPACE_COMMITS_NOT_COMBINABLE")
}

func TestSessionsAPI_GetWorkspaceCommitSelection(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceSelection = sessionsvc.WorkspaceCommitSelection{
		View:       sessionsvc.WorkspaceViewInfo{Kind: sessionsvc.WorkspaceViewBranch, Name: "feature/login", TipSHA: "tip", BaseSHA: "base"},
		Contiguous: false, Combinable: true,
		Files:   []sessionsvc.WorkspaceFileSummary{{Path: "a.txt", Status: sessionsvc.WorkspaceFileAdded, Additions: 1, Size: 2}},
		Summary: sessionsvc.WorkspaceSummary{Files: 1, Additions: 1},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/history/selection", `{"branch":"feature/login","expectedTipSha":"tip","commitShas":["aaa","bbb"],"workspaceVersion":"v1"}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("POST commit selection = %d body=%s", status, body)
	}
	if svc.workspaceSelectionView != (sessionsvc.WorkspaceView{Branch: "feature/login", ExpectedTipSHA: "tip"}) || !slices.Equal(svc.workspaceSelectionSHAs, []string{"aaa", "bbb"}) || svc.workspaceSelectionVersion != "v1" {
		t.Fatalf("service args = view %+v shas %v version %q", svc.workspaceSelectionView, svc.workspaceSelectionSHAs, svc.workspaceSelectionVersion)
	}
	var got controllers.WorkspaceCommitSelectionResponse
	mustJSON(t, body, &got)
	if got.SessionID != "tp-1" || got.TipSHA != "tip" || got.BaseSHA != "base" || got.Contiguous || !got.Combinable || got.Conflict != nil || len(got.Files) != 1 || got.Files[0].Path != "a.txt" || got.Summary.Files != 1 {
		t.Fatalf("response = %+v", got)
	}

	// Commits that cannot be combined are data, not an error.
	svc.workspaceSelection = sessionsvc.WorkspaceCommitSelection{
		View:     sessionsvc.WorkspaceViewInfo{TipSHA: "tip"},
		Conflict: &sessionsvc.WorkspaceCommitSelectionConflict{CommitSHA: "bbb", Paths: []string{"a.txt"}, Reason: sessionsvc.WorkspaceCommitConflict},
	}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/history/selection", `{"commitShas":["aaa","bbb"]}`)
	if status != http.StatusOK {
		t.Fatalf("POST conflicting selection = %d body=%s", status, body)
	}
	for _, want := range []string{`"combinable":false`, `"files":[]`, `"conflict":{"commitSha":"bbb","paths":["a.txt"],"reason":"conflict"}`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}

	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/history/selection", `{"commitShas":`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{apierr.Invalid("INVALID_WORKSPACE_COMMIT_SHA", "Commit SHAs must be full object names", nil), http.StatusBadRequest, "INVALID_WORKSPACE_COMMIT_SHA"},
		{apierr.NotFound("WORKSPACE_COMMIT_NOT_FOUND", "Commit is not available"), http.StatusNotFound, "WORKSPACE_COMMIT_NOT_FOUND"},
		{apierr.Conflict("WORKSPACE_REF_MOVED", "The reviewed branch moved", map[string]any{"tipSha": "new"}), http.StatusConflict, "WORKSPACE_REF_MOVED"},
	} {
		svc.workspaceErr = tc.err
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/workspace/history/selection", `{"commitShas":["x"]}`)
		assertErrorCode(t, body, status, tc.status, tc.code)
	}
}

func TestSessionsAPI_SearchWorkspaceFiles(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceSearch = sessionsvc.WorkspaceFileSearch{SessionID: "tp-1", Query: "read", Results: []sessionsvc.WorkspaceFileSearchResult{{Path: "README.md", Status: sessionsvc.WorkspaceFileModified}}, NextCursor: "1"}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/search?query=read&cursor=0&limit=20", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace search = %d body=%s", status, body)
	}
	if svc.workspaceSearchQuery != "read" || svc.workspaceSearchCursor != "0" || svc.workspaceSearchLimit != 20 {
		t.Fatalf("service args = query:%q cursor:%q limit:%d", svc.workspaceSearchQuery, svc.workspaceSearchCursor, svc.workspaceSearchLimit)
	}
	var got controllers.WorkspaceFileSearchResponse
	mustJSON(t, body, &got)
	if len(got.Results) != 1 || got.Results[0].Path != "README.md" || got.NextCursor != "1" {
		t.Fatalf("response = %#v", got)
	}
}

func TestSessionsAPI_ResolveWorkspacePath(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceResolution = sessionsvc.WorkspacePathResolution{SessionID: "tp-1", Kind: sessionsvc.WorkspacePathKindWorkspace, Path: "src/app.ts"}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/resolve?path="+url.QueryEscape("/root/src/app.ts"), "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET workspace resolve = %d body=%s", status, body)
	}
	if svc.workspaceResolvePath != "/root/src/app.ts" {
		t.Fatalf("service path = %q, want /root/src/app.ts", svc.workspaceResolvePath)
	}
	var got controllers.WorkspacePathResolutionResponse
	mustJSON(t, body, &got)
	if got.SessionID != "tp-1" || got.Kind != sessionsvc.WorkspacePathKindWorkspace || got.Path != "src/app.ts" {
		t.Fatalf("response = %#v", got)
	}
}

func TestSessionsAPI_GetWorkspaceFileBlob(t *testing.T) {
	svc := newFakeSessionService()
	svc.workspaceBlob = sessionsvc.WorkspaceFileBlob{
		Path:      "docs/logo.png",
		MediaType: "image/png",
		Data:      []byte{0x89, 'P', 'N', 'G'},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/file/blob?side=before&path="+url.QueryEscape("docs/logo.png"), "")
	if status != http.StatusOK {
		t.Fatalf("GET workspace blob = %d, want 200; body=%s", status, body)
	}
	if contentType := headers.Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", contentType)
	}
	if cache := headers.Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cache)
	}
	if !bytes.Equal(body, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("body = %q, want raw png bytes", body)
	}
	if svc.workspaceBlob.Side != "" {
		t.Fatalf("fake blob mutated: %#v", svc.workspaceBlob)
	}
}

func TestSessionsAPI_GetWorkspaceFileBlobRequiresPath(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/workspace/file/blob?side=after", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WORKSPACE_PATH_REQUIRED")
}

func TestSessionsAPI_StreamWorkspaceChanges(t *testing.T) {
	workspace := t.TempDir()
	svc := newFakeSessionService()
	// Permit the startup reconciliation, then block refresh work to verify
	// that the invalidation edge is sent before scanning the changed workspace.
	scans := 0
	svc.workspaceListHook = func(ctx context.Context) error {
		scans++
		if scans == 1 {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}

	session := svc.sessions["tp-1"]
	session.Metadata.WorkspacePath = workspace
	svc.sessions["tp-1"] = session
	srv := newSessionTestServer(t, svc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/workspace/events", nil)
	if err != nil {
		t.Fatalf("new workspace stream request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET workspace stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET workspace stream = %d body=%s", resp.StatusCode, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	if svc.workspaceReconciles != 1 {
		t.Fatalf("workspace reconciles before ready = %d, want 1", svc.workspaceReconciles)
	}

	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	event := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if scanner.Text() == "event: workspace_changed" {
				event <- scanner.Text()
				return
			}
		}
	}()
	select {
	case got := <-event:
		if got != "event: workspace_changed" {
			t.Fatalf("event = %q, want workspace_changed", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for workspace change event")
	}
}

func TestSessionsAPI_SnapshotReadsCarrySourceAndSelection(t *testing.T) {
	for _, route := range []string{"file", "tree", "file/blob"} {
		t.Run(route, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)
			sha := strings.Repeat("a", 40)
			body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/"+route+"?snapshot=true&path=src&branch=feature&expectedTipSha="+sha+"&commitShas="+sha+"&filter=needle", "")
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, body)
			}
			got := svc.workspaceSnapshotInput
			if got.Path != "src" || got.View.Branch != "feature" || got.View.ExpectedTipSHA != sha || len(got.CommitSHAs) != 1 || got.CommitSHAs[0] != sha {
				t.Fatalf("snapshot input=%+v", got)
			}
			if route == "tree" && (svc.workspaceSnapshotFilter != "needle" || !strings.Contains(string(body), `"snapshot":true`)) {
				t.Fatalf("tree=%s filter=%s", body, svc.workspaceSnapshotFilter)
			}
			if route == "file" && !strings.Contains(string(body), `"historical":true`) {
				t.Fatalf("snapshot file missing its marker: %s", body)
			}
		})
	}
}

func TestSessionsAPI_SnapshotRejectsMalformedSource(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/file?snapshot=true&path=src&prNumber=invalid", "")
	if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_WORKSPACE_VIEW") {
		t.Fatalf("response=%d %s", status, body)
	}
}

func TestSessionsAPI_SourceSelectorCannotFallBackToLiveFiles(t *testing.T) {
	for _, route := range []string{"file", "tree", "file/blob"} {
		t.Run(route, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/workspace/"+route+"?path=src&branch=feature", "")
			if status != http.StatusOK || svc.workspaceSnapshotInput.View.Branch != "feature" {
				t.Fatalf("source selector fell back to the worktree: %d %s input=%+v", status, body, svc.workspaceSnapshotInput)
			}
		})
	}
}

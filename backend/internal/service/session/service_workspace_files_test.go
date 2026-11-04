package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestListWorkspaceFilesReturnsTrackedAndUntrackedStatus(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "goodbye\nupdated\n")
	if err := os.Remove(filepath.Join(repo, "src", "app.go")); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, repo, "notes.txt", "agent note\n")
	writeWorkspaceFile(t, repo, "node_modules/cache.txt", "ignored\n")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range got.Files {
		byPath[file.Path] = file
	}
	if got.SessionID != "tp-1" {
		t.Fatalf("session id = %q, want tp-1", got.SessionID)
	}
	if byPath["README.md"].Status != WorkspaceFileModified {
		t.Fatalf("README status = %q, want modified", byPath["README.md"].Status)
	}
	if byPath["src/app.go"].Status != WorkspaceFileDeleted {
		t.Fatalf("src/app.go status = %q, want deleted", byPath["src/app.go"].Status)
	}
	if byPath["notes.txt"].Status != WorkspaceFileAdded {
		t.Fatalf("notes.txt status = %q, want added", byPath["notes.txt"].Status)
	}
	if _, ok := byPath["node_modules/cache.txt"]; ok {
		t.Fatal("ignored file was listed")
	}
	if byPath["README.md"].Additions == 0 || byPath["README.md"].Deletions == 0 {
		t.Fatalf("README counts = +%d -%d, want non-zero diff counts", byPath["README.md"].Additions, byPath["README.md"].Deletions)
	}
}

func TestListWorkspaceFilesRepoUnavailableWrapsSentinel(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	// Simulate a project whose source repository has been deleted from disk:
	// the worktree directory still exists, but its git metadata (and so the
	// owning repo's plumbing) is gone. Every git read against it must fail as
	// repository-unavailable rather than a raw 500.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	_, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err == nil {
		t.Fatal("ListWorkspaceFiles succeeded with a missing repository")
	}
	if !errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		t.Fatalf("error = %v, want errors.Is(ErrWorkspaceRepoUnavailable)", err)
	}
}

func TestListWorkspaceFilesClassifiesGitReadFailure(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("not a git index"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	_, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err == nil {
		t.Fatal("ListWorkspaceFiles succeeded with a corrupt Git index")
	}
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want typed API error", err)
	}
	if apiErr.Code != "WORKSPACE_GIT_READ_FAILED" {
		t.Fatalf("error code = %q, want WORKSPACE_GIT_READ_FAILED", apiErr.Code)
	}
}

func TestListWorkspaceFilesTreatsStandaloneWorkerAsNonGitWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, "notes.txt", "standalone note\n")
	st := newFakeStore()
	st.sessions["standalone-1"] = domain.SessionRecord{
		ID:       "standalone-1",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "standalone-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "notes.txt" || got.Files[0].Status != WorkspaceFileAdded {
		t.Fatalf("standalone files = %#v, want added notes.txt", got.Files)
	}
}

func TestListWorkspaceFilesHidesTallpineManagedStandaloneFiles(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, ".kimi/.gitignore", tallpineManagedGitignoreSentinel+"\n/.gitignore\n/AGENTS.md\n")
	writeWorkspaceFile(t, workspace, ".kimi/AGENTS.md", "Tallpine instructions\n")
	writeWorkspaceFile(t, workspace, ".kimi/draft.md", "agent-created work\n")
	writeWorkspaceFile(t, workspace, "notes.txt", "user work\n")
	st := newFakeStore()
	st.sessions["standalone-1"] = domain.SessionRecord{
		ID:       "standalone-1",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "standalone-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".kimi/draft.md", "notes.txt"}
	if len(got.Files) != len(want) {
		t.Fatalf("standalone files = %#v, want user-created files %v", got.Files, want)
	}
	for i, path := range want {
		if got.Files[i].Path != path {
			t.Fatalf("standalone file[%d] = %q, want %q", i, got.Files[i].Path, path)
		}
	}
}

func TestListWorkspaceFilesHidesTallpineManagedStandaloneDirectoryContents(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, ".tallpine/.gitignore", tallpineManagedGitignoreSentinel+"\n/.gitignore\n/hooks\n")
	writeWorkspaceFile(t, workspace, ".tallpine/hooks/run.sh", "#!/bin/sh\n")
	writeWorkspaceFile(t, workspace, "notes.txt", "user work\n")
	st := newFakeStore()
	st.sessions["standalone-1"] = domain.SessionRecord{
		ID:       "standalone-1",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "standalone-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "notes.txt" {
		t.Fatalf("standalone files = %#v, want only user-created notes.txt", got.Files)
	}
}

func TestListWorkspaceFilesHidesTallpineManagedCopilotProfile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, ".github/agents/tallpine-standalone-4.agent.md", "---\nname: tallpine-standalone-4\ntarget: github-copilot\n---\n\n"+tallpineManagedCopilotProfileSentinel+"\n\nTallpine instructions\n")
	writeWorkspaceFile(t, workspace, "result.md", "agent-created work\n")
	st := newFakeStore()
	st.sessions["standalone-4"] = domain.SessionRecord{
		ID:       "standalone-4",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "standalone-4")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "result.md" {
		t.Fatalf("standalone files = %#v, want only agent-created result.md", got.Files)
	}
}

func TestListWorkspaceFilesTreatsMigratedScratchWorkerAsNonGitWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, "notes.txt", "migrated scratch note\n")
	st := newFakeStore()
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:       "scratch-1",
		Kind:     domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "notes.txt" || got.Files[0].Status != WorkspaceFileAdded {
		t.Fatalf("migrated scratch files = %#v, want added notes.txt", got.Files)
	}
}

func TestGetWorkspaceFileReturnsContentAndDiff(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "goodbye\nupdated\n")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	got, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "README.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "README.md" {
		t.Fatalf("path = %q, want README.md", got.Path)
	}
	if got.Content != "goodbye\nupdated\n" {
		t.Fatalf("content = %q", got.Content)
	}
	if !strings.Contains(got.Diff, "-hello") || !strings.Contains(got.Diff, "+updated") {
		t.Fatalf("diff did not include expected old/new lines:\n%s", got.Diff)
	}
}

func TestGetWorkspaceFileReportsImageMediaType(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("after"))
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	got, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "docs/logo.png", "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Binary || got.ImageMediaType != "image/png" {
		t.Fatalf("binary = %v, imageMediaType = %q, want true and image/png", got.Binary, got.ImageMediaType)
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty for a binary file", got.Content)
	}
}

func TestGetWorkspaceFileLeavesTextFilesWithoutImageMediaType(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	// An SVG is text, so it keeps its line diff instead of an image preview.
	writeWorkspaceFile(t, repo, "docs/icon.svg", "<svg></svg>\n")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	got, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "docs/icon.svg", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ImageMediaType != "" {
		t.Fatalf("imageMediaType = %q, want empty for a text file", got.ImageMediaType)
	}
}

func TestGetWorkspaceFileBlobReturnsBothSides(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("after"))
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st}

	before, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobBefore)
	if err != nil {
		t.Fatal(err)
	}
	if string(before.Data) != pngBytes("before") || before.MediaType != "image/png" {
		t.Fatalf("before blob = %q (%s)", before.Data, before.MediaType)
	}
	after, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobAfter)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.Data) != pngBytes("after") || after.Path != "docs/logo.png" {
		t.Fatalf("after blob = %q (%s)", after.Data, after.Path)
	}
}

// gitWorkspaceBlob asks git for the object size before it asks for the bytes,
// so a blob past the limit is refused instead of buffered into memory.
func TestGitWorkspaceBlobRefusesOversizedObjectBeforeReadingIt(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes(strings.Repeat("x", 4096)))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")

	if _, err := gitWorkspaceBlob(context.Background(), repo, "HEAD", "docs/logo.png", 64); err == nil {
		t.Fatal("oversized blob: want error, got nil")
	} else {
		var e *apierr.Error
		if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "WORKSPACE_BLOB_TOO_LARGE" {
			t.Fatalf("err = %v, want apierr.Invalid WORKSPACE_BLOB_TOO_LARGE", err)
		}
	}
	if _, err := gitWorkspaceBlob(context.Background(), repo, "HEAD", "docs/logo.png", 1<<20); err != nil {
		t.Fatalf("blob within the limit: %v", err)
	}
}

func TestGetWorkspaceFileBlobHasNoBeforeSideForAddedFile(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("new"))
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st}

	if _, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobBefore); err == nil {
		t.Fatal("before blob for an added file: want error, got nil")
	} else {
		var e *apierr.Error
		if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "WORKSPACE_BLOB_NOT_FOUND" {
			t.Fatalf("err = %v, want apierr.NotFound WORKSPACE_BLOB_NOT_FOUND", err)
		}
	}
	after, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobAfter)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.Data) != pngBytes("new") {
		t.Fatalf("after blob = %q", after.Data)
	}
}

func TestGetWorkspaceFileBlobHasNoAfterSideForDeletedFile(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")
	if err := os.Remove(filepath.Join(repo, "docs", "logo.png")); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st}

	before, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobBefore)
	if err != nil {
		t.Fatal(err)
	}
	if string(before.Data) != pngBytes("before") {
		t.Fatalf("before blob = %q", before.Data)
	}
	if _, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobAfter); err == nil {
		t.Fatal("after blob for a deleted file: want error, got nil")
	}
	detail, err := svc.GetWorkspaceFile(context.Background(), "tp-1", "docs/logo.png", "")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Deleted || detail.ImageMediaType != "image/png" {
		t.Fatalf("deleted = %v, imageMediaType = %q", detail.Deleted, detail.ImageMediaType)
	}
}

func TestGetWorkspaceFileBlobReadsRenamedFileFromPreviousPath(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")
	runGit(t, repo, "mv", "docs/logo.png", "docs/brand.png")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	before, err := (&Service{store: st}).GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/brand.png", WorkspaceBlobBefore)
	if err != nil {
		t.Fatal(err)
	}
	if string(before.Data) != pngBytes("before") {
		t.Fatalf("before blob = %q", before.Data)
	}
}

func TestGetWorkspaceFileBlobTypesRenamedBeforeSideByItsOwnPath(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.png", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add logo")
	runGit(t, repo, "mv", "docs/logo.png", "docs/logo.gif")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st}

	// A rename that changes the extension must not type the historical bytes by
	// the new path: the controller sends nosniff, so a PNG labelled image/gif
	// never renders.
	before, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.gif", WorkspaceBlobBefore)
	if err != nil {
		t.Fatal(err)
	}
	if before.MediaType != "image/png" {
		t.Fatalf("before mediaType = %q, want image/png", before.MediaType)
	}
	after, err := svc.GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.gif", WorkspaceBlobAfter)
	if err != nil {
		t.Fatal(err)
	}
	if after.MediaType != "image/gif" {
		t.Fatalf("after mediaType = %q, want image/gif", after.MediaType)
	}
}

func TestGetWorkspaceFileBlobRejectsRenameFromANonImagePath(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/logo.bin", pngBytes("before"))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "add blob")
	runGit(t, repo, "mv", "docs/logo.bin", "docs/logo.png")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	_, err := (&Service{store: st}).GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", WorkspaceBlobBefore)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "UNSUPPORTED_WORKSPACE_BLOB" {
		t.Fatalf("err = %v, want apierr.Invalid UNSUPPORTED_WORKSPACE_BLOB", err)
	}
}

func TestGetWorkspaceFileBlobRejectsNonImagePaths(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	_, err := (&Service{store: st}).GetWorkspaceFileBlob(context.Background(), "tp-1", "README.md", WorkspaceBlobAfter)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "UNSUPPORTED_WORKSPACE_BLOB" {
		t.Fatalf("err = %v, want apierr.Invalid UNSUPPORTED_WORKSPACE_BLOB", err)
	}
}

func TestGetWorkspaceFileBlobRejectsUnknownSide(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	_, err := (&Service{store: st}).GetWorkspaceFileBlob(context.Background(), "tp-1", "docs/logo.png", "sideways")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "INVALID_WORKSPACE_BLOB_SIDE" {
		t.Fatalf("err = %v, want apierr.Invalid INVALID_WORKSPACE_BLOB_SIDE", err)
	}
}

func TestWorkspaceFilesIncludeCommittedBranchDiffAgainstRecordedBase(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "README.md", "hello\ncommitted change\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "agent change")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID: "tp-1",
		Metadata: domain.SessionMetadata{
			Branch:        "tp/work",
			WorkspacePath: repo,
			DiffBaseSHA:   base,
			DiffBaseRef:   "main",
		},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	readme := byPath["README.md"]
	if readme.Status != WorkspaceFileModified {
		t.Fatalf("README status = %q, want modified for committed branch diff", readme.Status)
	}
	if readme.Additions == 0 {
		t.Fatalf("README additions = %d, want committed branch additions", readme.Additions)
	}
	if files.CompareMode != WorkspaceCompareBase || files.CompareBaseSHA != base || files.CompareBaseRef != "main" {
		t.Fatalf("compare metadata = mode:%q sha:%q ref:%q, want base %s main", files.CompareMode, files.CompareBaseSHA, files.CompareBaseRef, base)
	}

	detail, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "README.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != WorkspaceFileModified {
		t.Fatalf("detail status = %q, want modified", detail.Status)
	}
	if !strings.Contains(detail.Diff, "+committed change") {
		t.Fatalf("diff did not include committed branch change:\n%s", detail.Diff)
	}
	if detail.CompareMode != WorkspaceCompareBase || detail.CompareBaseSHA != base || detail.CompareBaseRef != "main" {
		t.Fatalf("detail compare metadata = mode:%q sha:%q ref:%q, want base %s main", detail.CompareMode, detail.CompareBaseSHA, detail.CompareBaseRef, base)
	}
}

func TestWorkspaceFilesRecomputesRecordedRefAfterBaseMoves(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	oldBase := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "agent.go", "package main\n")
	runGit(t, repo, "add", "agent.go")
	runGit(t, repo, "commit", "-m", "agent change")
	runGit(t, repo, "switch", "main")
	writeWorkspaceFile(t, repo, "mainonly.go", "package main\n")
	runGit(t, repo, "add", "mainonly.go")
	runGit(t, repo, "commit", "-m", "main moved")
	newBase := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "tp/work")
	runGit(t, repo, "rebase", "main")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID: "tp-1",
		Metadata: domain.SessionMetadata{
			WorkspacePath: repo,
			DiffBaseSHA:   oldBase,
			DiffBaseRef:   "main",
		},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != newBase {
		t.Fatalf("compare base = %q, want recomputed merge base %q", files.CompareBaseSHA, newBase)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["agent.go"].Status != WorkspaceFileAdded {
		t.Fatalf("agent.go status = %q, want added", byPath["agent.go"].Status)
	}
	if got := byPath["mainonly.go"]; got.Status != WorkspaceFileUnmodified || got.Additions != 0 || got.Deletions != 0 {
		t.Fatalf("mainonly.go = %#v, want unmodified after recomputing base", got)
	}
}

// TestWorkspaceFilesExcludeMainChangesMergedInAfterStaleOriginRef reproduces a
// file-count inflation bug: Tallpine never runs `git fetch` against a session
// worktree, so its refs/remotes/origin/main tracking ref can go stale relative
// to the real upstream. When the branch later merges a newer main in (e.g. to
// resolve a merge conflict), recomputing the compare base from that stale ref
// still "succeeds" but lands on an earlier commit than the branch's true
// merge point, so unrelated commits that landed on main afterward leak into
// the diff as if they were part of the PR.
func TestWorkspaceFilesExcludeMainChangesMergedInAfterStaleOriginRef(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")

	// M1: a commit lands on main before the session spawns. Tallpine records this
	// as the session's origin/main tracking ref at spawn time.
	writeWorkspaceFile(t, repo, "unrelated-1.go", "package main\n")
	runGit(t, repo, "add", "unrelated-1.go")
	runGit(t, repo, "commit", "-m", "unrelated change 1")
	m1 := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", m1)

	// The session's feature branch forks from here and makes its own change.
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	// M2: an unrelated PR merges into main after the session spawned. Nothing
	// in Tallpine refreshes refs/remotes/origin/main, so it still points at M1.
	runGit(t, repo, "switch", "main")
	writeWorkspaceFile(t, repo, "unrelated-2.go", "package main\n")
	runGit(t, repo, "add", "unrelated-2.go")
	runGit(t, repo, "commit", "-m", "unrelated change 2")
	m2 := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	// The developer resolves a conflict by merging current main into their
	// branch. HEAD gains M2 as an ancestor, but the stale tracking ref doesn't move.
	runGit(t, repo, "switch", "tp/work")
	runGit(t, repo, "merge", "main", "-m", "merge main to resolve conflict")

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata: domain.SessionMetadata{
			WorkspacePath: repo,
			DiffBaseRef:   "origin/main",
		},
	}
	// Tallpine's own PR sync has observed GitHub's current base tip (M2) even though
	// the local origin/main tracking ref hasn't moved.
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: m2},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["pr-file.go"].Status != WorkspaceFileAdded {
		t.Fatalf("pr-file.go status = %q, want added", byPath["pr-file.go"].Status)
	}
	if got, ok := byPath["unrelated-2.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("unrelated-2.go leaked into the diff via a stale origin/main tracking ref: %#v (compare base %q)", got, files.CompareBaseSHA)
	}
}

func TestWorkspaceFilesCompareForcePushDivergentHistoryPrefersPRWhenHeadMatches(t *testing.T) {
	t.Parallel()

	repo, head, _, replacementBase := newDivergentForcePushRepo(t)

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo, DiffBaseRef: "origin/main"},
	}
	// The PR sync has observed the exact commit the Files tab is displaying.
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: replacementBase, HeadSHA: head},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != replacementBase {
		t.Fatalf("compare base = %q, want the PR's replacement base %q when pr.HeadSHA matches local HEAD", files.CompareBaseSHA, replacementBase)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["pr-file.go"].Status != WorkspaceFileAdded {
		t.Fatalf("pr-file.go status = %q, want added", byPath["pr-file.go"].Status)
	}
	if got, ok := byPath["new-main.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("new-main.go leaked into the diff: %#v", got)
	}
}

func TestWorkspaceFilesCompareForcePushDivergentHistoryKeepsLocalWhenPRHeadStale(t *testing.T) {
	t.Parallel()

	repo, _, staleRef, replacementBase := newDivergentForcePushRepo(t)

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo, DiffBaseRef: "origin/main"},
	}
	// The persisted PR snapshot is stale: it still names an earlier local
	// commit, not the branch's current HEAD (which has since merged the
	// force-pushed main).
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: replacementBase, HeadSHA: "0000000000000000000000000000000000dead"},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != staleRef {
		t.Fatalf("compare base = %q, want the local ref candidate %q when the PR snapshot's head doesn't match local HEAD", files.CompareBaseSHA, staleRef)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["pr-file.go"].Status != WorkspaceFileAdded {
		t.Fatalf("pr-file.go status = %q, want added", byPath["pr-file.go"].Status)
	}
	// old-main.go predates the stale local candidate itself, so it must not
	// show up as changed against it.
	if got, ok := byPath["old-main.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("old-main.go = %#v, want unmodified against the stale local candidate", got)
	}
	// new-main.go legitimately postdates the stale local candidate, so
	// falling back to it (rather than trusting the unmatched PR snapshot)
	// correctly reports it as added — this is the expected cost of rejecting
	// a PR snapshot that doesn't describe local HEAD, not a leak.
	if byPath["new-main.go"].Status != WorkspaceFileAdded {
		t.Fatalf("new-main.go = %#v, want added against the stale local candidate", byPath["new-main.go"])
	}
}

func TestWorkspaceFilesCompareKeepsNewerRefCandidateOverOlderPR(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	older := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	writeWorkspaceFile(t, repo, "newer-main.go", "package main\n")
	runGit(t, repo, "add", "newer-main.go")
	runGit(t, repo, "commit", "-m", "main advanced further than the PR snapshot knows")
	newer := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", newer)

	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo, DiffBaseRef: "origin/main"},
	}
	// The PR sync hasn't caught up to main's latest commit yet.
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: older},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != newer {
		t.Fatalf("compare base = %q, want the newer ref-based candidate %q regardless of candidate order", files.CompareBaseSHA, newer)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if got, ok := byPath["newer-main.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("newer-main.go leaked into the diff via a stale PR base: %#v", got)
	}
}

func TestWorkspaceFilesCompareMissingRefKeepsRecordedSHAOverOlderPR(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")

	writeWorkspaceFile(t, repo, "older.go", "package main\n")
	runGit(t, repo, "add", "older.go")
	runGit(t, repo, "commit", "-m", "older main point")
	older := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	writeWorkspaceFile(t, repo, "newer.go", "package main\n")
	runGit(t, repo, "add", "newer.go")
	runGit(t, repo, "commit", "-m", "newer recorded point")
	recorded := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	// No DiffBaseRef recorded — only the spawn-time recorded SHA.
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo, DiffBaseSHA: recorded},
	}
	// A stale PR snapshot whose base predates the session's recorded base.
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: older},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != recorded {
		t.Fatalf("compare base = %q, want recordedSHA %q (newer than the stale PR base) even with no recorded ref", files.CompareBaseSHA, recorded)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if got, ok := byPath["newer.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("newer.go leaked into the diff via a stale PR base: %#v", got)
	}
}

func TestWorkspaceFilesCompareRejectsPRBaseWithNoCommonAncestor(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	// An orphan commit sharing no history with main: it exists locally (as if
	// fetched for some unrelated reason) but has no common ancestor with HEAD.
	runGit(t, repo, "checkout", "--orphan", "unrelated-history")
	runGit(t, repo, "rm", "-rf", ".")
	writeWorkspaceFile(t, repo, "unrelated-root.go", "package main\n")
	runGit(t, repo, "add", "unrelated-root.go")
	runGit(t, repo, "commit", "-m", "unrelated root commit")
	unrelated := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"},
	}
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: unrelated},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA == unrelated {
		t.Fatalf("compare base resolved to the unrelated PR SHA %q, which shares no history with HEAD", unrelated)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["pr-file.go"].Status != WorkspaceFileAdded {
		t.Fatalf("pr-file.go status = %q, want added", byPath["pr-file.go"].Status)
	}
	// A raw two-tree diff against the unrelated commit would make the
	// orphan's file appear "added" in the session's diff.
	if got, ok := byPath["unrelated-root.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("unrelated-root.go leaked into the diff via the rejected PR base: %#v", got)
	}
}

// TestWorkspaceFilesCompareRejectsPRBaseWithNoCommonAncestorAndNoLocalCandidate
// is the sharpest form of the no-common-ancestor problem: with no recorded
// ref or recorded SHA to fall back on, a PR base whose merge-base derivation
// fails must not be returned as a raw, unrelated SHA.
func TestWorkspaceFilesCompareRejectsPRBaseWithNoCommonAncestorAndNoLocalCandidate(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")

	runGit(t, repo, "checkout", "--orphan", "unrelated-history")
	runGit(t, repo, "rm", "-rf", ".")
	writeWorkspaceFile(t, repo, "unrelated-root.go", "package main\n")
	runGit(t, repo, "add", "unrelated-root.go")
	runGit(t, repo, "commit", "-m", "unrelated root commit")
	unrelated := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	runGit(t, repo, "checkout", "main")
	writeWorkspaceFile(t, repo, "pr-file.go", "package main\n")
	runGit(t, repo, "add", "pr-file.go")
	runGit(t, repo, "commit", "-m", "pr change")

	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "does-not-exist"}}
	// No recorded ref, no recorded SHA: the PR base is the only candidate.
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "proj",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo},
	}
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "does-not-exist", BaseSHA: unrelated},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA == unrelated {
		t.Fatalf("compare base resolved to the unrelated, raw PR SHA %q with no local candidate to fall back on", files.CompareBaseSHA)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if got, ok := byPath["unrelated-root.go"]; ok && got.Status != WorkspaceFileUnmodified {
		t.Fatalf("unrelated-root.go leaked into the diff via the rejected raw PR base: %#v", got)
	}
}

func TestWorkspaceFilesPRFallbackPrefersDefaultTargetPR(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	rootBase := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/root")
	writeWorkspaceFile(t, repo, "lower.go", "package main\n")
	runGit(t, repo, "add", "lower.go")
	runGit(t, repo, "commit", "-m", "lower stack change")
	childBase := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, repo, "upper.go", "package main\n")
	runGit(t, repo, "add", "upper.go")
	runGit(t, repo, "commit", "-m", "upper stack change")

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo},
	}
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "child", SessionID: "tp-1", Number: 2, TargetBranch: "tp/root", BaseSHA: childBase, UpdatedAt: time.Unix(200, 0)},
		{URL: "root", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: rootBase, UpdatedAt: time.Unix(100, 0)},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != rootBase || files.CompareBaseRef != "main" {
		t.Fatalf("compare base = sha:%q ref:%q, want root PR %s main", files.CompareBaseSHA, files.CompareBaseRef, rootBase)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["lower.go"].Status != WorkspaceFileAdded || byPath["upper.go"].Status != WorkspaceFileAdded {
		t.Fatalf("stack files = lower:%#v upper:%#v, want both visible from root PR base", byPath["lower.go"], byPath["upper.go"])
	}
}

func TestWorkspaceFilesPRFallbackUsesMergeBaseWhenTargetBranchAdvances(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	forkBase := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "worker.go", "package main\n")
	runGit(t, repo, "add", "worker.go")
	runGit(t, repo, "commit", "-m", "worker change")
	runGit(t, repo, "switch", "main")
	writeWorkspaceFile(t, repo, "mainonly.go", "package main\n")
	runGit(t, repo, "add", "mainonly.go")
	runGit(t, repo, "commit", "-m", "main advanced independently")
	prBaseSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "tp/work")

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:        "tp-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: repo},
	}
	st.prs["tp-1"] = []domain.PullRequest{
		{URL: "pr", SessionID: "tp-1", Number: 1, TargetBranch: "main", BaseSHA: prBaseSHA, UpdatedAt: time.Unix(100, 0)},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareBaseSHA != forkBase || files.CompareBaseRef != "main" {
		t.Fatalf("compare base = sha:%q ref:%q, want merge base %s main", files.CompareBaseSHA, files.CompareBaseRef, forkBase)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["worker.go"].Status != WorkspaceFileAdded {
		t.Fatalf("worker.go = %#v, want added", byPath["worker.go"])
	}
	if got, ok := byPath["mainonly.go"]; ok {
		t.Fatalf("mainonly.go = %#v, want excluded once compare resolves to the merge base instead of the advanced target tip", got)
	}
}

func TestWorkspaceFilesReportCommittedDeletionsAgainstRecordedBase(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	runGit(t, repo, "rm", "src/app.go")
	runGit(t, repo, "commit", "-m", "delete app")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["src/app.go"].Status != WorkspaceFileDeleted {
		t.Fatalf("src/app.go summary = %#v, want deleted", byPath["src/app.go"])
	}

	detail, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "src/app.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if !detail.Deleted || detail.Status != WorkspaceFileDeleted || !strings.Contains(detail.Diff, "deleted file mode") {
		t.Fatalf("deleted detail = %#v diff:\n%s", detail, detail.Diff)
	}
}

func TestWorkspaceFilesKeepBaseStatusWhenCommittedAddedFileIsModified(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, repo, "new.go", "package main\n")
	runGit(t, repo, "add", "new.go")
	runGit(t, repo, "commit", "-m", "add file")
	writeWorkspaceFile(t, repo, "new.go", "package main\n\nfunc Later() {}\n")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["new.go"].Status != WorkspaceFileAdded {
		t.Fatalf("new.go status = %q, want added from base diff despite HEAD status", byPath["new.go"].Status)
	}
}

func TestWorkspaceFileSectionsSplitByGitState(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")

	// Committed since base.
	writeWorkspaceFile(t, repo, "committed.go", "package main\n")
	runGit(t, repo, "add", "committed.go")
	runGit(t, repo, "commit", "-m", "agent: add committed.go")

	// Partially staged: index differs from HEAD, worktree differs from index.
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\nunstaged addition\n")

	// Untracked.
	writeWorkspaceFile(t, repo, "scratch.txt", "untracked note\n")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID: "tp-1",
		Metadata: domain.SessionMetadata{
			Branch:        "tp/work",
			WorkspacePath: repo,
			DiffBaseSHA:   base,
			DiffBaseRef:   "main",
		},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}

	staged := workspaceSectionPaths(files.Sections.Staged)
	if !staged["README.md"] {
		t.Fatalf("staged section = %v, want README.md", staged)
	}
	unstaged := workspaceSectionPaths(files.Sections.Unstaged)
	if !unstaged["README.md"] {
		t.Fatalf("unstaged section = %v, want README.md (partially staged file)", unstaged)
	}
	untracked := workspaceSectionPaths(files.Sections.Untracked)
	if !untracked["scratch.txt"] {
		t.Fatalf("untracked section = %v, want scratch.txt", untracked)
	}
	committed := workspaceSectionPaths(files.Sections.Committed)
	if !committed["committed.go"] {
		t.Fatalf("committed section = %v, want committed.go", committed)
	}
	if len(files.Commits) != 1 || files.Commits[0].Subject != "agent: add committed.go" {
		t.Fatalf("commits = %+v, want one commit for agent: add committed.go", files.Commits)
	}
	if len(files.Commits[0].Files) != 1 || files.Commits[0].Files[0].Path != "committed.go" || files.Commits[0].Files[0].Status != WorkspaceFileAdded || files.Commits[0].Files[0].Additions != 1 {
		t.Fatalf("commit files = %+v, want added committed.go with one addition", files.Commits[0].Files)
	}
	if files.Commits[0].Files[0].Editable {
		t.Fatal("historical commit file must not be editable")
	}
	if files.Summary.Files == 0 || files.Summary.Additions == 0 {
		t.Fatalf("summary = %+v, want non-zero files and additions", files.Summary)
	}
}

// TestWorkspaceFileDiffScopedToSection covers a partially staged file, where
// GetWorkspaceFile previously always returned the combined base..worktree
// diff no matter which section (Staged or Unstaged) the caller opened it
// from. Each section must resolve to its own diff: staged is index vs HEAD,
// unstaged is worktree vs index.
func TestWorkspaceFileDiffScopedToSection(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)

	// Partially staged: index differs from HEAD, worktree differs from index.
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\nunstaged addition\n")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: repo},
	}
	svc := &Service{store: st}

	staged, err := svc.GetWorkspaceFile(context.Background(), "tp-1", "README.md", WorkspaceFileSectionStaged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(staged.Diff, "+staged addition") || strings.Contains(staged.Diff, "+unstaged addition") {
		t.Fatalf("staged diff = %q, want only the staged hunk", staged.Diff)
	}

	unstaged, err := svc.GetWorkspaceFile(context.Background(), "tp-1", "README.md", WorkspaceFileSectionUnstaged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unstaged.Diff, "+unstaged addition") || strings.Contains(unstaged.Diff, "+staged addition") {
		t.Fatalf("unstaged diff = %q, want only the unstaged hunk", unstaged.Diff)
	}
}

func TestWorkspaceFilesAheadReportsPushCount(t *testing.T) {
	t.Parallel()

	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "HEAD")
	writeWorkspaceFile(t, repo, "ahead.go", "package main\n")
	runGit(t, repo, "add", "ahead.go")
	runGit(t, repo, "commit", "-m", "local ahead commit")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.Ahead == nil || *files.Ahead != 1 {
		t.Fatalf("ahead = %v, want 1", files.Ahead)
	}
	if files.Behind == nil || *files.Behind != 0 {
		t.Fatalf("behind = %v, want 0", files.Behind)
	}
}

func TestWorkspaceFilesBehindReportsPullCount(t *testing.T) {
	t.Parallel()

	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", remote)
	writeWorkspaceFile(t, repo, "upstream.go", "package main\n")
	runGit(t, repo, "add", "upstream.go")
	runGit(t, repo, "commit", "-m", "upstream commit")
	runGit(t, repo, "push", "-u", "origin", "HEAD")
	// Simulate a session worktree that hasn't picked up the commit its
	// tracking ref already knows the remote has.
	runGit(t, repo, "reset", "--hard", "HEAD~1")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.Ahead == nil || *files.Ahead != 0 {
		t.Fatalf("ahead = %v, want 0", files.Ahead)
	}
	if files.Behind == nil || *files.Behind != 1 {
		t.Fatalf("behind = %v, want 1", files.Behind)
	}
}

func TestWorkspaceFilesAheadBehindNilWithoutUpstream(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.Ahead != nil || files.Behind != nil {
		t.Fatalf("ahead/behind = %v/%v, want nil/nil without a configured upstream", files.Ahead, files.Behind)
	}
}

func TestWorkspaceFilesReportRenamesAgainstRecordedBase(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "tp/work")
	runGit(t, repo, "mv", "src/app.go", "src/main.go")
	runGit(t, repo, "commit", "-m", "rename app")

	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID: "tp-1",
		Metadata: domain.SessionMetadata{
			WorkspacePath: repo,
			DiffBaseSHA:   base,
			DiffBaseRef:   "main",
		},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	var renamed WorkspaceFileSummary
	for _, file := range files.Files {
		if file.Path == "src/main.go" {
			renamed = file
			break
		}
	}
	if renamed.Status != WorkspaceFileRenamed || renamed.PreviousPath != "src/app.go" {
		t.Fatalf("renamed summary = %#v, want R src/app.go -> src/main.go", renamed)
	}

	detail, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "src/main.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != WorkspaceFileRenamed || detail.PreviousPath != "src/app.go" {
		t.Fatalf("renamed detail = %#v, want previous path src/app.go", detail)
	}
	if !strings.Contains(detail.Diff, "rename from src/app.go") || !strings.Contains(detail.Diff, "rename to src/main.go") {
		t.Fatalf("rename diff missing rename headers:\n%s", detail.Diff)
	}
}

func TestWorkspaceFilesIncludeWorkspaceProjectChildRepoDiffs(t *testing.T) {
	t.Parallel()

	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	child := filepath.Join(root, "api")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, child, "init")
	runGit(t, child, "config", "user.email", "tallpine@example.com")
	runGit(t, child, "config", "user.name", "Tallpine Tests")
	writeWorkspaceFile(t, child, "service.go", "package api\n")
	runGit(t, child, "add", ".")
	runGit(t, child, "commit", "-m", "initial child")
	childBase := strings.TrimSpace(runGit(t, child, "rev-parse", "HEAD"))
	runGit(t, child, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, child, "service.go", "package api\n\nfunc Added() {}\n")
	runGit(t, child, "add", "service.go")
	runGit(t, child, "commit", "-m", "child change")

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{
		ID:        "ws-1",
		ProjectID: "ws",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "api", WorktreePath: child, BaseSHA: childBase},
	}

	svc := &Service{store: st}
	paths, err := svc.WorkspaceWatchPaths(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != root || paths[1] != child {
		t.Fatalf("workspace watch paths = %v, want root and child repo", paths)
	}

	files, err := svc.ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	childFile := byPath["api/service.go"]
	if childFile.Status != WorkspaceFileModified || childFile.Additions == 0 {
		t.Fatalf("child repo file = %#v, want modified with additions", childFile)
	}
	if _, ok := byPath["api/.git/HEAD"]; ok {
		t.Fatal("child .git internals must not be listed through the workspace root")
	}

	detail, err := svc.GetWorkspaceFile(context.Background(), "ws-1", "api/service.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Path != "api/service.go" || !strings.Contains(detail.Diff, "+func Added() {}") {
		t.Fatalf("child repo detail = %#v diff:\n%s", detail, detail.Diff)
	}
	if detail.CompareMode != WorkspaceCompareBase || detail.CompareBaseSHA != childBase {
		t.Fatalf("child detail compare = mode:%q sha:%q, want base %s", detail.CompareMode, detail.CompareBaseSHA, childBase)
	}
	if detail.CompareBaseRef != "" {
		t.Fatalf("child detail compare ref = %q, want empty when only the recorded SHA is authoritative", detail.CompareBaseRef)
	}
}

func TestWorkspaceProjectChildRepoRecomputesBaseAfterRebase(t *testing.T) {
	t.Parallel()

	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	child := filepath.Join(root, "api")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, child, "init")
	runGit(t, child, "config", "user.email", "tallpine@example.com")
	runGit(t, child, "config", "user.name", "Tallpine Tests")
	writeWorkspaceFile(t, child, "service.go", "package api\n")
	runGit(t, child, "add", ".")
	runGit(t, child, "commit", "-m", "initial child")
	runGit(t, child, "branch", "-M", "main")
	oldChildBase := strings.TrimSpace(runGit(t, child, "rev-parse", "HEAD"))
	runGit(t, child, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, child, "agent.go", "package api\n\nfunc Agent() {}\n")
	runGit(t, child, "add", "agent.go")
	runGit(t, child, "commit", "-m", "agent change")
	runGit(t, child, "switch", "main")
	writeWorkspaceFile(t, child, "baseonly.go", "package api\n\nfunc BaseOnly() {}\n")
	runGit(t, child, "add", "baseonly.go")
	runGit(t, child, "commit", "-m", "base moved")
	newChildBase := strings.TrimSpace(runGit(t, child, "rev-parse", "HEAD"))
	runGit(t, child, "switch", "tp/work")
	runGit(t, child, "rebase", "main")

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{
		ID:        "ws-1",
		ProjectID: "ws",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "api", WorktreePath: child, BaseSHA: oldChildBase, BaseRef: "refs/heads/main"},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
	}
	if byPath["api/agent.go"].Status != WorkspaceFileAdded {
		t.Fatalf("api/agent.go status = %q, want added", byPath["api/agent.go"].Status)
	}
	if got := byPath["api/baseonly.go"]; got.Status != WorkspaceFileUnmodified || got.Additions != 0 || got.Deletions != 0 {
		t.Fatalf("api/baseonly.go = %#v, want unmodified after recomputing child base", got)
	}

	detail, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "ws-1", "api/agent.go", "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.CompareMode != WorkspaceCompareBase || detail.CompareBaseSHA != newChildBase || detail.CompareBaseRef != "refs/heads/main" {
		t.Fatalf("child detail compare = mode:%q sha:%q ref:%q, want base %s refs/heads/main", detail.CompareMode, detail.CompareBaseSHA, detail.CompareBaseRef, newChildBase)
	}
}

func TestWorkspaceProjectCompareModeStaysBaseWithPartialFallback(t *testing.T) {
	t.Parallel()

	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	runGit(t, root, "switch", "-c", "tp/work")
	writeWorkspaceFile(t, root, "root.go", "package main\n")
	runGit(t, root, "add", "root.go")
	runGit(t, root, "commit", "-m", "root change")
	child := filepath.Join(root, "api")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, child, "init")
	runGit(t, child, "config", "user.email", "tallpine@example.com")
	runGit(t, child, "config", "user.name", "Tallpine Tests")
	writeWorkspaceFile(t, child, "scratch.go", "package api\n")
	runGit(t, child, "add", ".")
	runGit(t, child, "commit", "-m", "initial child")
	writeWorkspaceFile(t, child, "scratch.go", "package api\n\nfunc Dirty() {}\n")

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace, Config: domain.ProjectConfig{DefaultBranch: "missing-main"}}
	st.sessions["ws-1"] = domain.SessionRecord{
		ID:        "ws-1",
		ProjectID: "ws",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "api", WorktreePath: child, BaseSHA: "missing-base"},
	}

	files, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if files.CompareMode != WorkspaceCompareBase {
		t.Fatalf("workspace compare mode = %q, want base when at least one repo resolved", files.CompareMode)
	}
	if files.CompareBaseSHA != "" {
		t.Fatalf("workspace compare sha = %q, want empty for mixed repo bases", files.CompareBaseSHA)
	}
}

func TestAppendWorkspaceFilesWithCapEnforcesGlobalLimit(t *testing.T) {
	t.Parallel()

	existing := make([]WorkspaceFileSummary, maxWorkspaceFiles-1)
	added := []WorkspaceFileSummary{{Path: "one.go"}, {Path: "two.go"}}

	got, truncated := appendWorkspaceFilesWithCap(existing, added, false)
	if len(got) != maxWorkspaceFiles || !truncated {
		t.Fatalf("len,truncated = %d,%v; want %d,true", len(got), truncated, maxWorkspaceFiles)
	}
	if got[len(got)-1].Path != "one.go" {
		t.Fatalf("last appended path = %q, want one.go", got[len(got)-1].Path)
	}
}

func TestWorkspaceBaseRefCandidatesPreferRemoteDefault(t *testing.T) {
	t.Parallel()

	got := workspaceBaseRefCandidates("main")
	want := []string{"origin/main", "refs/remotes/origin/main", "main"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("workspace base candidates = %#v, want %#v", got, want)
	}

	got = workspaceBaseRefCandidates("")
	if len(got) != 0 {
		t.Fatalf("empty workspace base candidates = %#v, want none rather than a guessed main", got)
	}
}

func TestListWorkspaceFilesScratchUsesFilesystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "README.md", "one\ntwo\n")
	writeWorkspaceFile(t, root, ".env", "TOKEN=secret\n")
	writeWorkspaceFile(t, root, "nested/task.txt", "do it")
	writeWorkspaceFile(t, root, ".git/config", "[core]\n")
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	var paths []string
	for _, file := range got.Files {
		paths = append(paths, file.Path)
		byPath[file.Path] = file
		if file.Status != WorkspaceFileAdded {
			t.Fatalf("%s status = %q, want added", file.Path, file.Status)
		}
	}
	wantPaths := []string{".env", "README.md", "image.bin", "nested/task.txt"}
	if strings.Join(paths, "|") != strings.Join(wantPaths, "|") {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if byPath["README.md"].Additions != 0 || byPath["README.md"].Deletions != 0 {
		t.Fatalf("README counts = +%d -%d, want +0 -0", byPath["README.md"].Additions, byPath["README.md"].Deletions)
	}
	if byPath["nested/task.txt"].Additions != 0 {
		t.Fatalf("nested/task.txt additions = %d, want 0", byPath["nested/task.txt"].Additions)
	}
	if !byPath["image.bin"].Binary || byPath["image.bin"].Additions != 0 || byPath["image.bin"].Deletions != 0 {
		t.Fatalf("binary summary = %#v, want binary with zero counts", byPath["image.bin"])
	}
	if !byPath["README.md"].Editable || byPath["image.bin"].Editable {
		t.Fatalf("editable flags = README:%v image:%v, want true/false", byPath["README.md"].Editable, byPath["image.bin"].Editable)
	}
	if _, ok := byPath[".git/config"]; ok {
		t.Fatal(".git content should not be listed for scratch")
	}
}

func TestListWorkspaceFilesScratchStopsAtFileLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := 0; i < maxWorkspaceFiles+1; i++ {
		writeWorkspaceFile(t, root, fmt.Sprintf("file-%05d.txt", i), "content\n")
	}
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if len(got.Files) != maxWorkspaceFiles {
		t.Fatalf("file count = %d, want %d", len(got.Files), maxWorkspaceFiles)
	}
}

func TestListWorkspaceFilesScratchSkipsSymlinkEscapes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	writeWorkspaceFile(t, root, "real.txt", "inside\n")
	writeWorkspaceFile(t, outside, "secret.txt", "outside\n")
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "inside-link.txt")); err != nil {
		t.Skipf("creating inside symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "outside-link.txt")); err != nil {
		t.Skipf("creating outside symlink: %v", err)
	}
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}

	got, err := (&Service{store: st}).ListWorkspaceFiles(context.Background(), "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range got.Files {
		byPath[file.Path] = file
	}
	if _, ok := byPath["inside-link.txt"]; !ok {
		t.Fatalf("inside symlink was not listed: %#v", got.Files)
	}
	if _, ok := byPath["outside-link.txt"]; ok {
		t.Fatalf("outside symlink escape was listed: %#v", got.Files)
	}
}

func TestGetWorkspaceFileScratchReturnsContentWithEmptyDiff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "README.md", "one\ntwo\n")
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Metadata:  domain.SessionMetadata{WorkspacePath: root},
	}

	got, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "scratch-1", "README.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WorkspaceFileAdded {
		t.Fatalf("status = %q, want added", got.Status)
	}
	if got.Content != "one\ntwo\n" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Additions != 2 || got.Deletions != 0 {
		t.Fatalf("counts = +%d -%d, want +2 -0", got.Additions, got.Deletions)
	}
	if got.Diff != "" || got.DiffTruncated {
		t.Fatalf("scratch diff = %q truncated=%v, want empty", got.Diff, got.DiffTruncated)
	}
	if !got.Editable {
		t.Fatal("Editable = false, want true for a complete UTF-8 text file")
	}
}

func TestGetWorkspaceFileMarksOversizedTextAsNotEditable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeWorkspaceFile(t, root, "large.yaml", strings.Repeat("a", maxWorkspaceFileBytes+1))
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{ID: "scratch-1", ProjectID: "scratch", Metadata: domain.SessionMetadata{WorkspacePath: root}}

	got, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "scratch-1", "large.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Editable || !got.ContentTruncated {
		t.Fatalf("editable=%v truncated=%v, want false/true", got.Editable, got.ContentTruncated)
	}
}

func TestGetWorkspaceFileRejectsTraversal(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	_, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "../secrets.txt", "")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "INVALID_WORKSPACE_PATH" {
		t.Fatalf("err = %v, want bad request INVALID_WORKSPACE_PATH", err)
	}
}

func TestGetWorkspaceFileRejectsIntermediateSymlinkEscape(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	outside := t.TempDir()
	writeWorkspaceFile(t, outside, "secret.txt", "outside workspace\n")
	linkWorkspaceDir(t, outside, filepath.Join(repo, "link"))
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}

	_, err := (&Service{store: st}).GetWorkspaceFile(context.Background(), "tp-1", "link/secret.txt", "")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "INVALID_WORKSPACE_PATH" {
		t.Fatalf("err = %v, want bad request INVALID_WORKSPACE_PATH", err)
	}
}

func TestUpdateWorkspaceFileReplacesExistingTextWithOptimisticFingerprint(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "notes.txt", "before\n")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st, workspaceCache: newWorkspaceCache(workspaceCacheTTL, time.Now)}

	before, err := svc.GetWorkspaceFile(context.Background(), "tp-1", "notes.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	after, err := svc.UpdateWorkspaceFile(context.Background(), "tp-1", UpdateWorkspaceFileInput{
		Path:                    "notes.txt",
		Content:                 "after\n",
		ExpectedFileFingerprint: before.FileFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != "after\n" || after.FileFingerprint == before.FileFingerprint {
		t.Fatalf("updated detail = %#v", after)
	}
	data, err := os.ReadFile(filepath.Join(repo, "notes.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("workspace content = %q err=%v", data, err)
	}
}

func TestUpdateWorkspaceFileRejectsStaleFingerprintWithoutWriting(t *testing.T) {
	t.Parallel()

	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "notes.txt", "current\n")
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := &Service{store: st, workspaceCache: newWorkspaceCache(workspaceCacheTTL, time.Now)}

	_, err := svc.UpdateWorkspaceFile(context.Background(), "tp-1", UpdateWorkspaceFileInput{
		Path:                    "notes.txt",
		Content:                 "replacement\n",
		ExpectedFileFingerprint: "stale",
	})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindConflict || apiError.Code != "WORKSPACE_FILE_STALE" {
		t.Fatalf("err = %v, want conflict WORKSPACE_FILE_STALE", err)
	}
	data, readErr := os.ReadFile(filepath.Join(repo, "notes.txt"))
	if readErr != nil || string(data) != "current\n" {
		t.Fatalf("workspace content = %q err=%v", data, readErr)
	}
}

func TestWorkspaceFileSizeDoesNotTreatTruncatedUTF8RuneAsBinary(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	content := strings.Repeat("a", 8190) + "你tail"
	writeWorkspaceFile(t, repo, "translated.json", content)

	size, binary := workspaceFileSizeAndBinary(repo, "translated.json", WorkspaceFileModified)
	if binary {
		t.Fatal("valid UTF-8 file was classified as binary when the preview ended mid-rune")
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}
}

package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func TestWorkspaceLocationReturnsOnlyLiveSessionWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	got, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != workspace {
		t.Fatalf("WorkspaceLocation() = %q, want %q", got, workspace)
	}
}

func TestWorkspaceLocationDoesNotFallBackToProjectCheckout(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", ProjectID: "tp"}
	store.projects["tp"] = domain.ProjectRecord{ID: "tp", Path: t.TempDir()}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationRejectsMissingDirectory(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir() + "/gone"},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationRejectsFileInPlaceOfDirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: file},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_NOT_FOUND")
}

func TestWorkspaceLocationReportsUnreadableWorkspace(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	assertAPIErrorCode(t, err, "SESSION_WORKSPACE_UNREADABLE")
}

func TestWorkspaceLocationPreservesStoreFailure(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.getSessionErr = errors.New("storage unavailable")

	_, err := (&Service{store: store}).WorkspaceLocation(context.Background(), "tp-1")
	if err == nil || !errors.Is(err, store.getSessionErr) {
		t.Fatalf("WorkspaceLocation() error = %v, want wrapped storage failure", err)
	}
}

func assertAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != code {
		t.Fatalf("error = %v, want API code %s", err, code)
	}
}

func TestArtifactLocationReturnsTheStoredArtifactDirectory(t *testing.T) {
	t.Parallel()

	artifactDir := t.TempDir()
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{ArtifactDir: artifactDir}}

	got, err := (&Service{store: store, dataDir: t.TempDir()}).ArtifactLocation(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != artifactDir {
		t.Fatalf("ArtifactLocation() = %q, want %q", got, artifactDir)
	}
}

func TestArtifactLocationFallsBackToTheDataDirLayout(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "tp", "tp-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1"}

	got, err := (&Service{store: store, dataDir: dataDir}).ArtifactLocation(context.Background(), "tp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != artifactDir {
		t.Fatalf("ArtifactLocation() = %q, want %q", got, artifactDir)
	}
}

func TestArtifactLocationRejectsAMissingDirectory(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{ArtifactDir: filepath.Join(t.TempDir(), "gone")}}

	_, err := (&Service{store: store}).ArtifactLocation(context.Background(), "tp-1")
	assertAPIErrorCode(t, err, "SESSION_ARTIFACTS_NOT_FOUND")
}

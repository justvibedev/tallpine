package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestWriteSpawnAttachments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	m := New(Deps{DataDir: t.TempDir()})
	refs, err := m.writeSpawnAttachments(context.Background(), "tp-1", dir, []ports.SpawnAttachment{
		{Ext: ".html", Data: []byte("first")},
		{Ext: ".png", Data: []byte("second")},
		{Ext: "", Data: []byte("third")},
	})
	if err != nil {
		t.Fatalf("writeSpawnAttachments: %v", err)
	}

	want := []string{".tallpine/attachments/attachment-1.html", ".tallpine/attachments/attachment-2.png", ".tallpine/attachments/attachment-3.bin"}
	if len(refs) != len(want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
	for i, ref := range refs {
		if ref != want[i] {
			t.Errorf("ref[%d] = %q, want %q", i, ref, want[i])
		}
		got, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref)))
		if readErr != nil {
			t.Fatalf("read %s: %v", ref, readErr)
		}
		if len(got) == 0 {
			t.Errorf("attachment %s is empty on disk", ref)
		}
	}
}

func TestStageAttachmentsUsesNeutralFileNames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dataDir := t.TempDir()
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: dir},
	}
	m := New(Deps{Store: st, Workspace: &fakeWorkspace{}, DataDir: dataDir})

	refs, err := m.StageAttachments(context.Background(), "tp-1", []ports.SpawnAttachment{
		{Ext: ".html", Data: []byte("<main>hi</main>")},
	})
	if err != nil {
		t.Fatalf("StageAttachments: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %v, want one", refs)
	}
	if !strings.HasPrefix(refs[0], ".tallpine/attachments/attachment-") || !strings.HasSuffix(refs[0], ".html") {
		t.Fatalf("ref = %q, want neutral attachment name with .html extension", refs[0])
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(refs[0]))); err != nil {
		t.Fatalf("staged attachment missing on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "attachments", "tp-1", filepath.Base(refs[0]))); err != nil {
		t.Fatalf("canonical attachment missing on disk: %v", err)
	}
}

func TestStageAttachmentsRetriesGeneratedNameCollisionsWithoutOverwritingHistory(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	dataDir := t.TempDir()
	st := newFakeStore()
	st.sessions["tp-1"] = domain.SessionRecord{
		ID:       "tp-1",
		Metadata: domain.SessionMetadata{WorkspacePath: workspace},
	}
	m := New(Deps{Store: st, Workspace: &fakeWorkspace{}, DataDir: dataDir})
	suffixes := []string{"deadbeef00", "cafebabe00"}
	m.attachmentSuffix = func() (string, error) {
		next := suffixes[0]
		suffixes = suffixes[1:]
		return next, nil
	}

	oldName := "attachment-deadbeef00.png"
	for _, dir := range []string{
		filepath.Join(dataDir, "attachments", "tp-1"),
		filepath.Join(workspace, filepath.FromSlash(attachmentsDir)),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, oldName), []byte("historical bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	refs, err := m.StageAttachments(context.Background(), "tp-1", []ports.SpawnAttachment{{Ext: ".png", Data: []byte("new bytes")}})
	if err != nil {
		t.Fatalf("StageAttachments: %v", err)
	}
	if len(refs) != 1 || refs[0] != ".tallpine/attachments/attachment-cafebabe00.png" {
		t.Fatalf("refs = %v, want retried collision-free name", refs)
	}
	oldBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(attachmentsDir), oldName))
	if err != nil || string(oldBytes) != "historical bytes" {
		t.Fatalf("historical attachment = %q, %v; want unchanged", oldBytes, err)
	}
	newBytes, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(refs[0])))
	if err != nil || string(newBytes) != "new bytes" {
		t.Fatalf("new attachment = %q, %v; want new bytes", newBytes, err)
	}
}

func TestAppendAttachmentReferences(t *testing.T) {
	t.Parallel()

	t.Run("appends after a brief", func(t *testing.T) {
		got := appendAttachmentReferences("Fix the button", []string{".tallpine/attachments/attachment-1.html"})
		if !strings.HasPrefix(got, "Fix the button\n\n") {
			t.Errorf("brief not preserved: %q", got)
		}
		if !strings.Contains(got, "- .tallpine/attachments/attachment-1.html") {
			t.Errorf("missing reference: %q", got)
		}
	})

	t.Run("handles empty brief", func(t *testing.T) {
		got := appendAttachmentReferences("", []string{".tallpine/attachments/attachment-1.html"})
		if strings.HasPrefix(got, "\n") {
			t.Errorf("leading blank line for empty brief: %q", got)
		}
		if !strings.Contains(got, "Attached files") {
			t.Errorf("missing header: %q", got)
		}
		if strings.Contains(got, "Attached images") {
			t.Errorf("header still describes attachments as images: %q", got)
		}
	})

	t.Run("no refs returns prompt unchanged", func(t *testing.T) {
		if got := appendAttachmentReferences("brief", nil); got != "brief" {
			t.Errorf("got %q, want %q", got, "brief")
		}
	})
}

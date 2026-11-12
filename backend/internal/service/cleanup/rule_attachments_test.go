package cleanup

import (
	"path/filepath"
	"testing"
	"time"
)

func attachmentFixture(t *testing.T) (dataDir string, path func(id, name string) string, store *fakeStore) {
	t.Helper()
	dataDir = t.TempDir()
	path = func(id, name string) string { return filepath.Join(dataDir, "attachments", id, name) }
	old, fresh := scratchNow.Add(-20*24*time.Hour), scratchNow.Add(-time.Hour)
	writeFile(t, path("ended", "old.png"), 100, old)
	writeFile(t, path("ended", "new.png"), 50, fresh)
	writeFile(t, path("alive", "old.png"), 70, old)
	writeFile(t, path("gone", "old.png"), 30, old)
	store = &fakeStore{policy: "{}"}
	store.sessions = append(store.sessions, terminated("ended", "p", 30*24*time.Hour), live("alive", "p"))
	return dataDir, path, store
}

func TestAttachmentRuleSweepsOldFilesOfEndedAndOrphanedSessions(t *testing.T) {
	dataDir, path, store := attachmentFixture(t)
	rule := NewAttachmentRule(dataDir)
	if dry := runRules(t, store, RunRequest{DryRun: true}, rule); dry.Categories[0].WouldFreeBytes != 130 || !exists(path("ended", "old.png")) {
		t.Fatalf("dry run: would free %d", dry.Categories[0].WouldFreeBytes)
	}
	report := runRules(t, store, RunRequest{}, rule)
	if report.FreedBytes != 130 || exists(path("ended", "old.png")) || exists(path("gone", "old.png")) || exists(filepath.Dir(path("gone", "x"))) {
		t.Fatalf("freed %d; ended old=%t gone=%t", report.FreedBytes, exists(path("ended", "old.png")), exists(path("gone", "old.png")))
	}
	if !exists(path("ended", "new.png")) {
		t.Fatal("removed a file inside the retention window")
	}
}

func TestAttachmentRuleKeepsLiveSessionAttachments(t *testing.T) {
	dataDir, path, store := attachmentFixture(t)
	runRules(t, store, RunRequest{}, NewAttachmentRule(dataDir))
	if !exists(path("alive", "old.png")) {
		t.Fatal("removed an old attachment of a live session")
	}
}

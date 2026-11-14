package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func TestListBatchesKanbanReads(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer"}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr-1", HeadSHA: "head-1"}
	st.pr["mer-2"] = domain.PRFacts{URL: "pr-2", HeadSHA: "head-2"}
	st.reviewRuns["mer-1"] = []domain.CurrentHeadReviewRun{{SessionID: "mer-1", PRURL: "pr-1", Status: domain.ReviewRunRunning, ID: "run-1", CreatedAt: time.Now().UTC()}}
	st.reviewRuns["mer-2"] = []domain.CurrentHeadReviewRun{{SessionID: "mer-2", PRURL: "pr-2", Status: domain.ReviewRunRunning, ID: "run-2", CreatedAt: time.Now().UTC()}}

	list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if st.listPRFactsCalls != 1 {
		t.Fatalf("ListPRFacts calls = %d, want 1 batched call", st.listPRFactsCalls)
	}
	if st.listReviewRunsCalls != 1 {
		t.Fatalf("ListCurrentHeadReviewRuns calls = %d, want 1 batched call", st.listReviewRunsCalls)
	}
}

func TestInterruptedChatSessionShowsInterruptedOnTheBoard(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.interrupted = map[domain.SessionID]bool{"mer-1": true}
	svc := &Service{store: st}

	list, err := svc.List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.SessionID]domain.DisplayStatus{"mer-1": "Interrupted", "mer-2": "Awaiting PR"}
	for _, sess := range list {
		if sess.KanbanColumn != "building" || sess.DisplayStatus != want[sess.ID] {
			t.Fatalf("list %s = %q/%q, want building/%q", sess.ID, sess.KanbanColumn, sess.DisplayStatus, want[sess.ID])
		}
	}
	sess, err := svc.Get(context.Background(), "mer-1")
	if err != nil || sess.DisplayStatus != "Interrupted" {
		t.Fatalf("get displayStatus = %q, %v; want Interrupted", sess.DisplayStatus, err)
	}
}

func TestTaskPreparationsStayOutOfSessionReads(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTaskPreparation: true}
	svc := &Service{store: st}

	list, err := svc.List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v, want no preparations", list)
	}
	_, err = svc.Get(context.Background(), "mer-1")
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("get preparation error = %v", err)
	}
}

func TestGetRecordReadsOnlyTheStoredRow(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Metadata: domain.SessionMetadata{PreviewURL: "http://localhost:5173/"}}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", IsTaskPreparation: true}
	svc := &Service{store: st}

	rec, err := svc.GetRecord(context.Background(), "mer-1")
	if err != nil || rec.Metadata.PreviewURL != "http://localhost:5173/" {
		t.Fatalf("GetRecord = %+v, %v", rec, err)
	}
	if st.listPRFactsCalls != 0 || st.listReviewRunsCalls != 0 {
		t.Fatalf("GetRecord read PR facts %d times and review runs %d times, want none", st.listPRFactsCalls, st.listReviewRunsCalls)
	}
	for _, id := range []domain.SessionID{"mer-2", "missing"} {
		_, err = svc.GetRecord(context.Background(), id)
		var apiError *apierr.Error
		if !errors.As(err, &apiError) || apiError.Code != "SESSION_NOT_FOUND" {
			t.Fatalf("GetRecord(%s) error = %v, want SESSION_NOT_FOUND", id, err)
		}
	}
}

func TestSessionListAppliesActivityBeforePRFacts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		activity domain.ActivityState
		want     domain.SessionStatus
	}{
		{"active", domain.ActivityActive, domain.StatusWorking},
		{"idle", domain.ActivityIdle, domain.StatusCIFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: tt.activity}}
			st.pr["mer-1"] = domain.PRFacts{URL: "pr1", CI: domain.CIFailing}

			list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 || list[0].Status != tt.want {
				t.Fatalf("got %+v, want status %q", list, tt.want)
			}
		})
	}
}

func TestSessionRenameUpdatesDisplayName(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	err := (&Service{store: st}).Rename(context.Background(), "mer-1", "  Fix issue #90  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].DisplayName; got != "Fix issue #90" {
		t.Fatalf("display name = %q, want trimmed rename", got)
	}
}

func TestSessionRenameRejectsOrchestrator(t *testing.T) {
	t.Parallel()

	for _, terminated := range []bool{false, true} {
		st := newFakeStore()
		st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: terminated}

		err := (&Service{store: st}).Rename(context.Background(), "mer-1", "My orchestrator")
		var e *apierr.Error
		if !errors.As(err, &e) || e.Kind != apierr.KindInvalid || e.Code != "ORCHESTRATOR_RENAME_UNSUPPORTED" {
			t.Fatalf("terminated=%v: err = %v, want apierr Invalid ORCHESTRATOR_RENAME_UNSUPPORTED", terminated, err)
		}
		if got := st.sessions["mer-1"].DisplayName; got != "" {
			t.Fatalf("terminated=%v: display name = %q, want unchanged", terminated, got)
		}
	}
}

func TestSessionRenameRejectsOverlongDisplayName(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	overlong := strings.Repeat("x", 101)
	err := (&Service{store: st}).Rename(context.Background(), "mer-1", overlong)
	if err == nil {
		t.Fatal("expected error for overlong display name, got nil")
	}
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "DISPLAY_NAME_TOO_LONG" {
		t.Fatalf("err = %v, want DISPLAY_NAME_TOO_LONG", err)
	}
}

func TestSessionPinAndUnpin(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	sess, err := (&Service{store: st, clock: time.Now}).Pin(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.IsPinned || sess.PinnedAt == nil {
		t.Fatalf("pin was not persisted: session=%+v", sess)
	}
	if !st.sessions["mer-1"].IsPinned || st.sessions["mer-1"].PinnedAt == nil {
		t.Fatalf("pin was not stored: session=%+v", st.sessions["mer-1"])
	}

	sess, err = (&Service{store: st, clock: time.Now}).Unpin(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.IsPinned || sess.PinnedAt != nil {
		t.Fatalf("unpin was not persisted: session=%+v", sess)
	}
	if st.sessions["mer-1"].IsPinned || st.sessions["mer-1"].PinnedAt != nil {
		t.Fatalf("unpin was not stored: session=%+v", st.sessions["mer-1"])
	}
}

func TestSessionPinUnknownSession(t *testing.T) {
	t.Parallel()

	if _, err := (&Service{store: newFakeStore()}).Pin(context.Background(), "ghost-1"); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestSessionUnpinUnknownSession(t *testing.T) {
	t.Parallel()

	if _, err := (&Service{store: newFakeStore()}).Unpin(context.Background(), "ghost-1"); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestSessionSetPreviewPersistsURL(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	sess, err := (&Service{store: st, clock: time.Now}).SetPreview(context.Background(), "mer-1", "file:///tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Metadata.PreviewURL != "file:///tmp/index.html" {
		t.Fatalf("returned preview url = %q, want set value", sess.Metadata.PreviewURL)
	}
	if got := st.sessions["mer-1"].Metadata.PreviewURL; got != "file:///tmp/index.html" {
		t.Fatalf("persisted preview url = %q, want set value", got)
	}
}

func TestSessionSetPreviewUnknownSession(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	if _, err := (&Service{store: st}).SetPreview(context.Background(), "ghost-1", "http://x"); err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestSessionSetTerminateOnPRMergePersistsPolicy(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}

	sess, err := (&Service{store: st}).SetTerminateOnPRMerge(context.Background(), "mer-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.TerminateOnPRMerge || !st.sessions["mer-1"].TerminateOnPRMerge {
		t.Fatalf("terminate-on-merge policy was not persisted: session=%+v stored=%+v", sess, st.sessions["mer-1"])
	}
}

func TestSessionSetTerminateOnPRMergeUnknownSession(t *testing.T) {
	t.Parallel()

	if _, err := (&Service{store: newFakeStore()}).SetTerminateOnPRMerge(context.Background(), "ghost-1", true); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestSessionSetAutoInjectReviewPersistsPolicy(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, AutoInjectReview: true}

	sess, err := (&Service{store: st}).SetAutoInjectReview(context.Background(), "mer-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if sess.AutoInjectReview || st.sessions["mer-1"].AutoInjectReview {
		t.Fatalf("auto-inject policy was not disabled: session=%+v stored=%+v", sess, st.sessions["mer-1"])
	}
}

func TestSessionSetAutoInjectReviewUnknownSession(t *testing.T) {
	t.Parallel()

	if _, err := (&Service{store: newFakeStore()}).SetAutoInjectReview(context.Background(), "ghost-1", false); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestSessionSetAutoInjectCIPersistsDefault(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, AutoInjectCI: true}

	sess, err := (&Service{store: st}).SetAutoInjectCI(context.Background(), "mer-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if sess.AutoInjectCI || st.sessions["mer-1"].AutoInjectCI {
		t.Fatalf("auto-inject CI default was not disabled: returned=%+v stored=%+v", sess, st.sessions["mer-1"])
	}
}

func TestSessionSetAutoInjectCIUnknownSession(t *testing.T) {
	t.Parallel()

	if _, err := (&Service{store: newFakeStore()}).SetAutoInjectCI(context.Background(), "ghost-1", false); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestSessionSetReviewerHarnessPersistsPerSession(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker}

	sess, err := (&Service{store: st}).SetReviewerHarness(context.Background(), "mer-1", domain.ReviewerKiro, domain.AgentConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ReviewerHarness != domain.ReviewerKiro || st.sessions["mer-1"].ReviewerHarness != domain.ReviewerKiro {
		t.Fatalf("reviewer harness was not persisted: session=%+v stored=%+v", sess, st.sessions["mer-1"])
	}
	if got := st.sessions["mer-2"].ReviewerHarness; got != "" {
		t.Fatalf("other session reviewer harness = %q, want empty", got)
	}
}

func TestSessionSetReviewerHarnessRejectsUnknownHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1"}
	if _, err := (&Service{store: st}).SetReviewerHarness(context.Background(), "mer-1", "unknown", domain.AgentConfig{}); err == nil {
		t.Fatal("expected invalid harness error")
	}
}

func TestSessionSetReviewerHarnessAllowsConfigWithoutHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1"}

	sess, err := (&Service{store: st}).SetReviewerHarness(context.Background(), "mer-1", "", domain.AgentConfig{Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ReviewerHarness != "" || sess.ReviewerConfig.Model != "gpt-5" {
		t.Fatalf("session reviewer override = (%q, %+v), want default harness with model override", sess.ReviewerHarness, sess.ReviewerConfig)
	}
	if stored := st.sessions["mer-1"]; stored.ReviewerHarness != "" || stored.ReviewerConfig.Model != "gpt-5" {
		t.Fatalf("stored reviewer override = (%q, %+v), want default harness with model override", stored.ReviewerHarness, stored.ReviewerConfig)
	}
}

func TestSessionSetAutoReviewPersistsToggle(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	svc := &Service{store: st}

	for _, enabled := range []bool{true, false} {
		sess, err := svc.SetAutoReview(context.Background(), "mer-1", enabled)
		if err != nil {
			t.Fatal(err)
		}
		if sess.AutoReviewEnabled != enabled || st.sessions["mer-1"].AutoReviewEnabled != enabled {
			t.Fatalf("auto review=%v, want %v", sess.AutoReviewEnabled, enabled)
		}
	}
}

func TestSessionRenameMissingSessionReturnsNotFound(t *testing.T) {
	t.Parallel()

	st := newFakeStore()

	err := (&Service{store: st}).Rename(context.Background(), "mer-404", "Missing")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

// TestGetBackfillsEmptyArtifactDirOnRead covers the review-flagged gap: a
// session row created before artifact_dir existed carries it as ” (the
// migration's default), even though session_manager always prompts the
// agent to write into the deterministic dataDir/artifacts/<id> path. Get
// must fall back to that derived path immediately, rather than showing no
// artifacts (and a broken preview root) until the artifact poller's next
// tick persists the backfill.
func TestGetBackfillsEmptyArtifactDirOnRead(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: ""},
	}

	got, err := (&Service{store: st, dataDir: dataDir}).Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.ArtifactDir != artifactDir {
		t.Fatalf("ArtifactDir = %q, want backfilled %q", got.Metadata.ArtifactDir, artifactDir)
	}
	if len(got.ArtifactFiles) != 1 || got.ArtifactFiles[0].Name != "report.html" {
		t.Fatalf("ArtifactFiles = %+v, want [report.html]", got.ArtifactFiles)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("OutputType = %q, want %q reflected in this same response, not just a future one", got.OutputType, domain.SessionOutputArtifact)
	}
}

// TestGetBackfillsArtifactDirAndReconcilesEvenForTerminatedSessions is the
// review regression for a gap in the read-triggered backfill above: the
// artifact-output poller (observe/artifacts.Observer) explicitly skips
// terminated sessions, so a terminated legacy row (ArtifactDir == "" from
// before that column existed) could never get its durable OutputType
// repaired through the poller alone, leaving its real artifact files hidden
// from anything that filters on OutputType. Get must trigger the durable
// reconcile unconditionally of IsTerminated.
func TestGetBackfillsArtifactDirAndReconcilesEvenForTerminatedSessions(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: ""},
	}

	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir, OutputTypeReconciler: reconciler})

	got, err := svc.Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.ArtifactDir != artifactDir {
		t.Fatalf("ArtifactDir = %q, want backfilled %q even though the session is terminated", got.Metadata.ArtifactDir, artifactDir)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("OutputType = %q, want %q", got.OutputType, domain.SessionOutputArtifact)
	}
	if len(reconciler.reconciled) != 1 || reconciler.reconciled[0] != "mer-1" {
		t.Fatalf("reconciled = %v, want the durable reconcile triggered for the terminated session too", reconciler.reconciled)
	}
}

// A terminated session whose agent wrote an artifact before any reconcile ran
// keeps a persisted ArtifactDir but a stale OutputType of none, and the
// artifact poller skips terminated sessions. Get must repair it durably.
func TestGetReconcilesTerminatedSessionWithPersistedDirAndUnreconciledArtifact(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "notes.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		IsTerminated: true,
		OutputType:   domain.SessionOutputNone,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: artifactDir},
	}

	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir, OutputTypeReconciler: reconciler})

	got, err := svc.Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("OutputType = %q, want %q", got.OutputType, domain.SessionOutputArtifact)
	}
	if len(reconciler.reconciled) != 1 || reconciler.reconciled[0] != "mer-1" {
		t.Fatalf("reconciled = %v, want the durable reconcile triggered on read", reconciler.reconciled)
	}
}

func TestGetToleratesUnwalkableArtifactRoot(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not enforced on Windows")
	}
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(artifactDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(artifactDir, 0o755) })

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: artifactDir},
	}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir})

	got, err := svc.Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Get failed on an unwalkable artifact root: %v", err)
	}
	if len(got.ArtifactFiles) != 0 {
		t.Fatalf("ArtifactFiles = %+v, want empty", got.ArtifactFiles)
	}
}

func TestGetDoesNotReconcileWhenPersistedOutputTypeAlreadyHasArtifact(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "notes.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		OutputType: domain.SessionOutputArtifact,
		Metadata:   domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: artifactDir},
	}

	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir, OutputTypeReconciler: reconciler})
	if _, err := svc.Get(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(reconciler.reconciled) != 0 {
		t.Fatalf("reconciled = %v, want none for an already-reconciled session", reconciler.reconciled)
	}
}

func TestGetReconcilesWhenPersistedArtifactOutputHasNoFilesLeft(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		OutputType: domain.SessionOutputArtifact,
		Metadata:   domain.SessionMetadata{WorkspacePath: "/ws", ArtifactDir: artifactDir},
	}
	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir, OutputTypeReconciler: reconciler})

	got, err := svc.Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputType.HasArtifact() {
		t.Fatalf("OutputType = %q, want no artifact once the directory is empty", got.OutputType)
	}
	if len(reconciler.reconciled) != 1 {
		t.Fatalf("reconciled = %v, want the removal persisted", reconciler.reconciled)
	}
}

func TestSessionReadsExposeOnlyCurrentTerminatedCleanupFacts(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, CleanupGeneration: 2}
	st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{
		"mer-1": {SessionID: "mer-1", SessionGeneration: 2, WorkspaceDisposition: domain.DispositionFailed},
	}
	svc := &Service{store: st}
	for _, tc := range []struct {
		terminated bool
		generation int64
		want       domain.WorkspaceDisposition
	}{
		{true, 2, domain.DispositionFailed}, {false, 2, ""}, {true, 3, ""},
	} {
		rec := st.sessions["mer-1"]
		rec.IsTerminated, rec.CleanupGeneration = tc.terminated, tc.generation
		st.sessions[rec.ID] = rec
		got, err := svc.Get(context.Background(), rec.ID)
		if err != nil || got.WorkspaceCleanup != tc.want {
			t.Fatalf("cleanup=%q want=%q err=%v", got.WorkspaceCleanup, tc.want, err)
		}
		list, err := svc.List(context.Background(), ListFilter{})
		if err != nil || len(list) != 1 || list[0].WorkspaceCleanup != tc.want {
			t.Fatalf("list=%+v err=%v", list, err)
		}
	}
}

func TestDeleteArtifactsDeletesThroughTheDeleterAndReconciles(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		OutputType: domain.SessionOutputArtifact,
	}
	deleter := &fakeArtifactDeleter{}
	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: t.TempDir(), OutputTypeReconciler: reconciler, ArtifactDeleter: deleter})

	if err := svc.DeleteArtifacts(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(deleter.deleted) != 1 || deleter.deleted[0] != "mer-1" {
		t.Fatalf("deleted = %v, want [mer-1]", deleter.deleted)
	}
	if len(reconciler.reconciled) != 1 || reconciler.reconciled[0] != "mer-1" {
		t.Fatalf("reconciled = %v, want [mer-1]", reconciler.reconciled)
	}
}

func TestDeleteArtifactsReconcilesAndReportsAFailedDelete(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	deleter := &fakeArtifactDeleter{err: errors.New("file in use")}
	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{Store: st, DataDir: t.TempDir(), OutputTypeReconciler: reconciler, ArtifactDeleter: deleter})

	if err := svc.DeleteArtifacts(context.Background(), "mer-1"); err == nil {
		t.Fatal("DeleteArtifacts succeeded, want the deleter's error")
	}
	if len(reconciler.reconciled) != 1 {
		t.Fatalf("reconciled = %v, want a reconcile after the failure too", reconciler.reconciled)
	}
}

func TestDeleteArtifactsRefusesLiveSession(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	deleter := &fakeArtifactDeleter{}
	svc := NewWithDeps(Deps{Store: st, DataDir: t.TempDir(), ArtifactDeleter: deleter})

	err := svc.DeleteArtifacts(context.Background(), "mer-1")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_NOT_TERMINATED" {
		t.Fatalf("err = %v, want SESSION_NOT_TERMINATED", err)
	}
	if len(deleter.deleted) != 0 {
		t.Fatalf("deleted = %v for a live session, want none", deleter.deleted)
	}
}

func TestDeleteArtifactsRefusesAnArtifactDirOutsideTheDataDir(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{ArtifactDir: filepath.Join(t.TempDir(), "old-data", "artifacts", "mer-1")},
	}
	deleter := &fakeArtifactDeleter{}
	svc := NewWithDeps(Deps{Store: st, DataDir: t.TempDir(), ArtifactDeleter: deleter})

	if err := svc.DeleteArtifacts(context.Background(), "mer-1"); err == nil {
		t.Fatal("DeleteArtifacts succeeded, want a refusal")
	}
	if len(deleter.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", deleter.deleted)
	}
}

func TestDeleteArtifactsAcceptsTheCanonicalArtifactDir(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{ArtifactDir: filepath.Join(dataDir, "artifacts", "mer", "mer-1")},
	}
	deleter := &fakeArtifactDeleter{}
	svc := NewWithDeps(Deps{Store: st, DataDir: dataDir, ArtifactDeleter: deleter})

	if err := svc.DeleteArtifacts(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(deleter.deleted) != 1 {
		t.Fatalf("deleted = %v, want [mer-1]", deleter.deleted)
	}
}

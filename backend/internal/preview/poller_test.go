package preview

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakePreviewSessions struct {
	sessions  []domain.SessionRecord
	sets      []previewSet
	listCalls int
	getCalls  []domain.SessionID
}

type previewSet struct {
	id  domain.SessionID
	url string
}

// ListLivePreviewSessions applies the store query's filter, so a session whose
// preview was cleared is reachable only through GetSession.
func (f *fakePreviewSessions) ListLivePreviewSessions(_ context.Context) ([]domain.SessionRecord, error) {
	f.listCalls++
	var out []domain.SessionRecord
	for _, sess := range f.sessions {
		if !sess.IsTerminated && sess.Kind == domain.KindWorker && sess.Metadata.PreviewURL != "" {
			out = append(out, sess)
		}
	}
	return out, nil
}

func (f *fakePreviewSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.getCalls = append(f.getCalls, id)
	for _, sess := range f.sessions {
		if sess.ID == id {
			return sess, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakePreviewSessions) SetPreview(_ context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	f.sets = append(f.sets, previewSet{id: id, url: previewURL})
	for i, sess := range f.sessions {
		if sess.ID == id {
			sess.Metadata.PreviewURL = previewURL
			f.sessions[i] = sess
			return domain.Session{SessionRecord: sess}, nil
		}
	}
	return domain.Session{}, nil
}

func TestPollerDoesNotOpenUntargetedWorkerEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc, poller := newWorkerPoller(workspace, "")

	mustPoll(t, poller, "Poll")

	assertSets(t, svc.sets)
}

func TestPollerNormalizesExplicitWorkspaceEntrypoint(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc, poller := newWorkerPoller(workspace, "dist/index.html")

	mustPoll(t, poller, "Poll")

	assertOnlySet(t, svc, "dist/index.html")
}

func TestPollerDoesNotReplaceExplicitEntrypointWithAnotherStaticFile(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "public", "index.html"), "<main>public</main>")
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc, poller := newWorkerPoller(workspace, "dist/index.html")

	mustPoll(t, poller, "Poll")

	assertOnlySet(t, svc, "dist/index.html")
}

func TestPollerRefreshesOnlyWhenEntrypointChanges(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc, poller := newWorkerPoller(workspace, "index.html")

	mustPoll(t, poller, "first Poll")
	mustPoll(t, poller, "second Poll")
	if len(svc.sets) != 1 {
		t.Fatalf("sets after unchanged entry = %#v, want one set", svc.sets)
	}

	writeFile(t, entry, "<main>v2 changed</main>")
	nextMod := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(entry, nextMod, nextMod); err != nil {
		t.Fatalf("chtimes entry: %v", err)
	}
	mustPoll(t, poller, "third Poll")

	if len(svc.sets) != 2 {
		t.Fatalf("sets after changed entry = %#v, want refresh set", svc.sets)
	}
}

func TestPollerRediscoverEntryAfterDeleteAndRecreate(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc, poller := newWorkerPoller(workspace, "index.html")

	// First poll normalizes the explicitly selected workspace entry.
	mustPoll(t, poller, "first Poll")
	wantURL := mustFileURL(t, "http://127.0.0.1:3101", "tp-1", "index.html")
	assertSets(t, svc.sets, previewSet{id: "tp-1", url: wantURL})

	// Delete the entry — poller must clear the preview and mark the session cleared.
	if err := os.Remove(entry); err != nil {
		t.Fatalf("remove index.html: %v", err)
	}
	mustPoll(t, poller, "second Poll (delete)")
	if len(svc.sets) != 2 {
		t.Fatalf("sets after delete = %#v, want clear + set", svc.sets)
	}
	if svc.sets[1].url != "" {
		t.Fatalf("second set.url = %q, want empty (clear)", svc.sets[1].url)
	}

	// Recreate the entry — poller must re-discover.
	mustPoll(t, poller, "third Poll (still deleted)")
	if len(svc.sets) != 2 {
		t.Fatalf("sets while entry remains deleted = %#v, want no repeated clear", svc.sets)
	}

	writeFile(t, entry, "<main>v2</main>")
	mustPoll(t, poller, "fourth Poll (recreate)")
	if len(svc.sets) != 3 {
		t.Fatalf("sets after recreate = %#v, want 3 sets (discover + clear + rediscover)", svc.sets)
	}
	if svc.sets[2].url != wantURL {
		t.Fatalf("third set.url = %q, want %q", svc.sets[2].url, wantURL)
	}
}

func TestPollerDoesNotRestoreClearedPreviewAfterRestart(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	sess := workerSession("tp-1", workspace, "")
	sess.Metadata.PreviewRevision = 2
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{sess}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3101", PollerConfig{Logger: discardLogger()})

	mustPoll(t, poller, "Poll")

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want cleared preview to remain empty after restart", svc.sets)
	}
}

func TestPollerDoesNotOverrideExplicitPreviewTarget(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc, poller := newWorkerPoller(workspace, "file:///C:/tmp/other.html")

	mustPoll(t, poller, "Poll")

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no automatic override", svc.sets)
	}
}

func TestPollerMigratesLegacyWorkspacePreviewURL(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	legacy := "http://127.0.0.1:3101/api/v1/sessions/tp-1/preview/files/docs/report.html"
	svc, poller := newWorkerPoller(workspace, legacy)

	mustPoll(t, poller, "Poll")

	assertOnlySet(t, svc, "docs/report.html")
}

func TestPollerPreservesStoredRelativeEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>default</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	svc, poller := newWorkerPoller(workspace, "docs/report.html")

	mustPoll(t, poller, "Poll")

	assertOnlySet(t, svc, "docs/report.html")
}

func TestPollerRewritesStoredOriginToActualDaemonPortWithoutChangingEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>default</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	old := mustFileURL(t, "http://127.0.0.1:3101", "tp-1", "docs/report.html")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("tp-1", workspace, old)}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:49152", PollerConfig{Logger: discardLogger()})

	mustPoll(t, poller, "Poll")

	assertSets(t, svc.sets, previewSet{
		id:  "tp-1",
		url: mustFileURL(t, "http://127.0.0.1:49152", "tp-1", "docs/report.html"),
	})
}

func TestPollerDoesNotAutoPreviewMarkdownOnlyNewSession(t *testing.T) {
	// A brand-new session (never explicitly previewed) whose workspace contains
	// only Markdown documents — no index.html — must NOT be auto-previewed.
	// Surfacing an arbitrary repo README as the initial browser target is noise.
	// See issue #2859.
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "README.md"), "# project")
	writeFile(t, filepath.Join(workspace, "docs", "setup.md"), "# setup")
	svc, poller := newWorkerPoller(workspace, "")

	mustPoll(t, poller, "Poll")
	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no auto-preview for a Markdown-only new session", svc.sets)
	}
}

func TestPollerKeepsMarkdownPreviewFreshOnceExplicitlySet(t *testing.T) {
	// Once a session has been explicitly previewed against a workspace Markdown
	// file (workspaceOwned), the poller must still keep that preview fresh — the
	// document-fallback restriction only applies to never-previewed sessions.
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "docs", "report.md"), "# report")
	relative := "docs/report.md"
	svc, poller := newWorkerPoller(workspace, relative)

	mustPoll(t, poller, "Poll")
	assertSets(t, svc.sets, previewSet{
		id:  "tp-1",
		url: mustFileURL(t, "http://127.0.0.1:3101", "tp-1", relative),
	})
}

func TestPollerSkipsNonWorkerSessions(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{{
		ID:   "tp-orch",
		Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
		},
	}}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3101", PollerConfig{Logger: discardLogger()})

	mustPoll(t, poller, "Poll")

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no preview updates for orchestrator sessions", svc.sets)
	}
}

func workerSession(id domain.SessionID, workspace, previewURL string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:   id,
		Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
			PreviewURL:    previewURL,
		},
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertSets(t *testing.T, got []previewSet, want ...previewSet) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sets[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPollerReadsByIDOnlySessionsItCleared pins the scan's cost: a steady poll
// is one listing of the live previewing workers, and a session is read by id
// only while the poller itself holds its preview cleared.
func TestPollerReadsByIDOnlySessionsItCleared(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	blank := workerSession("tp-2", workspace, "")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("tp-1", workspace, "index.html"), blank}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3101", PollerConfig{Logger: discardLogger()})
	for range 3 {
		if err := poller.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if svc.listCalls != 3 || len(svc.getCalls) != 0 {
		t.Fatalf("steady polls: list calls = %d, by-id reads = %v; want 3 and none", svc.listCalls, svc.getCalls)
	}

	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := poller.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The clear poll lists tp-1 itself; only the next poll needs it by id.
	if len(svc.getCalls) != 1 || svc.getCalls[0] != "tp-1" {
		t.Fatalf("by-id reads while cleared = %v, want only tp-1 once", svc.getCalls)
	}

	// Once the session ends it is no longer tracked or read.
	svc.sessions[0].IsTerminated = true
	for range 2 {
		if err := poller.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(svc.getCalls) != 2 {
		t.Fatalf("by-id reads after termination = %v, want the one that saw it end", svc.getCalls)
	}
}

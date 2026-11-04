package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

type handoffFixture struct {
	svc  *Service
	st   *fakeStore
	fc   *fakeCommander
	note string
}

// newHandoffFixture: project "mer" with an orchestrator agent, active
// orchestrator mer-1 with a note in its artifact dir, and a running worker.
func newHandoffFixture(t *testing.T, orchestratorHarness domain.AgentHarness) *handoffFixture {
	t.Helper()
	dataDir := t.TempDir()
	st := newFakeStore()
	var cfg domain.ProjectConfig
	cfg.Orchestrator.Harness = orchestratorHarness
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}
	st.sessions["mer-w"] = domain.SessionRecord{ID: "mer-w", ProjectID: "mer", Kind: domain.KindWorker}
	dir := sessionartifacts.Dir(dataDir, "mer-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "state.md")
	if err := os.WriteFile(note, []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCommander{}
	fc.retireFunc = func(_ context.Context, id domain.SessionID) error {
		fc.retired = append(fc.retired, id)
		rec := st.sessions[id]
		rec.IsTerminated = true
		st.sessions[id] = rec
		return nil
	}
	fc.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{ID: "mer-2", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: orchestratorHarness, Mode: cfg.RequestedMode}
		st.sessions[rec.ID] = rec
		return rec
	}
	// Resolve the temp dir the way handoffNote does (macOS /var -> /private/var).
	resolved, err := filepath.EvalSymlinks(note)
	if err != nil {
		t.Fatal(err)
	}
	return &handoffFixture{svc: &Service{manager: fc, store: st, dataDir: dataDir}, st: st, fc: fc, note: resolved}
}

func TestHandoffOrchestratorReplacesAndSeedsTheNewOne(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	got, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", f.note)
	if err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if got.ID != "mer-2" {
		t.Fatalf("new id = %q, want mer-2", got.ID)
	}
	if len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("retired = %v, want only the old orchestrator (workers keep running)", f.fc.retired)
	}
	if len(f.fc.killed) != 0 || f.st.sessions["mer-w"].IsTerminated {
		t.Fatalf("the worker must not be touched: killed=%v", f.fc.killed)
	}
	want := "Read " + f.note + " and continue. You replaced orchestrator mer-1; worker reports now come to you."
	if f.fc.spawnedCfg.Prompt != want {
		t.Fatalf("seed prompt = %q, want %q", f.fc.spawnedCfg.Prompt, want)
	}
	if f.fc.spawnedCfg.Kind != domain.KindOrchestrator || f.fc.spawnedCfg.RequestedMode != domain.SessionModeChat {
		t.Fatalf("spawn cfg = %+v, want an orchestrator in the old one's mode", f.fc.spawnedCfg)
	}
	if len(f.fc.sent) != 0 {
		t.Fatalf("the retiring orchestrator wrote the note itself; no retire notice expected: %v", f.fc.sent)
	}
}

func TestHandoffOrchestratorIsIdempotent(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	first, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", f.note)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", f.note)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second.ID != first.ID || f.fc.spawnCalls != 1 {
		t.Fatalf("second call = %q (spawns=%d), want the same id %q and a single spawn", second.ID, f.fc.spawnCalls, first.ID)
	}
}

func TestHandoffOrchestratorRefusesWithoutAnOrchestratorAgentAndKeepsTheOld(t *testing.T) {
	f := newHandoffFixture(t, "")
	_, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", f.note)
	if err == nil || !strings.Contains(err.Error(), "no orchestrator agent") {
		t.Fatalf("err = %v, want the no-orchestrator-agent refusal", err)
	}
	if len(f.fc.retired) != 0 || f.fc.spawnCalls != 0 || f.st.sessions["mer-1"].IsTerminated {
		t.Fatalf("a refused handoff must leave the old orchestrator running: retired=%v", f.fc.retired)
	}
}

func TestHandoffOrchestratorNoteMustBeInsideTheArtifactDir(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	outside := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(sessionartifacts.Dir(f.svc.dataDir, "mer-2"), "note.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(f.note), "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"outside the data dir": outside,
		"another session's":    other,
		"traversal":            filepath.Join(filepath.Dir(f.note), "..", "mer-2", "note.md"),
		"symlink out":          link,
		"relative":             "state.md",
		"the dir itself":       filepath.Dir(f.note),
		"missing":              filepath.Join(filepath.Dir(f.note), "nope.md"),
	} {
		if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", path); err == nil {
			t.Errorf("%s: handoff accepted %q", name, path)
		}
	}
	if len(f.fc.retired) != 0 {
		t.Fatalf("a rejected note must not retire anything: %v", f.fc.retired)
	}
}

func TestHandoffOrchestratorDefaultsToTheNewestNote(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", ""); err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if !strings.Contains(f.fc.spawnedCfg.Prompt, "state.md") {
		t.Fatalf("prompt = %q, want the newest note", f.fc.spawnedCfg.Prompt)
	}
	empty := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := os.Remove(empty.note); err != nil {
		t.Fatal(err)
	}
	if _, err := empty.svc.HandoffOrchestrator(context.Background(), "mer-1", ""); err == nil || !strings.Contains(err.Error(), "no state note") {
		t.Fatalf("err = %v, want a note-required refusal", err)
	}
}

func TestHandoffOrchestratorOnlyForOrchestrators(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	workerNote := filepath.Join(sessionartifacts.Dir(f.svc.dataDir, "mer-w"), "state.md")
	if err := os.MkdirAll(filepath.Dir(workerNote), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workerNote, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if realNote, err := filepath.EvalSymlinks(workerNote); err == nil {
		workerNote = realNote
	}
	if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-w", workerNote); err == nil {
		t.Fatal("a worker was handed off")
	}
	if len(f.fc.retired) != 0 {
		t.Fatalf("retired = %v", f.fc.retired)
	}
}

func TestHandoffOrchestratorDefaultNoteSkipsNonTextFiles(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	dir := filepath.Dir(f.note)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(f.note, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", ""); err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if !strings.Contains(f.fc.spawnedCfg.Prompt, "state.md") || strings.Contains(f.fc.spawnedCfg.Prompt, "shot.png") {
		t.Fatalf("prompt = %q, want the older .md over the newer .png", f.fc.spawnedCfg.Prompt)
	}
	// Only non-notes left: still note-required.
	if err := os.Remove(f.note); err != nil {
		t.Fatal(err)
	}
	other := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := os.Rename(other.note, filepath.Join(filepath.Dir(other.note), "shot.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := other.svc.HandoffOrchestrator(context.Background(), "mer-1", ""); err == nil || !strings.Contains(err.Error(), "no state note") {
		t.Fatalf("err = %v, want note-required when only non-text files exist", err)
	}
}

// A handoff keeps the old orchestrator's lane as recorded and does not resolve
// it against the backlog, so it still works after the lane's item was closed
// or deleted.
func TestHandoffOrchestratorKeepsTheLaneOfAClosedItem(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	f.st.lanes = map[domain.SessionID]domain.SessionLane{"mer-1": {LaneItemID: "closed-lane"}}
	if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", f.note); err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if f.fc.spawnedCfg.LaneItemID != "closed-lane" {
		t.Fatalf("successor lane = %q, want closed-lane", f.fc.spawnedCfg.LaneItemID)
	}
}

// B241: an exited default orchestrator is replaced, not reused.
func TestSpawnOrchestratorReplacesExitedDefault(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	rec := f.st.sessions["mer-1"]
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	f.st.sessions["mer-1"] = rec
	got, err := f.svc.SpawnOrchestrator(context.Background(), "mer", false, "", "")
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if got.ID != "mer-2" || len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("got %q retired %v, want mer-2 replacing exited mer-1", got.ID, f.fc.retired)
	}
}

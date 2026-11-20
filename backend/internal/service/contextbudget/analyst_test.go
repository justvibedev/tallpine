package contextbudget

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type analystRig struct {
	*rig
	spawns []string
	ended  bool
	killed []domain.SessionID
	limits domain.AgentAccountLimits
	lerr   error
}

func win(five, week float64) domain.AgentAccountLimits {
	return domain.AgentAccountLimits{Windows: []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: five}, {ID: "seven_day", UsedPercent: week}}}
}

func newAnalystRig(t *testing.T) *analystRig {
	t.Helper()
	a := &analystRig{rig: newRig(t, true), limits: win(10, 10)}
	a.analyst = &Analyst{
		Root: a.root, Log: slog.Default(),
		Limits: func(context.Context) (domain.AgentAccountLimits, error) { return a.limits, a.lerr },
		Spawn: func(_ context.Context, _ domain.ProjectID, prompt string) (domain.SessionID, error) {
			a.spawns = append(a.spawns, prompt)
			return "s1", nil
		},
		Ended: func(context.Context, domain.SessionID) bool { return a.ended },
		Kill: func(_ context.Context, id domain.SessionID) error {
			a.killed = append(a.killed, id)
			return nil
		},
	}
	return a
}

func (a *analystRig) outDir(t *testing.T) string {
	t.Helper()
	run, ok := a.analyst.load(proj)
	if !ok {
		t.Fatal("no analyst run recorded")
	}
	return run.OutDir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalystGate(t *testing.T) {
	cases := []struct {
		name string
		l    domain.AgentAccountLimits
		want bool
	}{
		{"room", win(49, 79), true},
		{"five hour at 50", win(50, 10), false},
		{"week at 80", win(10, 80), false},
		{"no windows", domain.AgentAccountLimits{}, false},
		{"week missing", domain.AgentAccountLimits{Windows: []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: 1}}}, false},
	}
	for _, c := range cases {
		if got := AnalystGate(c.l); got != c.want {
			t.Errorf("%s: gate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestQueuedBudgetFindingSpawnsOneAnalystAndHoldsItsCard(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	if len(a.spawns) != 1 {
		t.Fatalf("spawns = %d, want 1", len(a.spawns))
	}
	if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingQueued || len(a.asksList()) != 0 {
		t.Fatalf("finding %s asks %d: the card must wait for the analyst", f.State, len(a.asksList()))
	}
	a.sweep(25 * time.Hour) // still running, daily check again
	if len(a.spawns) != 1 {
		t.Fatalf("spawns = %d after the next day, want still 1", len(a.spawns))
	}
}

func TestSecondAnalystWithinSevenDaysIsNotStarted(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	a.ended = true
	a.sweep(25 * time.Hour) // ingests (nothing written), files the card
	// A new cycle: back under budget, then over again.
	a.claude = "short"
	a.sweep(25 * time.Hour)
	a.claude = strings.Repeat("a", 3300)
	a.sweep(25 * time.Hour)
	if len(a.spawns) != 1 {
		t.Fatalf("spawns = %d inside 7 days, want 1", len(a.spawns))
	}
	a.sweep(8 * 24 * time.Hour)
	a.claude = "short"
	a.sweep(25 * time.Hour)
	a.claude = strings.Repeat("a", 3300)
	a.sweep(25 * time.Hour)
	if len(a.spawns) != 2 {
		t.Fatalf("spawns = %d after 7 days, want 2", len(a.spawns))
	}
}

func TestNoRoomOrUnreadableLimitsSpawnNothingAndStillCard(t *testing.T) {
	for name, set := range map[string]func(*analystRig){
		"five hour busy": func(a *analystRig) { a.limits = win(50, 10) },
		"week busy":      func(a *analystRig) { a.limits = win(10, 80) },
		"unreadable":     func(a *analystRig) { a.lerr = errors.New("cli failed") },
	} {
		a := newAnalystRig(t)
		set(a)
		a.sweep(0)
		if len(a.spawns) != 0 {
			t.Errorf("%s: spawned %d analysts", name, len(a.spawns))
		}
		if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen {
			t.Errorf("%s: finding %s, want open with evidence only", name, f.State)
		}
	}
}

func TestValidAnalystFindingsOpenTheCardWithThePatch(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	f := a.findings()["instruction_budget"]
	out := a.outDir(t)
	writeFile(t, out, "trim.patch", "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n")
	body, _ := json.Marshal(map[string]any{"findings": []any{map[string]any{"id": f.ID, "summary": "move detail out", "patch": "trim.patch"}}})
	writeFile(t, out, "findings.json", string(body))
	a.ended = true
	a.sweep(time.Hour)
	f = a.findings()["instruction_budget"]
	if f.State != domain.ContextFindingOpen || f.ArtifactDir == "" {
		t.Fatalf("finding = %s dir %q, want open with an artifact dir", f.State, f.ArtifactDir)
	}
	if b, err := os.ReadFile(filepath.Join(f.ArtifactDir, AnalystPatchFile)); err != nil || !strings.Contains(string(b), "CLAUDE.md") {
		t.Fatalf("patch file: %q %v", b, err)
	}
	in, brief := Brief(f)
	if !strings.Contains(brief, AnalystPatchFile) || in.Title == "" {
		t.Fatalf("brief does not name the patch: %s", brief)
	}
}

func TestInvalidAnalystFindingsOpenEvidenceOnly(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	writeFile(t, a.outDir(t), "findings.json", "{not json")
	a.ended = true
	a.sweep(time.Hour)
	f := a.findings()["instruction_budget"]
	if f.State != domain.ContextFindingOpen {
		t.Fatalf("finding = %s, want open", f.State)
	}
	if _, err := os.Stat(filepath.Join(f.ArtifactDir, AnalystPatchFile)); err == nil {
		t.Fatal("a patch exists for an invalid findings.json")
	}
}

func TestAnalystThatEndedWithoutFilesStillCards(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	a.ended = true
	a.sweep(time.Hour)
	if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen {
		t.Fatalf("finding = %s, want open", f.State)
	}
}

func TestMemoryActionsAreCappedArchiveOnlyAndFeedbackUserOnlyProposed(t *testing.T) {
	a := newAnalystRig(t)
	a.claude = "short"
	a.memory = &Memory{Dir: "/m", IndexTokens: 9000, IndexBytes: 27000, Files: 40, DirTokens: 50000}
	a.sweep(0)
	f := a.findings()["memory_budget"]
	var acts []map[string]string
	for i := range 14 {
		typ := "project"
		if i == 0 {
			typ = "feedback"
		}
		if i == 1 {
			typ = "user"
		}
		acts = append(acts, map[string]string{"op": "archive", "file": "e" + string(rune('a'+i)) + ".md", "type": typ, "reason": "old"})
	}
	acts = append([]map[string]string{{"op": "delete", "file": "gone.md", "type": "project"}, {"op": "archive", "file": "../escape.md", "type": "project"}}, acts...)
	body, _ := json.Marshal(map[string]any{"findings": []any{map[string]any{"id": f.ID, "summary": "archive", "actions": acts}}})
	writeFile(t, a.outDir(t), "findings.json", string(body))
	a.ended = true
	a.sweep(time.Hour)
	f = a.findings()["memory_budget"]
	b, err := os.ReadFile(filepath.Join(f.ArtifactDir, MemoryActionsFile))
	if err != nil {
		t.Fatal(err)
	}
	var got []Action
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxMemoryActions {
		t.Fatalf("actions = %d, want %d", len(got), MaxMemoryActions)
	}
	for i, ac := range got {
		if ac.Op != "archive" || strings.Contains(ac.File, "/") || ac.File == "gone.md" {
			t.Fatalf("action %d = %+v: only archive of a plain name is allowed", i, ac)
		}
		if want := i < 2; ac.ProposeOnly != want {
			t.Fatalf("action %d (%s) proposeOnly = %v, want %v", i, ac.Type, ac.ProposeOnly, want)
		}
	}
}

func TestAnalystPromptForbidsTrimmingRulesAndMessaging(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	p := a.spawns[0]
	for _, want := range []string{"Never trim or weaken a behavioral rule", "do not message anyone", a.outDir(t)} {
		if !strings.Contains(p, want) {
			t.Errorf("task prompt lacks %q", want)
		}
	}
}

func TestIngestionStopsTheAnalystSession(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	a.ended = true
	a.sweep(time.Hour)
	if len(a.killed) != 1 || a.killed[0] != "s1" {
		t.Fatalf("killed = %v, want [s1]", a.killed)
	}
}

func TestIdleAnalystThatWroteFindingsIsIngestedWithoutEnding(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	out := a.outDir(t)
	writeFile(t, out, "findings.json", `{"findings":[]}`)
	old := a.now.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(out, "findings.json"), old, old); err != nil {
		t.Fatal(err)
	}
	a.sweep(time.Hour) // the session is still "running" (idle)
	if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen {
		t.Fatalf("finding = %s, want open once findings.json settled", f.State)
	}
	if len(a.killed) != 1 {
		t.Fatalf("killed = %v, want the idle analyst stopped", a.killed)
	}
}

func TestResolvedQueuedFindingSpawnsNoAnalyst(t *testing.T) {
	a := newAnalystRig(t)
	a.claude = "short"
	a.finder = []Finder{extra{n: 4}}
	a.limits = win(90, 10) // busy: four findings, three cards, one queued
	a.sweep(0)
	a.limits = win(10, 10)
	a.finder = []Finder{extra{n: 0}} // everything is back under budget in this sweep
	a.sweep(25 * time.Hour)
	if len(a.spawns) != 0 {
		t.Fatalf("spawns = %d for findings that were resolved in the same sweep", len(a.spawns))
	}
}

func TestQueuedFindingThatAlreadyHasADraftIsNotSentAgain(t *testing.T) {
	a := newAnalystRig(t)
	a.claude = "short"
	a.finder = []Finder{extra{n: 4}}
	a.sweep(0)
	var entries []any
	for _, f := range a.findings() {
		entries = append(entries, map[string]any{"id": f.ID, "summary": "draft"})
	}
	body, _ := json.Marshal(map[string]any{"findings": entries})
	writeFile(t, a.outDir(t), "findings.json", string(body))
	a.ended = true
	a.sweep(time.Hour) // three cards open, the fourth stays queued with its draft
	a.sweep(9 * 24 * time.Hour)
	if len(a.spawns) != 1 {
		t.Fatalf("spawns = %d, want the drafted queued finding left alone", len(a.spawns))
	}
}

func TestUnreadableAnalystStateCountsAsRecentRun(t *testing.T) {
	a := newAnalystRig(t)
	dir := filepath.Join(a.root, proj, "context-health")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, analystStateFile, "{garbage")
	a.sweep(0)
	if len(a.spawns) != 0 {
		t.Fatalf("spawns = %d with an unreadable state file, want 0", len(a.spawns))
	}
}

func TestRunIsRecordedBeforeTheAnalystStarts(t *testing.T) {
	a := newAnalystRig(t)
	inner := a.analyst.Spawn
	a.analyst.Spawn = func(ctx context.Context, p domain.ProjectID, prompt string) (domain.SessionID, error) {
		if _, ok := a.analyst.load(p); !ok {
			t.Error("no run record when the analyst started")
		}
		return inner(ctx, p, prompt)
	}
	a.sweep(0)
	if len(a.spawns) != 1 {
		t.Fatalf("spawns = %d", len(a.spawns))
	}
}

func TestFailedAnalystStartLeavesNoRunRecord(t *testing.T) {
	a := newAnalystRig(t)
	a.analyst.Spawn = func(context.Context, domain.ProjectID, string) (domain.SessionID, error) {
		return "", errors.New("no slot")
	}
	a.sweep(0)
	if _, ok := a.analyst.load(proj); ok || a.analyst.ranRecently(proj, a.now) {
		t.Fatal("a failed start must not count as this week's analyst")
	}
	if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingOpen {
		t.Fatalf("finding = %s, want the evidence-only card", f.State)
	}
}

func TestPatchStillBeingWrittenIsNotReadYet(t *testing.T) {
	a := newAnalystRig(t)
	a.sweep(0)
	out := a.outDir(t)
	writeFile(t, out, "findings.json", `{"findings":[]}`)
	old := a.now.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(out, "findings.json"), old, old); err != nil {
		t.Fatal(err)
	}
	writeFile(t, out, "trim.patch", "--- partial") // modified just now, by the rig's clock
	recent := a.now.Add(59 * time.Minute)
	if err := os.Chtimes(filepath.Join(out, "trim.patch"), recent, recent); err != nil {
		t.Fatal(err)
	}
	a.sweep(time.Hour) // the patch is 0 s old: not settled
	if len(a.killed) != 0 {
		t.Fatalf("killed %v: read the output while a patch was still changing", a.killed)
	}
	if f := a.findings()["instruction_budget"]; f.State != domain.ContextFindingQueued {
		t.Fatalf("finding = %s, want still held", f.State)
	}
}

func TestWeeklyDigestGetsAnalystPatchAndCardCarriesIt(t *testing.T) {
	a := newAnalystRig(t)
	a.claude = "small"
	a.finder = []Finder{digestStub{[]domain.ContextHealthFinding{weeklyDigest()}}}
	a.sweep(0)
	f := a.findings()[DigestFingerprint]
	if f.State != domain.ContextFindingQueued || len(a.spawns) != 1 || !strings.Contains(a.spawns[0], "doc-pointer gaps") {
		t.Fatalf("state %s, spawns %d: the digest must wait for the analyst", f.State, len(a.spawns))
	}
	out := a.outDir(t)
	writeFile(t, out, "pointers.patch", "--- a/AGENTS.md\n+++ b/AGENTS.md\n")
	body, _ := json.Marshal(map[string]any{"findings": []any{map[string]any{"id": f.ID, "summary": "AGENTS.md: Docs site lives in justvibedev/tallpine-site", "patch": "pointers.patch"}}})
	writeFile(t, out, "findings.json", string(body))
	a.ended = true
	a.sweep(time.Hour)
	f = a.findings()[DigestFingerprint]
	if f.State != domain.ContextFindingOpen {
		t.Fatalf("state = %s, want open", f.State)
	}
	if b, err := os.ReadFile(filepath.Join(f.ArtifactDir, AnalystPatchFile)); err != nil || !strings.Contains(string(b), "AGENTS.md") {
		t.Fatalf("patch file: %q %v", b, err)
	}
	if _, brief := Brief(f); !strings.Contains(brief, AnalystPatchFile) {
		t.Fatalf("approving must hand the worker the patch: %s", brief)
	}
}

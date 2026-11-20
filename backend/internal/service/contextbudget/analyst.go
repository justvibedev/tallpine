package contextbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The analyst is a short-lived Haiku session the daemon starts at most once a
// project and week to draft patches for budget findings (B827, folds B555).
// It writes findings.json and patch files into its own directory; the daemon
// validates them, copies what is valid into each finding's artifact directory
// and files the cards. A session that wrote nothing usable costs the finding
// only its patch: the card is filed with the evidence alone.
const (
	analystEvery = 7 * 24 * time.Hour
	// analystHold is how long a finding waits for the analyst before its card
	// is filed without a patch.
	analystHold = 6 * time.Hour
	// analystSettle is how long findings.json must sit untouched before it is read.
	analystSettle = 2 * time.Minute
	// Quota ceilings of the account the analyst would run on.
	analystFiveHourMax = 50.0
	analystWeeklyMax   = 80.0

	// MaxMemoryActions caps the archive actions kept for one finding.
	MaxMemoryActions = 10
	maxPatchBytes    = 64 << 10
	maxSummaryRunes  = 2000
	analystStateFile = "analyst.json"
	// AnalystPatchFile and MemoryActionsFile are written into a finding's artifact directory.
	AnalystPatchFile  = "analyst.patch"
	AnalystNotesFile  = "analyst.md"
	MemoryActionsFile = "memory-actions.json"
)

// AnalystGate reports whether the account has room for the analyst: the
// five-hour window is under 50 % and the weekly window under 80 %. A window the
// provider did not report counts as no room: unreadable limits never spawn.
func AnalystGate(limits domain.AgentAccountLimits) bool {
	five, week := -1.0, -1.0
	for _, w := range limits.Windows {
		switch w.ID {
		case "five_hour":
			five = w.UsedPercent
		case "seven_day":
			week = w.UsedPercent
		}
	}
	return five >= 0 && week >= 0 && five < analystFiveHourMax && week < analystWeeklyMax
}

// Analyst starts and reads back the context analyst sessions.
type Analyst struct {
	// Root is the artifact root, the same as Deps.ArtifactRoot.
	Root string
	// Limits reads the usage windows of the account the analyst would use.
	Limits func(ctx context.Context) (domain.AgentAccountLimits, error)
	// Spawn starts a claude-code, haiku, low-effort context analyst with the prompt.
	Spawn func(ctx context.Context, project domain.ProjectID, prompt string) (domain.SessionID, error)
	// Ended reports whether the analyst session is over (or gone).
	Ended func(ctx context.Context, id domain.SessionID) bool
	// Kill stops the session and discards its worktree: a finished Claude
	// worker goes idle, it never ends on its own.
	Kill func(ctx context.Context, id domain.SessionID) error
	Log  *slog.Logger
}

type analystTarget struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Kind        string `json:"kind"`
}

type analystRun struct {
	SessionID string          `json:"sessionId"`
	SpawnedAt time.Time       `json:"spawnedAt"`
	OutDir    string          `json:"outDir"`
	Targets   []analystTarget `json:"targets"`
	Ingested  bool            `json:"ingested"`
}

func (a *Analyst) projectDir(project domain.ProjectID) string {
	return filepath.Join(a.Root, string(project), "context-health")
}

func (a *Analyst) load(project domain.ProjectID) (analystRun, bool) {
	b, err := os.ReadFile(filepath.Join(a.projectDir(project), analystStateFile))
	if err != nil {
		return analystRun{}, false
	}
	var r analystRun
	return r, json.Unmarshal(b, &r) == nil
}

func (a *Analyst) save(project domain.ProjectID, r analystRun) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(a.projectDir(project), analystStateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ranRecently is true when an analyst started inside the last 7 days. A state
// file that exists but cannot be read counts as a recent run: the spend gate
// fails closed.
func (a *Analyst) ranRecently(project domain.ProjectID, now time.Time) bool {
	if _, err := os.Stat(filepath.Join(a.projectDir(project), analystStateFile)); err != nil {
		return false
	}
	r, ok := a.load(project)
	return !ok || now.Sub(r.SpawnedAt) < analystEvery
}

// analyzable is the findings an analyst drafts for: budget findings waiting for a card.
func analyzable(f domain.ContextHealthFinding) bool {
	return f.State == domain.ContextFindingQueued && (f.Kind == domain.ContextInstructionBudget || f.Kind == domain.ContextMemoryBudget || f.Kind == domain.ContextRoutingPointer)
}

// Held is the finding ids a live analyst is still drafting for; their cards wait.
func (a *Analyst) Held(ctx context.Context, project domain.ProjectID, now time.Time) map[string]bool {
	r, ok := a.load(project)
	if !ok || r.Ingested || now.Sub(r.SpawnedAt) >= analystHold || a.Ended(ctx, domain.SessionID(r.SessionID)) {
		return nil
	}
	held := map[string]bool{}
	for _, t := range r.Targets {
		held[t.ID] = true
	}
	return held
}

// MaybeSpawn starts one analyst for the queued budget findings when none ran in
// the last 7 days and the account has room. It reports whether it started one.
func (a *Analyst) MaybeSpawn(ctx context.Context, project domain.ProjectID, findings []domain.ContextHealthFinding, now time.Time) bool {
	var targets []analystTarget
	var todo []domain.ContextHealthFinding
	for _, f := range findings {
		if analyzable(f) {
			targets = append(targets, analystTarget{ID: f.ID, Fingerprint: f.Fingerprint, Kind: string(f.Kind)})
			todo = append(todo, f)
		}
	}
	if len(targets) == 0 {
		return false
	}
	if a.ranRecently(project, now) {
		return false
	}
	limits, err := a.Limits(ctx)
	if err != nil || !AnalystGate(limits) {
		return false // retried at the next daily check
	}
	out := filepath.Join(a.projectDir(project), "analyst-"+now.UTC().Format("20060102T150405"))
	if err := os.MkdirAll(out, 0o750); err != nil {
		a.Log.Warn("context health: analyst dir", "err", err)
		return false
	}
	// The record comes first: a crash or a failed save after the spawn must
	// not let the next check spend Haiku again.
	run := analystRun{SpawnedAt: now, OutDir: out, Targets: targets}
	if err := a.save(project, run); err != nil {
		a.Log.Warn("context health: analyst state not saved; not starting", "err", err)
		return false
	}
	id, err := a.Spawn(ctx, project, analystPrompt(out, todo))
	if err != nil {
		a.Log.Warn("context health: analyst did not start", "project", project, "err", err)
		_ = os.Remove(filepath.Join(a.projectDir(project), analystStateFile))
		return false
	}
	run.SessionID = string(id)
	if err := a.save(project, run); err != nil {
		a.Log.Warn("context health: analyst session not recorded", "err", err)
	}
	return true
}

// Pending reports the run whose results are ready to read: its session ended or
// its hold ran out.
func (a *Analyst) Pending(ctx context.Context, project domain.ProjectID, now time.Time) (analystRun, bool) {
	r, ok := a.load(project)
	if !ok || r.Ingested {
		return analystRun{}, false
	}
	if !a.Ended(ctx, domain.SessionID(r.SessionID)) && !a.wrote(r, now) && now.Sub(r.SpawnedAt) < analystHold {
		return analystRun{}, false
	}
	return r, true
}

// wrote reports whether the analyst's output is complete and quiet: findings.json
// exists and no file in the output directory changed in the last analystSettle.
// The newest file counts, not findings.json alone, so a patch still being
// written is never read.
func (a *Analyst) wrote(r analystRun, now time.Time) bool {
	if _, err := os.Stat(filepath.Join(r.OutDir, "findings.json")); err != nil {
		return false
	}
	entries, err := os.ReadDir(r.OutDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if fi, err := e.Info(); err != nil || now.Sub(fi.ModTime()) < analystSettle {
			return false
		}
	}
	return true
}

// Done marks a run read and stops the analyst session.
func (a *Analyst) Done(ctx context.Context, project domain.ProjectID, r analystRun) error {
	if a.Kill != nil && r.SessionID != "" {
		if err := a.Kill(ctx, domain.SessionID(r.SessionID)); err != nil {
			a.Log.Warn("context health: analyst session not stopped", "session", r.SessionID, "err", err)
		}
	}
	r.Ingested = true
	return a.save(project, r)
}

func analystPrompt(out string, findings []domain.ContextHealthFinding) string {
	var b strings.Builder
	b.WriteString("Draft patches for these context health findings. Output directory (write only here): " + out + "\n\n")
	b.WriteString("Write findings.json there: {\"findings\":[{\"id\":\"<finding id>\",\"summary\":\"<what you propose and why, under 2000 characters>\",\"patch\":\"<file name of a unified diff in the output directory, or empty>\",\"actions\":[{\"op\":\"archive\",\"file\":\"<memory file name>\",\"type\":\"user|feedback|project|reference\",\"reason\":\"<why it is stale>\"}]}]}.\n")
	b.WriteString("Write every patch file first and findings.json last: the daemon reads the directory as soon as findings.json has sat untouched for two minutes.\n")
	b.WriteString("A routing_pointer finding is the week's doc-pointer gaps (sessions searched for something, then read a doc no instruction file names). Draft ONE patch for the project's instruction file (AGENTS.md, CLAUDE.md or the like) that adds one short pointer line per real gap, for example: <topic> lives in <path or repo>. Take every topic and path from the evidence below, never from this prompt. Drop a gap that is not a stable fact; each line must name the doc or path it points to. In its summary list, per gap, the file, the exact line added, and the evidence. Add only lines, change no existing line.\n")
	b.WriteString("Instruction-file findings get a patch and no actions. Memory findings get archive actions (at most 10) and no patch. Never trim or weaken a behavioral rule; never delete; do not message anyone.\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "\n---\nFinding id: %s\nKind: %s\n\n%s\n", f.ID, f.Kind, f.Evidence)
	}
	return b.String()
}

// Action is one archive the analyst proposes for the Claude memory.
type Action struct {
	Op     string `json:"op"`
	File   string `json:"file"`
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
	// ProposeOnly marks a feedback or user entry: the owner confirms it first.
	ProposeOnly bool `json:"proposeOnly,omitempty"`
}

// Draft is what the analyst proposed for one finding, after validation.
type Draft struct {
	Summary string
	Patch   string
	Actions []Action
}

func (d Draft) empty() bool { return d.Summary == "" && d.Patch == "" && len(d.Actions) == 0 }

type rawFindings struct {
	Findings []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Patch   string `json:"patch"`
		Actions []struct {
			Op     string `json:"op"`
			File   string `json:"file"`
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"actions"`
	} `json:"findings"`
}

// ReadDrafts parses and validates the analyst's findings.json in dir. Entries
// for an unknown finding are ignored; a missing or malformed file yields none.
// Only archive actions on a plain file name survive, at most MaxMemoryActions
// per finding; feedback and user entries are marked propose-only.
func ReadDrafts(dir string, targets []analystTarget) map[string]Draft {
	b, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		return nil
	}
	var raw rawFindings
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	known := map[string]string{}
	for _, t := range targets {
		known[t.ID] = t.Kind
	}
	out := map[string]Draft{}
	for _, e := range raw.Findings {
		kind, ok := known[e.ID]
		if !ok {
			continue
		}
		d := Draft{Summary: clip(strings.TrimSpace(e.Summary), maxSummaryRunes)}
		if kind == string(domain.ContextInstructionBudget) || kind == string(domain.ContextRoutingPointer) {
			d.Patch = readPatch(dir, e.Patch)
		}
		if kind == string(domain.ContextMemoryBudget) {
			for _, ac := range e.Actions {
				if len(d.Actions) == MaxMemoryActions {
					break
				}
				if ac.Op != "archive" || !plainName(ac.File) || strings.EqualFold(ac.File, "MEMORY.md") {
					continue
				}
				typ := strings.ToLower(strings.TrimSpace(ac.Type))
				d.Actions = append(d.Actions, Action{Op: "archive", File: ac.File, Type: typ, Reason: clip(strings.TrimSpace(ac.Reason), 300), ProposeOnly: typ == "feedback" || typ == "user" || typ == ""})
			}
		}
		if !d.empty() {
			out[e.ID] = d
		}
	}
	return out
}

func plainName(n string) bool {
	return n != "" && n == filepath.Base(n) && n != "." && n != ".." && !strings.ContainsAny(n, `/\`)
}

func readPatch(dir, name string) string {
	if !plainName(name) {
		return ""
	}
	path := filepath.Join(dir, name)
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxPatchBytes {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// WriteDraft stores a validated draft beside the finding's evidence.
func WriteDraft(dir string, d Draft) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	var notes strings.Builder
	notes.WriteString("# Analyst draft\n\n" + d.Summary + "\n")
	if d.Patch != "" {
		if err := os.WriteFile(filepath.Join(dir, AnalystPatchFile), []byte(d.Patch), 0o600); err != nil {
			return err
		}
		notes.WriteString("\nPatch: " + AnalystPatchFile + " (a draft; check every rule survives)\n")
	}
	if len(d.Actions) > 0 {
		b, err := json.MarshalIndent(d.Actions, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, MemoryActionsFile), b, 0o600); err != nil {
			return err
		}
		notes.WriteString("\nMemory actions (archive only; propose-only entries need the owner's confirmation):\n")
		for _, a := range d.Actions {
			tag := ""
			if a.ProposeOnly {
				tag = " [propose only]"
			}
			fmt.Fprintf(&notes, "- archive %s (%s)%s: %s\n", a.File, a.Type, tag, a.Reason)
		}
	}
	return os.WriteFile(filepath.Join(dir, AnalystNotesFile), []byte(notes.String()), 0o600)
}

// ingestAnalyst reads back a finished analyst run: each finding with a valid
// draft gets its files in the artifact directory, then the findings the run
// held are carded (with the draft, or evidence-only when it left none).
func (s *Sweeper) ingestAnalyst(ctx context.Context, proj domain.ProjectRecord) error {
	a := s.deps.Analyst
	if a == nil {
		return nil
	}
	pid, now := domain.ProjectID(proj.ID), s.deps.Now()
	run, ok := a.Pending(ctx, pid, now)
	if !ok {
		return nil
	}
	drafts := ReadDrafts(run.OutDir, run.Targets)
	stored, err := s.deps.Store.ListFindings(ctx, pid)
	if err != nil {
		return err
	}
	byFP := map[string]domain.ContextHealthFinding{}
	seen := map[string]bool{}
	for _, f := range stored {
		if d, ok := drafts[f.ID]; ok && (f.State == domain.ContextFindingQueued || f.State == domain.ContextFindingOpen) {
			dir := filepath.Join(a.projectDir(pid), f.ID)
			if err := WriteDraft(dir, d); err != nil {
				s.deps.Log.Warn("context health: analyst draft not stored", "finding", f.ID, "err", err)
			} else if f.ArtifactDir != dir {
				from := f.State
				f.ArtifactDir = dir
				if _, err := s.deps.Store.SaveFindingTransition(ctx, f, from); err != nil {
					return err
				}
			}
		}
		if f.State == domain.ContextFindingQueued || f.State == domain.ContextFindingOpen {
			byFP[f.Fingerprint], seen[f.Fingerprint] = f, true
		}
	}
	if err := a.Done(ctx, pid, run); err != nil {
		return err
	}
	return s.promote(ctx, proj, byFP, seen, nil)
}

// analystHold starts the analyst when it is due and returns the findings whose
// cards wait for it.
func (s *Sweeper) analystHold(ctx context.Context, pid domain.ProjectID, byFP map[string]domain.ContextHealthFinding, seen map[string]bool, now time.Time) map[string]bool {
	a := s.deps.Analyst
	if a == nil {
		return nil
	}
	list := make([]domain.ContextHealthFinding, 0, len(byFP))
	for fp, f := range byFP {
		// Only a finding still over its limit and without a draft is worth Haiku.
		if _, err := os.Stat(filepath.Join(f.ArtifactDir, AnalystNotesFile)); seen[fp] && (f.ArtifactDir == "" || err != nil) {
			list = append(list, f)
		}
	}
	a.MaybeSpawn(ctx, pid, list, now)
	return a.Held(ctx, pid, now)
}

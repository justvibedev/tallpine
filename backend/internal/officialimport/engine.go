package officialimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/devimport"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Target is the live registry the import writes into. The daemon's SQLite
// store satisfies it (ImportWorkspaceProject is the same primitive the
// `dev import-projects` copy uses and enforces the id/path conflict rules).
type Target = devimport.Store

// Run snapshots the official database, plans the import against target, and
// (unless opts.DryRun) applies it. Settings are handled by RunWithSettings; Run
// is the projects-only convenience.
func Run(ctx context.Context, target Target, opts Options) (Report, error) {
	return RunWithSettings(ctx, target, nil, opts)
}

// RunWithSettings is Run plus the global settings that map 1:1. settings may be
// nil, in which case no settings are planned.
func RunWithSettings(ctx context.Context, target Target, settings SettingsTarget, opts Options) (Report, error) {
	rep := Report{SourceDataDir: opts.SourceDataDir, DryRun: opts.DryRun, IncludeSettings: opts.IncludeSettings}
	snap, err := takeSnapshot(ctx, opts.SourceDataDir, opts.StagingDir)
	if err != nil {
		if errors.Is(err, ErrSourceNotFound) {
			return rep, nil
		}
		return rep, err
	}
	defer func() { _ = snap.Close() }()
	rep.Found = true

	db, err := openSnapshot(ctx, snap.db)
	if err != nil {
		return rep, err
	}
	defer func() { _ = db.Close() }()
	src, err := readSource(ctx, db)
	if err != nil {
		return rep, err
	}

	selected, err := selection(opts.ProjectIDs, src.Projects)
	if err != nil {
		return rep, err
	}
	if err := planProjects(ctx, target, src.Projects, selected, &rep); err != nil {
		return rep, err
	}
	if !opts.DryRun {
		applyProjects(ctx, target, &rep)
	}

	if settings != nil {
		if err := planSettings(ctx, settings, src.Settings, &rep); err != nil {
			return rep, err
		}
		if opts.IncludeSettings && !opts.DryRun {
			applySettings(&rep)
		}
	}
	rep.Skipped = skippedList(src)
	rep.count()
	return rep, nil
}

// UnknownProjectError reports --project ids absent from the source.
type UnknownProjectError struct{ IDs []string }

func (e *UnknownProjectError) Error() string {
	return fmt.Sprintf("not registered in Agent Orchestrator: %s", strings.Join(e.IDs, ", "))
}

// selection resolves the requested ids against the source. A nil result means
// "all".
func selection(ids []string, projects []sourceProject) (map[string]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	have := map[string]bool{}
	for _, p := range projects {
		have[p.ID] = true
	}
	want := map[string]bool{}
	var unknown []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || want[id] {
			continue
		}
		want[id] = true
		if !have[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, &UnknownProjectError{IDs: unknown}
	}
	return want, nil
}

func planProjects(ctx context.Context, target Target, projects []sourceProject, selected map[string]bool, rep *Report) error {
	targetProjects, err := target.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list target projects: %w", err)
	}
	byPath := make(map[string]domain.ProjectRecord, len(targetProjects))
	for _, p := range targetProjects {
		byPath[p.Path] = p
	}

	for _, sp := range projects {
		plan := ProjectPlan{
			ID: sp.ID, Name: displayName(sp), Path: sp.Path, Kind: sp.Kind, Archived: sp.Archived,
			RepoURL: sp.RepoOriginURL, ChildRepos: len(sp.Repos),
			Selected: selected == nil || selected[sp.ID],
		}
		// Archived projects are listed so the UI can explain their absence, but
		// are never selectable.
		if sp.Archived {
			plan.Selected = false
		}
		if err := classify(ctx, target, byPath, sp, &plan); err != nil {
			return err
		}
		rep.Projects = append(rep.Projects, plan)
	}
	return nil
}

func displayName(sp sourceProject) string {
	if sp.DisplayName != "" {
		return sp.DisplayName
	}
	return sp.ID
}

// classify fills plan.Action and the mapped record. Cheap source-only checks
// come first so a skipped project never needs the target.
func classify(ctx context.Context, target Target, byPath map[string]domain.ProjectRecord, sp sourceProject, plan *ProjectPlan) error {
	skip := func(reason string) { plan.Action, plan.Reason = ActionSkip, reason }
	switch {
	case sp.Archived:
		skip("archived in Agent Orchestrator")
		return nil
	case sp.Kind != string(domain.ProjectKindSingleRepo) && sp.Kind != string(domain.ProjectKindWorkspace):
		// Scratch projects are plain directories AO manages inside its own data
		// dir, which the official install owns.
		skip(fmt.Sprintf("%s projects live inside the official data dir and are not imported", sp.Kind))
		return nil
	case !filepath.IsAbs(sp.Path):
		skip("project path is not absolute")
		return nil
	}
	if !isDir(sp.Path) {
		skip("project directory does not exist on this machine")
		return nil
	}

	mapped, mapErr := mapConfig(sp.ConfigJSON, sp.RepoOriginURL)
	plan.Warnings = append(plan.Warnings, mapped.Warnings...)
	if mapErr != nil {
		// A bad config fails this project only; the run goes on.
		plan.Action, plan.Error = ActionError, mapErr.Error()
		return nil //nolint:nilerr // reported on the plan; one bad project must not stop the run
	}
	plan.ConfigItems = mapped.Items

	record := domain.ProjectRecord{
		ID: sp.ID, Path: sp.Path, RepoOriginURL: sp.RepoOriginURL, DisplayName: sp.DisplayName,
		RegisteredAt: sp.RegisteredAt, Kind: domain.ProjectKind(sp.Kind).WithDefault(), Config: mapped.Config,
	}
	if record.DisplayName == "" {
		record.DisplayName = sp.ID
	}
	var repos []domain.WorkspaceRepoRecord
	if record.Kind == domain.ProjectKindWorkspace {
		for _, r := range sp.Repos {
			git := domain.GitStatus(r.GitStatus)
			if git != domain.GitStatusNeedsInit {
				git = domain.GitStatusReady
			}
			repos = append(repos, domain.WorkspaceRepoRecord{
				ProjectID: domain.ProjectID(sp.ID), Name: r.Name, RelativePath: r.RelativePath,
				RepoOriginURL: r.RepoOriginURL, DefaultBranch: r.DefaultBranch,
				RegisteredAt: r.RegisteredAt, GitStatus: git,
			})
			if _, err := os.Stat(filepath.Join(sp.Path, r.RelativePath)); err != nil {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("child repo %q is missing on disk", r.Name))
			}
		}
	}
	plan.record, plan.repos = record, repos

	existing, ok, err := target.GetProject(ctx, sp.ID)
	if err != nil {
		return fmt.Errorf("get target project %s: %w", sp.ID, err)
	}
	if ok {
		switch {
		case !existing.ArchivedAt.IsZero():
			conflict(plan, domain.ProjectImportConflictSameIDArchivedTarget, existing)
		case existing.Path != sp.Path:
			conflict(plan, domain.ProjectImportConflictSameIDDifferentActivePath, existing)
		default:
			same, err := sameAsTarget(ctx, target, existing, record, repos)
			if err != nil {
				return err
			}
			plan.Action = ActionUpdate
			if same {
				plan.Action = ActionUnchanged
			}
		}
		return nil
	}
	if other, ok := byPath[sp.Path]; ok {
		conflict(plan, domain.ProjectImportConflictSamePathDifferentActiveID, other)
		return nil
	}
	plan.Action = ActionCreate
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func conflict(plan *ProjectPlan, reason string, target domain.ProjectRecord) {
	plan.Action, plan.Reason = ActionConflict, reason
	plan.TargetID, plan.TargetPath = target.ID, target.Path
}

// sameAsTarget compares what an import would write with what is stored, so a
// rerun reports "unchanged" instead of rewriting identical rows.
func sameAsTarget(ctx context.Context, target Target, existing, want domain.ProjectRecord, wantRepos []domain.WorkspaceRepoRecord) (bool, error) {
	if existing.DisplayName != want.DisplayName || existing.RepoOriginURL != want.RepoOriginURL ||
		existing.Kind.WithDefault() != want.Kind.WithDefault() {
		return false, nil
	}
	a, _ := json.Marshal(existing.Config)
	b, _ := json.Marshal(want.Config)
	if !bytes.Equal(a, b) {
		return false, nil
	}
	have, err := target.ListWorkspaceRepos(ctx, existing.ID)
	if err != nil {
		return false, fmt.Errorf("list target workspace repos for %s: %w", existing.ID, err)
	}
	return reposEqual(have, wantRepos), nil
}

func reposEqual(a, b []domain.WorkspaceRepoRecord) bool {
	if len(a) != len(b) {
		return false
	}
	sorted := func(in []domain.WorkspaceRepoRecord) []domain.WorkspaceRepoRecord {
		out := append([]domain.WorkspaceRepoRecord(nil), in...)
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	sa, sb := sorted(a), sorted(b)
	for i := range sa {
		x, y := sa[i], sb[i]
		if x.Name != y.Name || x.RelativePath != y.RelativePath || x.RepoOriginURL != y.RepoOriginURL ||
			x.DefaultBranch != y.DefaultBranch || x.GitStatus.WithDefault() != y.GitStatus.WithDefault() ||
			!x.RegisteredAt.Equal(y.RegisteredAt) {
			return false
		}
	}
	return true
}

func applyProjects(ctx context.Context, target Target, rep *Report) {
	for i := range rep.Projects {
		p := &rep.Projects[i]
		if !p.Selected || (p.Action != ActionCreate && p.Action != ActionUpdate) {
			continue
		}
		if err := target.ImportWorkspaceProject(ctx, p.record, p.repos); err != nil {
			var c *domain.ProjectImportConflictError
			if errors.As(err, &c) {
				p.Action, p.Reason = ActionConflict, c.Conflict.Reason
				p.TargetID, p.TargetPath = c.Conflict.TargetID, c.Conflict.TargetPath
				continue
			}
			p.Error = fmt.Sprintf("write failed: %v", err)
			p.Action = ActionError
			continue
		}
		p.Applied = true
	}
}

func (r *Report) count() {
	for _, p := range r.Projects {
		// Archived and otherwise unimportable projects are never selected, but
		// the summary still has to account for them.
		if !p.Selected && p.Action != ActionSkip {
			continue
		}
		switch p.Action {
		case ActionCreate:
			r.Counts.Create++
		case ActionUpdate:
			r.Counts.Update++
		case ActionUnchanged:
			r.Counts.Unchanged++
		case ActionConflict:
			r.Counts.Conflict++
		case ActionSkip:
			r.Counts.Skip++
		case ActionError:
			r.Counts.Error++
		}
	}
	for _, s := range r.Settings {
		if s.Error != "" {
			r.Counts.Error++
		}
	}
}

// skippedList is the fixed statement of what an import never touches, with the
// row counts that were left behind where they are cheap to know.
func skippedList(src sourceData) []Skipped {
	return []Skipped{
		{What: "sessions, worktrees and PR/CI state", Why: "live runtime state belongs to the install that created it; migrate projects, then start fresh sessions", Count: src.SessionCount},
		{What: "chat history and provider events", Why: "conversation transcripts are large, session-bound and not portable", Count: src.ConversationCount},
		{What: "worker reports and review runs", Why: "tied to sessions that are not imported", Count: src.ReportCount},
		{What: "artifacts, attachments, review reports, handoffs", Why: "files under the official data dir are never read or copied"},
		{What: "browser data (profile, cookies, downloads)", Why: "only an explicit browser-profile import may read browsers"},
		{What: "tokens, secrets, keychain items, agent account credentials, mobile pairing", Why: "credentials are never copied between installs; sign in again"},
		{What: "automations, cues, usage and cost history", Why: "out of scope for the project migration"},
		{What: "notification preferences", Why: "not stored in ao.db (they live in the official desktop app's own profile), so there is nothing to read"},
		{What: "Electron app state and update settings", Why: "specific to the official app bundle"},
	}
}

package review

import (
	stdctx "context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
	"tallpine.eu/tallpine/backend/internal/skillsprompt"
)

// ReviewerSkill is a text-only skill copy a reviewer pass gets: SKILL.md and
// Markdown references, no scripts, executable bits or symlinks.
type ReviewerSkill = skillbridge.TextSkill

// reviewerSkillsFor collects the skills switched on for the reviewer role
// (project switches, then app): the project's and the app's custom skills (off unless switched on, B740 q1) and the
// .tallpine/reviewer-skills of the default branch (on unless switched off,
// n3), read at base so a PR cannot add or change the skills it is reviewed
// with. Both share one n1 budget, custom skills first; every skill or file
// left out is named in the returned notes.
func (e *Engine) reviewerSkillsFor(ctx stdctx.Context, project domain.ProjectRecord, app domain.SkillRoles, harness domain.ReviewerHarness, base *baseRef) ([]ReviewerSkill, []string) {
	roles := project.Config.SkillRoles
	taken := map[string]bool{}
	want := func(src domain.SkillSource) func(string) bool {
		return func(name string) bool {
			return !taken[name] && domain.SkillOn(roles, app, domain.RoleReviewer, string(harness), name, src)
		}
	}
	budget := skillbridge.NewTextBudget()
	var skills []ReviewerSkill
	var skipped []string
	// The project's custom skills, then the app's (a project skill of the
	// same name wins, as on the skills page).
	for _, dir := range []string{skillbridge.CustomDir(e.dataDir, project.ID), skillbridge.AppCustomDir(e.dataDir)} {
		if dir == "" {
			continue
		}
		custom, notes := skillbridge.CustomStore{Root: dir}.ReadCustomText(want(domain.SkillSourceCustom), budget)
		skills, skipped = append(skills, custom...), append(skipped, notes...)
		for _, s := range custom {
			taken[s.Name] = true
		}
	}
	if base == nil || base.repo == "" {
		return skills, skipped
	}
	ref := base.get(ctx)
	repo, notes, err := skillbridge.ReadReviewerRepo(ctx, base.repo, ref, want(domain.SkillSourceReviewerRepo), taken, budget)
	if err != nil {
		slog.Warn("review: reviewer skills unreadable at the default branch; reviewing without them", "project", project.ID, "ref", ref, "err", err)
		return skills, append(skipped, fmt.Sprintf("the %s skills could not be read from %s", skillbridge.ReviewerSkillsDir, ref))
	}
	skipped = append(skipped, notes...)
	skills = append(skills, repo...)
	if note := localNote("The "+skillbridge.ReviewerSkillsDir+" skills were", ref); note != "" && len(repo) > 0 {
		skipped = append(skipped, note)
	}
	return skills, skipped
}

// reviewerSkillsDir is where one pass's skill copies live: inside the
// request directory under the reviewer prompt root, which every reviewer
// harness reads (it reads the task file next to it).
func reviewerSkillsDir(requestDir string) string { return filepath.Join(requestDir, "skills") }

// writeReviewerSkills writes the pass's text-only copies under dir, owner-only,
// replacing any copy a relaunch of the same run left.
func writeReviewerSkills(dir string, skills []ReviewerSkill) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if len(skills) == 0 {
		return nil
	}
	for _, s := range skills {
		if err := skillbridge.ValidSkillName(s.Name); err != nil {
			return err
		}
		if err := skillbridge.WriteTextFiles(filepath.Join(dir, s.Name), s.Files); err != nil {
			return fmt.Errorf("copy reviewer skill %s: %w", s.Name, err)
		}
	}
	return nil
}

// reviewSkillsText is the task file's "Skills available to you" section: the
// copies by path under dir, and the skills left out. Without a copy dir
// (nothing written) only the left-out notes are listed.
func reviewSkillsText(dir string, skills []ReviewerSkill, skipped []string) string {
	var entries []skillsprompt.Entry
	if dir != "" {
		for _, s := range skills {
			entries = append(entries, skillsprompt.Entry{Name: s.Name, Description: s.Description, Path: filepath.ToSlash(filepath.Join(dir, s.Name, "SKILL.md"))})
		}
	}
	section := skillsprompt.AvailableSection(entries, skipped)
	if section == "" {
		return ""
	}
	return "\n" + section + "\n"
}

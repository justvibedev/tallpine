package skillbridge

import (
	"context"
	"path"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitblob"
)

// ScopeReviewerRepo is a reviewer-only skill committed under
// ReviewerSkillsDir. It is always read from the project's default branch,
// never from a PR head or a checkout, so a PR cannot change the skills it is
// reviewed with.
const ScopeReviewerRepo Scope = "reviewer-repo"

// ConvReviewerRepo is the .tallpine/reviewer-skills directory family.
const ConvReviewerRepo Convention = "reviewer-repo"

// ReviewerSkillsDir is the repository directory of reviewer-only skills.
const ReviewerSkillsDir = ".tallpine/reviewer-skills"

// repoSkill is one skill directory of a ListTreeAt listing.
type repoSkill struct {
	name    string
	keep    []gitblob.Entry
	size    int64
	skipped []string
	hasMD   bool
}

// listReviewerRepo groups the reviewer-skills tree at ref into skills, with
// the files a text-only copy keeps and the ones it skips.
func listReviewerRepo(ctx context.Context, repo, ref string) ([]*repoSkill, []string, error) {
	entries, err := gitblob.ListTreeAt(ctx, repo, ref, ReviewerSkillsDir)
	if err != nil {
		return nil, nil, err
	}
	const src = domain.SkillSourceReviewerRepo
	byName := map[string]*repoSkill{}
	var skipped []string
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Path, ReviewerSkillsDir+"/")
		if !ok {
			continue
		}
		name, rel, ok := strings.Cut(rest, "/")
		if !ok {
			skipped = append(skipped, skippedFile(src, "", rest, "outside a skill directory"))
			continue
		}
		s := byName[name]
		if s == nil {
			s = &repoSkill{name: name}
			byName[name] = s
		}
		reason := ""
		switch {
		case e.Mode == gitblob.ModeSymlink:
			reason = skipSymlink
		case e.Mode == gitblob.ModeExecutable:
			reason = skipExecutable
		case e.Mode != gitblob.ModeRegular:
			reason = skipNotRegular
		case textFileRel(rel) != rel:
			reason = skipBadPath
		case !isMarkdown(rel):
			reason = skipNotMarkdown
		}
		if reason != "" {
			s.skipped = append(s.skipped, skippedFile(src, name, rel, reason))
			continue
		}
		s.keep = append(s.keep, e)
		s.size += e.Size
		s.hasMD = s.hasMD || rel == "SKILL.md"
	}
	out := make([]*repoSkill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].name < out[b].name })
	return out, skipped, nil
}

// ReviewerRepoSkills lists the reviewer-only skills at ref with their
// SKILL.md descriptions, for the skills page. Invalid names and skills
// without SKILL.md are left out.
func ReviewerRepoSkills(ctx context.Context, repo, ref string) ([]Skill, error) {
	list, _, err := listReviewerRepo(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, s := range list {
		if ValidSkillName(s.name) != nil || !s.hasMD {
			continue
		}
		p := path.Join(ReviewerSkillsDir, s.name)
		sk := Skill{Name: s.name, Scope: ScopeReviewerRepo, Convention: ConvReviewerRepo, Path: p}
		if data, found, err := gitblob.ReadAt(ctx, repo, ref, p+"/SKILL.md", MaxTextSkillBytes); err == nil && found {
			_, sk.Description = frontmatterOf(data)
		}
		out = append(out, sk)
	}
	return out, nil
}

// ReadReviewerRepo is the text-only copy of the reviewer-only skills at ref
// for one reviewer pass: per skill SKILL.md and other regular .md files,
// without symlinks, executables or other files. want filters by name (a
// switched-off skill is never read); the rest take budget in name order and
// a skill that does not fit is skipped. A name in taken (already delivered,
// e.g. a custom skill) is skipped before the budget. Only a skill actually
// returned takes budget. Every left-out skill and file is named in the
// returned notes.
func ReadReviewerRepo(ctx context.Context, repo, ref string, want func(name string) bool, taken map[string]bool, budget *TextBudget) ([]TextSkill, []string, error) {
	list, skipped, err := listReviewerRepo(ctx, repo, ref)
	if err != nil {
		return nil, nil, err
	}
	const src = domain.SkillSourceReviewerRepo
	var out []TextSkill
	for _, s := range list {
		if want != nil && !want(s.name) {
			continue
		}
		if taken[s.name] {
			skipped = append(skipped, skippedSkill(src, s.name, "a custom skill has the same name"))
			continue
		}
		skipped = append(skipped, s.skipped...)
		if err := ValidSkillName(s.name); err != nil {
			skipped = append(skipped, skippedSkill(src, s.name, "invalid skill name"))
			continue
		}
		if !s.hasMD {
			skipped = append(skipped, skippedSkill(src, s.name, "no SKILL.md"))
			continue
		}
		// Check before reading (sizes come from the tree), reserve after.
		if reason := budget.fits(s.size); reason != "" {
			skipped = append(skipped, skippedSkill(src, s.name, reason))
			continue
		}
		text, note, err := readRepoSkill(ctx, repo, ref, s)
		if err != nil {
			return nil, nil, err
		}
		if note != "" {
			skipped = append(skipped, note)
			continue
		}
		if reason := budget.take(text.Size()); reason != "" {
			skipped = append(skipped, skippedSkill(src, s.name, reason))
			continue
		}
		out = append(out, text)
	}
	return out, skipped, nil
}

func readRepoSkill(ctx context.Context, repo, ref string, s *repoSkill) (TextSkill, string, error) {
	const src = domain.SkillSourceReviewerRepo
	text := TextSkill{Name: s.name, Source: src}
	for _, e := range s.keep {
		rel := strings.TrimPrefix(e.Path, ReviewerSkillsDir+"/"+s.name+"/")
		data, found, err := gitblob.ReadAt(ctx, repo, ref, e.Path, MaxTextSkillBytes+1)
		if err != nil {
			return TextSkill{}, "", err
		}
		if !found || int64(len(data)) != e.Size || !isText(data) {
			return TextSkill{}, skippedSkill(src, s.name, "file "+rel+" could not be read as text"), nil
		}
		text.Files = append(text.Files, TextFile{Rel: rel, Data: data})
		if rel == "SKILL.md" {
			_, text.Description = frontmatterOf(data)
		}
	}
	return text, "", nil
}

package skillbridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rs = ReviewerSkillsDir

// reviewerRepo commits reviewer skills on main and a different version of
// them on a PR head branch, checked out in the worktree.
func reviewerRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	for p, body := range files {
		write(t, filepath.Join(dir, p), body)
	}
	if err := os.Chmod(filepath.Join(dir, rs, "good", "run.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../secret.md", filepath.Join(dir, rs, "good", "link.md")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "pr-head")
	write(t, filepath.Join(dir, rs, "good", "SKILL.md"), "---\ndescription: HEAD VERSION\n---\nHEAD BODY\n")
	write(t, filepath.Join(dir, rs, "headonly", "SKILL.md"), "HEAD ONLY")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "head")
	return dir
}

func baseFiles() map[string]string {
	return map[string]string{
		"secret.md":                "SECRET",
		rs + "/good/SKILL.md":      "---\ndescription: BASE VERSION\n---\nBASE BODY\n",
		rs + "/good/refs/guide.md": "GUIDE",
		rs + "/good/run.md":        "EXEC",
		rs + "/good/script.py":     "print(1)",
		rs + "/off/SKILL.md":       "OFF",
		rs + "/noskill/README.md":  "no SKILL.md",
		rs + "/Bad Name/SKILL.md":  "bad",
		rs + "/big/SKILL.md":       strings.Repeat("x", MaxTextSkillBytes+1),
		rs + "/stray.md":           "top-level",
	}
}

func TestReadReviewerRepoReadsTheRefTextOnly(t *testing.T) {
	dir := reviewerRepo(t, baseFiles())
	want := func(n string) bool { return n != "off" }
	skills, skipped, err := ReadReviewerRepo(context.Background(), dir, "main", want, nil, NewTextBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "good" {
		t.Fatalf("skills = %+v", skills)
	}
	good := skills[0]
	got := map[string]string{}
	for _, f := range good.Files {
		got[f.Rel] = string(f.Data)
	}
	if len(got) != 2 || !strings.Contains(got["SKILL.md"], "BASE BODY") || got["refs/guide.md"] != "GUIDE" || good.Description != "BASE VERSION" {
		t.Fatalf("good files = %v desc %q", got, good.Description)
	}
	notes := strings.Join(skipped, "\n")
	for _, w := range []string{`"run.md": executable`, `"link.md": symlink`, `"script.py": not a .md file`, `"big": over the 64 KiB per-skill limit`, `"noskill": no SKILL.md`, `"Bad Name": invalid skill name`, `"stray.md": outside a skill directory`} {
		if !strings.Contains(notes, w) {
			t.Errorf("skipped notes lack %q:\n%s", w, notes)
		}
	}
	if strings.Contains(notes, `"off"`) || strings.Contains(notes, "headonly") {
		t.Fatalf("a switched-off or head-only skill is named:\n%s", notes)
	}
}

func TestReadReviewerRepoCaps(t *testing.T) {
	files := map[string]string{rs + "/good/SKILL.md": "g", rs + "/good/run.md": "x"}
	for i := 0; i < MaxTextSkills+1; i++ {
		files[rs+"/s"+string(rune('a'+i))+"/SKILL.md"] = "s"
	}
	dir := reviewerRepo(t, files)
	skills, skipped, err := ReadReviewerRepo(context.Background(), dir, "main", nil, nil, NewTextBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != MaxTextSkills || !strings.Contains(strings.Join(skipped, "\n"), "over the 20-skill limit") {
		t.Fatalf("count cap: %d skills, skipped %v", len(skills), skipped)
	}

	files = map[string]string{rs + "/good/SKILL.md": "g", rs + "/good/run.md": "x"}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files[rs+"/"+n+"/SKILL.md"] = strings.Repeat("y", 60<<10)
	}
	dir = reviewerRepo(t, files)
	skills, skipped, err = ReadReviewerRepo(context.Background(), dir, "main", nil, nil, NewTextBudget())
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(skipped, "\n")
	// a-d take 240 KiB; e does not fit the 256 KiB total, the 1-byte good does.
	if len(skills) != 5 || !strings.Contains(notes, `"e": over the 256 KiB total limit`) {
		t.Fatalf("total cap: %d skills, skipped %v", len(skills), skipped)
	}
}

func TestReviewerRepoSkillsListsTheRef(t *testing.T) {
	dir := reviewerRepo(t, baseFiles())
	list, err := ReviewerRepoSkills(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range list {
		names = append(names, s.Name+"="+s.Description)
	}
	if strings.Join(names, ",") != "big=,good=BASE VERSION,off=" {
		t.Fatalf("listed %v", names)
	}
}

// A repo skill shadowed by a custom skill of the same name is skipped before
// the budget, so it cannot push a later skill over the n1 total.
func TestReadReviewerRepoShadowedSkillTakesNoBudget(t *testing.T) {
	files := map[string]string{rs + "/good/SKILL.md": "g", rs + "/good/run.md": "x"}
	for _, n := range []string{"a", "b", "c", "dup", "e"} {
		files[rs+"/"+n+"/SKILL.md"] = strings.Repeat("y", 60<<10)
	}
	dir := reviewerRepo(t, files)
	budget := NewTextBudget()
	budget.Bytes -= 15 << 10 // a 15 KiB custom skill already delivered
	budget.Skills--
	skills, skipped, err := ReadReviewerRepo(context.Background(), dir, "main", nil, map[string]bool{"dup": true}, budget)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name)
	}
	notes := strings.Join(skipped, "\n")
	if strings.Join(names, ",") != "a,b,c,e,good" || !strings.Contains(notes, `"dup": a custom skill has the same name`) || strings.Contains(notes, "total limit") {
		t.Fatalf("delivered %v, skipped:\n%s", names, notes)
	}
}

package skillbridge

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func perm(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestCustomSkillNamesCannotEscape(t *testing.T) {
	data := t.TempDir()
	store := CustomStore{Root: CustomDir(data, "mer")}
	for _, name := range []string{"../x", "..", "a/b", `a\b`, ".hidden", "A", "", "a..b", strings.Repeat("a", 65), "/abs"} {
		if _, err := store.Create(name, "body"); err == nil {
			t.Errorf("Create(%q) accepted", name)
		}
		if _, _, err := store.Import(name, t.TempDir()); err == nil {
			t.Errorf("Import(%q) accepted", name)
		}
		if err := store.Delete(name); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Delete(%q) = %v, want a name error", name, err)
		}
	}
	for _, id := range []string{"", "../x", "a/b", ".."} {
		if CustomDir(data, id) != "" {
			t.Errorf("CustomDir(%q) is not empty", id)
		}
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatalf("data dir written: %v", entries)
	}
}

func TestCustomSkillCRUDOwnerOnly(t *testing.T) {
	data := t.TempDir()
	root := CustomDir(data, "mer")
	store := CustomStore{Root: root}
	s, err := store.Create("review-sql", "---\ndescription: SQL checks\n---\nbody\n")
	if err != nil || s.Description != "SQL checks" || s.Path != filepath.Join(root, "review-sql") {
		t.Fatalf("Create = %+v %v", s, err)
	}
	if _, err := store.Create("review-sql", "x"); !errors.Is(err, ErrSkillExists) {
		t.Fatalf("duplicate Create = %v", err)
	}
	for p, want := range map[string]os.FileMode{root: 0o700, s.Path: 0o700, filepath.Join(s.Path, "SKILL.md"): 0o600} {
		if got := perm(t, p); got != want {
			t.Errorf("%s mode %o, want %o", p, got, want)
		}
	}
	if s, err = store.Update("review-sql", "new body"); err != nil || s.Content != "new body" {
		t.Fatalf("Update = %+v %v", s, err)
	}
	if got := perm(t, filepath.Join(s.Path, "SKILL.md")); got != 0o600 {
		t.Fatalf("updated SKILL.md mode %o", got)
	}
	if _, err := store.Update("nope", "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Update missing = %v", err)
	}
	if _, err := store.Create("big", strings.Repeat("x", MaxTextSkillBytes+1)); err == nil {
		t.Fatal("over-cap SKILL.md accepted")
	}
	list, _ := store.List()
	if len(list) != 1 || list[0].Name != "review-sql" {
		t.Fatalf("List = %+v", list)
	}
	if err := store.Delete("review-sql"); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Fatalf("after Delete: %+v", list)
	}
}

func TestCustomSkillImportIsTextOnly(t *testing.T) {
	src := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(t, outside, "SECRET")
	write(t, filepath.Join(src, "SKILL.md"), "---\ndescription: imported\n---\n")
	write(t, filepath.Join(src, "refs", "a.md"), "A")
	write(t, filepath.Join(src, "tool.sh"), "#!/bin/sh")
	write(t, filepath.Join(src, "x.md"), "EXEC")
	if err := os.Chmod(filepath.Join(src, "x.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "leak.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(src, "linkdir")); err != nil {
		t.Fatal(err)
	}
	store := CustomStore{Root: CustomDir(t.TempDir(), "mer")}
	s, skipped, err := store.Import("imp", src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Files, ",") != "refs/a.md" || s.Description != "imported" {
		t.Fatalf("imported %+v", s)
	}
	notes := strings.Join(skipped, "\n")
	for _, w := range []string{`"tool.sh": not a .md file`, `"x.md": executable`, `"leak.md": symlink`, `"linkdir": symlink`} {
		if !strings.Contains(notes, w) {
			t.Errorf("skipped lacks %q:\n%s", w, notes)
		}
	}
	if got := perm(t, filepath.Join(s.Path, "refs", "a.md")); got != 0o600 {
		t.Fatalf("imported file mode %o", got)
	}
	if got := perm(t, filepath.Join(s.Path, "refs")); got != 0o700 {
		t.Fatalf("imported dir mode %o", got)
	}
	// The chosen folder may itself be a link (account skills often are).
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if viaLink, _, err := store.Import("via-link", link); err != nil || strings.Join(viaLink.Files, ",") != "refs/a.md" {
		t.Fatalf("import through a linked folder = %+v %v", viaLink, err)
	}
	if _, _, err := store.Import("nomd", t.TempDir()); err == nil {
		t.Fatal("import without SKILL.md accepted")
	}
}

// A symlink planted at a skill's name is never followed: Get does not read
// its target and Delete removes only the link.
func TestCustomSkillSymlinkEntryNotFollowed(t *testing.T) {
	root := CustomDir(t.TempDir(), "mer")
	target := t.TempDir()
	write(t, filepath.Join(target, "SKILL.md"), "TARGET")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	store := CustomStore{Root: root}
	if _, err := store.Get("evil"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get through a symlink = %v", err)
	}
	if err := store.Delete("evil"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("Delete removed the link target: %v", err)
	}
}

// Workers get enabled custom skills; a switched-off one, a reviewer by
// default, and a custom skill shadowed by a same-named repo skill do not.
func TestAvailableForListsEnabledCustomSkills(t *testing.T) {
	data := t.TempDir()
	ws := t.TempDir()
	store := CustomStore{Root: CustomDir(data, "mer")}
	for _, n := range []string{"on", "off", "dup"} {
		if _, err := store.Create(n, "---\ndescription: d "+n+"\n---\n"); err != nil {
			t.Fatal(err)
		}
	}
	skill(t, filepath.Join(ws, ".claude", "skills"), "dup", "repo")
	inv := Discover(Options{Workspace: ws, DataDir: data}.WithProject("mer"))
	cfg := domain.ProjectConfig{SkillRoles: domain.SkillRoles{"worker": {"off": false}}}
	names := func(role string) string {
		var out []string
		for _, e := range AvailableFor(inv, cfg, nil, role, "") {
			out = append(out, e.Name+"@"+e.Path)
		}
		return strings.Join(out, ",")
	}
	if got, want := names("worker"), "on@"+filepath.Join(store.Root, "on", "SKILL.md"); got != want {
		t.Fatalf("worker = %q, want %q", got, want)
	}
	if got := names("reviewer"); got != "" {
		t.Fatalf("reviewer default = %q", got)
	}
	launch, _ := RoleLaunch(inv, cfg, nil, "worker", "claude-code")
	if strings.Contains(strings.Join(launch.Off, ","), "off") {
		t.Fatalf("launch hides a prompt-delivered skill: %v", launch.Off)
	}
	st := Statuses(inv, HarnessClaude)
	for i, s := range inv.Skills {
		if s.Scope == ScopeCustom && s.Name == "on" && st[i] != StatusListed {
			t.Fatalf("custom status = %s", st[i])
		}
	}
}

package skillbridge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeselectedSkillIsNeitherOfferedNorCreated(t *testing.T) {
	_, ws := productParsing(t)
	offers := OffersExcept(ws, []string{HarnessClaude}, []string{"graphify"})
	if len(offers) != 2 || len(offers[1].Items) != 2 {
		t.Fatalf("offers = %+v", offers)
	}
	for _, it := range offers[1].Items {
		if filepath.Base(it.Path) == "graphify" {
			t.Fatalf("deselected skill offered: %+v", it)
		}
	}
	if _, err := ApplyOfferExcept(ws, []string{HarnessClaude}, []string{"graphify"}, OfferClaudeSkills); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude", "skills", "graphify")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deselected skill was created: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude", "skills", "source-regression-qa")); err != nil {
		t.Fatalf("selected skill not created: %v", err)
	}
	// Deselecting everything leaves no skills offer at all.
	for _, o := range OffersExcept(ws, []string{HarnessClaude}, []string{"graphify", "source-regression-qa", "source-refresh-operations"}) {
		if o.ID == OfferClaudeSkills {
			t.Fatalf("offer still listed: %+v", o)
		}
	}
}

func TestRemoveBridgedLinkDeletesOnlyTheLink(t *testing.T) {
	_, ws := productParsing(t)
	if _, err := ApplyOffer(ws, []string{HarnessClaude}, OfferClaudeSkills); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(ws, ".agents", "skills", "graphify")
	before := digest(t, src)
	if !Removable(ws, ".claude/skills/graphify") {
		t.Fatal("link not removable")
	}
	if err := RemoveBridged(ws, ".claude/skills/graphify"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".claude", "skills", "graphify")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link still there: %v", err)
	}
	if digest(t, src) != before || !readable(t, filepath.Join(src, "SKILL.md")) {
		t.Fatal("the source skill changed")
	}
	if !readable(t, filepath.Join(ws, ".claude", "skills", "source-regression-qa", "SKILL.md")) {
		t.Fatal("another bridged skill was removed")
	}
}

func TestRemoveBridgedCopyOnlyWhenIdenticalToItsSource(t *testing.T) {
	old := symlink
	symlink = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { symlink = old })
	_, ws := productParsing(t)
	if _, err := ApplyOffer(ws, []string{HarnessClaude}, OfferClaudeSkills); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(ws, ".claude", "skills", "graphify")
	if st, _ := os.Lstat(copyDir); st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("setup: expected a copy")
	}
	// Edited copy: no longer what the bridge made, so it is the user's work.
	write(t, filepath.Join(copyDir, "SKILL.md"), "---\nname: graphify\n---\nedited\n")
	if err := RemoveBridged(ws, ".claude/skills/graphify"); !errors.Is(err, ErrNotBridged) {
		t.Fatalf("edited copy: err = %v", err)
	}
	if !readable(t, filepath.Join(copyDir, "SKILL.md")) {
		t.Fatal("edited copy was deleted")
	}
	// Untouched copy goes; the source stays.
	other := filepath.Join(ws, ".claude", "skills", "source-regression-qa")
	src := filepath.Join(ws, ".codex", "skills", "source-regression-qa")
	before := digest(t, src)
	if err := RemoveBridged(ws, ".claude/skills/source-regression-qa"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("copy still there")
	}
	if digest(t, src) != before {
		t.Fatal("the source skill changed")
	}
}

func TestRemoveBridgedRefusesSourcesAndEscapes(t *testing.T) {
	_, ws := productParsing(t)
	// A skill that exists once is a source, never removable.
	for _, rel := range []string{".agents/skills/graphify", ".codex/skills/source-regression-qa"} {
		if err := RemoveBridged(ws, rel); !errors.Is(err, ErrNotBridged) {
			t.Fatalf("%s: err = %v", rel, err)
		}
		if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel), "SKILL.md")); err != nil {
			t.Fatalf("%s was damaged: %v", rel, err)
		}
	}
	for _, rel := range []string{"AGENTS.md", "../x", ".claude/skills", ".claude/skills/a/b", ".github/skills/x", ".claude/skills/.hidden"} {
		if err := RemoveBridged(ws, rel); err == nil {
			t.Fatalf("%q accepted", rel)
		}
	}
	// A link pointing outside the checkout is not the bridge's.
	outside := t.TempDir()
	skill(t, outside, "ext", "e")
	if err := os.MkdirAll(filepath.Join(ws, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "ext"), filepath.Join(ws, ".claude", "skills", "ext")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBridged(ws, ".claude/skills/ext"); !errors.Is(err, ErrNotBridged) {
		t.Fatalf("outside link: err = %v", err)
	}
	if !readable(t, filepath.Join(outside, "ext", "SKILL.md")) {
		t.Fatal("outside skill touched")
	}
}

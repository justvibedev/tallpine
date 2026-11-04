package skillbridge

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// productParsing mirrors the real repo: AGENTS.md only, skills in .codex and .agents.
func productParsing(t *testing.T) (main, ws string) {
	t.Helper()
	return repo(t, map[string]string{
		"AGENTS.md": "# rules\n",
		".codex/skills/source-regression-qa/SKILL.md":      "---\nname: source-regression-qa\ndescription: QA\n---\n",
		".codex/skills/source-refresh-operations/SKILL.md": "---\nname: source-refresh-operations\ndescription: ops\n---\n",
		".agents/skills/graphify/SKILL.md":                 "---\nname: graphify\ndescription: g\n---\n",
	})
}

func TestClaudeOffersForAgentsOnlyRepoMakeFilesClaudeLoads(t *testing.T) {
	_, ws := productParsing(t)
	home := t.TempDir()
	skill(t, filepath.Join(home, ".agents", "skills"), "user-one", "u")
	homeBefore := digest(t, home)

	offers := Offers(ws, []string{HarnessClaude})
	if len(offers) != 2 || offers[0].ID != OfferClaudeMD || offers[1].ID != OfferClaudeSkills || len(offers[1].Items) != 3 {
		t.Fatalf("offers = %+v", offers)
	}
	if status(t, ws) != "" {
		t.Fatal("computing offers must not write")
	}
	for _, id := range []string{OfferClaudeMD, OfferClaudeSkills} {
		if _, err := ApplyOffer(ws, []string{HarnessClaude}, id); err != nil {
			t.Fatal(err)
		}
	}
	// What Claude Code loads: ./CLAUDE.md importing AGENTS.md, and .claude/skills/<n>/SKILL.md.
	if b, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md")); string(b) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q", b)
	}
	for _, n := range []string{"source-regression-qa", "source-refresh-operations", "graphify"} {
		p := filepath.Join(ws, ".claude", "skills", n)
		if !readable(t, filepath.Join(p, "SKILL.md")) {
			t.Errorf("claude cannot read %s", n)
		}
		if tgt, err := os.Readlink(p); err != nil || filepath.IsAbs(tgt) {
			t.Errorf("%s: want a relative symlink, got %q %v", n, tgt, err)
		}
	}
	// Plain uncommitted changes: untracked, nothing staged, nothing committed.
	st := status(t, ws)
	if !strings.Contains(st, "?? CLAUDE.md") || !strings.Contains(st, "?? .claude/skills/graphify") || strings.Contains(st, "A ") {
		t.Errorf("status:\n%s", st)
	}
	if digest(t, home) != homeBefore {
		t.Error("user dirs touched")
	}
	// Applied: nothing left to offer.
	if got := Offers(ws, []string{HarnessClaude}); len(got) != 0 {
		t.Errorf("offers after apply = %+v", got)
	}
}

func TestOffersNeverOverwrite(t *testing.T) {
	_, ws := repo(t, map[string]string{
		"AGENTS.md": "a\n", "CLAUDE.md": "mine\n",
		".claude/skills/qa/SKILL.md": "---\nname: qa\ndescription: tracked\n---\n",
		".codex/skills/qa/SKILL.md":  "---\nname: qa\ndescription: codex\n---\n",
	})
	if got := Offers(ws, []string{HarnessClaude}); len(got) != 0 {
		t.Fatalf("offers = %+v", got)
	}
	// A file that appears between listing and applying is skipped, not replaced.
	_, ws2 := productParsing(t)
	if err := os.WriteFile(filepath.Join(ws2, "CLAUDE.md"), []byte("late\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ApplyOffer(ws2, []string{HarnessClaude}, OfferClaudeMD)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("offer should be gone, err=%v", err)
	}
	it := Item{Path: "CLAUDE.md", Kind: "file", Content: "x"}
	if _, err := create(ws2, it); !errors.Is(err, fs.ErrExist) {
		t.Errorf("create over existing: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws2, "CLAUDE.md")); string(b) != "late\n" {
		t.Errorf("overwritten: %q", b)
	}
}

func TestCodexOffersAndHarnessGating(t *testing.T) {
	_, ws := productParsing(t)
	// Codex has AGENTS.md and .agents/skills; only the repo .codex skills (which
	// Codex does not scan) are offered, never the .agents one it already sees.
	if got := Offers(ws, []string{HarnessCodex}); len(got) != 1 || got[0].ID != OfferCodexSkills || len(got[0].Items) != 2 {
		t.Errorf("codex offers = %+v", got)
	}
	if got := Offers(ws, []string{"kiro"}); len(got) != 0 {
		t.Errorf("unverified harness offered: %+v", got)
	}

	_, ws2 := repo(t, map[string]string{
		"CLAUDE.md":                  "claude rules\n",
		".claude/skills/qa/SKILL.md": "---\nname: qa\ndescription: q\n---\n",
	})
	offers := Offers(ws2, []string{HarnessCodex})
	if len(offers) != 2 || offers[0].ID != OfferCodexMD || offers[1].ID != OfferCodexSkills {
		t.Fatalf("offers = %+v", offers)
	}
	for _, o := range offers {
		if _, err := ApplyOffer(ws2, []string{HarnessCodex}, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(ws2, "AGENTS.md")); string(b) != "claude rules\n" {
		t.Errorf("AGENTS.md = %q", b)
	}
	if !readable(t, filepath.Join(ws2, ".agents", "skills", "qa", "SKILL.md")) {
		t.Error("codex cannot read the linked skill")
	}
}

func TestLinkFallsBackToCopyWhenSymlinksFail(t *testing.T) {
	_, ws := productParsing(t)
	old := symlink
	symlink = func(string, string) error { return errors.New("symlinks not permitted") }
	defer func() { symlink = old }()
	made, err := ApplyOffer(ws, []string{HarnessClaude}, OfferClaudeSkills)
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 3 || made[0].Mode != "copy" {
		t.Fatalf("made = %+v", made)
	}
	p := filepath.Join(ws, ".claude", "skills", "graphify")
	if st, _ := os.Lstat(p); st.Mode()&os.ModeSymlink != 0 {
		t.Error("expected a real copy")
	}
	if !readable(t, filepath.Join(p, "SKILL.md")) {
		t.Error("copy is missing SKILL.md")
	}
}

// A symlinked ancestor must never carry a write out of the checkout: with
// .claude/skills pointing at ~/.claude/skills the offer is not made, applying
// it errors, and the home directory is untouched.
func TestOffersNeverWriteThroughSymlinkedParents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(userSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	_, ws := repo(t, map[string]string{
		"AGENTS.md":                 "a\n",
		".codex/skills/qa/SKILL.md": "---\nname: qa\ndescription: q\n---\n",
	})
	if err := os.MkdirAll(filepath.Join(ws, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(userSkills, filepath.Join(ws, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	before := digest(t, home)

	for _, o := range Offers(ws, []string{HarnessClaude}) {
		if o.ID == OfferClaudeSkills {
			t.Fatalf("offered a link through a symlinked .claude/skills: %+v", o)
		}
	}
	// Even if an offer were applied directly, the write is refused.
	if _, err := create(ws, Item{Path: ".claude/skills/qa", Kind: "link", Target: "../../.codex/skills/qa"}); !errors.Is(err, errUnsafe) {
		t.Errorf("create through symlinked parent: %v", err)
	}
	if digest(t, home) != before {
		t.Error("home directory changed")
	}
}

func TestOffersNeverTargetUserSkillRootsWhenProjectIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skill(t, filepath.Join(home, ".codex", "skills"), "qa", "q")
	before := digest(t, home)
	for _, o := range Offers(home, []string{HarnessClaude}) {
		if o.ID == OfferClaudeSkills {
			t.Fatalf("offered to write into ~/.claude/skills: %+v", o)
		}
	}
	if _, err := create(home, Item{Path: ".claude/skills/qa", Kind: "link", Target: "../../.codex/skills/qa"}); !errors.Is(err, errUnsafe) {
		t.Errorf("create under ~/.claude: %v", err)
	}
	if digest(t, home) != before {
		t.Error("home directory changed")
	}
}

func TestCopyFallbackFollowsSymlinkedSubdirectories(t *testing.T) {
	_, ws := repo(t, map[string]string{".codex/skills/qa/SKILL.md": "---\nname: qa\ndescription: q\n---\n"})
	shared := filepath.Join(ws, "shared-refs") // inside the checkout; links out of it are skipped
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "note.md"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(ws, ".codex", "skills", "qa", "refs")); err != nil {
		t.Fatal(err)
	}
	old := symlink
	symlink = func(string, string) error { return errors.New("symlinks not permitted") }
	defer func() { symlink = old }()
	if _, err := ApplyOffer(ws, []string{HarnessClaude}, OfferClaudeSkills); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, ".claude", "skills", "qa", "refs", "note.md")); string(b) != "n" {
		t.Errorf("symlinked subdirectory not copied: %q", b)
	}
}

// Sessions are worktrees of a commit. After applying offers to the main
// checkout, the new files are untracked: a fresh worktree must not see them and
// the main checkout must report them as pending, not native, until committed.
func TestAppliedOffersArePendingUntilCommitted(t *testing.T) {
	main, _ := productParsing(t)
	for _, id := range []string{OfferClaudeMD, OfferClaudeSkills} {
		if _, err := ApplyOffer(main, []string{HarnessClaude}, id); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{Workspace: main, Committed: GitCommitted(main)}
	inv := Discover(opts)
	if got := InstructionStatus(inv.Instructions, HarnessClaude); got != StatusPending {
		t.Errorf("claude instructions = %s, want pending", got)
	}
	var skillStatus []string
	for i, st := range Statuses(inv, HarnessClaude) {
		if inv.Skills[i].Convention == ConvClaude {
			skillStatus = append(skillStatus, st)
		}
	}
	if len(skillStatus) != 3 {
		t.Fatalf("want 3 linked skills, got %v", skillStatus)
	}
	for _, st := range skillStatus {
		if st != StatusPending {
			t.Errorf("linked skill status = %s, want pending", st)
		}
	}
	want := map[string]bool{"CLAUDE.md": true, ".claude/skills/graphify": true, ".claude/skills/source-regression-qa": true, ".claude/skills/source-refresh-operations": true}
	for _, p := range inv.PendingCommit {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("not reported as pending: %v (got %v)", want, inv.PendingCommit)
	}

	// A session created now gets none of it.
	fresh := filepath.Join(t.TempDir(), "fresh")
	git(t, main, "worktree", "add", "-q", "-b", "fresh", fresh)
	fi := Discover(Options{Workspace: fresh, Committed: GitCommitted(fresh)})
	if fi.Instructions.ClaudeMD || InstructionStatus(fi.Instructions, HarnessClaude) == StatusNative {
		t.Error("a fresh worktree sees the uncommitted CLAUDE.md")
	}
	for i, st := range Statuses(fi, HarnessClaude) {
		if fi.Skills[i].Convention == ConvClaude {
			t.Errorf("a fresh worktree has .claude skill %s (%s)", fi.Skills[i].Name, st)
		}
	}

	// Once committed, they are native.
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "claude files")
	inv = Discover(Options{Workspace: main, Committed: GitCommitted(main)})
	if got := InstructionStatus(inv.Instructions, HarnessClaude); got != StatusNative || len(inv.PendingCommit) != 0 {
		t.Errorf("after commit: %s, pending %v", got, inv.PendingCommit)
	}
}

func TestPendingNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CLAUDE.md"), "x")
	inv := Discover(Options{Workspace: dir, Committed: GitCommitted(dir)})
	if inv.Instructions.ClaudeMDPending || len(inv.PendingCommit) != 0 {
		t.Errorf("flagged pending outside git: %+v", inv.PendingCommit)
	}
}

// A symlink committed inside a skill must not smuggle a file from outside the
// checkout into the copy fallback.
func TestCopyFallbackSkipsSymlinksOutOfCheckout(t *testing.T) {
	_, ws := productParsing(t)
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(ws, ".agents", "skills", "graphify", "key")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	old := symlink
	symlink = func(string, string) error { return errors.New("symlinks not permitted") }
	defer func() { symlink = old }()
	if _, err := ApplyOffer(ws, []string{HarnessClaude}, OfferClaudeSkills); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(ws, ".claude", "skills", "graphify")
	if !readable(t, filepath.Join(copied, "SKILL.md")) {
		t.Fatal("copy is missing SKILL.md")
	}
	if _, err := os.Lstat(filepath.Join(copied, "key")); err == nil {
		t.Fatal("a symlink out of the checkout was copied into the skill")
	}
}

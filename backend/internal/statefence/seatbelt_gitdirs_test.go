package statefence

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
)

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitLayout is a project repo on record and a checkout the agent controls.
type gitLayout struct {
	f                fenceEnv
	repo, common, ws string
	private          string
}

func newGitLayout(t *testing.T) gitLayout {
	t.Helper()
	f := newFenceEnv(t)
	repo := filepath.Join(f.root, "repo")
	common := filepath.Join(repo, ".git")
	l := gitLayout{f: f, repo: repo, common: common, ws: filepath.Join(f.root, "w"), private: filepath.Join(common, "worktrees", "w")}
	mkdir(t, l.private)
	mkdir(t, l.ws)
	return l
}

func (l gitLayout) link(t *testing.T) {
	t.Helper()
	writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+l.private+"\n")
	writeFile(t, filepath.Join(l.private, "commondir"), "../..\n")
	writeFile(t, filepath.Join(l.private, "gitdir"), filepath.Join(l.ws, ".git")+"\n")
}

func TestGitFenceGrantsOnlyTheRecordedRepo(t *testing.T) {
	linked := func(t *testing.T, l gitLayout) { l.link(t) }
	cases := []struct {
		name  string
		plant func(t *testing.T, l gitLayout)
		repo  func(l gitLayout) string
		grant bool
	}{
		{name: "normal linked worktree", plant: linked, grant: true},
		{name: "relative gitdir claim", grant: true, plant: func(t *testing.T, l gitLayout) {
			l.link(t)
			rel, err := filepath.Rel(l.ws, l.private)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+rel+"\n")
		}},
		{name: "rewritten commondir is ignored", grant: true, plant: func(t *testing.T, l gitLayout) {
			l.link(t)
			writeFile(t, filepath.Join(l.private, "commondir"), l.f.home+"\n")
		}},
		{name: "gitdir names home", plant: func(t *testing.T, l gitLayout) {
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+l.f.home+"\n")
		}},
		{name: "gitdir names a look-alike outside the repo", plant: func(t *testing.T, l gitLayout) {
			fake := filepath.Join(l.ws, "fake", "worktrees", "w")
			mkdir(t, fake)
			writeFile(t, filepath.Join(fake, "commondir"), l.f.home+"\n")
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+fake+"\n")
		}},
		{name: "gitdir names a sibling worktree", plant: func(t *testing.T, l gitLayout) {
			other := filepath.Join(l.common, "worktrees", "other")
			mkdir(t, other)
			otherWS := filepath.Join(l.f.root, "other-ws")
			mkdir(t, otherWS)
			writeFile(t, filepath.Join(otherWS, ".git"), "gitdir: "+other+"\n")
			writeFile(t, filepath.Join(other, "gitdir"), filepath.Join(otherWS, ".git")+"\n")
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+other+"\n")
		}},
		{name: "git symlink", plant: func(t *testing.T, l gitLayout) {
			if err := os.Symlink(l.f.home, filepath.Join(l.ws, ".git")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "planted symlink inside worktrees", plant: func(t *testing.T, l gitLayout) {
			evil := filepath.Join(l.common, "worktrees", "evil")
			if err := os.Symlink(l.f.home, evil); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+evil+"\n")
		}},
		{name: "no repo on record", plant: linked, repo: func(gitLayout) string { return "" }},
		{name: "relative repo on record", plant: func(t *testing.T, l gitLayout) {
			l.link(t)
			// From here "repo" resolves to the real repo, relative to the cwd.
			t.Chdir(l.f.root)
		}, repo: func(gitLayout) string { return "repo" }},
		{name: "recorded repo .git is a symlink", plant: func(t *testing.T, l gitLayout) {
			l.link(t)
			repo := filepath.Join(l.f.root, "symlink-repo")
			mkdir(t, repo)
			if err := os.Symlink(l.common, filepath.Join(repo, ".git")); err != nil {
				t.Fatal(err)
			}
		}, repo: func(l gitLayout) string { return filepath.Join(l.f.root, "symlink-repo") }},
		{name: "recorded repo .git is a file", plant: func(t *testing.T, l gitLayout) {
			l.link(t)
			repo := filepath.Join(l.f.root, "linked-repo")
			mkdir(t, repo)
			writeFile(t, filepath.Join(repo, ".git"), "gitdir: "+l.private+"\n")
			writeFile(t, filepath.Join(l.ws, ".git"), "gitdir: "+l.private+"\n")
		}, repo: func(l gitLayout) string { return filepath.Join(l.f.root, "linked-repo") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newGitLayout(t)
			tc.plant(t, l)
			repo := l.repo
			if tc.repo != nil {
				repo = tc.repo(l)
			}
			p := mustProfile(t, l.f.env, protectOnRepo(t, repo), l.ws, HarnessPaths{})
			allowed := section(p, "allow file-write*")
			if slices.Contains(allowed, sub(l.f.home)) {
				t.Fatalf("HOME became writable:\n%s", p)
			}
			if slices.Contains(section(p, "deny file-write*"), sub(filepath.Join(l.f.home, "hooks"))) {
				t.Fatalf("a path the checkout names reached the profile:\n%s", p)
			}
			if slices.Contains(allowed, sub(l.common)) {
				t.Fatalf("the common dir entry itself must not be granted:\n%s", p)
			}
			if got := slices.Contains(allowed, below(l.common)); got != tc.grant {
				t.Fatalf("common dir grant = %v, want %v:\n%s", got, tc.grant, p)
			}
			if tc.grant {
				denied := section(p, "deny file-write*")
				for _, want := range []string{filepath.Join(l.common, "config"), filepath.Join(l.common, "hooks"), filepath.Join(l.common, "commondir")} {
					if !slices.Contains(denied, sub(want)) {
						t.Fatalf("%s must stay read-only:\n%s", want, p)
					}
				}
			}
		})
	}
}

// The sibling entry and worktrees/ are denied, so neither can be renamed away;
// names are taken literally and a planted link names nothing.
func TestGitFenceProtectsSiblingWorktrees(t *testing.T) {
	l := newGitLayout(t)
	l.link(t)
	worktrees := filepath.Join(l.common, "worktrees")
	sibling := filepath.Join(worktrees, "other")
	glob := filepath.Join(worktrees, "*")
	mkdir(t, sibling)
	mkdir(t, glob)
	if err := os.Symlink(l.f.home, filepath.Join(worktrees, "planted")); err != nil {
		t.Fatal(err)
	}
	p := mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
	denied := section(p, "deny file-write*")
	if !slices.Contains(denied, sub(sibling)) || !slices.Contains(denied, sub(glob)) {
		t.Fatalf("sibling private dirs must be read-only:\n%s", p)
	}
	if !slices.Contains(denied, `(literal "`+worktrees+`")`) {
		t.Fatalf("worktrees/ must not be renamable:\n%s", p)
	}
	for _, g := range denied {
		if g == sub(l.private) || g == sub(l.f.home) || strings.Contains(g, "regex") && strings.Contains(g, "worktrees") {
			t.Fatalf("own dir, a planted link or a glob reached the denies (%s):\n%s", g, p)
		}
	}
}

// Too many entries to list means no grant, never an unprotected sibling.
func TestGitFenceGrantsNothingWhenWorktreesOverflow(t *testing.T) {
	l := newGitLayout(t)
	l.link(t)
	for i := range maxSiblingWorktrees + 1 {
		mkdir(t, filepath.Join(l.common, "worktrees", "s"+strconv.Itoa(i)))
	}
	p := mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
	if slices.Contains(section(p, "allow file-write*"), below(l.common)) {
		t.Fatalf("an unlistable worktrees dir must not be granted:\n%s", p)
	}
}

// A plain checkout's .git sits under the workspace grant. Nothing is granted
// from it, and its config and hooks stay denied.
func TestGitFencePlainCheckoutGrantsNothingExtra(t *testing.T) {
	f := newFenceEnv(t)
	ws := filepath.Join(f.root, "w")
	dotGit := filepath.Join(ws, ".git")
	mkdir(t, dotGit)
	p := mustProfile(t, f.env, protectOn(t, nil), ws, HarnessPaths{})
	if slices.Contains(section(p, "allow file-write*"), sub(dotGit)) {
		t.Errorf(".git must not get its own grant:\n%s", p)
	}
	if !slices.Contains(section(p, "deny file-write*"), sub(filepath.Join(dotGit, "hooks"))) {
		t.Errorf("hooks must stay read-only:\n%s", p)
	}
	if !slices.Contains(section(p, "deny file-write*"), sub(filepath.Join(dotGit, "commondir"))) {
		t.Errorf("commondir must stay read-only:\n%s", p)
	}
}

// The git grant is fixed once gitWriteSet returns: swapping <repo>/.git for a
// link afterwards must not change what the rule opens.
func TestGitGrantIgnoresSwapAfterTheCheck(t *testing.T) {
	l := newGitLayout(t)
	l.link(t)
	grant := gitWriteSet(l.repo, l.ws).grant
	if grant != l.common {
		t.Fatalf("grant = %q, want %q", grant, l.common)
	}
	moved := filepath.Join(l.f.root, "moved")
	if err := os.Rename(l.common, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(l.f.home, l.common); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	allowBelow(&b, grant)
	if got := b.String(); strings.Contains(got, below(l.f.home)) || !strings.Contains(got, below(l.common)) {
		t.Fatalf("rule followed the swapped link:\n%s", got)
	}
}

// Every write grant is replaceable by the process it fences, so none may follow
// a symlink planted at its own path.
func TestWriteGrantsDoNotFollowAPlantedLink(t *testing.T) {
	for name, target := range map[string]func(l gitLayout) string{
		"workspace":   func(l gitLayout) string { return l.ws },
		"tmpdir":      func(l gitLayout) string { return l.f.tmp },
		"build cache": func(l gitLayout) string { return filepath.Join(l.f.home, ".cache") },
	} {
		t.Run(name, func(t *testing.T) {
			l := newGitLayout(t)
			victim := filepath.Join(l.f.root, "victim")
			mkdir(t, victim)
			path := target(l)
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, path); err != nil {
				t.Fatal(err)
			}
			p := mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
			if strings.Contains(p, victim) {
				t.Fatalf("a planted link widened the profile:\n%s", p)
			}
			if !slices.Contains(section(p, "allow file-write*"), sub(path)) {
				t.Fatalf("the grant itself must remain:\n%s", p)
			}
		})
	}
}

// An auth home is both written and re-allowed for reading, so a link planted at
// it must not re-open a denied credential home on the next launch.
func TestAuthReadDoesNotFollowAPlantedLink(t *testing.T) {
	f := newFenceEnv(t)
	aws := filepath.Join(f.home, ".aws")
	ssh := filepath.Join(f.home, ".ssh")
	extra := HarnessPaths{Write: []string{"~/.aws"}, AuthRead: []string{"~/.aws"}}
	mkdir(t, aws)
	mkdir(t, ssh)
	p := mustProfile(t, f.env, policytest.Hardened(t), "", extra)
	if !slices.Contains(section(p, "allow file-read*"), sub(aws)) {
		t.Fatalf("a real auth home must stay readable:\n%s", p)
	}
	if err := os.RemoveAll(aws); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ssh, aws); err != nil {
		t.Fatal(err)
	}
	p = mustProfile(t, f.env, policytest.Hardened(t), "", extra)
	reAllowed := section(p, "allow file-read*")
	if slices.Contains(reAllowed, sub(ssh)) {
		t.Fatalf("a planted link re-opened a credential home:\n%s", p)
	}
	if !slices.Contains(reAllowed, sub(aws)) {
		t.Fatalf("the re-allow itself must remain:\n%s", p)
	}
}

//go:build darwin

package statefence

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// commondirAttack is a real repo plus, in the agent's checkout, a git dir whose
// config runs a script from any git that reads it: what a planted commondir
// would point the owner's unfenced git at.
type commondirAttack struct {
	f                     fenceEnv
	repo, common, ws, ptr string
	marker, evil          string
	gitEnv                []string
}

func newCommondirAttack(t *testing.T, ws string) commondirAttack {
	t.Helper()
	f := newFenceEnv(t)
	repo := testenv.GitRepo(t)
	if ws == "" {
		ws = repo
	}
	cfg := filepath.Join(f.root, "gitconfig")
	writeFile(t, cfg, "[user]\n\tname = t\n\temail = t@example.com\n")
	return commondirAttack{
		f: f, repo: repo, common: filepath.Join(repo, ".git"), ws: ws,
		ptr: filepath.Join(ws, "ptr"), marker: filepath.Join(f.root, "marker"), evil: filepath.Join(ws, "evil"),
		gitEnv: []string{"HOME=" + f.home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + cfg, "GIT_CONFIG_NOSYSTEM=1"},
	}
}

// newLinkedCommondirAttack is the attack set up from a real linked worktree.
func newLinkedCommondirAttack(t *testing.T) commondirAttack {
	t.Helper()
	return newNamedLinkedAttack(t, "w")
}

func newNamedLinkedAttack(t *testing.T, name string) commondirAttack {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newCommondirAttack(t, filepath.Join(root, name))
	a.git(t, a.repo, "worktree", "add", "-q", a.ws, "-b", name)
	return a
}

func (a commondirAttack) plantPayload(t *testing.T) {
	t.Helper()
	script := filepath.Join(a.ws, "evil.sh")
	writeFile(t, script, "#!/bin/sh\necho pwned >> "+a.marker+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(a.evil, "objects"))
	mkdir(t, filepath.Join(a.evil, "refs"))
	writeFile(t, filepath.Join(a.evil, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(a.evil, "config"), "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tfsmonitor = "+script+"\n")
	writeFile(t, a.ptr, a.evil+"\n")
}

func (a commondirAttack) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = a.gitEnv
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (a commondirAttack) ownerGitRanPayload(t *testing.T) bool {
	t.Helper()
	return a.gitRanPayloadIn(t, a.repo)
}

func (a commondirAttack) gitRanPayloadIn(t *testing.T, checkout string) bool {
	t.Helper()
	_ = os.Remove(a.marker)
	a.git(t, checkout, "status", "--short")
	_, err := os.Stat(a.marker)
	return err == nil
}

func (a commondirAttack) runFenced(profile, script string) ([]byte, error) {
	argv := Wrap([]string{"/bin/sh", "-c", script}, profile)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = a.gitEnv
	return cmd.CombinedOutput()
}

// assertPayloadLive proves the attack is real, so a refused write below means
// something: planted without the fence, the file does run the agent's script.
func (a commondirAttack) assertPayloadLive(t *testing.T, commondir string) {
	t.Helper()
	writeFile(t, commondir, a.evil+"\n")
	ran := a.ownerGitRanPayload(t)
	if err := os.Remove(commondir); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if a.ownerGitRanPayload(t) {
		t.Fatal("payload still runs after removing commondir")
	}
}

func commondirPlants(a commondirAttack, gitDir string) map[string]string {
	target := filepath.Join(gitDir, "commondir")
	return map[string]string{
		"write":             "printf '%s\\n' " + a.evil + " > " + target,
		"differently cased": "printf '%s\\n' " + a.evil + " > " + filepath.Join(gitDir, "CommonDir"),
		"rename onto":       "mv " + a.ptr + " " + target,
		"hard link":         "ln " + a.ptr + " " + target,
		"symlink":           "ln -s " + a.ptr + " " + target,
		"directory":         "mkdir " + target,
	}
}

// B628: below the common dir the fence opens everything but config and hooks,
// and git follows a commondir file in a main git dir too. Planting one would run
// the agent's config in the owner's unfenced git in the main checkout.
func TestFenceDeniesPlantingCommondirInTheMainGitDir(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	for name := range commondirPlants(commondirAttack{}, "") {
		t.Run(name, func(t *testing.T) {
			a := newLinkedCommondirAttack(t)
			a.plantPayload(t)
			a.assertPayloadLive(t, filepath.Join(a.common, "commondir"))
			profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
			if out, err := a.runFenced(profile, commondirPlants(a, a.common)[name]); err == nil {
				t.Fatalf("fenced plant succeeded (%s)", out)
			}
			if a.ownerGitRanPayload(t) {
				t.Fatal("the owner's unfenced git ran the agent's config")
			}
		})
	}
}

// A second linked worktree's private dir is under the git grant, and its .git
// file is out of the agent's reach, so the grant is the only way in. Each plant
// must fail, including the ones that first rename the entry out of a path deny.
func TestFenceDeniesPlantingThroughASiblingWorktree(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	plants := map[string]func(a commondirAttack, worktrees string) string{
		"write commondir": func(a commondirAttack, worktrees string) string {
			return "printf '%s\\n' " + a.evil + " > " + filepath.Join(worktrees, "sib", "commondir")
		},
		"rename the sibling away": func(a commondirAttack, worktrees string) string {
			moved := filepath.Join(worktrees, "moved")
			return "mv " + filepath.Join(worktrees, "sib") + " " + moved + " && printf '%s\\n' " + a.evil + " > " + filepath.Join(moved, "commondir") + " && mv " + moved + " " + filepath.Join(worktrees, "sib")
		},
		"rename worktrees away": func(a commondirAttack, worktrees string) string {
			moved := filepath.Join(a.common, "moved")
			return "mv " + worktrees + " " + moved + " && printf '%s\\n' " + a.evil + " > " + filepath.Join(moved, "sib", "commondir") + " && mv " + moved + " " + worktrees
		},
	}
	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			a := newLinkedCommondirAttack(t)
			sib := filepath.Join(filepath.Dir(a.ws), "sib")
			a.git(t, a.repo, "worktree", "add", "-q", sib, "-b", "sib")
			a.plantPayload(t)
			worktrees := filepath.Join(a.common, "worktrees")
			commondir := filepath.Join(worktrees, "sib", "commondir")
			original, err := os.ReadFile(commondir)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, commondir, a.evil+"\n")
			if !a.gitRanPayloadIn(t, sib) {
				t.Fatal("payload never ran: this test no longer exercises the attack")
			}
			writeFile(t, commondir, string(original))
			profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
			if out, err := a.runFenced(profile, plant(a, worktrees)); err == nil {
				t.Fatalf("fenced plant succeeded (%s)", out)
			}
			if a.gitRanPayloadIn(t, sib) {
				t.Fatal("the sibling's unfenced git ran the agent's config")
			}
		})
	}
}

// The same file in a plain checkout's .git, which is under the workspace grant.
// TestFenceDeniesMovingAPlainCheckoutsGitDirAway covers moving .git aside first.
func TestFenceDeniesPlantingCommondirInAPlainCheckout(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newCommondirAttack(t, "")
	a.plantPayload(t)
	a.assertPayloadLive(t, filepath.Join(a.common, "commondir"))
	profile := mustProfile(t, a.f.env, protectOn(t, nil), a.ws, HarnessPaths{})
	for name, script := range commondirPlants(a, a.common) {
		if out, err := a.runFenced(profile, script); err == nil {
			t.Errorf("%s: fenced plant succeeded (%s)", name, out)
		}
	}
	if a.ownerGitRanPayload(t) {
		t.Fatal("the owner's unfenced git ran the agent's config")
	}
	if out, err := a.runFenced(profile, "touch "+filepath.Join(a.ws, "still-writable")); err != nil {
		t.Fatalf("the checkout must stay writable: %v (%s)", err, out)
	}
}

// Denying commondir must not cost a linked worktree any normal git, including
// the worktree add that writes a new private dir's commondir.
func TestFenceKeepsNormalGitInALinkedWorktree(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newLinkedCommondirAttack(t)
	remote := filepath.Join(a.ws, "remote.git")
	a.git(t, a.ws, "init", "-q", "--bare", remote)
	// A live sibling with reflogs for gc to expire, and a stale one to prune.
	root := filepath.Dir(a.ws)
	sib := filepath.Join(root, "sib")
	a.git(t, a.repo, "worktree", "add", "-q", sib, "-b", "sib")
	a.git(t, sib, "commit", "-q", "--allow-empty", "-m", "sibling")
	stale := filepath.Join(root, "stale")
	a.git(t, a.repo, "worktree", "add", "-q", stale, "-b", "stale")
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	script := "cd " + a.ws + " && set -e" +
		" && echo hi > tracked && git status --short && git add tracked && git commit -q -m change" +
		" && git branch side && git push -q " + remote + " w && git fetch -q " + remote + " w" +
		" && git -c gc.reflogExpire=now -c gc.reflogExpireUnreachable=now -c gc.worktreePruneExpire=now gc -q && git worktree add -q nested -b nested && git worktree remove --force nested"
	if out, err := a.runFenced(profile, script); err != nil {
		t.Fatalf("normal git failed inside the fence: %v\n%s", err, out)
	}
}

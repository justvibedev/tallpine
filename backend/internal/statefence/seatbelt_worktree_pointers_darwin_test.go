//go:build darwin

package statefence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func (a commondirAttack) configBody() string {
	return "\n[core]\n\tfsmonitor = " + filepath.Join(a.ws, "evil.sh") + "\n"
}

// shellConfigBody is configBody as a printf argument for a fenced shell.
func (a commondirAttack) shellConfigBody() string {
	return "'" + strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(a.configBody()) + "'"
}

func (a commondirAttack) privateDir() string {
	return filepath.Join(a.common, "worktrees", filepath.Base(a.ws))
}

// assertRewriteRuns proves writing body to file makes unfenced git in checkout
// run the payload, then restores file, so a refused write below means something.
func (a commondirAttack) assertRewriteRuns(t *testing.T, checkout, file, body string) {
	t.Helper()
	original, err := os.ReadFile(file)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		t.Fatal(err)
	}
	writeFile(t, file, body)
	ran := a.gitRanPayloadIn(t, checkout)
	if missing {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	} else {
		writeFile(t, file, string(original))
	}
	if !ran {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if a.gitRanPayloadIn(t, checkout) {
		t.Fatal("payload still runs after restoring the file")
	}
}

func (a commondirAttack) assertAllDenied(t *testing.T, profile, checkout string, plants map[string]string) {
	t.Helper()
	for name, script := range plants {
		if out, err := a.runFenced(profile, script); err == nil {
			t.Errorf("%s: fenced plant succeeded (%s)", name, out)
		}
		if a.gitRanPayloadIn(t, checkout) {
			t.Fatalf("%s: unfenced git ran the agent's config", name)
		}
	}
}

func (a commondirAttack) addSubmodule(t *testing.T) {
	t.Helper()
	upstream := filepath.Join(a.f.root, "upstream")
	a.git(t, a.f.root, "init", "-q", "-b", "main", upstream)
	a.git(t, upstream, "commit", "-q", "--allow-empty", "-m", "u")
	a.git(t, a.repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", upstream, "sub")
	a.git(t, a.repo, "commit", "-q", "-m", "add sub")
}

// B629: unfenced git in the agent's own checkout follows its .git file and its
// private dir's commondir, so neither may be repointed, directly or by moving
// the entry that holds it out of the way first.
func TestFenceDeniesRepointingTheAgentsOwnCheckout(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newLinkedCommondirAttack(t)
	a.plantPayload(t)
	private := a.privateDir()
	dotGit := filepath.Join(a.ws, ".git")
	a.assertRewriteRuns(t, a.ws, filepath.Join(private, "commondir"), a.evil+"\n")
	a.assertRewriteRuns(t, a.ws, dotGit, "gitdir: "+a.evil+"\n")
	away := filepath.Join(a.f.env["TMPDIR"], "away")
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	plants := commondirPlants(a, private)
	plants["hard link out and write through it"] = "ln " + filepath.Join(private, "commondir") + " " + a.ptr + "2 && printf '%s\\n' " + a.evil + " > " + a.ptr + "2"
	plants["rename the private dir away"] = "mv " + private + " " + private + "-moved && printf '%s\\n' " + a.evil + " > " + private + "-moved/commondir && mv " + private + "-moved " + private
	plants["write .git"] = "printf 'gitdir: %s\\n' " + a.evil + " > " + dotGit
	plants["rename .git away"] = "mv " + dotGit + " " + dotGit + "2 && printf 'gitdir: %s\\n' " + a.evil + " > " + dotGit
	plants["remove .git"] = "rm " + dotGit + " && printf 'gitdir: %s\\n' " + a.evil + " > " + dotGit
	plants["replace .git with a link"] = "ln -sf " + a.ptr + " " + dotGit
	plants["rename the checkout away"] = "mv " + a.ws + " " + away + " && printf 'gitdir: %s\\n' " + a.evil + " > " + away + "/.git && mv " + away + " " + a.ws
	a.assertAllDenied(t, profile, a.ws, plants)
	if out, err := a.runFenced(profile, "touch "+filepath.Join(a.ws, "still-writable")+" "+filepath.Join(private, "still-writable")); err != nil {
		t.Fatalf("the checkout and private dir must stay writable: %v (%s)", err, out)
	}
}

// Moving a plain checkout's .git, or the checkout, past the denies inside it.
func TestFenceDeniesMovingAPlainCheckoutsGitDirAway(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newCommondirAttack(t, "")
	a.plantPayload(t)
	dotGit := a.common
	a.assertRewriteRuns(t, a.ws, filepath.Join(dotGit, "config"), a.configBody())
	away := filepath.Join(a.f.env["TMPDIR"], "away")
	body := a.shellConfigBody()
	profile := mustProfile(t, a.f.env, protectOn(t, nil), a.ws, HarnessPaths{})
	a.assertAllDenied(t, profile, a.ws, map[string]string{
		"rename .git away":         "mv " + dotGit + " " + dotGit + "2 && printf " + body + " >> " + dotGit + "2/config && mv " + dotGit + "2 " + dotGit,
		"rename the checkout away": "mv " + a.ws + " " + away + " && printf " + body + " >> " + away + "/.git/config && mv " + away + " " + a.ws,
	})
}

// Tallpine names a later session's worktree after the agent's own, so those
// private dirs are denied though the profile predates them. A worktree added
// under any other name cannot be denied without breaking worktree add/remove,
// so it stays open until the next launch; this records that bound.
func TestFenceDeniesWorktreesOfLaterSessions(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newNamedLinkedAttack(t, "proj-12")
	a.plantPayload(t)
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	root := filepath.Dir(a.ws)
	worktrees := filepath.Join(a.common, "worktrees")
	for _, name := range []string{"proj-13", "proj-orchestrator"} {
		ws := filepath.Join(root, name)
		a.git(t, a.repo, "worktree", "add", "-q", ws, "-b", name)
		commondir := filepath.Join(worktrees, name, "commondir")
		a.assertRewriteRuns(t, ws, commondir, a.evil+"\n")
		moved := filepath.Join(worktrees, name+"-moved")
		a.assertAllDenied(t, profile, ws, map[string]string{
			"write commondir":       "printf '%s\\n' " + a.evil + " > " + commondir,
			"rename the dir away":   "mv " + filepath.Join(worktrees, name) + " " + moved + " && printf '%s\\n' " + a.evil + " > " + moved + "/commondir && mv " + moved + " " + filepath.Join(worktrees, name),
			"write config.worktree": "printf " + a.shellConfigBody() + " > " + filepath.Join(worktrees, name, "config.worktree"),
		})
	}
	hand := filepath.Join(root, "handmade")
	a.git(t, a.repo, "worktree", "add", "-q", hand, "-b", "handmade")
	if out, err := a.runFenced(profile, "printf '%s\\n' "+a.evil+" > "+filepath.Join(worktrees, "handmade", "commondir")); err != nil {
		t.Fatalf("a hand-named later worktree was expected to stay writable: %v (%s)", err, out)
	}
	if !a.gitRanPayloadIn(t, hand) {
		t.Fatal("the documented open path no longer runs: update this test and the note on denyLaterSessionWorktrees")
	}
}

// The agent's own private dir matches the session shape it denies for others.
func TestFenceKeepsNormalGitInASessionNamedWorktree(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newNamedLinkedAttack(t, "proj-12")
	remote := filepath.Join(a.ws, "remote.git")
	a.git(t, a.ws, "init", "-q", "--bare", remote)
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	script := "cd " + a.ws + " && set -e" +
		" && echo hi > tracked && git add tracked && git commit -q -m change" +
		" && git push -q " + remote + " proj-12 && git fetch -q " + remote + " proj-12" +
		" && git gc -q && git worktree add -q side -b side && git worktree remove --force side"
	if out, err := a.runFenced(profile, script); err != nil {
		t.Fatalf("normal git failed inside the fence: %v\n%s", err, out)
	}
}

// Tallpine turns extensions.worktreeConfig on in every repository it manages,
// so the main checkout's config.worktree is live and the agent must not write
// it. The agent's own stays open: the git shim sends `git config` there, and
// worktree add copies it, so both must keep working.
func TestFenceProtectsTheOwnersConfigWorktree(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newLinkedCommondirAttack(t)
	a.plantPayload(t)
	a.git(t, a.repo, "config", "extensions.worktreeConfig", "true")
	a.git(t, a.ws, "config", "--worktree", "user.name", "w")
	target := filepath.Join(a.common, "config.worktree")
	a.assertRewriteRuns(t, a.repo, target, a.configBody())
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	body := a.shellConfigBody()
	a.assertAllDenied(t, profile, a.repo, map[string]string{
		"write":       "printf " + body + " > " + target,
		"rename onto": "printf " + body + " > " + a.ptr + " && mv " + a.ptr + " " + target,
	})
	script := "cd " + a.ws + " && set -e && git config --worktree core.abbrev 9 && git worktree add -q nested -b nested && git worktree remove --force nested"
	if out, err := a.runFenced(profile, script); err != nil {
		t.Fatalf("per-worktree config must keep working: %v\n%s", err, out)
	}
}

// Without the extension git never reads config.worktree, so it needs no deny.
func TestConfigWorktreeIsInertWithoutTheExtension(t *testing.T) {
	a := newLinkedCommondirAttack(t)
	a.plantPayload(t)
	writeFile(t, filepath.Join(a.privateDir(), "config.worktree"), a.configBody())
	if a.gitRanPayloadIn(t, a.ws) {
		t.Fatal("git read config.worktree without extensions.worktreeConfig")
	}
}

// The main checkout's submodules run their config in the owner's git status.
func TestFenceProtectsTheOwnersSubmodules(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newLinkedCommondirAttack(t)
	a.plantPayload(t)
	a.addSubmodule(t)
	config := filepath.Join(a.common, "modules", "sub", "config")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	a.assertRewriteRuns(t, a.repo, config, string(data)+a.configBody())
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	modules := filepath.Join(a.common, "modules")
	a.assertAllDenied(t, profile, a.repo, map[string]string{
		"write config":   "printf " + a.shellConfigBody() + " >> " + config,
		"plant a hook":   "mkdir -p " + filepath.Join(modules, "sub", "hooks") + " && touch " + filepath.Join(modules, "sub", "hooks", "post-checkout"),
		"rename modules": "mv " + modules + " " + modules + "2",
	})
}

// A linked worktree clones into its own private dir, past the deny above, and a
// plain checkout's .git/modules is not denied at all.
func TestFenceKeepsSubmoduleUpdate(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	update := "git -c protocol.file.allow=always submodule update --init"
	t.Run("linked", func(t *testing.T) {
		a := newLinkedCommondirAttack(t)
		a.addSubmodule(t)
		a.git(t, a.ws, "merge", "-q", "--ff-only", "main")
		profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
		if out, err := a.runFenced(profile, "cd "+a.ws+" && "+update); err != nil {
			t.Fatalf("submodule update failed inside the fence: %v\n%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(a.privateDir(), "modules", "sub", "HEAD")); err != nil {
			t.Fatalf("the submodule did not land in the private dir: %v", err)
		}
	})
	t.Run("plain", func(t *testing.T) {
		a := newCommondirAttack(t, "")
		a.addSubmodule(t)
		profile := mustProfile(t, a.f.env, protectOn(t, nil), a.ws, HarnessPaths{})
		if out, err := a.runFenced(profile, "cd "+a.ws+" && "+update+" && echo hi > tracked && git add tracked && git commit -q -m c && git gc -q"); err != nil {
			t.Fatalf("normal git failed inside the fence: %v\n%s", err, out)
		}
	})
}

// A gitlink to a repository the agent builds in its own checkout runs that
// repository's config in unfenced git there. Submodule update needs exactly
// those writes, so own-checkout git stays reachable: this records the bound the
// denies above do not cross, and that it stops at the agent's own checkout.
func TestAgentsOwnCheckoutStaysReachableThroughAGitlink(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	a := newLinkedCommondirAttack(t)
	a.plantPayload(t)
	profile := mustProfile(t, a.f.env, protectOnRepo(t, a.repo), a.ws, HarnessPaths{})
	script := "cd " + a.ws + " && set -e && git init -q nest && git -C nest commit -q --allow-empty -m n" +
		" && printf " + a.shellConfigBody() + " >> nest/.git/config" +
		" && git update-index --add --cacheinfo 160000,$(git -C nest rev-parse HEAD),nest"
	if out, err := a.runFenced(profile, script); err != nil {
		t.Fatalf("plant failed: %v\n%s", err, out)
	}
	if !a.gitRanPayloadIn(t, a.ws) {
		t.Fatal("the gitlink no longer runs: update this test and the note on gitFence.writeRules")
	}
	if a.ownerGitRanPayload(t) {
		t.Fatal("the gitlink reached the main checkout's git")
	}
}

// A filter driver's name is the agent's choice, so no global option turns it
// off: hardened git status, which the daemon runs in the agent's checkout, still
// runs one set in its own config.worktree. Closing that needs a deny on the file
// that `git config --worktree` writes. This records the bound.
func TestHardenedGitStillRunsAnAgentsFilterDriver(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	file := filepath.Join(w.Path, "f")
	writeFile(t, file, "a\n")
	writeFile(t, filepath.Join(w.Path, ".gitattributes"), "f filter=x\n")
	w.Git(t, w.Path, "add", "f", ".gitattributes")
	w.Git(t, w.Path, "commit", "-q", "-m", "c")
	w.Git(t, w.Path, "config", "--worktree", "filter.x.clean", w.Script)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatal(err)
	}
	w.Git(t, w.Path, append(ports.GitHardenedArgs(), "status", "--porcelain")...)
	if !w.Ran() {
		t.Fatal("hardened git no longer runs the filter: update this test and the notes on gitFence.writeRules and ports.GitHardenedArgs")
	}
}

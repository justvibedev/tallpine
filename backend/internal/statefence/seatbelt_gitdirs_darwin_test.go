//go:build darwin

package statefence

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
)

// B619: everything an agent writes in its checkout must not reach the profile.
// Each case plants a layout, builds the real profile and runs a real write into
// HOME inside it.
func TestFenceDoesNotWidenThroughCraftedGitLayout(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	cases := map[string]func(t *testing.T, ws, home string){
		"gitdir file names home": func(t *testing.T, ws, home string) {
			writeFile(t, filepath.Join(ws, ".git"), "gitdir: "+home+"\n")
		},
		"gitdir file names a private dir whose commondir names home": func(t *testing.T, ws, home string) {
			private := filepath.Join(ws, "fake", "worktrees", "w")
			mkdir(t, private)
			writeFile(t, filepath.Join(private, "commondir"), home+"\n")
			writeFile(t, filepath.Join(ws, ".git"), "gitdir: "+private+"\n")
		},
		".git symlink to home": func(t *testing.T, ws, home string) {
			if err := os.Symlink(home, filepath.Join(ws, ".git")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFenceEnv(t)
			ws := filepath.Join(f.root, "wt")
			mkdir(t, ws)
			plant(t, ws, f.home)
			// A real repo on record: the crafted layout must still not reach HOME.
			repo := filepath.Join(f.root, "repo")
			mkdir(t, filepath.Join(repo, ".git", "worktrees"))
			profile := mustProfile(t, f.env, protectOnRepo(t, repo), ws, HarnessPaths{})
			target := filepath.Join(f.home, "pwned")
			argv := Wrap([]string{"touch", target}, profile)
			if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err == nil {
				t.Fatalf("fenced write into HOME succeeded through a crafted git layout (%s)", out)
			}
		})
	}
}

// Rename-and-symlink attacks, run inside the real fence: the first profile is
// the one the agent runs under, the second the one the next launch builds.
func TestFenceDoesNotWidenThroughRenameAndSymlink(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	run := func(profile, script string) error {
		argv := Wrap([]string{"/bin/sh", "-c", script}, profile)
		return exec.Command(argv[0], argv[1:]...).Run()
	}
	setup := func(t *testing.T) (gitLayout, string) {
		l := newGitLayout(t)
		l.link(t)
		writeFile(t, filepath.Join(l.common, "config"), "[core]\n")
		return l, mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
	}
	swaps := map[string]func(l gitLayout) string{
		"tmpdir": func(l gitLayout) string {
			return "mv " + l.f.tmp + " " + l.ws + "/moved && ln -s " + l.f.home + " " + l.f.tmp
		},
	}
	for name, script := range swaps {
		t.Run(name, func(t *testing.T) {
			l, p := setup(t)
			if err := run(p, script(l)); err != nil {
				t.Fatalf("the swap itself should run inside the fence: %v", err)
			}
			next := mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
			if run(next, "touch "+filepath.Join(l.f.home, "pwned")) == nil {
				t.Fatal("the next launch can write HOME")
			}
		})
	}
	// The checkout's own entry is denied now, so the fence refuses the first
	// step; the swap is then made unfenced, since the next launch must hold anyway.
	t.Run("workspace", func(t *testing.T) {
		l, p := setup(t)
		moved := filepath.Join(l.f.tmp, "moved")
		if run(p, "mv "+l.ws+" "+moved) == nil {
			t.Fatal("the checkout was moved out of the fence's reach")
		}
		if err := os.Rename(l.ws, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(l.f.home, l.ws); err != nil {
			t.Fatal(err)
		}
		next := mustProfile(t, l.f.env, protectOnRepo(t, l.repo), l.ws, HarnessPaths{})
		if run(next, "touch "+filepath.Join(l.f.home, "pwned")) == nil {
			t.Fatal("the next launch can write HOME")
		}
	})
	t.Run("common dir cannot be moved", func(t *testing.T) {
		l, p := setup(t)
		if run(p, "mv "+l.common+" "+l.ws+"/moved") == nil {
			t.Fatal("the common git dir was moved out of the fence's reach")
		}
		if err := run(p, "touch "+filepath.Join(l.common, "packed-refs.lock")); err != nil {
			t.Fatalf("files below the common dir must stay writable: %v", err)
		}
		if run(p, "echo evil >> "+filepath.Join(l.common, "config")) == nil {
			t.Fatal("shared config must stay read-only")
		}
	})
}

// The read side of the same attack, under the hardened profile: swap an auth
// home for a link to ~/.ssh, then read a private key from the next launch.
func TestHardenedFenceKeepsKeysHiddenAfterAuthHomeSwap(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	f := newFenceEnv(t)
	aws := filepath.Join(f.home, ".aws")
	ssh := filepath.Join(f.home, ".ssh")
	mkdir(t, aws)
	mkdir(t, ssh)
	key := filepath.Join(ssh, "id_ed25519")
	writeFile(t, key, "secret")
	extra := HarnessPaths{Write: []string{"~/.aws"}, AuthRead: []string{"~/.aws"}}
	run := func(profile, script string) error {
		argv := Wrap([]string{"/bin/sh", "-c", script}, profile)
		return exec.Command(argv[0], argv[1:]...).Run()
	}
	first := mustProfile(t, f.env, policytest.Hardened(t), "", extra)
	if run(first, "cat "+key) == nil {
		t.Fatal("the key must be hidden before the swap")
	}
	if err := run(first, "mv "+aws+" "+f.tmp+"/aws && ln -s "+ssh+" "+aws); err != nil {
		t.Fatalf("the swap itself should run inside the fence: %v", err)
	}
	next := mustProfile(t, f.env, policytest.Hardened(t), "", extra)
	if run(next, "cat "+key) == nil {
		t.Fatal("the next launch can read a private key through the planted link")
	}
}

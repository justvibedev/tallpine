//go:build darwin

package statefence

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
)

// TestSeatbeltProfileEnforced runs real commands under sandbox-exec with a
// generated profile against a scratch data dir.
func TestSeatbeltProfileEnforced(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not present")
	}
	f := newFenceEnv(t)
	for _, p := range []string{filepath.Join(f.dataDir, "ao.db"), daemontoken.Path(f.runFile)} {
		if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(f.root, "repo")
	ws := filepath.Join(f.root, "wt")
	gitEnv := append(os.Environ(), "HOME="+f.home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(argv ...string) error {
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = gitEnv
		out, err := c.CombinedOutput()
		if err != nil {
			t.Logf("%q: %v\n%s", argv, err, out)
		}
		return err
	}
	if err := run("git", "init", "-q", repo); err != nil {
		t.Fatal(err)
	}
	if err := run("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := run("git", "-C", repo, "worktree", "add", "-q", "-b", "wt", ws); err != nil {
		t.Fatal(err)
	}
	profile := mustProfile(t, f.env, protectOnRepo(t, repo), ws, HarnessPaths{})
	fenced := func(argv ...string) error { return run(Wrap(argv, profile)...) }

	if fenced("cat", filepath.Join(f.dataDir, "ao.db")) == nil {
		t.Error("reading ao.db must fail")
	}
	if fenced("cat", daemontoken.Path(f.runFile)) == nil {
		t.Error("reading the install token must fail")
	}
	if err := fenced("touch", filepath.Join(ws, "ok")); err != nil {
		t.Error("write in worktree must succeed")
	}
	if err := fenced("touch", filepath.Join(f.tmp, "ok")); err != nil {
		t.Error("write in TMPDIR must succeed")
	}
	if fenced("touch", filepath.Join(f.home, "nope")) == nil {
		t.Error("write in HOME must fail")
	}
	if err := fenced("git", "-C", ws, "commit", "-q", "--allow-empty", "-m", "fenced"); err != nil {
		t.Error("git commit in a linked worktree must succeed")
	}
	if fenced("git", "-C", ws, "config", "--local", "x.y", "z") == nil {
		t.Error("writing the shared .git/config must fail")
	}
}

// shortSocketDir is a TMUX_TMPDIR short enough for sun_path.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestSeatbeltRefusesTallpineTmuxSocket connects for real: file rules do not
// govern connect(2), so without the socket rule this reaches the server.
func TestSeatbeltRefusesTallpineTmuxSocket(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	dir := shortSocketDir(t)
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv(brand.Env("TMUX_SOCKET_NAME"), "tp-test")
	sockets := TmuxSocketPaths()
	f := newFenceEnv(t)
	profile := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{})
	for _, sock := range sockets {
		if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		link := filepath.Join(dir, "link-"+filepath.Base(sock))
		if err := os.Symlink(sock, link); err != nil {
			t.Fatal(err)
		}
		connect := func(fenced bool, target string) error {
			argv := []string{"/usr/bin/nc", "-U", "-w", "2", target}
			if fenced {
				argv = Wrap(argv, profile)
			}
			c := exec.Command(argv[0], argv[1:]...)
			return c.Run()
		}
		if err := connect(false, sock); err != nil {
			t.Fatalf("control: unfenced connect to %s failed: %v", sock, err)
		}
		if connect(true, sock) == nil {
			t.Errorf("fenced process connected to %s", sock)
		}
		if connect(true, link) == nil {
			t.Errorf("fenced process connected to %s through a symlink", sock)
		}
	}
}

// TestSeatbeltDeniesLaunchEnvFile reads a live env file the way a poller would.
func TestSeatbeltDeniesLaunchEnvFile(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	dir := agentlaunch.EnvFileDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(dir, "env-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	if _, err := file.WriteString("export TALLPINE_SESSION_TOKEN='secret'\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	f := newFenceEnv(t)
	profile := mustProfile(t, f.env, protectOn(t, nil), "", HarnessPaths{})
	for _, argv := range [][]string{{"cat", file.Name()}, {"ls", dir}} {
		fenced := Wrap(argv, profile)
		if out, err := exec.Command(fenced[0], fenced[1:]...).CombinedOutput(); err == nil {
			t.Errorf("%v must fail under the fence, got %s", argv, out)
		}
	}
}

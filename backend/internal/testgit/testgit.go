// Package testgit resolves the real git binary for tests that put a fake git
// first on PATH.
package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Real returns the first git on PATH that is not a Tallpine session git shim.
// Inside a worker session exec.LookPath("git") returns the shim; a fake git
// that execs it is then the first git on PATH again, and the pair used to
// exec each other forever. The shim directories end in git-shim/<hash>.
func Real() (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Base(filepath.Dir(dir)) == "git-shim" {
			continue
		}
		candidate := filepath.Join(dir, "git")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: "git", Err: exec.ErrNotFound}
}

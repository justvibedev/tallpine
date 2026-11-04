package ports

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Services reach git through ports.Git, which scrubs the repository variables
// a session environment may carry. A direct process call to git in a service
// bypasses that.
func TestServicesDoNotExecGitDirectly(t *testing.T) {
	direct := regexp.MustCompile(`(?:Command|CommandContext)\(\s*(?:ctx,\s*)?(?:"git"|[a-zA-Z.]*git[a-zA-Z.]*,)`)
	root := filepath.Join("..", "service")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, readErr := os.ReadFile(path) // #nosec G304 G122 -- walks this repository's own service tree
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			if direct.MatchString(line) {
				t.Errorf("%s:%d runs git directly; use ports.DefaultGit(): %s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

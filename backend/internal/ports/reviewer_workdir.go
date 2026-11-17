package ports

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReviewerNeutralDir returns, creating it if needed, the Tallpine-owned
// directory a reviewer process for harness runs in instead of the worker
// checkout. Harnesses load agent config from their working directory up to its
// project root (AGENTS.md, CLAUDE.md, .agents/skills, project config), so a
// reviewer started in a PR checkout would take instructions from the PR head it
// reviews. The directory sits under the reviewer's task prompt root, outside
// any git repository, and Tallpine writes no agent config into it. The reviewer
// still reads the checkout by its absolute path.
func ReviewerNeutralDir(taskPromptRoot, harness string) (string, error) {
	root := strings.TrimSpace(taskPromptRoot)
	if root == "" {
		return "", errors.New("reviewer working directory requires a task prompt root")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("reviewer task prompt root %q is not absolute", root)
	}
	dir := filepath.Join(root, "cwd", harness)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create reviewer working directory: %w", err)
	}
	return dir, nil
}

//go:build !windows

package codex

import "os"

func replaceCodexFile(source, target string) error {
	return os.Rename(source, target)
}

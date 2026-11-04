//go:build !windows

package codex

import "os"

func renameCodexAccountDirectory(source, target string) error {
	return os.Rename(source, target)
}

package migratevo

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// CopyClaudeDirs clones each planned Claude project dir to its new name. It is
// copy-only: sources are never moved, modified or deleted, and an existing
// destination is left alone. The clone goes to a sibling temp name first so an
// interrupted copy never leaves a half-filled destination.
func (e *Env) CopyClaudeDirs(ctx context.Context, copies []ClaudeCopy) (copied int, err error) {
	for _, c := range copies {
		if c.Skip != "" || c.Src == "" {
			continue
		}
		if _, statErr := os.Lstat(c.Dst); statErr == nil {
			continue
		}
		if _, statErr := os.Stat(c.Src); statErr != nil {
			continue
		}
		tmp := c.Dst + ".migrating"
		if err := os.RemoveAll(tmp); err != nil { // our own leftover from a crash
			return copied, err
		}
		args := []string{"-R", c.Src, tmp}
		if runtime.GOOS == "darwin" {
			args = append([]string{"-c"}, args...) // APFS clonefile
		}
		if _, err := e.Run(ctx, "", "cp", args...); err != nil {
			_ = os.RemoveAll(tmp)
			return copied, fmt.Errorf("copy %s: %w", c.Src, err)
		}
		if err := os.Rename(tmp, c.Dst); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}

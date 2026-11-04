package cleanup

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"time"
)

// measureYieldEvery is how many entries a size walk visits between short
// sleeps, so measuring a large tree does not monopolize the disk.
const measureYieldEvery = 5000

// dirSize sums the regular files under dir without following symlinks. A
// missing dir measures as zero; unreadable entries are skipped.
func dirSize(ctx context.Context, dir string) (int64, error) {
	var size int64
	visited := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == dir {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited%measureYieldEvery == 0 && !sleep(ctx, time.Millisecond) {
			return ctx.Err()
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return size, err
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

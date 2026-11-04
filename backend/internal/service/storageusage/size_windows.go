//go:build windows

package storageusage

import "io/fs"

type inodeKey struct{ dev, ino uint64 }

func diskBytes(info fs.FileInfo) (int64, inodeKey, bool) { return info.Size(), inodeKey{}, false }

//go:build !darwin || !cgo

package workspacewatch

import "context"

func watchRootNative(ctx context.Context, root string) (<-chan struct{}, error) {
	return watchRootFSNotify(ctx, root)
}

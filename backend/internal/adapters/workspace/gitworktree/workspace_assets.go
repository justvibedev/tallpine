package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (w *Workspace) copyWorkspaceAssets(ctx context.Context, sourceRoot, destinationRoot string, assets []ports.WorkspaceProjectAssetConfig) error {
	for _, asset := range assets {
		rel, err := cleanRelativePath(asset.RelativePath)
		if err != nil || hasWorkspaceManagedComponent(rel) {
			return fmt.Errorf("gitworktree: workspace asset %q: %w", asset.RelativePath, ErrUnsafePath)
		}
		source, err := physicalAbs(asset.SourcePath)
		if err != nil || !pathx.Within(sourceRoot, source) {
			return fmt.Errorf("gitworktree: workspace asset %q source: %w", asset.RelativePath, ErrUnsafePath)
		}
		destination := filepath.Join(destinationRoot, filepath.FromSlash(rel))
		if !pathx.Within(destinationRoot, destination) {
			return fmt.Errorf("gitworktree: workspace asset %q destination: %w", asset.RelativePath, ErrUnsafePath)
		}
		if err := copyWorkspaceAssetTree(sourceRoot, source, destination); err != nil {
			return fmt.Errorf("gitworktree: copy workspace asset %q: %w", asset.RelativePath, err)
		}
	}
	return w.seedWorktreeInclude(ctx, sourceRoot, destinationRoot, false)
}

// validateAssetSymlink returns the link target of the symlink at source when it
// is relative and resolves, physically, inside sourceRoot and outside managed
// (.git/.tallpine, legacy .ao) data. Anything else wraps ErrUnsafePath.
func validateAssetSymlink(sourceRoot, source string) (string, error) {
	target, err := os.Readlink(source)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute symlink %q: %w", source, ErrUnsafePath)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(source), target))
	if !pathx.Within(sourceRoot, resolved) || hasWorkspaceManagedComponent(relativeOrEmpty(sourceRoot, resolved)) {
		return "", fmt.Errorf("symlink %q escapes or targets managed data: %w", source, ErrUnsafePath)
	}
	physicalTarget, err := filepath.EvalSymlinks(resolved)
	if err != nil || !pathx.Within(sourceRoot, physicalTarget) {
		return "", fmt.Errorf("symlink %q has an unsafe or unresolved target: %w", source, ErrUnsafePath)
	}
	return target, nil
}

func copyWorkspaceAssetTree(sourceRoot, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := validateAssetSymlink(sourceRoot, source)
		if err != nil {
			return err
		}
		if existing, err := os.Lstat(destination); err == nil {
			if existing.IsDir() {
				return fmt.Errorf("cannot overlay symlink on directory %q", destination)
			}
			if err := os.Remove(destination); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Symlink(target, destination)
	}
	if info.IsDir() {
		if existing, err := os.Lstat(destination); err == nil {
			if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("cannot overlay directory on non-directory %q", destination)
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
				return err
			}
		} else {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".git" || entry.Name() == ".tallpine" || entry.Name() == ".ao" {
				continue
			}
			if err := copyWorkspaceAssetTree(sourceRoot, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported file type at %q", source)
	}
	if existing, err := os.Lstat(destination); err == nil {
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("cannot overlay file on non-regular path %q", destination)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return copyPath(source, destination)
}

func relativeOrEmpty(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return rel
}

func hasWorkspaceManagedComponent(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if component == ".git" || component == ".tallpine" || component == ".ao" {
			return true
		}
	}
	return false
}

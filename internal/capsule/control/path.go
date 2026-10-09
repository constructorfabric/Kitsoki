package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWorkspacePath turns an agent-supplied relative path into a real path
// beneath root. It rejects absolute paths, traversal, and both existing and
// newly-created symlink escapes. The caller chooses whether the target itself
// must already exist; write operations commonly require only the parent.
func ResolveWorkspacePath(root, relative string, mustExist bool) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("capsule fs: workspace root is required")
	}
	if strings.TrimSpace(relative) == "" {
		return "", fmt.Errorf("capsule fs: relative path is required")
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("capsule fs: absolute path is denied")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("capsule fs: path escapes workspace")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("capsule fs: resolve workspace root: %w", err)
	}
	candidate := filepath.Join(realRoot, clean)
	if err := pathWithin(realRoot, candidate); err != nil {
		return "", err
	}
	if mustExist {
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("capsule fs: resolve path: %w", err)
		}
		if err := pathWithin(realRoot, real); err != nil {
			return "", err
		}
		return real, nil
	}
	parent := filepath.Dir(candidate)
	for {
		if _, err := os.Lstat(parent); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("capsule fs: inspect parent: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("capsule fs: no existing parent")
		}
		parent = next
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", fmt.Errorf("capsule fs: resolve parent: %w", err)
	}
	if err := pathWithin(realRoot, realParent); err != nil {
		return "", err
	}
	return candidate, nil
}

func pathWithin(root, candidate string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("capsule fs: path escapes workspace")
	}
	return nil
}

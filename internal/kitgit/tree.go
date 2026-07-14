package kitgit

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// This file is the tree cache — the non-git sibling of Materialize's commit
// cache. Git-tier kits are already content-addressed by commit; every other
// tier (@kitsoki/<name>, local paths, the embedded library) resolves against
// a MUTABLE directory, so pinning one (kit update staging a candidate) means
// snapshotting its bytes. MaterializeTree copies a directory into
// ${XDG_CACHE_HOME:-~/.cache}/kitsoki/kits/tree/<tree-hash>/ under the same
// idempotent-sentinel + atomic-rename discipline as the commit cache, and
// CachedTree is the offline read side.

// treeCacheBaseDir returns ${XDG_CACHE_HOME:-~/.cache}/kitsoki/kits/tree,
// creating it if absent.
func treeCacheBaseDir() (string, error) {
	git, err := cacheBaseDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(git), "tree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("kitgit: create tree cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// MaterializeTree snapshots the directory at srcDir into the tree cache,
// keyed by its DirTreeHash, and returns the cached root plus the hash. An
// already-cached tree is returned without copying (content-addressed keys
// make the copy idempotent).
func MaterializeTree(srcDir string) (root, treeHash string, err error) {
	treeHash, err = DirTreeHash(srcDir)
	if err != nil {
		return "", "", err
	}
	base, err := treeCacheBaseDir()
	if err != nil {
		return "", "", err
	}
	root = filepath.Join(base, treeHash)
	if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr == nil {
		return root, treeHash, nil
	}

	tmp, err := os.MkdirTemp(base, ".snapshot-*")
	if err != nil {
		return "", "", fmt.Errorf("kitgit: create temp snapshot dir: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed away

	if err := copyTree(srcDir, tmp); err != nil {
		return "", "", fmt.Errorf("kitgit: snapshot %q: %w", srcDir, err)
	}
	// The sentinel mirrors the commit cache's two-line format with an empty
	// commit line, so readSentinelTreeHash works on both cache kinds.
	if err := os.WriteFile(filepath.Join(tmp, ".materialized"), []byte("\n"+treeHash+"\n"), 0o644); err != nil {
		return "", "", fmt.Errorf("kitgit: write sentinel: %w", err)
	}
	if renErr := os.Rename(tmp, root); renErr != nil {
		// A concurrent winner already snapshotted this tree — reuse it.
		if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr == nil {
			return root, treeHash, nil
		}
		return "", "", fmt.Errorf("kitgit: publish tree cache dir %q: %w", root, renErr)
	}
	return root, treeHash, nil
}

// CachedTree looks up an already-snapshotted tree hash in the tree cache
// without recomputing or copying anything. ok is false when the hash has no
// cache entry.
func CachedTree(treeHash string) (root string, ok bool, err error) {
	base, err := treeCacheBaseDir()
	if err != nil {
		return "", false, err
	}
	root = filepath.Join(base, treeHash)
	if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr != nil {
		return "", false, nil
	}
	return root, true, nil
}

// copyTree copies every regular file under src into dst, preserving relative
// paths. Symlinks and irregular files are skipped — kit manifests are plain
// YAML/text trees, and DirTreeHash (the cache key) walks regular files only,
// so copying more than it hashes would let uncached content hide in the
// snapshot.
func copyTree(src, dst string) error {
	fsys := os.DirFS(src)
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		out := filepath.Join(dst, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		in, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	})
}

// FileChange is one entry in a TreeDiff summary.
type FileChange struct {
	Path string `json:"path" yaml:"path"`
	Kind string `json:"kind" yaml:"kind"` // added | removed | modified
}

// TreeDiff compares two on-disk trees by relative path and content, in
// stable lexical order — the cheap changed-files summary `kit update` prints
// (both sides are materialized cache dirs, so no network and no git). It
// deliberately reuses DirTreeHash's walk rules: regular files only, cache
// sentinels excluded.
func TreeDiff(oldRoot, newRoot string) ([]FileChange, error) {
	oldFiles, err := treeFiles(oldRoot)
	if err != nil {
		return nil, err
	}
	newFiles, err := treeFiles(newRoot)
	if err != nil {
		return nil, err
	}

	paths := make(map[string]struct{}, len(oldFiles)+len(newFiles))
	for p := range oldFiles {
		paths[p] = struct{}{}
	}
	for p := range newFiles {
		paths[p] = struct{}{}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	var changes []FileChange
	for _, p := range sorted {
		_, inOld := oldFiles[p]
		_, inNew := newFiles[p]
		switch {
		case inOld && !inNew:
			changes = append(changes, FileChange{Path: p, Kind: "removed"})
		case !inOld && inNew:
			changes = append(changes, FileChange{Path: p, Kind: "added"})
		default:
			same, cmpErr := sameFileContent(filepath.Join(oldRoot, filepath.FromSlash(p)), filepath.Join(newRoot, filepath.FromSlash(p)))
			if cmpErr != nil {
				return nil, cmpErr
			}
			if !same {
				changes = append(changes, FileChange{Path: p, Kind: "modified"})
			}
		}
	}
	return changes, nil
}

// treeFiles collects the relative paths of every regular file under root,
// excluding the cache sentinels DirTreeHash also excludes.
func treeFiles(root string) (map[string]struct{}, error) {
	files := map[string]struct{}{}
	fsys := os.DirFS(root)
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == ".materialized" || d.Name() == ".gitkeep" {
			return nil
		}
		files[path] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("kitgit: walk %q: %w", root, err)
	}
	return files, nil
}

// sameFileContent compares two files byte-for-byte, sizes first.
func sameFileContent(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if ia.Size() != ib.Size() {
		return false, nil
	}
	ba, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return string(ba) == string(bb), nil
}

// Package storydigest computes a stable runtime-closure digest for a Kitsoki
// story. A Capsule CI receipt must bind more than app.yaml: included rooms,
// imported stories, prompts, Starlark, schemas, views, and the project kit lock
// can all change behavior without changing the entry manifest.
package storydigest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

const Schema = "capsule-story-closure/v1"

// Result is the content-addressed story closure. Files are project-relative
// forward-slash paths and are useful in doctor/receipt diagnostics.
type Result struct {
	Schema string   `json:"schema"`
	Digest string   `json:"digest"`
	Files  []string `json:"files"`
}

var ignoredDirs = map[string]bool{
	".artifacts":   true,
	".capsules":    true,
	".git":         true,
	".temp":        true,
	"baked":        true,
	"cassettes":    true,
	"flows":        true,
	"node_modules": true,
	"scenarios":    true,
	"testdata":     true,
}

var runtimeExtensions = map[string]bool{
	".json":   true,
	".md":     true,
	".pongo":  true,
	".pongo2": true,
	".star":   true,
	".tmpl":   true,
	".txt":    true,
	".yaml":   true,
	".yml":    true,
}

// Compute loads the story through the real app loader, then hashes the
// behavior-bearing files below every loaded manifest root. It deliberately
// uses a conservative superset: changing a runtime-adjacent file may invalidate
// a receipt, but changing a flow/cassette/test fixture does not.
func Compute(projectRoot, storyPath string) (Result, error) {
	project, err := canonicalDir(projectRoot)
	if err != nil {
		return Result{}, err
	}
	story, err := filepath.Abs(storyPath)
	if err != nil {
		return Result{}, err
	}
	if !filepath.IsAbs(storyPath) {
		story = filepath.Join(project, storyPath)
	}
	story = filepath.Clean(story)
	if !within(project, story) {
		return Result{}, fmt.Errorf("capsule story digest: story escapes project: %s", storyPath)
	}
	def, err := app.Load(story)
	if err != nil {
		return Result{}, fmt.Errorf("capsule story digest: load story: %w", err)
	}
	manifests := append([]string(nil), def.LoadedManifests...)
	if len(manifests) == 0 {
		manifests = []string{story}
	}
	files := map[string]string{}
	for _, manifest := range manifests {
		manifest, err = filepath.Abs(manifest)
		if err != nil {
			return Result{}, err
		}
		if !within(project, manifest) {
			return Result{}, fmt.Errorf("capsule story digest: imported manifest escapes project: %s", manifest)
		}
		if err := collectRoot(project, filepath.Dir(manifest), files); err != nil {
			return Result{}, err
		}
	}
	for _, rel := range []string{filepath.Join(".kitsoki", "kits.lock"), "kits.lock", "kit.yaml"} {
		path := filepath.Join(project, rel)
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
			files[filepath.ToSlash(rel)] = path
		}
	}
	logical := make([]string, 0, len(files))
	for path := range files {
		logical = append(logical, path)
	}
	sort.Strings(logical)
	h := sha256.New()
	_, _ = h.Write([]byte(Schema + "\n"))
	for _, rel := range logical {
		path := files[rel]
		info, err := os.Lstat(path)
		if err != nil {
			return Result{}, fmt.Errorf("capsule story digest: stat %s: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("capsule story digest: dependency is not a regular file: %s", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return Result{}, fmt.Errorf("capsule story digest: read %s: %w", rel, err)
		}
		fmt.Fprintf(h, "%s\x00%04o\x00%d\x00", rel, info.Mode().Perm(), len(raw))
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	return Result{Schema: Schema, Digest: "sha256:" + hex.EncodeToString(h.Sum(nil)), Files: logical}, nil
}

func collectRoot(project, root string, files map[string]string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			if ignoredDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("capsule story digest: symlink dependency is not allowed: %s", path)
		}
		if !info.Mode().IsRegular() || !runtimeExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if strings.EqualFold(entry.Name(), "README.md") {
			return nil
		}
		rel, err := filepath.Rel(project, path)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("capsule story digest: dependency escapes project: %s", path)
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
}

func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("capsule story digest: project root is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

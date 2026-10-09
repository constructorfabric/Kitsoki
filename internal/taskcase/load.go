package taskcase

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	goyaml "github.com/goccy/go-yaml"
)

func Load(path string) (*Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Case
	if err := goyaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse task case %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	c.Path = abs
	return &c, nil
}

func LoadAll(root string) ([]*Case, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var paths []string
	if !info.IsDir() {
		paths = append(paths, root)
	} else {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			switch filepath.Ext(path) {
			case ".yaml", ".yml":
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	out := make([]*Case, 0, len(paths))
	for _, path := range paths {
		c, err := Load(path)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

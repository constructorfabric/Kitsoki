// Package extdocs builds a deterministic documentation index for Kitsoki
// extension packages, stories, and their publishable component contracts.
package extdocs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
)

const (
	// ManifestFileName is the conventional docs sidecar filename.
	ManifestFileName = "docs.yaml"
	// Schema is the only docs sidecar schema this package accepts.
	Schema = "kitsoki.docs/v1"
)

// Manifest is a kitsoki.docs/v1 source-owned documentation sidecar.
type Manifest struct {
	Schema      string      `json:"schema" yaml:"schema"`
	Owner       Owner       `json:"owner" yaml:"owner"`
	Title       string      `json:"title,omitempty" yaml:"title,omitempty"`
	SummaryPath string      `json:"summary_path,omitempty" yaml:"summary_path,omitempty"`
	Audiences   []string    `json:"audiences,omitempty" yaml:"audiences,omitempty"`
	Extends     []Extension `json:"extends,omitempty" yaml:"extends,omitempty"`
	Docs        []DocEntry  `json:"docs,omitempty" yaml:"docs,omitempty"`
	Components  []Component `json:"components,omitempty" yaml:"components,omitempty"`

	Dir string `json:"-" yaml:"-"`
}

type Owner struct {
	Kind string `json:"kind" yaml:"kind"`
	ID   string `json:"id" yaml:"id"`
}

type Extension struct {
	Component string `json:"component" yaml:"component"`
	Merge     string `json:"merge,omitempty" yaml:"merge,omitempty"`
}

type Component struct {
	Kind          string          `json:"kind" yaml:"kind"`
	ID            string          `json:"id" yaml:"id"`
	Owner         string          `json:"owner,omitempty" yaml:"owner,omitempty"`
	Title         string          `json:"title,omitempty" yaml:"title,omitempty"`
	Summary       string          `json:"summary,omitempty" yaml:"summary,omitempty"`
	Path          string          `json:"path,omitempty" yaml:"path,omitempty"`
	GeneratedFrom string          `json:"generated_from,omitempty" yaml:"generated_from,omitempty"`
	Publish       string          `json:"publish,omitempty" yaml:"publish,omitempty"`
	Tags          []string        `json:"tags,omitempty" yaml:"tags,omitempty"`
	Uses          []string        `json:"uses,omitempty" yaml:"uses,omitempty"`
	Facts         []ComponentFact `json:"facts,omitempty" yaml:"facts,omitempty"`
	Docs          []DocEntry      `json:"docs,omitempty" yaml:"docs,omitempty"`
}

type ComponentFact struct {
	Label string `json:"label" yaml:"label"`
	Value string `json:"value" yaml:"value"`
}

type DocEntry struct {
	ID            string   `json:"id" yaml:"id"`
	Title         string   `json:"title,omitempty" yaml:"title,omitempty"`
	Path          string   `json:"path,omitempty" yaml:"path,omitempty"`
	GeneratedFrom string   `json:"generated_from,omitempty" yaml:"generated_from,omitempty"`
	Kind          string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Audience      []string `json:"audience,omitempty" yaml:"audience,omitempty"`
	Order         int      `json:"order,omitempty" yaml:"order,omitempty"`
	Publish       string   `json:"publish,omitempty" yaml:"publish,omitempty"`
	Tags          []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Template      bool     `json:"template,omitempty" yaml:"template,omitempty"`
	Shared        bool     `json:"shared,omitempty" yaml:"shared,omitempty"`
	Overlay       bool     `json:"overlay,omitempty" yaml:"overlay,omitempty"`
	Replaces      string   `json:"replaces,omitempty" yaml:"replaces,omitempty"`
	Extends       string   `json:"extends,omitempty" yaml:"extends,omitempty"`
}

func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read docs manifest: %w", err)
	}
	var m Manifest
	if err := goyaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse docs manifest %s: %w", path, err)
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		abs = filepath.Dir(path)
	}
	m.Dir = abs
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("docs manifest %s: %w", path, err)
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	if m.Schema != Schema {
		return fmt.Errorf("schema must be %q", Schema)
	}
	if strings.TrimSpace(m.Owner.Kind) == "" || strings.TrimSpace(m.Owner.ID) == "" {
		return fmt.Errorf("owner.kind and owner.id are required")
	}
	if m.SummaryPath != "" && !safeRelPath(m.SummaryPath) {
		return fmt.Errorf("summary_path %q must be a relative in-package path", m.SummaryPath)
	}
	seen := map[string]bool{}
	for i := range m.Docs {
		if err := validateDocEntry("docs", i, &m.Docs[i]); err != nil {
			return err
		}
		if seen[m.Docs[i].ID] {
			return fmt.Errorf("docs[%d].id %q is duplicated", i, m.Docs[i].ID)
		}
		seen[m.Docs[i].ID] = true
	}
	for ci := range m.Components {
		c := &m.Components[ci]
		if strings.TrimSpace(c.Kind) == "" || strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("components[%d].kind and id are required", ci)
		}
		componentSeen := map[string]bool{}
		for di := range c.Docs {
			if err := validateDocEntry(fmt.Sprintf("components[%d].docs", ci), di, &c.Docs[di]); err != nil {
				return err
			}
			if componentSeen[c.Docs[di].ID] {
				return fmt.Errorf("components[%d].docs[%d].id %q is duplicated", ci, di, c.Docs[di].ID)
			}
			componentSeen[c.Docs[di].ID] = true
		}
	}
	sort.SliceStable(m.Docs, func(i, j int) bool { return docLess(m.Docs[i], m.Docs[j]) })
	return nil
}

func validateDocEntry(prefix string, i int, d *DocEntry) error {
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("%s[%d].id is required", prefix, i)
	}
	if d.Path == "" && d.GeneratedFrom == "" {
		return fmt.Errorf("%s[%d] must set path or generated_from", prefix, i)
	}
	if d.Path != "" && !safeRelPath(d.Path) {
		return fmt.Errorf("%s[%d].path %q must be a relative in-package path", prefix, i, d.Path)
	}
	if d.Publish == "" {
		d.Publish = defaultPublish(d.Path)
	}
	if !validPublish(d.Publish) {
		return fmt.Errorf("%s[%d].publish %q must be one of true, false, summary, full", prefix, i, d.Publish)
	}
	return nil
}

func safeRelPath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func defaultPublish(path string) string {
	if strings.HasPrefix(filepath.ToSlash(path), "prompts/") {
		return "summary"
	}
	return "true"
}

func validPublish(p string) bool {
	switch p {
	case "true", "false", "summary", "full":
		return true
	default:
		return false
	}
}

func docLess(a, b DocEntry) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.ID < b.ID
}

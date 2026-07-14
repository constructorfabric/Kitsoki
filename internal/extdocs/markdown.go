package extdocs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WriteMarkdown(idx *Index, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Markdown(idx)), 0o644)
}

func Markdown(idx *Index) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# Kitsoki Extension Library Index")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Root: `%s`\n\n", idx.Root)
	fmt.Fprintf(&b, "Packages: %d  Stories: %d  Components: %d  Docs: %d\n\n", len(idx.Packages), len(idx.Stories), len(idx.Components), len(idx.Docs))
	if len(idx.Packages) > 0 {
		fmt.Fprintln(&b, "## Packages")
		fmt.Fprintln(&b)
		for _, p := range idx.Packages {
			fmt.Fprintf(&b, "- `%s` (%s", p.ID, p.Kind)
			if p.Version != "" {
				fmt.Fprintf(&b, ", %s", p.Version)
			}
			fmt.Fprintf(&b, ") - %s\n", p.Title)
			if p.Summary != "" {
				fmt.Fprintf(&b, "  %s\n", p.Summary)
			}
			if len(p.Provides) > 0 {
				fmt.Fprintf(&b, "  Provides: %s\n", strings.Join(p.Provides, ", "))
			}
		}
		fmt.Fprintln(&b)
	}
	if len(idx.Stories) > 0 {
		fmt.Fprintln(&b, "## Stories")
		fmt.Fprintln(&b)
		for _, s := range idx.Stories {
			fmt.Fprintf(&b, "- `%s` - %s\n", s.ID, s.Title)
			fmt.Fprintf(&b, "  Path: `%s`; states: %d; intents: %d; flows: %d\n", s.Path, len(s.States), len(s.Intents), len(s.Flows))
			if len(s.HostInterfaces) > 0 {
				fmt.Fprintf(&b, "  Interfaces: %s\n", strings.Join(s.HostInterfaces, ", "))
			}
			if len(s.Agents) > 0 {
				fmt.Fprintf(&b, "  Agents: %s\n", strings.Join(s.Agents, ", "))
			}
		}
		fmt.Fprintln(&b)
	}
	if len(idx.Components) > 0 {
		fmt.Fprintln(&b, "## Generated Components")
		fmt.Fprintln(&b)
		for _, c := range idx.Components {
			fmt.Fprintf(&b, "- `%s:%s`", c.Kind, c.ID)
			if c.Title != "" {
				fmt.Fprintf(&b, " - %s", c.Title)
			}
			if c.Publish != "" {
				fmt.Fprintf(&b, " (publish: %s)", c.Publish)
			}
			fmt.Fprintln(&b)
			if c.Summary != "" {
				fmt.Fprintf(&b, "  %s\n", c.Summary)
			}
			if c.GeneratedFrom != "" {
				fmt.Fprintf(&b, "  Generated from: `%s`\n", c.GeneratedFrom)
			}
		}
		fmt.Fprintln(&b)
	}
	if len(idx.Docs) > 0 {
		fmt.Fprintln(&b, "## Authored Docs")
		fmt.Fprintln(&b)
		for _, d := range idx.Docs {
			fmt.Fprintf(&b, "- `%s`", d.ID)
			if d.Title != "" {
				fmt.Fprintf(&b, " - %s", d.Title)
			}
			fmt.Fprintf(&b, " (publish: %s)\n", d.Publish)
		}
	}
	return b.String()
}

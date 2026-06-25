package baseskills

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Report records what [Install] wrote into a target project so the caller (the
// CLI command and the onboarding room) can show a deterministic summary.
type Report struct {
	Target     string   `json:"target"`
	Skills     []string `json:"skills"`      // skill names linked into .claude/skills
	Agents     []string `json:"agents"`      // agent names linked into .claude/agents
	MCPPath    string   `json:"mcp_path"`    // .mcp.json that gained the kitsoki server
	MCPWritten bool     `json:"mcp_written"` // false when the kitsoki entry already matched
}

// MCPArgs is the studio MCP invocation written into a target's .mcp.json. The
// stories dir is project-relative so the entry is portable across checkouts.
var MCPArgs = []any{"mcp", "--stories-dir", "stories"}

// Install materializes the embedded agent toolkit and installs it into target
// as a checked-in, project-scoped setup that mirrors the kitsoki repo's own
// layout:
//
//   - .agents/skills/<name>/…   (source of truth, copied from the binary)
//   - .agents/agents/<name>.md  (source of truth, copied from the binary)
//   - .claude/skills/<name>   → ../../.agents/skills/<name>   (relative symlink)
//   - .claude/agents/<name>.md → ../../.agents/agents/<name>.md (relative symlink)
//   - .mcp.json   (kitsoki studio MCP server registered/merged, other servers kept)
//
// It is idempotent: source trees are overwritten from the binary, our own
// symlinks are refreshed, and a non-symlink a human placed at a link path is
// left untouched (skipped, not clobbered). Returns [ErrNotStaged] when the
// toolkit was not staged into the binary.
func Install(ctx context.Context, target string) (Report, error) {
	rep := Report{Target: target}
	root, err := Materialize(ctx)
	if err != nil {
		return rep, err
	}

	skills, err := installTree(root, target, "skills", func(name string) bool {
		_, statErr := os.Stat(filepath.Join(root, "skills", name, "SKILL.md"))
		return statErr == nil
	}, false)
	if err != nil {
		return rep, err
	}
	rep.Skills = skills

	agents, err := installTree(root, target, "agents", func(name string) bool {
		if filepath.Ext(name) != ".md" {
			return false
		}
		switch name {
		case "AGENTS.md", "CLAUDE.md":
			return false // dir-level notes, not agent definitions
		}
		return true
	}, true)
	if err != nil {
		return rep, err
	}
	rep.Agents = agents

	written, mcpPath, err := writeMCP(target)
	if err != nil {
		return rep, err
	}
	rep.MCPPath = mcpPath
	rep.MCPWritten = written
	return rep, nil
}

// installTree copies one source tree (skills|agents) from the materialized root
// into target/.agents/<kind>, then relative-symlinks each accepted entry into
// target/.claude/<kind>. fileEntries selects file entries (agents) vs directory
// entries (skills). accept filters by entry base name.
func installTree(root, target, kind string, accept func(name string) bool, fileEntries bool) ([]string, error) {
	src := filepath.Join(root, kind)
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("baseskills: read embedded %s: %w", kind, err)
	}

	srcDst := filepath.Join(target, ".agents", kind)
	linkDst := filepath.Join(target, ".claude", kind)
	if err := os.MkdirAll(srcDst, 0o755); err != nil {
		return nil, fmt.Errorf("baseskills: mkdir %q: %w", srcDst, err)
	}
	if err := os.MkdirAll(linkDst, 0o755); err != nil {
		return nil, fmt.Errorf("baseskills: mkdir %q: %w", linkDst, err)
	}

	var names []string
	for _, e := range entries {
		name := e.Name()
		if fileEntries == e.IsDir() {
			continue // skills want dirs, agents want files
		}
		if !accept(name) {
			continue
		}
		if err := copyPath(filepath.Join(src, name), filepath.Join(srcDst, name)); err != nil {
			return nil, err
		}
		if err := relink(filepath.Join("../..", ".agents", kind, name), filepath.Join(linkDst, name)); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// relink refreshes our own symlink at link → rel. It removes a pre-existing
// symlink (ours to refresh) but leaves a real file/dir a human placed there.
func relink(rel, link string) error {
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil // not ours — leave as-is
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("baseskills: refresh symlink %q: %w", link, err)
		}
	}
	if err := os.Symlink(rel, link); err != nil {
		return fmt.Errorf("baseskills: symlink %q -> %q: %w", link, rel, err)
	}
	return nil
}

// copyPath copies a file or directory tree from src to dst, replacing dst.
func copyPath(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("baseskills: clear %q: %w", dst, err)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// writeMCP registers the kitsoki studio MCP server in target/.mcp.json,
// preserving any other servers already configured. Returns (written, path):
// written is false when the kitsoki entry already matched exactly.
func writeMCP(target string) (bool, string, error) {
	path := filepath.Join(target, ".mcp.json")
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return false, path, fmt.Errorf("baseskills: parse existing %q: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return false, path, fmt.Errorf("baseskills: read %q: %w", path, err)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	want := map[string]any{"command": "kitsoki", "args": MCPArgs}
	if equalJSON(servers["kitsoki"], want) {
		return false, path, nil
	}
	servers["kitsoki"] = want
	doc["mcpServers"] = servers

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, path, fmt.Errorf("baseskills: marshal %q: %w", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return false, path, fmt.Errorf("baseskills: write %q: %w", path, err)
	}
	return true, path, nil
}

func equalJSON(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

package baseskills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestInstall verifies the project-scoped layout: source trees copied into
// .agents, relative symlinks into .claude, and the kitsoki MCP registered in
// .mcp.json. Skipped when the toolkit was not staged into the binary (a fresh
// checkout before `make embed-skills`).
func TestInstall(t *testing.T) {
	if _, err := Materialize(context.Background()); err == ErrNotStaged {
		t.Skip("agent toolkit not staged; run `make embed-skills`")
	}

	target := t.TempDir()
	rep, err := Install(context.Background(), target)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rep.Skills) == 0 {
		t.Fatal("expected at least one skill installed")
	}
	if len(rep.Agents) == 0 {
		t.Fatal("expected at least one agent installed")
	}

	// Skill: source dir present, .claude symlink resolves into .agents.
	skill := rep.Skills[0]
	if _, err := os.Stat(filepath.Join(target, ".agents", "skills", skill, "SKILL.md")); err != nil {
		t.Fatalf("skill source missing: %v", err)
	}
	link := filepath.Join(target, ".claude", "skills", skill)
	dest, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("skill symlink missing: %v", err)
	}
	if want := filepath.Join("../..", ".agents", "skills", skill); dest != want {
		t.Fatalf("skill symlink = %q, want %q", dest, want)
	}

	// MCP registered.
	if !rep.MCPWritten {
		t.Fatal("expected .mcp.json to be written")
	}
	b, err := os.ReadFile(filepath.Join(target, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
			Args    []any  `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	kit, ok := doc.MCPServers["kitsoki"]
	if !ok || kit.Command != "kitsoki" {
		t.Fatalf("kitsoki MCP not registered: %s", b)
	}

	// Idempotent: a second install does not rewrite the matching MCP entry and
	// preserves a co-existing server.
	doc2 := map[string]any{"mcpServers": map[string]any{
		"other":   map[string]any{"command": "x"},
		"kitsoki": map[string]any{"command": "kitsoki", "args": MCPArgs},
	}}
	mb, _ := json.Marshal(doc2)
	if err := os.WriteFile(filepath.Join(target, ".mcp.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}
	rep2, err := Install(context.Background(), target)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if rep2.MCPWritten {
		t.Fatal("expected matching MCP entry to be left untouched")
	}
	b2, _ := os.ReadFile(filepath.Join(target, ".mcp.json"))
	if !containsServer(t, b2, "other") {
		t.Fatalf("co-existing MCP server dropped: %s", b2)
	}
}

func containsServer(t *testing.T, b []byte, name string) bool {
	t.Helper()
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, ok := doc.MCPServers[name]
	return ok
}

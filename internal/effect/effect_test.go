package effect

import "testing"

func TestToolClassSlideyMCP(t *testing.T) {
	tests := map[string]Effect{
		"mcp__slidey__workspace_tree": Read,
		"mcp__slidey__read_spec":      Read,
		"mcp__slidey__layout_gallery": Read,
		"mcp__slidey__schema":         Read,
		"mcp__slidey__validate":       Read,
		"mcp__slidey__docs":           Read,
		"mcp__slidey__write_spec":     Write,
		"mcp__slidey__patch_spec":     Write,
		"mcp__slidey__remove_slide":   Write,
		"mcp__slidey__meme_search":    External,
		"mcp__slidey__add_meme":       External,
	}

	for tool, want := range tests {
		if got := ToolClass(tool); got != want {
			t.Fatalf("ToolClass(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestFromToolsSlideyMCPWriteWins(t *testing.T) {
	got := FromTools([]string{
		"mcp__slidey__workspace_tree",
		"mcp__slidey__read_spec",
		"mcp__slidey__patch_spec",
	})
	if got != Write {
		t.Fatalf("FromTools(slidey read+write MCP tools) = %q, want %q", got, Write)
	}
}

package host

// agent_studio_mcp_test.go — coverage for the Task 1.3 precondition fix
// (room-workbench mcp-fix): the "kitsoki" studio MCP server must be
// auto-attached to a claude-backend --mcp-config file whenever the effective
// tool surface names a tool under the mcp__kitsoki__ studio namespace, and
// must NOT be attached (and must NOT clobber a caller-declared entry) when it
// isn't. No live LLM involved — pure composition of the mcp-config content.

import (
	"os"
	"testing"
)

func TestDeclaresStudioMCPTools(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tools []string
		want  bool
	}{
		{"empty", nil, false},
		{"unrelated tools", []string{"Read", "Grep", "mcp__validator__submit"}, false},
		{"bash mcp only", []string{"mcp__kitsoki-bash__Bash"}, false},
		{"studio tool present", []string{"Read", "mcp__kitsoki__studio_ping"}, true},
		{"studio tool only", []string{"mcp__kitsoki__story_read"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := declaresStudioMCPTools(tc.tools); got != tc.want {
				t.Errorf("declaresStudioMCPTools(%v) = %v, want %v", tc.tools, got, tc.want)
			}
		})
	}
}

func TestAttachStudioMCPServer_ToolboxWithStudio(t *testing.T) {
	t.Parallel()
	tools := []string{"Read", "mcp__kitsoki__studio_ping", "mcp__kitsoki__studio_work"}

	out := attachStudioMCPServer(map[string]any{"validator": map[string]any{"command": "kitsoki-validator"}}, tools)

	entry, ok := out[kitsokiStudioServerName].(map[string]any)
	if !ok {
		t.Fatalf("expected %q entry to be attached, got servers=%v", kitsokiStudioServerName, out)
	}
	if got := entry["command"]; got != "kitsoki" {
		t.Errorf("command = %v, want %q", got, "kitsoki")
	}
	args, ok := entry["args"].([]any)
	if !ok || len(args) != 3 {
		t.Fatalf("args = %v, want [mcp --stories-dir stories]", entry["args"])
	}
	if args[0] != "mcp" || args[1] != "--stories-dir" || args[2] != "stories" {
		t.Errorf("args = %v, want [mcp --stories-dir stories]", args)
	}
	// The pre-existing validator entry must survive untouched.
	if _, ok := out["validator"]; !ok {
		t.Error("validator entry was clobbered by studio attachment")
	}
}

func TestAttachStudioMCPServer_ToolboxWithoutStudio(t *testing.T) {
	t.Parallel()
	tools := []string{"Read", "Grep", "mcp__validator__submit"}
	in := map[string]any{"validator": map[string]any{"command": "kitsoki-validator"}}

	out := attachStudioMCPServer(in, tools)

	if _, ok := out[kitsokiStudioServerName]; ok {
		t.Errorf("kitsoki studio entry attached without a studio tool declared: %v", out)
	}
	if len(out) != 1 {
		t.Errorf("unexpected mutation of mcpServers: %v", out)
	}
}

func TestAttachStudioMCPServer_NilMap(t *testing.T) {
	t.Parallel()
	// A studio-only toolbox with no other MCP server declared must still get
	// the "kitsoki" entry created from a nil map (the len(mcpServers) > 0
	// gate at each call site must see it after this call, not before).
	out := attachStudioMCPServer(nil, []string{"mcp__kitsoki__studio_ping"})
	if len(out) != 1 {
		t.Fatalf("expected exactly 1 entry from a nil map, got %v", out)
	}
	if _, ok := out[kitsokiStudioServerName]; !ok {
		t.Errorf("kitsoki entry missing: %v", out)
	}
}

func TestAttachStudioMCPServer_ExplicitDeclarationWins(t *testing.T) {
	t.Parallel()
	// An author-declared mcp.servers.kitsoki entry (e.g. pointing at a
	// non-default binary/args for a test) must NOT be overwritten by the
	// auto-attach.
	custom := map[string]any{"command": "/custom/kitsoki", "args": []any{"mcp"}}
	in := map[string]any{kitsokiStudioServerName: custom}

	out := attachStudioMCPServer(in, []string{"mcp__kitsoki__studio_ping"})

	got, ok := out[kitsokiStudioServerName].(map[string]any)
	if !ok {
		t.Fatalf("kitsoki entry missing: %v", out)
	}
	if got["command"] != "/custom/kitsoki" {
		t.Errorf("explicit declaration was overwritten: %v", got)
	}
}

func TestKitsokiStudioMCPServerEntry_StoriesDirEnvOverride(t *testing.T) {
	t.Setenv(kitsokiStudioStoriesDirEnv, "/abs/other-stories")
	entry := kitsokiStudioMCPServerEntry()
	args, _ := entry["args"].([]any)
	if len(args) != 3 || args[2] != "/abs/other-stories" {
		t.Errorf("args = %v, want override to take effect", args)
	}
}

func TestKitsokiStudioMCPServerEntry_DefaultRelativeStoriesDir(t *testing.T) {
	// Ensure no ambient env leaks between tests (t.Setenv above is scoped to
	// its own test, but be explicit).
	os.Unsetenv(kitsokiStudioStoriesDirEnv)
	entry := kitsokiStudioMCPServerEntry()
	if entry["command"] != "kitsoki" {
		t.Errorf("command = %v, want kitsoki", entry["command"])
	}
	args, _ := entry["args"].([]any)
	if len(args) != 3 || args[0] != "mcp" || args[1] != "--stories-dir" || args[2] != "stories" {
		t.Errorf("args = %v, want [mcp --stories-dir stories] (relative, not absolute)", args)
	}
}

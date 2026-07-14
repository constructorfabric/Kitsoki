package codexcli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProjectCodexConfigKeepsSlideyPortable guards the checked-in config that
// Codex loads in preference to ~/.codex/config.toml. A developer-specific
// launcher or workspace path makes Slidey fail before the MCP handshake.
func TestProjectCodexConfigKeepsSlideyPortable(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve project config test path")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", ".codex", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read project Codex config: %v", err)
	}

	slidey := tomlSection(string(raw), "mcp_servers.slidey")
	if slidey == "" {
		t.Fatal("project Codex config must define mcp_servers.slidey")
	}
	for _, want := range []string{
		`command = "slidey-mcp"`,
		`args = ["--root", "."]`,
		`cwd = "."`,
	} {
		if !strings.Contains(slidey, want) {
			t.Errorf("mcp_servers.slidey must contain %q; got:\n%s", want, slidey)
		}
	}
	for _, machinePath := range []string{"/Users/", `C:\\Users\\`} {
		if strings.Contains(slidey, machinePath) {
			t.Errorf("mcp_servers.slidey contains machine-specific path %q:\n%s", machinePath, slidey)
		}
	}
}

func tomlSection(src, name string) string {
	header := "[" + name + "]"
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

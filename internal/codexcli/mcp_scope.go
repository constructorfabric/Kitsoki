package codexcli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MCPServerScopeArgs returns Codex -c overrides that make a launch's MCP server
// set explicit. Codex merges -c mcp_servers.<name>.* overrides with the user's
// config.toml, so registering one server does not remove globally configured
// servers. Disable inherited servers that are not requested, and explicitly
// enable requested servers in case a user config has them disabled.
func MCPServerScopeArgs(enabledNames []string) []string {
	enabled := make(map[string]bool, len(enabledNames))
	for _, name := range enabledNames {
		name = strings.TrimSpace(name)
		if name != "" {
			enabled[name] = true
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	inherited := ConfiguredMCPServerNames()
	var out []string
	for _, name := range inherited {
		if !enabled[name] {
			out = append(out, "-c", "mcp_servers."+tomlKeySegment(name)+".enabled=false")
		}
	}

	names := make([]string, 0, len(enabled))
	for name := range enabled {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, "-c", "mcp_servers."+tomlKeySegment(name)+".enabled=true")
	}
	return out
}

// ConfiguredMCPServerNames returns server names declared in Codex's config.toml.
func ConfiguredMCPServerNames() []string {
	path := codexConfigPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ConfiguredMCPServerNamesFromTOML(string(raw))
}

// ConfiguredMCPServerNamesFromTOML extracts mcp_servers table names from a
// Codex config file. It intentionally handles only section headers because that
// is how `codex mcp add` persists servers.
func ConfiguredMCPServerNamesFromTOML(src string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
		if name := mcpServerNameFromSection(line); name != "" {
			seen[name] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func codexConfigPath() string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "config.toml")
}

func mcpServerNameFromSection(section string) string {
	rest, ok := strings.CutPrefix(section, "mcp_servers.")
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	if strings.HasPrefix(rest, `"`) {
		name, ok := parseQuotedTOMLKey(rest)
		if !ok {
			return ""
		}
		return name
	}
	if before, _, ok := strings.Cut(rest, "."); ok {
		rest = before
	}
	return strings.TrimSpace(rest)
}

func parseQuotedTOMLKey(s string) (string, bool) {
	var b strings.Builder
	escaped := false
	for _, r := range s[1:] {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			return b.String(), true
		default:
			b.WriteRune(r)
		}
	}
	return "", false
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func tomlKeySegment(name string) string {
	if name != "" {
		bare := true
		for _, r := range name {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				continue
			}
			bare = false
			break
		}
		if bare {
			return name
		}
	}
	repl := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + repl.Replace(name) + `"`
}

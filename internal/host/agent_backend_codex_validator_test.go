package host

import (
	"strings"
	"testing"
)

func TestCodexMCPConfigArgsRenamesGenericTaskValidator(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	args := CodexMCPConfigArgsForServers(map[string]CodexMCPServerConfig{
		"validator": {
			Command: "/tmp/kitsoki",
			Args:    []string{"mcp-validator", "--schema", "/tmp/schema.json"},
		},
	}, t.TempDir())
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `mcp_servers.kitsoki-validator.command="/tmp/kitsoki"`) {
		t.Fatalf("Codex validator registration = %q, want kitsoki-validator command", joined)
	}
	if !strings.Contains(joined, `mcp_servers.kitsoki-validator.enabled=true`) {
		t.Fatalf("Codex validator registration = %q, want enabled kitsoki-validator", joined)
	}
	if strings.Contains(joined, `mcp_servers.validator.enabled=true`) {
		t.Fatalf("Codex validator registration retained generic validator name: %q", joined)
	}
}

package codexcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredMCPServerNamesFromTOML(t *testing.T) {
	got := ConfiguredMCPServerNamesFromTOML(`
[mcp_servers.kitsoki]
command = "kitsoki"

[mcp_servers.slidey.env]
ROOT = "/tmp/project"

[mcp_servers."codex.app"]
command = "codex-app"

[projects."/tmp/project"]
trust_level = "trusted"
`)
	require.Equal(t, []string{"codex.app", "kitsoki", "slidey"}, got)
}

func TestMCPServerScopeArgsDisablesInheritedServers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[mcp_servers.kitsoki]
command = "/bin/kitsoki"

[mcp_servers.slidey]
command = "/bin/slidey"

[mcp_servers.codex_app]
command = "/bin/codex-app"
`), 0644))

	got := MCPServerScopeArgs([]string{"kitsoki"})
	require.Equal(t, []string{
		"-c", "mcp_servers.codex_app.enabled=false",
		"-c", "mcp_servers.slidey.enabled=false",
		"-c", "mcp_servers.kitsoki.enabled=true",
	}, got)
}

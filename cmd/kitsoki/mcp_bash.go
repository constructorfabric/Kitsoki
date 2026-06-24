// mcp_bash.go — implements `kitsoki mcp-bash --profile-config <path>`.
//
// Runs an MCP stdio server that exposes a single `Bash` tool. The server reads
// a profile config (written by host.BuildBashMCPEntry) and applies the
// BashProfile to every Bash invocation before exec. Used by host.agent.ask /
// host.agent.decide to route claude's Bash tool through kitsoki-side argv
// gating rather than letting the built-in run unrestricted.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
)

func mcpBashCmd() *cobra.Command {
	var profileConfigPath string
	cmd := &cobra.Command{
		Use:   "mcp-bash",
		Short: "Run the kitsoki Bash MCP server with an applied profile",
		Long: `mcp-bash is the stdio MCP server entry point for the wrapped Bash tool
used by host.agent.ask and host.agent.decide. It reads a profile config file
(written by the caller, owned by the kitsoki host package) and applies the
BashProfile via host.ApplyBashProfile to every incoming Bash invocation.

Not intended to be invoked by hand; the caller writes the config file as a
tempfile and exec's this subcommand via the MCP server map of an agent call.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if profileConfigPath == "" {
				return fmt.Errorf("mcp-bash: --profile-config is required")
			}
			return host.RunBashMCPServerFromConfig(cmd.Context(), profileConfigPath, os.Stdin, os.Stdout, os.Stderr)
		},
	}
	cmd.Flags().StringVar(&profileConfigPath, "profile-config", "",
		"path to the kitsoki-written BashMCPConfig JSON (profile + working_dir)")
	return cmd
}

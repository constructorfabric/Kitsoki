package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/baseskills"
)

// projectToolsCmd groups commands that install the kitsoki agent toolkit (the
// project skills + shared subagents) and the studio MCP registration into a
// target project, so an onboarded checkout is a fully working kitsoki
// environment without a kitsoki source tree on disk.
func projectToolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project-tools",
		Short: "Install kitsoki skills/agents + the studio MCP into a project",
		Long: `Install the kitsoki agent toolkit into a target project.

The project-scoped, checked-in layout mirrors the kitsoki repo itself:
  .agents/skills/<name>/      source of truth (copied from the binary)
  .agents/agents/<name>.md    source of truth (copied from the binary)
  .claude/skills/<name>     → ../../.agents/skills/<name>     (relative symlink)
  .claude/agents/<name>.md  → ../../.agents/agents/<name>.md  (relative symlink)
  .mcp.json                   registers the kitsoki studio MCP server

The toolkit is embedded in the binary (run 'make embed-skills' before building),
so this works in a freshly onboarded project with no kitsoki checkout present.`,
	}
	cmd.AddCommand(projectToolsInstallCmd())
	cmd.AddCommand(projectToolsUpgradeCmd())
	return cmd
}

func projectToolsInstallCmd() *cobra.Command {
	var (
		target string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install skills/agents + the studio MCP into a target project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			abs, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve --target: %w", err)
			}
			rep, err := baseskills.Install(cmd.Context(), abs)
			if err != nil {
				return err
			}
			if asJSON {
				b, _ := json.MarshalIndent(rep, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed kitsoki toolkit into %s\n", rep.Target)
			fmt.Fprintf(cmd.OutOrStdout(), "  skills: %d linked into .claude/skills\n", len(rep.Skills))
			fmt.Fprintf(cmd.OutOrStdout(), "  agents: %d linked into .claude/agents\n", len(rep.Agents))
			if rep.MCPWritten {
				fmt.Fprintf(cmd.OutOrStdout(), "  mcp:    registered kitsoki server in %s\n", rep.MCPPath)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  mcp:    kitsoki server already registered in %s\n", rep.MCPPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "target project root to install into")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a JSON install report")
	return cmd
}

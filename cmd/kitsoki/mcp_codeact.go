// mcp_codeact.go — implements `kitsoki mcp-codeact`.
//
// Runs a standalone stdio MCP server that exposes a single codeact_eval tool.
// Unlike host.agent.codeact, this server does not launch another LLM: the
// attached agent supplies one Starlark snippet per tool call, and Kitsoki runs it
// through the same capability-scoped sandbox used by host.starlark.run. This is
// the MCP-facing code-action primitive for sessions that should not receive
// Bash/Python/Node as their execution surface.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	starlarkhost "kitsoki/internal/host/starlark"
	kitsokimcp "kitsoki/internal/mcp"
)

func mcpCodeactCmd() *cobra.Command {
	var (
		workingDir       string
		capabilitiesJSON string
		capabilitiesFile string
		toolName         string
	)
	cmd := &cobra.Command{
		Use:   "mcp-codeact",
		Short: "Run a stdio MCP server for capability-scoped Starlark code actions",
		Long: `mcp-codeact exposes one MCP tool, codeact_eval, that runs a caller
supplied Starlark snippet through Kitsoki's host.starlark.run sandbox.

This is the MCP-facing counterpart to host.agent.codeact: host.agent.codeact is
self-contained and agentic inside a story room; mcp-codeact is the deterministic
code-action tool you can attach to Claude/Codex sessions or harness profiles
when you want to deny Bash/Python/Node and allow only declared ctx surfaces.

Capabilities are configured when the server starts and cannot be broadened by a
tool call. By default the tool has pure stdlib helpers plus read-only ctx.world,
with no filesystem, process probe, network, or host access.

Examples:

  kitsoki mcp-codeact --working-dir . \
    --capabilities-json '{"fs":{"read":["**"]},"vcs":"read"}'

  kitsoki mcp-codeact --working-dir . \
    --capabilities-file .context/codeact-capabilities.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			caps, err := parseCodeactCapabilities(capabilitiesJSON, capabilitiesFile)
			if err != nil {
				return err
			}
			srv, err := kitsokimcp.NewCodeactServer(kitsokimcp.CodeactConfig{
				ToolName:     toolName,
				WorkingDir:   workingDir,
				Capabilities: caps,
			})
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&workingDir, "working-dir", "", "filesystem root for ctx.fs and ctx.probe (default: current directory)")
	cmd.Flags().StringVar(&capabilitiesJSON, "capabilities-json", "", "JSON object for the server-side Starlark capability ceiling")
	cmd.Flags().StringVar(&capabilitiesFile, "capabilities-file", "", "file containing a JSON object for the server-side Starlark capability ceiling")
	cmd.Flags().StringVar(&toolName, "tool-name", "", "MCP tool name to expose (default: codeact_eval)")
	return cmd
}

func parseCodeactCapabilities(inline, path string) (starlarkhost.CapabilitySpec, error) {
	inline = strings.TrimSpace(inline)
	path = strings.TrimSpace(path)
	if inline != "" && path != "" {
		return starlarkhost.CapabilitySpec{}, fmt.Errorf("use only one of --capabilities-json or --capabilities-file")
	}
	if inline == "" && path == "" {
		return starlarkhost.DefaultCapabilities(), nil
	}
	raw := []byte(inline)
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return starlarkhost.CapabilitySpec{}, fmt.Errorf("read --capabilities-file: %w", err)
		}
		raw = data
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return starlarkhost.CapabilitySpec{}, fmt.Errorf("parse capabilities JSON: %w", err)
	}
	caps, err := starlarkhost.ParseCapabilities(m)
	if err != nil {
		return starlarkhost.CapabilitySpec{}, fmt.Errorf("capabilities: %w", err)
	}
	return caps, nil
}

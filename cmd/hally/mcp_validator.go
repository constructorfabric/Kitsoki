// mcp_validator.go — implements `hally mcp-validator --schema <path>`.
//
// Runs an MCP stdio server whose single `submit` tool validates incoming
// JSON arguments against the schema at <path>. Used by Claude (or any
// other MCP client) to enforce structured-output contracts in-conversation:
// invalid payloads come back as isError: true with a human-readable error
// list, and the LLM corrects and re-calls until validation succeeds.
//
// Auto-attached by host.oracle.ask_with_mcp when the effect declares a
// `schema:` arg without an `mcp_servers:` block (see oracle_ask_with_mcp.go).
//
// Optional `--post-cmd` plumbing layers a semantic verifier on top of the
// schema check: after schema-pass, the validator spawns the configured
// command with the submitted JSON as `--submitted-json <tmp>` (plus any
// `--post-cmd-arg key=value` entries forwarded as `--key value`). A
// non-zero exit is treated as a verifier rejection — the captured stderr
// (ANSI-stripped, capped at 2000 chars) is returned to the LLM so it can
// re-submit. `--max-retries` caps total submit attempts; on exhaustion the
// validator marks the session as RetriesExhausted and the next caller can
// route to error.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	hallymcp "hally/internal/mcp"
)

func mcpValidatorCmd() *cobra.Command {
	var (
		schemaPath   string
		outputPath   string
		toolName     string
		description  string
		postCmd      string
		postCmdArgs  []string
		postCmdCwd   string
		maxRetries   int
	)
	cmd := &cobra.Command{
		Use:   "mcp-validator",
		Short: "Run a stdio MCP server that validates JSON against a JSON Schema",
		Long: `mcp-validator runs an MCP stdio server exposing one tool — by
default named "submit" — whose input schema is the JSON Schema at
--schema. The tool validates each call's arguments and returns a
human-readable error list (with isError: true) on failure so the
calling LLM can correct and call again within the same conversation.

This is the typed-output primitive used by host.oracle.ask_with_mcp.
Authors who set a "schema:" arg get the validator auto-attached; this
subcommand also lets external MCP clients (Claude Desktop, Claude
Code) wire it up directly via --mcp-config.

Example claude --mcp-config entry:

  {
    "mcpServers": {
      "validator": {
        "command": "hally",
        "args": ["mcp-validator", "--schema", "schemas/03-fix-proposal.json"]
      }
    }
  }

The schema must be a JSON Schema object whose top-level "type" is
"object" — that's the MCP tool input-schema invariant.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if schemaPath == "" {
				return fmt.Errorf("--schema is required")
			}
			abs, err := filepath.Abs(schemaPath)
			if err != nil {
				return fmt.Errorf("resolve --schema: %w", err)
			}
			raw, err := os.ReadFile(abs)
			if err != nil {
				return fmt.Errorf("read schema %q: %w", abs, err)
			}

			// Parse --post-cmd-arg key=value pairs into an ordered slice.
			parsedArgs := make([]hallymcp.PostCmdArg, 0, len(postCmdArgs))
			for _, kv := range postCmdArgs {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return fmt.Errorf("--post-cmd-arg %q: must be in key=value form", kv)
				}
				parsedArgs = append(parsedArgs, hallymcp.PostCmdArg{Key: k, Value: v})
			}

			srv, err := hallymcp.NewValidatorServer(hallymcp.ValidatorConfig{
				SchemaJSON:      raw,
				ToolName:        toolName,
				ToolDescription: description,
				OutputPath:      outputPath,
				PostCmd:         postCmd,
				PostCmdArgs:     parsedArgs,
				PostCmdCwd:      postCmdCwd,
				MaxRetries:      maxRetries,
			})
			if err != nil {
				return fmt.Errorf("build validator: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			fmt.Fprintf(os.Stderr, "hally: mcp-validator stdio server (schema=%s)\n", abs)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&schemaPath, "schema", "", "path to a JSON Schema (required)")
	cmd.Flags().StringVar(&outputPath, "output", "",
		"on each successful submit, write the validated JSON to this path (atomic; last call wins)")
	cmd.Flags().StringVar(&toolName, "tool-name", "", `override the tool name (default: "submit")`)
	cmd.Flags().StringVar(&description, "tool-description", "",
		"override the tool description shown to the LLM")
	cmd.Flags().StringVar(&postCmd, "post-cmd", "",
		"shell-quoted command run after schema-pass (e.g. \"python3 -m bugfix verify-impl\"). "+
			"Receives --submitted-json <tmp> plus any --post-cmd-arg key=value entries as --key value. "+
			"Exit 0 = accept; non-zero = LLM-visible reject with stderr returned (capped at 2000 chars).")
	cmd.Flags().StringArrayVar(&postCmdArgs, "post-cmd-arg", nil,
		"repeatable key=value forwarded to --post-cmd as --key value (e.g. ticket=PLTFRM-89912)")
	cmd.Flags().StringVar(&postCmdCwd, "post-cmd-cwd", "",
		"working directory for the --post-cmd subprocess (default: hally's cwd)")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 5,
		"max submit attempts (schema-fail + post-cmd-fail combined). On exhaustion the next call "+
			"returns a final-error response and Run() reports OutcomeRetriesExhausted.")
	return cmd
}

// mcp_validator.go — implements `kitsoki mcp-validator --schema <path>`.
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

	kitsokimcp "kitsoki/internal/mcp"
)

func mcpValidatorCmd() *cobra.Command {
	var (
		schemaPath    string
		outputPath    string
		toolName      string
		description   string
		postCmd       string
		postCmdArgs   []string
		postCmdCwd    string
		maxRetries    int
		stateFilePath string
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
        "command": "kitsoki",
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
			parsedArgs := make([]kitsokimcp.PostCmdArg, 0, len(postCmdArgs))
			for _, kv := range postCmdArgs {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || strings.TrimSpace(k) == "" {
					return fmt.Errorf("--post-cmd-arg %q: must be in key=value form", kv)
				}
				parsedArgs = append(parsedArgs, kitsokimcp.PostCmdArg{Key: k, Value: v})
			}

			srv, err := kitsokimcp.NewValidatorServer(kitsokimcp.ValidatorConfig{
				SchemaJSON:      raw,
				ToolName:        toolName,
				ToolDescription: description,
				OutputPath:      outputPath,
				PostCmd:         postCmd,
				PostCmdArgs:     parsedArgs,
				PostCmdCwd:      postCmdCwd,
				MaxRetries:      maxRetries,
				StateFilePath:   stateFilePath,
			})
			if err != nil {
				return fmt.Errorf("build validator: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: mcp-validator stdio server (schema=%s)\n", abs)
			runErr := srv.Run(ctx)
			// Surface the outcome to stderr so an external orchestrator
			// (one that's not using the in-process API) can observe what
			// happened. The exit code is also non-zero on retries-exhausted
			// so wrappers can branch on it without parsing logs.
			outcome := srv.Outcome()
			attempts, successful, lastErr := srv.Stats()
			fmt.Fprintf(os.Stderr,
				"kitsoki: mcp-validator outcome=%s attempts=%d successful_submits=%d last_error=%q\n",
				outcome, attempts, successful, lastErr)
			if runErr != nil {
				return runErr
			}
			if outcome == kitsokimcp.OutcomeRetriesExhausted {
				return fmt.Errorf("mcp-validator: max retries exhausted (%d attempts, no successful submit)", attempts)
			}
			return nil
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
		"working directory for the --post-cmd subprocess (default: kitsoki's cwd)")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 5,
		"max submit attempts (schema-fail + post-cmd-fail combined). On exhaustion the next call "+
			"returns a final-error response and Run() reports OutcomeRetriesExhausted.")
	cmd.Flags().StringVar(&stateFilePath, "state-file", "",
		"persist session counters (attempts/successful_submits/last_error) to this JSON file. "+
			"At startup the file is read to seed counters; after every submit it is rewritten "+
			"atomically. Used by host.oracle.ask_with_mcp to keep one logical validator session "+
			"across multiple `claude --resume` re-engagements. Empty = volatile in-memory only.")
	return cmd
}

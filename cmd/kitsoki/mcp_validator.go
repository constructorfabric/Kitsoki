// mcp_validator.go — implements `kitsoki mcp-validator --schema <path>`.
//
// Runs an MCP stdio server whose single `submit` tool validates incoming
// JSON arguments against the schema at <path>. Used by Claude (or any
// other MCP client) to enforce structured-output contracts in-conversation:
// invalid payloads come back as isError: true with a human-readable error
// list, and the LLM corrects and re-calls until validation succeeds.
//
// Auto-attached by host.agent.ask_with_mcp when the effect declares a
// `schema:` arg without an `mcp_servers:` block (see agent_ask_with_mcp.go).
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
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	kitsokimcp "kitsoki/internal/mcp"
)

// Embedded schemas live under stories/<app>/mcp/ in the source tree and
// are compiled into the kitsoki binary so external MCP clients (Claude
// Desktop, Claude Code) can launch a validator by name without needing
// the app's repo on disk. Authors can still pass `--schema <path>` to
// validate against an on-disk schema; the embedded names are a
// convenience for the proposal §9.6 invocation
// `kitsoki mcp-validator illness`.
//
//go:embed embedded_schemas/*.json
var embeddedSchemasFS embed.FS

// embeddedSchemas maps the positional name (e.g. "illness") to the
// embedded file's path inside embeddedSchemasFS. Add new entries when a
// story wants its schema reachable by name; keep the source-of-truth
// JSON under stories/<app>/mcp/ and mirror it into
// cmd/kitsoki/embedded_schemas/ at build time. Currently the mirror is
// manual (one schema, low churn); if more arrive we should add a
// `go generate` step.
var embeddedSchemas = map[string]string{
	"illness": "embedded_schemas/illness.json",
}

// resolveEmbeddedSchema returns the raw JSON for a named schema, or
// (nil, false) if the name is not registered.
func resolveEmbeddedSchema(name string) ([]byte, bool) {
	path, ok := embeddedSchemas[name]
	if !ok {
		return nil, false
	}
	data, err := embeddedSchemasFS.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// embeddedSchemaNames returns the registered names sorted for stable
// help-text output.
func embeddedSchemaNames() []string {
	out := make([]string, 0, len(embeddedSchemas))
	for k := range embeddedSchemas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mcpValidatorCmd() *cobra.Command {
	var (
		schemaPath          string
		outputPath          string
		toolName            string
		description         string
		postCmd             string
		postCmdArgs         []string
		postCmdCwd          string
		maxRetries          int
		minInformationRatio float64
		minInformationBits  float64
		stateFilePath       string
		validateOnce        bool
	)
	cmd := &cobra.Command{
		Use:   "mcp-validator [name]",
		Short: "Run a stdio MCP server that validates JSON against a JSON Schema",
		Long: `mcp-validator runs an MCP stdio server exposing one tool — by
default named "submit" — whose input schema is the JSON Schema at
--schema (or, when a positional [name] is given, the embedded schema
with that name). The tool validates each call's arguments and returns
a human-readable error list (with isError: true) on failure so the
calling LLM can correct and call again within the same conversation.

This is the typed-output primitive used by host.agent.ask_with_mcp.
Authors who set a "schema:" arg get the validator auto-attached; this
subcommand also lets external MCP clients (Claude Desktop, Claude
Code) wire it up directly via --mcp-config.

Two invocation forms:

  # 1. Path-based: validate against a schema file on disk. This is the
  # form auto-attached by host.agent.ask_with_mcp when an effect
  # declares schema: <path>.
  kitsoki mcp-validator --schema stories/oregon-trail/mcp/illness.json

  # 2. Name-based: validate against an embedded schema (compiled into
  # the binary). Use this from external MCP clients that don't have
  # the app repo on disk. Names are registered in mcp_validator.go.
  kitsoki mcp-validator illness

Example claude --mcp-config entry:

  {
    "mcpServers": {
      "validator": {
        "command": "kitsoki",
        "args": ["mcp-validator", "illness"]
      }
    }
  }

The schema must be a JSON Schema object whose top-level "type" is
"object" — that's the MCP tool input-schema invariant.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw []byte
			var sourceLabel string
			switch {
			case len(args) == 1:
				name := args[0]
				data, ok := resolveEmbeddedSchema(name)
				if !ok {
					return fmt.Errorf("unknown embedded schema %q. Known: %s",
						name, strings.Join(embeddedSchemaNames(), ", "))
				}
				if schemaPath != "" {
					return fmt.Errorf("cannot combine positional schema name with --schema")
				}
				raw = data
				sourceLabel = "embedded:" + name
			case schemaPath != "":
				abs, err := filepath.Abs(schemaPath)
				if err != nil {
					return fmt.Errorf("resolve --schema: %w", err)
				}
				data, err := os.ReadFile(abs)
				if err != nil {
					return fmt.Errorf("read schema %q: %w", abs, err)
				}
				raw = data
				sourceLabel = abs
			default:
				return fmt.Errorf("either a positional schema name (one of: %s) or --schema is required",
					strings.Join(embeddedSchemaNames(), ", "))
			}

			// --validate-once: read one JSON payload from stdin, validate
			// it against the schema, and exit. Bypasses the MCP server
			// machinery — useful for CI checks and human debugging.
			//
			// SilenceUsage suppresses cobra's usage dump on validation
			// failure — the operator already gets the schema error on
			// stderr; the usage block adds noise.
			if validateOnce {
				cmd.SilenceUsage = true
				return runValidateOnce(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), raw, sourceLabel)
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
				SchemaJSON:          raw,
				ToolName:            toolName,
				ToolDescription:     description,
				OutputPath:          outputPath,
				PostCmd:             postCmd,
				PostCmdArgs:         parsedArgs,
				PostCmdCwd:          postCmdCwd,
				MaxRetries:          maxRetries,
				MinInformationRatio: &minInformationRatio,
				MinInformationBits:  &minInformationBits,
				StateFilePath:       stateFilePath,
			})
			if err != nil {
				return fmt.Errorf("build validator: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: mcp-validator stdio server (schema=%s)\n", sourceLabel)
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
	cmd.Flags().Float64Var(&minInformationRatio, "min-information-ratio", kitsokimcp.DefaultMinInformationRatio,
		"reject a schema-valid submission below this fraction of the richest parsed attempt (0 disables)")
	cmd.Flags().Float64Var(&minInformationBits, "min-information-bits", kitsokimcp.DefaultMinInformationBits,
		"activate the relative information gate only after an attempt reaches this Shannon-information score")
	cmd.Flags().StringVar(&stateFilePath, "state-file", "",
		"persist session counters (attempts/successful_submits/last_error) to this JSON file. "+
			"At startup the file is read to seed counters; after every submit it is rewritten "+
			"atomically. Used by host.agent.ask_with_mcp to keep one logical validator session "+
			"across multiple `claude --resume` re-engagements. Empty = volatile in-memory only.")
	cmd.Flags().BoolVar(&validateOnce, "validate-once", false,
		"read one JSON payload from stdin, validate it against the resolved schema, and "+
			"exit 0 on pass / non-zero on fail. Bypasses the MCP server machinery; useful "+
			"for CI checks (e.g. `kitsoki mcp-validator illness --validate-once < payload.json`).")
	return cmd
}

// runValidateOnce reads a single JSON payload from stdin (`in`) and
// validates it against the resolved schema. Writes a one-line summary
// to stdout and returns a non-nil error (which cobra surfaces as a
// non-zero exit) when validation fails. The schema is compiled with the
// same custom formats (jql, etc.) as the MCP server so behaviour
// matches. Threading the reader as a parameter (rather than using
// os.Stdin directly) lets tests drive the function deterministically.
func runValidateOnce(in io.Reader, stdout, stderr io.Writer, schemaJSON []byte, sourceLabel string) error {
	var schemaProbe map[string]any
	if err := json.Unmarshal(schemaJSON, &schemaProbe); err != nil {
		return fmt.Errorf("parse schema %s: %w", sourceLabel, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("validator-schema.json", schemaProbe); err != nil {
		return fmt.Errorf("register schema %s: %w", sourceLabel, err)
	}
	compiled, err := compiler.Compile("validator-schema.json")
	if err != nil {
		return fmt.Errorf("compile schema %s: %w", sourceLabel, err)
	}

	payload, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return fmt.Errorf("no JSON payload on stdin (--validate-once expects one JSON object)")
	}
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		return fmt.Errorf("parse stdin payload as JSON: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		fmt.Fprintf(stderr, "validation FAILED against %s:\n%v\n", sourceLabel, err)
		return fmt.Errorf("payload does not conform to schema")
	}
	fmt.Fprintf(stdout, "validation passed against %s\n", sourceLabel)
	return nil
}

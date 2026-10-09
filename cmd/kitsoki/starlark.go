package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
)

// starlarkCmd exposes story-provided Starlark without requiring a YAML effect
// or a Python shim. The command deliberately calls the same host adapter used
// by stories and agent-serve, so sidecars, capabilities, and error mapping do
// not fork between transports.
func starlarkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "starlark",
		Short: "Run story-provided Starlark glue",
	}
	cmd.AddCommand(starlarkRunCmd())
	return cmd
}

func starlarkRunCmd() *cobra.Command {
	var inputsRaw, worldRaw, capabilitiesRaw, function string
	cmd := &cobra.Command{
		Use:   "run <script.star>",
		Short: "Run a sidecar-validated Starlark function",
		Long: `Run a story-provided .star script through host.starlark.run.

The script must have a sibling .star.yaml sidecar. Inputs, world, and
capabilities accept inline JSON, @path/to/file.json, or - for stdin. The
default entry point is main(ctx); --function calls another exported function
with the same ctx value.

Examples:
  kitsoki starlark run stories/example/scripts/derive.star --inputs '{"id":"42"}'
  kitsoki starlark run scripts/report.star --world @world.json --capabilities '{"fs":{"read":["docs/**"]}}'
  kitsoki starlark run scripts/report.star --function report --inputs - < inputs.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			script, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("starlark: resolve script: %w", err)
			}
			inputs, err := readStarlarkJSONArg(inputsRaw, cmd.InOrStdin(), "inputs")
			if err != nil {
				return err
			}
			world, err := readStarlarkJSONArg(worldRaw, cmd.InOrStdin(), "world")
			if err != nil {
				return err
			}
			capabilities, err := readStarlarkJSONArg(capabilitiesRaw, cmd.InOrStdin(), "capabilities")
			if err != nil {
				return err
			}

			argsMap := map[string]any{
				"script": script,
				"inputs": inputs,
				"world":  world,
			}
			if capabilitiesRaw != "" {
				argsMap["capabilities"] = capabilities
			}
			if function != "" {
				argsMap["function"] = function
			}
			result, err := host.StarlarkRunHandler(cmd.Context(), argsMap)
			if err != nil {
				return err
			}
			if result.Error != "" {
				return fmt.Errorf("%s", result.Error)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			return enc.Encode(result.Data)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&inputsRaw, "inputs", "{}", "JSON object, @file, or - for ctx.inputs")
	flags.StringVar(&worldRaw, "world", "{}", "JSON object, @file, or - for ctx.world")
	flags.StringVar(&capabilitiesRaw, "capabilities", "", "JSON capability object, @file, or -")
	flags.StringVar(&function, "function", "main", "Starlark function to call")
	return cmd
}

func readStarlarkJSONArg(raw string, stdin io.Reader, name string) (any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	data := []byte(raw)
	switch {
	case raw == "-":
		var err error
		data, err = io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("starlark: read %s from stdin: %w", name, err)
		}
	case strings.HasPrefix(raw, "@"):
		var err error
		data, err = os.ReadFile(strings.TrimPrefix(raw, "@"))
		if err != nil {
			return nil, fmt.Errorf("starlark: read %s file: %w", name, err)
		}
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("starlark: parse %s JSON: %w", name, err)
	}
	return value, nil
}

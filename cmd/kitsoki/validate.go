// validate.go — `kitsoki validate <app.yaml>`: load a story through the full
// loader pipeline and report load-time validation errors WITHOUT starting a
// session or running a turn. The fast "is this story well-formed?" check.
package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
)

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <app.yaml>",
		Short: "Statically validate a story without running it",
		Long: `Load a story app.yaml through the full loader pipeline — imports, phase
expansion, interface resolution, and every load-time validator — and report any
errors. No session is started and no turn is run, so it is fast and free.

It surfaces the same errors a run/flow would hit at load, including:
  - unknown intents / transition targets, world-key typos, agent & host refs
  - host.starlark.run wiring: a missing script/sidecar, and an inputs: value
    that is a bare expression ("world.x" instead of "{{ world.x }}") or a
    literal that can never satisfy its declared sidecar type

Exit status is 0 when the story loads cleanly, 1 otherwise.

  kitsoki validate stories/slidey-edit/app.yaml`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if _, err := app.LoadWithResolver(path, nil, buildImportResolver()); err != nil {
				// errors.Join renders one validation error per line; present them
				// as a bulleted list on stderr so stdout stays clean for piping.
				fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s — invalid:\n", path)
				for _, line := range strings.Split(strings.TrimRight(err.Error(), "\n"), "\n") {
					if strings.TrimSpace(line) == "" {
						continue
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "  • %s\n", line)
				}
				return fmt.Errorf("validation failed")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s — valid\n", path)
			return nil
		},
	}
	return cmd
}

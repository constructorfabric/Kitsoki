// render.go — implements `kitsoki render`: produces one-way Markdown
// documentation from an app.yaml. YAML stays the source of truth; the
// rendered doc is a read-only work product.
//
// See `kitsoki docs render-format` for the output shape and
// `kitsoki docs apply-proposal` for the LLM-driven proposal workflow.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/app/render"
)

func renderCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "render <app.yaml>",
		Short: "Render human-readable Markdown documentation from an app.yaml",
		Long: `Render an app.yaml as a Markdown document: overview, Mermaid state
diagram, world-variable table, intent catalogue, and per-room transition
tables. The output is a one-way work product — edit app.yaml, then
re-render; the Markdown never feeds back into the engine.

See 'kitsoki docs render-format' for the output shape and
'kitsoki docs apply-proposal' for the LLM-driven proposal workflow that lets
humans propose changes in prose and have an LLM implement them in YAML.

Examples:
  kitsoki render testdata/apps/cloak/app.yaml -o testdata/apps/cloak/APP.md
  kitsoki render myapp.yaml | less
  kitsoki render myapp.yaml | claude -p 'add a settings room that...'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			// loadAppWithEnv publishes KITSOKI_APP_DIR so authors can
			// safely use `cwd: "${KITSOKI_APP_DIR}/..."` in app.yaml
			// — the loader's env-var validator runs during Load.
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}
			md, err := render.Markdown(def)
			if err != nil {
				return fmt.Errorf("render: %w", err)
			}
			return writeRendered(outPath, md, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", `output path (default: stdout; "-" also means stdout)`)
	return cmd
}

func writeRendered(path string, data []byte, stdout io.Writer) error {
	if path == "" || path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0644)
}

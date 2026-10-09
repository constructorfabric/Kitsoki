// tui_bridge.go — implements `kitsoki tui-serve`, a live pty-over-websocket
// bridge so Playwright and claude-in-chrome can drive the real running TUI
// (real keystrokes in, real ANSI render out) instead of the committed
// cassette replay in tools/mcp-demo. See
// .context/live-tui-browser-bridge-brief.md for the design rationale.
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/tuibridge"
)

func tuiServeCmd() *cobra.Command {
	var (
		addr    string
		workdir string
		exec    string
	)

	cmd := &cobra.Command{
		Use:   "tui-serve [-- <command> [args...]]",
		Short: "Serve a real TUI process over a pty-bridged websocket for browser drivers",
		Long: `Spawn a command in a real pty and bridge its bytes over a websocket at
/pty — one connection, one process, full ANSI fidelity in both directions
(keystrokes in as binary frames, rendered output out as binary frames; a
{"type":"resize","cols":n,"rows":n} JSON text frame resizes the pty).

With no trailing command this spawns the current kitsoki executable's bare
'run' subcommand (the interactive onboarding TUI). Pass a command after '--'
to drive anything else, e.g. a specific app or harness:

  kitsoki tui-serve --addr 127.0.0.1:4700 -- run myapp.yaml --harness replay --recording rec.yaml

Pair this with the static page in tools/tui-bridge/player (a forked xterm.js
scaffold) to drive it from Playwright or claude-in-chrome.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			command := args
			if exec != "" {
				command = append([]string{exec}, args...)
			} else if len(command) == 0 {
				self, err := os.Executable()
				if err != nil {
					return fmt.Errorf("resolve current executable: %w", err)
				}
				command = []string{self, "run"}
			} else {
				self, err := os.Executable()
				if err != nil {
					return fmt.Errorf("resolve current executable: %w", err)
				}
				command = append([]string{self}, args...)
			}

			srv := tuibridge.New(tuibridge.Options{Command: command, Dir: workdir})
			mux := http.NewServeMux()
			mux.Handle("/pty", srv)

			fmt.Fprintf(cmd.OutOrStdout(), "tui-serve: bridging %v over ws://%s/pty\n", command, addr)
			return http.ListenAndServe(addr, mux)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:4700", "listen address for the websocket bridge")
	cmd.Flags().StringVar(&workdir, "workdir", "", "working directory for the spawned process (default: this process's cwd)")
	cmd.Flags().StringVar(&exec, "exec", "", "explicit binary to spawn instead of the current kitsoki executable (the trailing args are still passed through)")

	return cmd
}

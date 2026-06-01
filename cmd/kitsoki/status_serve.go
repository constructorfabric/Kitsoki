// status_serve.go — implements `kitsoki status serve`, the live run-status
// web UI. It serves the bundled runstatus SPA and the JSON-RPC + SSE contract
// the SPA's live data source expects, reading session state from the SQLite
// store a run writes to. Read-only: it never mutates session state.
//
// See internal/runstatus/server for the HTTP surface and docs/tracing/ for the
// run-status UI overview.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/runstatus/server"
)

// statusCmd is the parent for run-status inspection subcommands.
func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect kitsoki runs via the run-status web UI",
		Long: `Inspect kitsoki runs with the run-status web UI.

For a live, updating view of an in-progress (or finished) run, use
'kitsoki status serve'. For a portable, self-contained HTML snapshot of a run,
use 'kitsoki export-status … -o run.html'.`,
	}
	cmd.AddCommand(statusServeCmd())
	return cmd
}

// statusServeCmd returns `kitsoki status serve <app.yaml> --trace <run.jsonl>`.
func statusServeCmd() *cobra.Command {
	var (
		tracePath string
		addr      string
	)

	cmd := &cobra.Command{
		Use:   "serve <app.yaml>",
		Short: "Serve the live run-status web UI for a JSONL trace",
		Long: `Serve the live run-status web UI over HTTP.

The UI shows a run as an interactive state diagram, a filterable trace
timeline, and a detail drawer — the same Snapshot the export-status HTML
artifact carries, but live: it re-reads the trace and streams new events to
the browser as the run appends to the file.

  kitsoki run myapp.yaml --trace run.jsonl        # in one terminal
  kitsoki status serve myapp.yaml --trace run.jsonl   # in another

--trace points at the JSONL trace the run writes (the same --trace you pass to
'kitsoki run'). The trace — not the SQLite session store — is the source
because it is the full-fidelity record: it carries per-event state_path,
call_id, and parent_turn, which the store does not persist and which the UI
needs (notably oracle-call pairing). The file may not exist yet when serving
starts; the UI shows an empty run until the first events are written.

The runstatus SPA must be bundled into the binary (run 'make build', which
runs 'pnpm build' under tools/runstatus/); otherwise the page reports the UI
as unbuilt. Read-only — serving never writes. Assumes a trusted localhost /
internal network; there is no authentication.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tracePath == "" {
				return fmt.Errorf("--trace is required (the JSONL trace the run writes; see `kitsoki run --trace`)")
			}

			def, err := loadAppWithEnv(args[0])
			if err != nil {
				return err
			}

			srv := server.New(tracePath, def)
			httpSrv := &http.Server{
				Addr:              addr,
				Handler:           srv.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			go func() {
				<-ctx.Done()
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutCancel()
				_ = httpSrv.Shutdown(shutCtx)
			}()

			fmt.Fprintf(cmd.ErrOrStderr(), "kitsoki: run-status UI for app %q on http://%s\n", def.App.ID, addr)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&tracePath, "trace", "", "path to the JSONL trace the run writes (required)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "HTTP listen address")
	return cmd
}

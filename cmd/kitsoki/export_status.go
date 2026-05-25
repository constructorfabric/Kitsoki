// export_status.go — implements the `kitsoki export-status` subcommand.
//
// Two modes (per docs/proposals/runstatus-proposal.md Phase 1 §2/§6):
//
//  1. --from-trace <path.jsonl> --app <app.yaml> [options]
//     Reads a recorded JSONL trace, loads the AppDef, synthesises a Snapshot,
//     and writes JSON to -o <out>. This is the fixture-generator path.
//
//  2. -o status.html  (live/active session mode)
//     Not yet implemented in this branch. Prints a message and exits 2.
//     See docs/proposals/runstatus-proposal.md phase 1 step 6.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/viz"
)

func exportStatusCmd() *cobra.Command {
	var (
		fromTrace    string
		appPath      string
		currentState string
		sessionID    string
		startedAt    string
		outPath      string
		withMermaid  bool
	)

	cmd := &cobra.Command{
		Use:   "export-status",
		Short: "Export a run snapshot as a self-contained JSON artifact",
		Long: `Export a kitsoki run as a self-contained Snapshot JSON file.

Fixture-generator mode (from a recorded trace):
  kitsoki export-status --from-trace run.jsonl --app myapp.yaml -o status.snapshot.json

  Options for --from-trace:
    --current-state <path>   override current state (default: derived from last trace event)
    --session-id <id>        override session ID (default: derived from trace)
    --started-at <iso8601>   override start time (default: earliest trace event time)

Live-mode export (reads the in-process ring buffer):
  kitsoki export-status -o status.html
  (not yet implemented — see docs/proposals/runstatus-proposal.md phase 1 step 6)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outPath == "" {
				return fmt.Errorf("--out / -o is required")
			}

			// ── --from-trace mode ─────────────────────────────────────────
			if fromTrace != "" {
				return runExportFromTrace(fromTrace, appPath, currentState, sessionID, startedAt, outPath, withMermaid)
			}

			// ── Live mode stub ────────────────────────────────────────────
			// Phase 1 step 6 of the runstatus proposal. The in-process ring
			// buffer read path and the single-file HTML template are tracked
			// in feat/runstatus-export-html (not this branch).
			fmt.Fprintln(cmd.ErrOrStderr(),
				"not yet implemented (see docs/proposals/runstatus-proposal.md phase 1 step 6)")
			os.Exit(2)
			return nil // unreachable; satisfies the compiler
		},
	}

	cmd.Flags().StringVar(&fromTrace, "from-trace", "", "path to a JSONL trace file produced by kitsoki run --trace")
	cmd.Flags().StringVar(&appPath, "app", "", "path to the app.yaml (required with --from-trace)")
	cmd.Flags().StringVar(&currentState, "current-state", "", "override current state path (dotted, e.g. bar.dark)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "override session ID")
	cmd.Flags().StringVar(&startedAt, "started-at", "", "override session start time (RFC3339, e.g. 2026-05-25T10:00:00Z)")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output file path (required)")
	cmd.Flags().BoolVar(&withMermaid, "with-mermaid", true, "populate mermaid.source and mermaid.node_map (default true when --from-trace is used)")

	return cmd
}

// runExportFromTrace reads a JSONL trace and an app.yaml, synthesises a
// Snapshot, and writes it as indented JSON to outPath.
func runExportFromTrace(tracePath, appPath, currentStateFlag, sessionIDFlag, startedAtFlag, outPath string, withMermaid bool) error {
	if appPath == "" {
		return fmt.Errorf("--app is required with --from-trace")
	}

	// ── Parse trace events ────────────────────────────────────────────────
	events, err := parseTraceFile(tracePath)
	if err != nil {
		return fmt.Errorf("parse trace %q: %w", tracePath, err)
	}

	// ── Load AppDef ───────────────────────────────────────────────────────
	// Use loadAppWithEnv so KITSOKI_APP_DIR is published before Load; this
	// matches the pattern used by runCmd, vizCmd, etc.
	def, err := loadAppWithEnv(appPath)
	if err != nil {
		return err
	}

	// ── Synthesise SessionHeader ──────────────────────────────────────────
	header := synthesiseSessionHeader(def, events, sessionIDFlag, currentStateFlag, startedAtFlag)

	// ── Mermaid ───────────────────────────────────────────────────────────
	// When --with-mermaid (default true) call FlowchartWithMap so the
	// exported snapshot has a fully-populated diagram and node map.
	// The UI degrades gracefully when Source is empty (no diagram panel),
	// so --with-mermaid=false is a valid way to produce a lighter fixture.
	var mermaid runstatus.MermaidSnapshot
	if withMermaid {
		result, err := viz.FlowchartWithMap(def, viz.FlowchartOptions{
			Detail: viz.DetailStates,
		})
		if err != nil {
			// Non-fatal: degrade gracefully rather than aborting the export.
			fmt.Fprintf(os.Stderr, "export-status: mermaid generation failed (continuing without diagram): %v\n", err)
		} else {
			mermaid = runstatus.MermaidSnapshot{
				Source:  result.Source,
				NodeMap: result.NodeMap,
			}
		}
	}

	// ── Build Snapshot ────────────────────────────────────────────────────
	snap := runstatus.Snapshot{
		Session: header,
		App:     def,
		Mermaid: mermaid,
		Events:  events,
	}

	// ── Write output ──────────────────────────────────────────────────────
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("create output directory: %w", err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", outPath, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return nil
}

// parseTraceFile reads a JSONL trace file line by line and returns a slice of
// TraceEvent values. Lines that fail to parse are skipped with a warning
// rather than aborting so a partial trace still produces a usable fixture.
func parseTraceFile(path string) ([]runstatus.TraceEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []runstatus.TraceEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MB line buffer, same as the trace pretty-printer

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev runstatus.TraceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Non-fatal: skip and warn. Real runs may have a partial final line
			// if the process was interrupted mid-write.
			fmt.Fprintf(os.Stderr, "export-status: skip trace line %d: %v\n", lineNum, err)
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan trace file: %w", err)
	}
	return events, nil
}

// synthesiseSessionHeader derives a SessionHeader from the parsed trace events
// and any flag overrides supplied by the caller.
//
// Derivation rules (flags win over trace-derived values):
//   - SessionID: flag --session-id, else first non-empty session_id from events.
//   - AppID: always from the loaded AppDef.
//   - CurrentState: flag --current-state, else state_path from the last event.
//   - Turn: max turn seen across all events.
//   - StartedAt: flag --started-at (RFC3339), else earliest non-zero event Time.
//   - Terminal: looks up CurrentState in AppDef; true if State.Terminal == true.
func synthesiseSessionHeader(def *app.AppDef, events []runstatus.TraceEvent, sessionIDFlag, currentStateFlag, startedAtFlag string) runstatus.SessionHeader {
	var (
		sessionID    string
		currentState string
		maxTurn      int
		startedAt    time.Time
	)

	// Walk events to collect derived values.
	for _, ev := range events {
		// SessionID: first non-empty session_id.
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		// CurrentState: state_path from the last event that has one.
		if ev.StatePath != "" {
			currentState = ev.StatePath
		}
		// Turn: max.
		if ev.Turn > maxTurn {
			maxTurn = ev.Turn
		}
		// StartedAt: earliest non-zero time.
		if !ev.Time.IsZero() && (startedAt.IsZero() || ev.Time.Before(startedAt)) {
			startedAt = ev.Time
		}
	}

	// Apply flag overrides.
	if sessionIDFlag != "" {
		sessionID = sessionIDFlag
	}
	if currentStateFlag != "" {
		currentState = currentStateFlag
	}
	if startedAtFlag != "" {
		if t, err := time.Parse(time.RFC3339, startedAtFlag); err == nil {
			startedAt = t
		}
	}

	// Terminal: look up currentState in AppDef.
	// We use app.Compile to access LookupState without reimplementing the
	// dot-path walk. The cost is negligible (no I/O).
	terminal := isStateTerminal(def, currentState)

	return runstatus.SessionHeader{
		SessionID:    sessionID,
		AppID:        def.App.ID,
		CurrentState: currentState,
		Turn:         maxTurn,
		StartedAt:    startedAt,
		Terminal:     terminal,
	}
}

// isStateTerminal returns true when the state at the given dot-path in def has
// Terminal == true. Returns false for unknown paths and empty paths.
func isStateTerminal(def *app.AppDef, statePath string) bool {
	if def == nil || statePath == "" {
		return false
	}
	compiled := app.Compile(def)
	s, ok := compiled.LookupState(app.StatePath(statePath))
	return ok && s != nil && s.Terminal
}

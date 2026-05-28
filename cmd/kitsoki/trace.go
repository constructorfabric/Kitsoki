// trace.go — implements the `kitsoki trace` subcommand.
//
// `kitsoki trace <file>` pretty-prints an EventSink JSONL trace file produced
// by `kitsoki run` or `kitsoki turn --trace`. Each line is a store.Event
// encoded in the traceEvent shape (turn, seq, ts, kind, state_path, payload).
//
// The slog-based tracing path (--trace / --trace-pretty / --trace-level flags
// on `kitsoki run`) was removed in the phase-A finalisation commit. The EventSink
// JSONL is now the only trace format. For ad-hoc inspection `jq` or this command
// both work; for programmatic consumption parse the JSONL directly.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// ─── Style helpers (NO_COLOR aware) ──────────────────────────────────────────

var noColor = os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"

func styleFor(s string, color lipgloss.Color) string {
	if noColor {
		return s
	}
	return lipgloss.NewStyle().Foreground(color).Render(s)
}

var (
	colorTurn    = lipgloss.Color("12")  // bright blue
	colorHarness = lipgloss.Color("10")  // bright green
	colorMachine = lipgloss.Color("11")  // bright yellow
	colorStore   = lipgloss.Color("14")  // bright cyan
	colorErr     = lipgloss.Color("9")   // bright red
	colorDim     = lipgloss.Color("8")   // dark gray
	colorOffPath = lipgloss.Color("214") // amber
	colorTimeout = lipgloss.Color("51")  // bright cyan-blue
	colorTele    = lipgloss.Color("135") // violet
	colorJob     = lipgloss.Color("33")  // mid blue
	colorSlot    = lipgloss.Color("220") // soft yellow
	colorInbox   = lipgloss.Color("245") // mid gray
)

// eventRecord is the minimum shape of an EventSink JSONL line.
// Fields mirror the traceEvent struct in internal/store/jsonl.go.
type eventRecord struct {
	Turn      int64  `json:"turn"`
	Seq       int    `json:"seq"`
	Ts        string `json:"ts"`
	Kind      string `json:"kind"`
	StatePath string `json:"state_path"`
	Payload   any    `json:"payload"`
}

// prettyEventLine formats one EventSink JSONL record for human output.
func prettyEventLine(rec eventRecord, extra map[string]any) string {
	var sb strings.Builder

	// Timestamp.
	ts := ""
	if rec.Ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.Ts); err == nil {
			ts = t.Format("15:04:05.000")
		} else {
			ts = rec.Ts
		}
	}

	// Turn prefix.
	var turnPrefix string
	if rec.Turn > 0 {
		turnPrefix = fmt.Sprintf("[T%d %s]", rec.Turn, ts)
	} else {
		turnPrefix = fmt.Sprintf("[   %s]", ts)
	}

	msg := rec.Kind

	// Route by kind prefix to pick color and indent level.
	switch {
	case strings.HasPrefix(msg, "turn."):
		line := styleFor(turnPrefix, colorTurn) + " " +
			styleFor(strings.ToUpper(strings.TrimPrefix(msg, "turn.")), colorTurn) +
			" " + formatKV(extra, rec.StatePath)
		sb.WriteString(line)

	case strings.HasPrefix(msg, "oracle.ask"), strings.HasPrefix(msg, "oracle.call"), strings.HasPrefix(msg, "oracle.off_path"):
		sb.WriteString("  " + styleFor("ORACLE", colorHarness) +
			" " + msg +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "harness."):
		sb.WriteString("  " + styleFor("HARNESS", colorHarness) +
			" " + strings.TrimPrefix(msg, "harness.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "machine."):
		sb.WriteString("  " + styleFor("MACHINE", colorMachine) +
			" " + strings.TrimPrefix(msg, "machine.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "world."):
		sb.WriteString("  " + styleFor("WORLD", colorStore) +
			" " + strings.TrimPrefix(msg, "world.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "scheduler."):
		sb.WriteString("  " + styleFor("JOB", colorJob) +
			" " + strings.TrimPrefix(msg, "scheduler.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "session."):
		sb.WriteString("  " + styleFor("SESSION", colorDim) +
			" " + strings.TrimPrefix(msg, "session.") +
			" " + formatKV(extra, ""))

	default:
		sb.WriteString(styleFor(turnPrefix, colorDim) + " " + msg +
			" " + formatKV(extra, ""))
	}

	// Color-code by family.
	_ = colorOffPath
	_ = colorTimeout
	_ = colorTele
	_ = colorSlot
	_ = colorInbox
	_ = colorErr

	return sb.String()
}

// formatKV formats extra key-value pairs as k=v k=v, omitting structural keys.
func formatKV(m map[string]any, statePath string) string {
	skip := map[string]bool{
		"turn":       true,
		"seq":        true,
		"ts":         true,
		"kind":       true,
		"state_path": true,
		"payload":    true,
	}
	var parts []string

	if statePath != "" {
		parts = append(parts, styleFor("state="+statePath, colorDim))
	}

	for k, v := range m {
		if skip[k] {
			continue
		}
		vs := fmt.Sprintf("%v", v)
		parts = append(parts, fmt.Sprintf("%s=%s", k, vs))
	}
	return strings.Join(parts, " ")
}

// prettyPrint reads EventSink JSONL from r and writes human-readable output to w.
func prettyPrint(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var currentTurn int64
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var rec eventRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			fmt.Fprintf(w, "%s\n", line)
			continue
		}

		// Decode all fields for extra KV display.
		var all map[string]any
		_ = json.Unmarshal([]byte(line), &all)

		// Print blank line between turns.
		if rec.Turn > 0 && rec.Turn != currentTurn && strings.HasPrefix(rec.Kind, "turn.start") {
			if currentTurn > 0 {
				fmt.Fprintln(w)
			}
			currentTurn = rec.Turn
		}

		fmt.Fprintf(w, "%s\n", prettyEventLine(rec, all))
	}
	return scanner.Err()
}

// ─── CLI command ──────────────────────────────────────────────────────────────

func traceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace [path]",
		Short: "Pretty-print an EventSink JSONL trace file",
		Long: `Pretty-print an EventSink JSONL trace file produced by 'kitsoki run'
or 'kitsoki turn --trace <path>'.

Each line is one store.Event encoded as:
  {"turn":<n>,"seq":<n>,"ts":"<RFC3339Nano>","kind":"<dotted>","state_path":"<path>","payload":{...}}

If path is '-', reads from stdin.

For ad-hoc field extraction, jq works equally well:
  jq 'select(.kind=="machine.state_entered") | .state_path' trace.jsonl`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("provide a trace file path or '-' for stdin")
			}

			var r io.Reader
			if args[0] == "-" {
				r = os.Stdin
			} else {
				f, err := os.Open(args[0])
				if err != nil {
					return fmt.Errorf("open trace file: %w", err)
				}
				defer func() { _ = f.Close() }()
				r = f
			}
			return prettyPrint(r, cmd.OutOrStdout())
		},
	}

	return cmd
}

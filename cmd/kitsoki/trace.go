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
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"kitsoki/internal/testrunner"
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

// digestTurn is one turn's story, distilled from its events for the --turns view.
type digestTurn struct {
	num       int64
	state     string
	input     string
	intent    string
	routedBy  string
	matchType string
	hostCalls []string
	prompts   []string // "verb: <truncated prompt>"
	ide       string   // ide.context_captured summary
	redirects []string // host.on_error.redirect targets
	errors    []string // any payload.error
	outcome   string
	newState  string
}

// digestTurns groups a trace by turn and prints a compact per-turn narrative:
// the operator input, which routing tier resolved it (and why), the host calls
// fired, the PROMPT each oracle verb dispatched (the source of truth for what
// the model saw — truncated), any editor context captured, on_error redirects,
// errors, and the outcome. This is the "what actually happened to my turn" view
// you otherwise reconstruct by hand with grep+jq.
func digestTurns(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)

	var order []int64
	byTurn := map[int64]*digestTurn{}
	get := func(turn int64) *digestTurn {
		d, ok := byTurn[turn]
		if !ok {
			d = &digestTurn{num: turn}
			byTurn[turn] = d
			order = append(order, turn)
		}
		return d
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec eventRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		p, _ := rec.Payload.(map[string]any)
		d := get(rec.Turn)
		if rec.StatePath != "" {
			d.state = rec.StatePath
		}
		if e := str(p["error"]); e != "" {
			d.errors = append(d.errors, rec.Kind+": "+e)
		}
		switch {
		case rec.Kind == "turn.input":
			if v := str(p["input"]); v != "" {
				d.input = v
			}
			if v := str(p["intent"]); v != "" {
				d.intent = v
			}
		case rec.Kind == "turn.start":
			if v := str(p["input"]); v != "" && d.input == "" {
				d.input = v
			}
			d.routedBy = str(p["routed_by"])
			d.matchType = str(p["match_type"])
		case rec.Kind == "harness.called", rec.Kind == "harness.dispatched":
			if ns := str(p["namespace"]); ns != "" {
				d.hostCalls = appendUniq(d.hostCalls, ns)
			}
		case rec.Kind == "oracle.call.start":
			d.prompts = append(d.prompts, str(p["verb"])+": "+truncate1(str(p["prompt"]), 160))
		case rec.Kind == "ide.context_captured":
			d.ide = fmtIDECapture(p)
		case rec.Kind == "host.on_error.redirect":
			d.redirects = append(d.redirects, str(p["to"])+" ("+str(p["from"])+")")
		case rec.Kind == "turn.end":
			d.outcome = str(p["outcome"])
			if v := str(p["to"]); v != "" {
				d.newState = v
			} else if v := str(p["new_state"]); v != "" {
				d.newState = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	for _, n := range order {
		d := byTurn[n]
		// Skip bookkeeping-only turns (e.g. turn 0's story snapshot) that carry
		// no input, host call, prompt, outcome, or error.
		if d.input == "" && d.intent == "" && len(d.hostCalls) == 0 &&
			len(d.prompts) == 0 && d.outcome == "" && len(d.errors) == 0 {
			continue
		}
		renderDigestTurn(w, d)
	}
	return nil
}

func renderDigestTurn(w io.Writer, d *digestTurn) {
	hdr := fmt.Sprintf("T%d", d.num)
	if d.state != "" {
		hdr += "  " + d.state
	}
	fmt.Fprintln(w, styleFor(hdr, colorTurn))
	if d.input != "" || d.intent != "" {
		route := d.routedBy
		if route == "" {
			route = "—"
		}
		if d.matchType != "" {
			route += " (" + d.matchType + ")"
		}
		in := d.input
		if in == "" {
			in = "[intent] " + d.intent
		}
		fmt.Fprintf(w, "  in     %-40s route=%s\n", truncate1(in, 40), route)
	}
	if d.ide != "" {
		fmt.Fprintf(w, "  ide    %s\n", d.ide)
	}
	for _, hc := range d.hostCalls {
		fmt.Fprintf(w, "  host   %s\n", hc)
	}
	for _, pr := range d.prompts {
		fmt.Fprintf(w, "  prompt %s\n", pr)
	}
	for _, rd := range d.redirects {
		fmt.Fprintf(w, "  %s\n", styleFor("on_error → "+rd, colorErr))
	}
	for _, e := range d.errors {
		fmt.Fprintf(w, "  %s\n", styleFor("ERROR "+e, colorErr))
	}
	out := d.outcome
	if d.newState != "" {
		out += " → " + d.newState
	}
	if strings.TrimSpace(out) != "" {
		fmt.Fprintf(w, "  out    %s\n", out)
	}
	fmt.Fprintln(w)
}

// fmtIDECapture renders an ide.context_captured payload as a one-liner.
func fmtIDECapture(p map[string]any) string {
	s := "source=" + str(p["source"])
	if f := str(p["file"]); f != "" {
		s += " file=" + f
	}
	if inj, ok := p["injected"].(bool); ok {
		s += fmt.Sprintf(" injected=%v", inj)
	}
	if r := str(p["reason"]); r != "" {
		s += " reason=" + r
	}
	return s
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncate1(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func appendUniq(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
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

--turns prints a compact per-turn DIGEST instead of the raw event stream: for
each turn it shows the operator input, which routing tier resolved it (and why),
the host calls fired, the PROMPT each oracle verb dispatched (the source of
truth for what the model actually saw), any editor context captured
(ide.context_captured), on_error redirects, errors, and the outcome. This is
the "what actually happened to my turn" view — use it first when a turn ran but
did the wrong thing (selection didn't reach the prompt, input mis-routed, …).

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
			if byTurn, _ := cmd.Flags().GetBool("turns"); byTurn {
				return digestTurns(r, cmd.OutOrStdout())
			}
			return prettyPrint(r, cmd.OutOrStdout())
		},
	}
	cmd.Flags().Bool("turns", false, "print a compact per-turn digest (input → route → prompt → outcome) instead of the raw event stream")

	cmd.AddCommand(traceToFlowCmd())
	return cmd
}

// traceToFlowCmd implements `kitsoki trace to-flow`: convert a recorded JSONL
// session trace into a replayable deterministic flow fixture (+ host cassette).
func traceToFlowCmd() *cobra.Command {
	var (
		outPath       string
		recordingPath string
		appPath       string
		appID         string
		initialState  string
	)

	cmd := &cobra.Command{
		Use:   "to-flow <trace.jsonl>",
		Short: "Convert a recorded session trace into a replayable flow fixture",
		Long: `Convert a recorded JSONL session trace into a deterministic flow fixture.

Each machine.transition in the trace becomes one flow turn (intent name +
resolved slots, verbatim, in order). Each recorded host.* call becomes one
host-cassette episode, in trace order, matched on handler — so per-call-varying
oracle/host responses (e.g. five distinct host.oracle.converse replies) replay
in sequence.

No expect_state / expect_world is emitted on the turns: a trace recorded against
an older version of a story may route differently against the current one;
strict expectations would hard-fail replay on the first divergence. The fixture
is a faithful re-drive of the recorded intents.

The flow is written to --out; the cassette (when the trace has host calls) is
written next to it (default <out-basename>.cassette.yaml) and referenced from
the fixture's host_cassette: field. Use --recording to override the cassette
path.

Replay the result with:
  kitsoki test flows <app.yaml> --flows <out> --trace-out <fresh-trace.jsonl>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tracePath := args[0]
			if outPath == "" {
				return fmt.Errorf("--out is required")
			}
			if appPath == "" {
				return fmt.Errorf("--app is required (written into the fixture's app: field)")
			}

			casPath := recordingPath
			if casPath == "" {
				casPath = strings.TrimSuffix(outPath, ".yaml") + ".cassette.yaml"
			}

			casRef := casPath
			if filepath.Dir(casPath) == filepath.Dir(outPath) {
				casRef = filepath.Base(casPath)
			}

			res, err := testrunner.ConvertTraceToFlow(tracePath, testrunner.ConvertOptions{
				AppPath:      appPath,
				CassettePath: casRef,
				AppID:        appID,
				InitialState: initialState,
			})
			if err != nil {
				return err
			}

			if err := os.WriteFile(outPath, res.FlowYAML, 0o644); err != nil {
				return fmt.Errorf("write flow %q: %w", outPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote flow fixture %s (%d turns)\n", outPath, res.NumTurns)

			if res.CassetteYAML != nil {
				if err := os.WriteFile(casPath, res.CassetteYAML, 0o644); err != nil {
					return fmt.Errorf("write cassette %q: %w", casPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote host cassette %s (%d episodes)\n", casPath, res.NumEpisodes)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "", "output path for the generated flow fixture (required)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "output path for the generated host cassette (default: <out>.cassette.yaml)")
	cmd.Flags().StringVar(&appPath, "app", "", "value for the fixture's app: field, e.g. ../app.yaml (required)")
	cmd.Flags().StringVar(&appID, "app-id", "", "value for the cassette's app_id: field (default: from-trace)")
	cmd.Flags().StringVar(&initialState, "initial-state", "", "override the derived initial state")

	return cmd
}

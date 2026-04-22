// trace.go — implements the `hally trace` subcommand (pretty-printer for JSONL
// trace files) and the trace-sink setup used by `hally run --trace`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// ─── Pretty-printer ───────────────────────────────────────────────────────────

// traceRecord is the minimum shape of a slog JSONL line.
type traceRecord struct {
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Msg     string         `json:"msg"`
	Session string         `json:"session_id"`
	Turn    int64          `json:"turn"`
	State   string         `json:"state_path"`
	Extra   map[string]any `json:"-"` // remaining fields
}

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
)

// prettyLine formats one trace record for human-readable output.
func prettyLine(rec traceRecord, extra map[string]any) string {
	var sb strings.Builder

	// Timestamp.
	ts := ""
	if rec.Time != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.Time); err == nil {
			ts = t.Format("15:04:05.000")
		} else {
			ts = rec.Time
		}
	}

	// Turn prefix.
	turnPrefix := ""
	if rec.Turn > 0 {
		turnPrefix = fmt.Sprintf("[T%d %s]", rec.Turn, ts)
	} else {
		turnPrefix = fmt.Sprintf("[   %s]", ts)
	}

	msg := rec.Msg

	// Route by msg prefix to pick color and indent level.
	switch {
	case strings.HasPrefix(msg, "turn."):
		line := styleFor(turnPrefix, colorTurn) + " " +
			styleFor(strings.ToUpper(strings.TrimPrefix(msg, "turn.")), colorTurn) +
			" " + formatKV(extra, rec.State, rec.Session)
		sb.WriteString(line)

	case strings.HasPrefix(msg, "harness."):
		sb.WriteString("  " + styleFor("HARNESS", colorHarness) +
			" " + strings.TrimPrefix(msg, "harness.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "machine."):
		sb.WriteString("  " + styleFor("MACHINE", colorMachine) +
			" " + strings.TrimPrefix(msg, "machine.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "store."):
		sb.WriteString("  " + styleFor("STORE", colorStore) +
			" " + strings.TrimPrefix(msg, "store.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "expr."):
		sb.WriteString("  " + styleFor("EXPR", colorErr) +
			" " + strings.TrimPrefix(msg, "expr.") +
			" " + formatKV(extra, "", ""))

	default:
		sb.WriteString(styleFor(turnPrefix, colorDim) + " " + msg +
			" " + formatKV(extra, "", ""))
	}

	return sb.String()
}

// formatKV formats extra key-value pairs as k=v k=v, omitting boring internal keys.
func formatKV(m map[string]any, statePath, sessionID string) string {
	skip := map[string]bool{
		"session_id": true,
		"state_path": true,
		"turn":       true,
		"time":       true,
		"level":      true,
		"msg":        true,
	}
	var parts []string

	// Emit state_path if present and interesting.
	if statePath != "" {
		parts = append(parts, styleFor("state="+statePath, colorDim))
	}

	for k, v := range m {
		if skip[k] {
			continue
		}
		vs := fmt.Sprintf("%v", v)
		// Redact API key patterns.
		if strings.Contains(strings.ToLower(k), "api_key") ||
			strings.Contains(strings.ToLower(k), "apikey") {
			vs = "[REDACTED]"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, vs))
	}
	return strings.Join(parts, " ")
}

// prettyPrint reads JSONL from r and writes human-readable trace to w.
func prettyPrint(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1MB line buffer

	var currentTurn int64
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// First decode into the known fields.
		var rec traceRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Print raw on parse error.
			fmt.Fprintf(w, "%s\n", line)
			continue
		}

		// Decode all fields into a generic map for extra KV.
		var all map[string]any
		_ = json.Unmarshal([]byte(line), &all)

		// Print blank line between turns.
		if rec.Turn > 0 && rec.Turn != currentTurn && strings.HasPrefix(rec.Msg, "turn.start") {
			if currentTurn > 0 {
				fmt.Fprintln(w)
			}
			currentTurn = rec.Turn
		}

		fmt.Fprintf(w, "%s\n", prettyLine(rec, all))
	}
	return scanner.Err()
}

// ─── CLI command ──────────────────────────────────────────────────────────────

func traceCmd() *cobra.Command {
	var (
		sessionID string
		dbPath    string
		level     string
	)

	cmd := &cobra.Command{
		Use:   "trace [path]",
		Short: "Pretty-print a JSONL trace file or reconstruct from stored events",
		Long: `Pretty-print a JSONL trace file produced by 'hally run --trace <path>'.

Examples:
  hally trace /tmp/cloak.jsonl
  hally trace --session <id> --db sessions.db   # reconstruct from store (limited)

If path is '-', reads from stdin.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Session reconstruction path.
			if sessionID != "" {
				return reconstructFromDB(sessionID, dbPath, cmd.OutOrStdout())
			}

			// File pretty-print path.
			if len(args) == 0 {
				return fmt.Errorf("provide a trace file path or --session <id>")
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
			_ = level // future: filter by level
			return prettyPrint(r, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "reconstruct trace from a stored session (requires --db)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite session database (for --session)")
	cmd.Flags().StringVar(&level, "level", "debug", "minimum level to show (debug|info|warn|error)")

	return cmd
}

// reconstructFromDB produces a limited trace summary from stored events.
// It lacks harness internals (prompt, raw response) — only machine-level events.
func reconstructFromDB(sessionID, dbPath string, w io.Writer) error {
	if dbPath == "" {
		dbPath = defaultDBPath()
	}

	fmt.Fprintf(w, "NOTE: Reconstruction from stored events is limited — harness internals\n")
	fmt.Fprintf(w, "      (prompt, raw response) are not available. Use --trace during a live\n")
	fmt.Fprintf(w, "      session to capture the full decision flow.\n\n")

	// Open the store.
	s, err := openStoreForTrace(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	return printSessionEvents(s, sessionID, w)
}

// ─── Trace sink setup for 'hally run' ────────────────────────────────────────

// TraceConfig holds the parsed trace flags.
type TraceConfig struct {
	// JSONLPath is the file to write JSONL trace to. "-" = stderr. "" = disabled.
	JSONLPath string
	// PrettyPath is the file to write human-readable trace to. "-" = stderr. "" = disabled.
	PrettyPath string
	// Level is the minimum slog level (default Debug when tracing is enabled).
	Level slog.Level
	// Redact enables redaction of sensitive values (default true).
	Redact bool
}

// BuildTraceLogger constructs a *slog.Logger for the given TraceConfig.
// Returns slog.Default() (no-op at ERROR level) when no trace is configured.
// The caller must call the returned cleanup function when done.
func BuildTraceLogger(cfg TraceConfig) (l *slog.Logger, cleanup func(), err error) {
	var closers []func()
	cleanup = func() {
		for _, c := range closers {
			c()
		}
	}

	if cfg.JSONLPath == "" && cfg.PrettyPath == "" {
		return slog.Default(), cleanup, nil
	}

	var handlers []slog.Handler

	// JSONL sink.
	if cfg.JSONLPath != "" {
		w, closer, err := openSink(cfg.JSONLPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("trace: open JSONL sink %q: %w", cfg.JSONLPath, err)
		}
		closers = append(closers, closer)
		// Use a line-buffered writer so each record is flushed atomically.
		bw := bufio.NewWriter(w)
		closers = append(closers, func() { _ = bw.Flush() })
		jsonHandler := slog.NewJSONHandler(bw, &slog.HandlerOptions{
			Level:     cfg.Level,
			AddSource: false,
		})
		handlers = append(handlers, jsonHandler)
	}

	// Pretty-print sink.
	if cfg.PrettyPath != "" {
		w, closer, err := openSink(cfg.PrettyPath)
		if err != nil {
			return nil, cleanup, fmt.Errorf("trace: open pretty sink %q: %w", cfg.PrettyPath, err)
		}
		closers = append(closers, closer)
		bw := bufio.NewWriter(w)
		closers = append(closers, func() { _ = bw.Flush() })
		handlers = append(handlers, &prettyHandler{w: bw, level: cfg.Level, redact: cfg.Redact})
	}

	if len(handlers) == 0 {
		return slog.Default(), cleanup, nil
	}

	if len(handlers) == 1 {
		return slog.New(handlers[0]), cleanup, nil
	}

	return slog.New(&multiHandler{handlers: handlers}), cleanup, nil
}

// openSink opens a write sink. "-" → os.Stderr; anything else → file.
func openSink(path string) (io.Writer, func(), error) {
	if path == "-" {
		return os.Stderr, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}

// ─── multiHandler fans out to multiple slog.Handler instances ─────────────────

type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: hs}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: hs}
}

// ─── prettyHandler ────────────────────────────────────────────────────────────

type prettyHandler struct {
	w        io.Writer
	level    slog.Level
	redact   bool
	preAttrs []slog.Attr
}

func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	rec := traceRecord{
		Time:  r.Time.Format(time.RFC3339Nano),
		Level: r.Level.String(),
		Msg:   r.Message,
	}
	extra := make(map[string]any)

	// Collect pre-set attrs first.
	for _, a := range h.preAttrs {
		switch a.Key {
		case "session_id":
			rec.Session = a.Value.String()
		case "turn":
			rec.Turn = a.Value.Int64()
		case "state_path":
			rec.State = a.Value.String()
		}
		extra[a.Key] = a.Value.Any()
	}

	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "session_id":
			rec.Session = a.Value.String()
		case "turn":
			rec.Turn = a.Value.Int64()
		case "state_path":
			rec.State = a.Value.String()
		}
		extra[a.Key] = a.Value.Any()
		return true
	})

	if h.redact {
		for k := range extra {
			if strings.Contains(strings.ToLower(k), "api_key") {
				extra[k] = "[REDACTED]"
			}
		}
	}

	line := prettyLine(rec, extra)
	_, err := fmt.Fprintln(h.w, line)
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.preAttrs)+len(attrs))
	copy(newAttrs, h.preAttrs)
	copy(newAttrs[len(h.preAttrs):], attrs)
	return &prettyHandler{w: h.w, level: h.level, redact: h.redact, preAttrs: newAttrs}
}

func (h *prettyHandler) WithGroup(_ string) slog.Handler { return h }

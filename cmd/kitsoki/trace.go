// trace.go — implements the `kitsoki trace` subcommand (pretty-printer for JSONL
// trace files) and the trace-sink setup used by `kitsoki run --trace`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"kitsoki/internal/trace"
)

// ringBufferCap is the always-on in-memory ring buffer size used by
// BuildTraceLogger. 5000 events keeps the memory footprint bounded
// (a few MB on a long session) while still capturing the recent past
// the meta-mode agent reads via its trace file.
const ringBufferCap = 5000

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
	colorOffPath = lipgloss.Color("214") // amber — matches off-path UX framing
	colorTimeout = lipgloss.Color("51")  // bright cyan-blue — quietly distinct from STORE
	colorTele    = lipgloss.Color("135") // violet — "you jumped sideways"
	colorJob     = lipgloss.Color("33")  // mid blue — background job lifecycle
	colorSlot    = lipgloss.Color("220") // soft yellow — paired with MACHINE-yellow visually
	colorInbox   = lipgloss.Color("245") // mid gray — notifications are low-key
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

	// Warn / error records (from slog.Warn / slog.Error reached via the
	// SetDefault bridge in runCmd) take priority over msg-prefix routing
	// so they pop out of the debug firehose.
	switch rec.Level {
	case "WARN":
		return styleFor(turnPrefix, colorErr) + " " +
			styleFor("WARN", colorErr) + " " + msg +
			" " + formatKV(extra, rec.State, rec.Session)
	case "ERROR":
		return styleFor(turnPrefix, colorErr) + " " +
			styleFor("ERROR", colorErr) + " " + msg +
			" " + formatKV(extra, rec.State, rec.Session)
	}

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

	case strings.HasPrefix(msg, "offpath."):
		sb.WriteString("  " + styleFor("OFFPATH", colorOffPath) +
			" " + strings.TrimPrefix(msg, "offpath.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "timeout."):
		sb.WriteString("  " + styleFor("TIMEOUT", colorTimeout) +
			" " + strings.TrimPrefix(msg, "timeout.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "teleport."):
		sb.WriteString("  " + styleFor("TELEPORT", colorTele) +
			" " + strings.TrimPrefix(msg, "teleport.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "job."):
		sb.WriteString("  " + styleFor("JOB", colorJob) +
			" " + strings.TrimPrefix(msg, "job.") +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "slotfill."), strings.HasPrefix(msg, "disambig."):
		// Group slot-fill and disambiguation under SLOTFILL — both are the
		// "I need more info before I can route" family.
		label := "SLOTFILL"
		trimmed := msg
		if strings.HasPrefix(msg, "slotfill.") {
			trimmed = strings.TrimPrefix(msg, "slotfill.")
		} else {
			label = "DISAMBIG"
			trimmed = strings.TrimPrefix(msg, "disambig.")
		}
		sb.WriteString("  " + styleFor(label, colorSlot) +
			" " + trimmed +
			" " + formatKV(extra, "", ""))

	case strings.HasPrefix(msg, "inbox."):
		sb.WriteString("  " + styleFor("INBOX", colorInbox) +
			" " + strings.TrimPrefix(msg, "inbox.") +
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
		Long: `Pretty-print a JSONL trace file produced by 'kitsoki run --trace <path>'.

Examples:
  kitsoki trace /tmp/cloak.jsonl
  kitsoki trace --session <id> --db sessions.db   # reconstruct from store (limited)

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

// ─── Trace sink setup for 'kitsoki run' ────────────────────────────────────────

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
//
// The returned *trace.RingBuffer is ALWAYS non-nil: even when no
// --trace flag is set, the engine captures recent activity into an
// in-memory ring (ringBufferCap events, debug level) so the meta-mode
// TUI can dump it to a temp file for the story-author agent to Read.
// File sinks (JSONL, pretty) remain opt-in via TraceConfig.
//
// The caller must call the returned cleanup function when done.
func BuildTraceLogger(cfg TraceConfig) (l *slog.Logger, ring *trace.RingBuffer, cleanup func(), err error) {
	var closers []func()
	// Cleanup runs closers in reverse of registration so bufio flushers
	// (appended after their underlying file closers) run before the files
	// are closed. Without this, buffered trace data is silently dropped
	// when the flush writes to an already-closed fd.
	cleanup = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	// Always-on ring buffer. The ring is itself a slog.Handler so it
	// joins the multiHandler set alongside any file sinks. Debug
	// level so it captures everything the file sinks could capture.
	ring = trace.NewRingBuffer(ringBufferCap)
	handlers := []slog.Handler{ring}

	// JSONL sink. We write directly to the underlying file (no bufio wrapper)
	// so each event lands on disk as soon as slog calls Write — supporting
	// `tail -f` of the trace file in real time. slog.JSONHandler emits one
	// Write per record so there is no atomicity concern.
	if cfg.JSONLPath != "" {
		w, closer, err := openSink(cfg.JSONLPath)
		if err != nil {
			return nil, nil, cleanup, fmt.Errorf("trace: open JSONL sink %q: %w", cfg.JSONLPath, err)
		}
		closers = append(closers, closer)
		jsonHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:     cfg.Level,
			AddSource: false,
		})
		handlers = append(handlers, jsonHandler)
	}

	// Pretty-print sink. Same reasoning — write straight through so the
	// pretty log is followable live.
	if cfg.PrettyPath != "" {
		w, closer, err := openSink(cfg.PrettyPath)
		if err != nil {
			return nil, nil, cleanup, fmt.Errorf("trace: open pretty sink %q: %w", cfg.PrettyPath, err)
		}
		closers = append(closers, closer)
		handlers = append(handlers, &prettyHandler{w: w, level: cfg.Level, redact: cfg.Redact})
	}

	if len(handlers) == 1 {
		return slog.New(handlers[0]), ring, cleanup, nil
	}

	return slog.New(&multiHandler{handlers: handlers}), ring, cleanup, nil
}

// defaultSessionTracePath returns the JSONL trace path used when the
// operator did not pass `--trace`. We walk upward from cwd looking for an
// existing `.kitsoki/` directory (or `.kitsoki-root` marker, the same
// signal `internal/app/imports.go` uses to identify a repo root). If none
// is found we anchor at cwd and create `.kitsoki/sessions/` there. The
// trace lives at
// `<anchor>/.kitsoki/sessions/<RFC3339-timestamp>-<app-id>.jsonl`.
// Returns "" if cwd lookup fails — caller treats that as "no default".
func defaultSessionTracePath(appID string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	anchor := cwd
	cur := cwd
	for {
		if fi, statErr := os.Stat(filepath.Join(cur, ".kitsoki")); statErr == nil && fi.IsDir() {
			anchor = cur
			break
		}
		if _, statErr := os.Stat(filepath.Join(cur, ".kitsoki-root")); statErr == nil {
			anchor = cur
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// No existing anchor found anywhere above us — create one
			// at cwd so the operator gets a stable, discoverable place
			// to look for traces. openSink would MkdirAll the sessions
			// subdir anyway, but doing it here means the bare
			// `.kitsoki/` is present even before the first event lands.
			if mkErr := os.MkdirAll(filepath.Join(cwd, ".kitsoki", "sessions"), 0o755); mkErr != nil {
				return ""
			}
			anchor = cwd
			break
		}
		cur = parent
	}
	safeApp := appID
	if safeApp == "" {
		safeApp = "session"
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	return filepath.Join(anchor, ".kitsoki", "sessions", stamp+"-"+safeApp+".jsonl")
}

// openSink opens a write sink. "-" → os.Stderr; anything else → file.
// Creates any missing parent directories so callers can pass a path
// like /tmp/kitsoki-traces/foo.jsonl without pre-creating the dir.
func openSink(path string) (io.Writer, func(), error) {
	if path == "-" {
		return os.Stderr, func() {}, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, func() {}, fmt.Errorf("mkdir %q: %w", dir, err)
		}
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
	var firstErr error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("slog handler panicked: %v", rec)
					}
				}
			}()
			if err := h.Handle(ctx, r); err != nil && firstErr == nil {
				firstErr = err
			}
		}()
	}
	return firstErr
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

// Command hally is the CLI entrypoint for the Hally deterministic LLM orchestrator.
// Subcommands: run, viz, trace, replay, test, serve (§9a, §12).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/host"
	hallymcp "hally/internal/mcp"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
	"hally/internal/tui"
	"hally/internal/viz"
)

const version = "0.0.1-scaffold"

func main() {
	root := &cobra.Command{
		Use:   "hally",
		Short: "Hally — deterministic LLM orchestrator",
		Long: `Hally lets a human drive a structured application with free-text input.
The LLM translates natural language into a finite alphabet of intents defined
by the application; the state machine decides what happens next.

Embedded documentation (ships inside this binary):
  hally docs             list available topics
  hally docs llm-guide   condensed manual for an LLM driving hally
  hally docs app-schema  authoritative reference for app.yaml
  hally docs all         print every topic, concatenated

See also the full design document (design.md) in the repo.`,
	}

	root.AddCommand(versionCmd())
	root.AddCommand(runCmd())
	root.AddCommand(vizCmd())
	root.AddCommand(traceCmd())
	root.AddCommand(replayCmd())
	root.AddCommand(testCmd())
	root.AddCommand(serveCmd())
	root.AddCommand(docsCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hally version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("hally %s\n", version)
		},
	}
}

func runCmd() *cobra.Command {
	var (
		harnessType    string
		claudeModel    string
		oraclePath     string
		recordPath     string
		dbPath         string
		tracePath      string
		tracePretty    string
		traceLevel     string
		traceRedact    bool
	)

	cmd := &cobra.Command{
		Use:   "run <app.yaml>",
		Short: "Start an interactive session for an app (TUI)",
		Long: `Load an app definition and open an interactive TUI session. The user
types free text; an LLM harness maps it to one of the app's intents; the
state machine applies the transition; the view is re-rendered.

Harness auto-selection (when --harness is omitted):
  1. 'claude' binary on PATH       → claude harness (no API key needed)
  2. ANTHROPIC_API_KEY set         → live harness (direct SDK)
  3. otherwise                     → replay (requires --oracle)

Examples:
  hally run testdata/apps/cloak/app.yaml
  hally run myapp.yaml --harness claude --claude-model opus
  hally run myapp.yaml --harness replay --oracle oracle.yaml
  hally run myapp.yaml --harness recording --record /tmp/rec.jsonl
  hally run myapp.yaml --trace /tmp/t.jsonl --trace-pretty -

See 'hally docs llm-guide' for the full operator guide.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// Load app definition.
			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}

			// Determine DB path.
			if dbPath == "" {
				dbPath = defaultDBPath()
			}

			// Open store.
			if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
				return fmt.Errorf("create db directory: %w", err)
			}
			s, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = s.Close() }()

			// Build trace logger.
			var level slog.Level
			switch traceLevel {
			case "debug", "":
				level = slog.LevelDebug
			case "info":
				level = slog.LevelInfo
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			default:
				return fmt.Errorf("unknown --trace-level %q (use debug|info|warn|error)", traceLevel)
			}

			traceCfg := TraceConfig{
				JSONLPath:  tracePath,
				PrettyPath: tracePretty,
				Level:      level,
				Redact:     traceRedact,
			}
			logger, traceCleanup, err := BuildTraceLogger(traceCfg)
			if err != nil {
				return fmt.Errorf("build trace logger: %w", err)
			}
			defer traceCleanup()

			// Redirect the package-level slog sink through the trace logger
			// so slog.Warn / slog.Error from deep in the harness stack
			// (e.g. retry-after-parse-failure in claude_cli.go) reach the
			// --trace file rather than stderr, which the alt-screen TUI
			// swallows. This is a no-op when tracing is disabled because
			// BuildTraceLogger returns slog.Default() in that case.
			prevDefault := slog.Default()
			slog.SetDefault(logger)
			defer slog.SetDefault(prevDefault)

			// Build machine.
			m, err := machine.New(def, machine.WithMachineLogger(logger))
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}

			// Build harness.
			h, err := buildHarness(harnessType, claudeModel, oraclePath, recordPath, def)
			if err != nil {
				return fmt.Errorf("build harness: %w", err)
			}
			defer func() { _ = h.Close() }()
			// Wire logger into harness.
			setHarnessLogger(h, logger)

			// Build host registry (built-in handlers + allow-list check).
			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)
			if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
				return fmt.Errorf("validate hosts: %w", err)
			}

			// Build orchestrator.
			orch := orchestrator.New(def, m, s, h,
				orchestrator.WithLogger(logger),
				orchestrator.WithHostRegistry(hostReg),
			)

			// Create a new session.
			ctx := context.Background()
			sid, err := orch.NewSession(ctx)
			if err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			// Get initial view.
			w := orch.InitialWorld()
			initialView, err := orch.InitialView(w)
			if err != nil {
				return fmt.Errorf("initial view: %w", err)
			}

			// Launch TUI.
			rootModel := tui.NewRootModel(orch, sid, initialView)
			p := tea.NewProgram(rootModel, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}

	cmd.Flags().StringVar(&harnessType, "harness", "",
		"harness type: claude|live|replay|recording (default: claude if `claude` binary on PATH, else live if ANTHROPIC_API_KEY set, else replay)")
	cmd.Flags().StringVar(&claudeModel, "claude-model", "",
		fmt.Sprintf("model passed to claude -p --model (default: %s); use 'opus' for higher quality at higher cost", harness.DefaultClaudeModel))
	cmd.Flags().StringVar(&oraclePath, "oracle", "",
		"path to oracle YAML file (required for --harness replay)")
	cmd.Flags().StringVar(&recordPath, "record", "",
		"path to output JSONL recording (for --harness recording)")
	cmd.Flags().StringVar(&dbPath, "db", "",
		"path to SQLite session database (default: $XDG_DATA_HOME/hally/sessions.db)")
	cmd.Flags().StringVar(&tracePath, "trace", "",
		"write JSONL trace events to this file; '-' writes to stderr")
	cmd.Flags().StringVar(&tracePretty, "trace-pretty", "",
		"write human-readable trace to this file in parallel; '-' writes to stderr")
	cmd.Flags().StringVar(&traceLevel, "trace-level", "debug",
		"minimum trace level: debug|info|warn|error (default: debug when --trace is set)")
	cmd.Flags().BoolVar(&traceRedact, "trace-redact", true,
		"redact sensitive values (API keys, etc.) in trace output")

	return cmd
}

// setHarnessLogger wires the logger into harness implementations that support it.
func setHarnessLogger(h harness.Harness, l *slog.Logger) {
	type withLogger interface{ WithLogger(*slog.Logger) }
	if wl, ok := h.(withLogger); ok {
		wl.WithLogger(l)
	}
}

// autoSelectHarness returns the harness type to use when --harness is not explicitly set.
//
// Precedence:
//  1. `claude` binary on PATH → use ClaudeCLIHarness (no API key needed).
//  2. ANTHROPIC_API_KEY set   → use LiveHarness (direct SDK).
//  3. Otherwise               → use "replay" (requires --oracle) or error.
func autoSelectHarness() string {
	if _, err := exec.LookPath("claude"); err == nil {
		return "claude"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "live"
	}
	// Fall back to replay; the caller will error if --oracle is not set.
	return "replay"
}

// buildHarness constructs the appropriate harness based on the harness type flag.
// If harnessType is empty, autoSelectHarness() is called to pick one.
// claudeModel is the model name for the ClaudeCLIHarness; pass "" to use the default.
func buildHarness(harnessType, claudeModel, oraclePath, recordPath string, def *app.AppDef) (harness.Harness, error) {
	if harnessType == "" {
		harnessType = autoSelectHarness()
	}

	switch harnessType {
	case "claude":
		return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{Model: claudeModel})

	case "replay":
		if oraclePath == "" {
			return nil, fmt.Errorf("--oracle is required when --harness replay is set")
		}
		return harness.NewReplay(oraclePath)

	case "live":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required for --harness live")
		}
		client := anthropic.NewClient()
		return harness.NewLive(&client, "", def)

	case "recording":
		if oraclePath != "" {
			// Wrap replay with recording.
			replay, err := harness.NewReplay(oraclePath)
			if err != nil {
				return nil, fmt.Errorf("replay harness for recording: %w", err)
			}
			if recordPath == "" {
				recordPath = "recording.jsonl"
			}
			return harness.NewRecording(replay, recordPath)
		}
		// Wrap live with recording.
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for recording mode without oracle")
		}
		client := anthropic.NewClient()
		live, err := harness.NewLive(&client, "", def)
		if err != nil {
			return nil, err
		}
		if recordPath == "" {
			recordPath = "recording.jsonl"
		}
		return harness.NewRecording(live, recordPath)

	default:
		return nil, fmt.Errorf("unknown harness type %q (use claude|live|replay|recording)", harnessType)
	}
}

// defaultDBPath returns the default SQLite database path.
func defaultDBPath() string {
	// Use $XDG_DATA_HOME/hally/sessions.db or ~/.local/share/hally/sessions.db.
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "hally", "sessions.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "hally-sessions.db")
	}
	return filepath.Join(home, ".local", "share", "hally", "sessions.db")
}

func vizCmd() *cobra.Command {
	var (
		outPath string
		doDot   bool
	)

	cmd := &cobra.Command{
		Use:   "viz <app.yaml>",
		Short: "Emit a Graphviz DOT graph for an app",
		Long: `Emit a Graphviz DOT graph for the given app definition. Useful for
getting a visual overview of a state machine before authoring/debugging.

The output is written to <appname>-viz.dot by default, or to --out.
Render it with: dot -Tpng <appname>-viz.dot -o graph.png

Examples:
  hally viz testdata/apps/cloak/app.yaml
  hally viz myapp.yaml --out /tmp/g.dot && dot -Tsvg /tmp/g.dot -o /tmp/g.svg`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}

			if outPath == "" {
				outPath = def.App.ID + "-viz.dot"
			}

			f, err := os.Create(outPath)
			if err != nil {
				return fmt.Errorf("create %q: %w", outPath, err)
			}
			defer func() { _ = f.Close() }()

			if err := viz.Export(def, f); err != nil {
				return fmt.Errorf("export DOT: %w", err)
			}

			fmt.Printf("wrote %s\n", outPath)
			fmt.Printf("render: dot -Tpng %s -o graph.png\n", outPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "", "output file (default: <appid>-viz.dot)")
	cmd.Flags().BoolVar(&doDot, "dot", true, "emit Graphviz DOT (default and only format)")
	return cmd
}

// traceCmd is defined in trace.go.

func replayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "replay <session>",
		Short: "Replay a session's event log and diff against snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(stage-4): use internal/trace.Replayer.
			return fmt.Errorf("not implemented")
		},
	}
}

func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run Mode 1 and Mode 2 tests for an app",
		Long: `Test sub-commands:
  hally test flows   <app.yaml>   — Mode 2: deterministic flow tests (no LLM)
  hally test intents <app.yaml>   — Mode 1: intent pass-rate tests

Fixture layout (defaults):
  <app-dir>/flows/*.yaml      — flow fixtures (run under 'test flows')
  <app-dir>/intents/*.yaml    — intent fixtures (run under 'test intents')
  <app-dir>/oracle.yaml       — oracle YAML (seeds replay/static harness)

See 'hally docs llm-guide' §7 for fixture shape.`,
	}
	cmd.AddCommand(testFlowsCmd())
	cmd.AddCommand(testIntentsCmd())
	return cmd
}

// serveCmd starts the hally MCP server on stdio for a given app.
// Usage: hally serve <app.yaml> [--db <path>]
//
// The server exposes the single `transition` tool to any MCP client
// (Claude Desktop, Claude Code, etc.) that connects via stdio.
//
// Example (smoke test via shell):
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}' | hally serve cloak.yaml
func serveCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "serve <app.yaml>",
		Short: "Start the MCP server on stdio for an app",
		Long: `Start the hally MCP server on stdin/stdout. External MCP clients
(Claude Desktop, Claude Code) can connect and drive the app via the
single 'transition' tool.

The server reads MCP JSON-RPC messages from stdin and writes responses
to stdout. It blocks until stdin is closed.

The 'transition' tool accepts:
  { intent: <string>, slots: <object?>, confidence: <float?>, session_id: <string> }

and returns either:
  { ok: true,  state: <path>, view: <string>, menu: [<intent>,...], world: <obj> }
or:
  { ok: false, error: { code: <string>, message: <string>, ... } }

Without --db, sessions are in-memory and lost on exit.

See 'hally docs llm-guide' for the full operator guide.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// Load the app definition.
			def, err := app.Load(appPath)
			if err != nil {
				return fmt.Errorf("load app %q: %w", appPath, err)
			}

			// Open the session store.
			var s store.Store
			if dbPath == "" {
				// Default: in-memory (ephemeral session for this serve invocation).
				s, err = store.OpenMemory()
			} else {
				s, err = store.Open(dbPath)
			}
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = s.Close() }()

			// Build the machine.
			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine for %q: %w", def.App.ID, err)
			}

			// Construct the MCP server.
			srv := hallymcp.NewServer(m, s, def)

			// Run until stdin closes or signal received.
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			fmt.Fprintf(os.Stderr, "hally: serving app %q via MCP stdio\n", def.App.ID)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite session database (default: in-memory)")
	return cmd
}

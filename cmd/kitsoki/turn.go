// turn.go — implements the `kitsoki turn` subcommand.
//
// Two modes:
//
//  1. Stateless one-shot (original behaviour): provide <app.yaml> as a
//     positional argument with --state, --intent (or --input), and optional
//     --world / --slots.  Nothing is persisted.
//
//  2. Trace-backed persistent turn (wave 3-entry): provide --app <path>,
//     --trace <path>, and --intent (plus optional --slot k=v repeatable flag).
//     The trace file is created if absent (header + events for turn 1) or
//     loaded and resumed if present.  New events appended during the turn are
//     written to the trace file AND echoed to stdout as JSONL so drivers can
//     stream without re-reading the file.
//
//     Exit codes for the trace path:
//     0  — intent accepted / transitioned
//     1  — intent rejected
//     2  — terminal state (story finished)
//     >2 — infrastructure error (returned via cobra, becomes os.Exit(1))
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// turnExitRejected is the exit code when an intent is rejected by the machine.
const turnExitRejected = 1

// turnExitTerminal is the exit code when the session reaches a terminal state.
const turnExitTerminal = 2

// turnExitInfraError is the exit code for infrastructure errors (missing app
// file, malformed slot, network failure, etc.) — distinct from a semantic
// rejection (exit 1) so drivers can distinguish "bad call" from "bad input".
// Finding 2.2/2.4: previously these collapsed to exit 1, conflating infra
// errors with intent rejections.
const turnExitInfraError = 3

// turnExitError is a sentinel that carries an exit code and optional message
// through cobra's error return without triggering cobra's "Error:" banner
// (handled by SilenceErrors on the command and by main()).
type turnExitError struct {
	code int
	msg  string
}

func (e turnExitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	switch e.code {
	case turnExitRejected:
		return "intent rejected"
	case turnExitTerminal:
		return "terminal state reached"
	case turnExitInfraError:
		return "infrastructure error"
	default:
		return fmt.Sprintf("turn exit %d", e.code)
	}
}

// IsTurnExitError reports whether err is a turn-exit sentinel and, if so,
// returns the exit code.  Used by main() to translate exit codes without
// printing an "error: …" line for semantic outcomes (exit 0–2).
// For exit 3 (infra error) main() prints the message to stderr before exiting.
//
// Exit code semantics:
//
//	0 — accepted / transitioned
//	1 — intent rejected by the machine (bad intent name, guard failed, etc.)
//	2 — session reached a terminal state
//	3 — infrastructure error (missing app file, malformed slot, etc.)
func IsTurnExitError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if te, ok := err.(turnExitError); ok { //nolint:errorlint
		return te.code, true
	}
	return 0, false
}

// infraError wraps a format string into a turnExitError with exit code 3.
// All infra failures in runTraceTurn use this helper so drivers can distinguish
// bad-call errors from intent rejections.
func infraError(format string, args ...any) error {
	return turnExitError{code: turnExitInfraError, msg: fmt.Sprintf(format, args...)}
}

func turnCmd() *cobra.Command {
	var (
		// Stateless one-shot flags (original path: positional app.yaml + --state)
		state         string
		worldFlag     string
		slotsFlag     string
		inputText     string
		harnessType   string
		recordingPath string

		// Shared: intent (used by both paths)
		intentName string

		// Trace-backed persistent turn flags (new path: --app + --trace)
		appFlag   string
		traceFlag string
		slotPairs []string // --slot k=v, repeatable
	)

	cmd := &cobra.Command{
		Use:           "turn [<app.yaml>]",
		Short:         "Run one turn (stateless probe or trace-backed persistent)",
		SilenceErrors: true, // finding 2.4: suppress cobra "Error: …" banner so stdout stays clean JSONL
		SilenceUsage:  true, // finding 2.4: suppress usage print on error (noise for scripted drivers)
		Long: `Two modes:

Stateless probe (original):
  kitsoki turn <app.yaml> --state <path> --intent <name> [--slots JSON] [--world JSON]
  kitsoki turn <app.yaml> --state <path> --input "free text" [--harness replay|claude|live]

  Runs a single stateless turn without persisting anything.  Outputs a JSON
  document describing the state transition, world diff, and view.

Trace-backed persistent turn (wave 3-entry):
  kitsoki turn --app <path> --trace <path> --intent <name> [--slot k=v ...]

  Loads an existing trace (or creates a fresh one), runs one turn, appends
  the new events to the trace file, and writes them to stdout as JSONL.

  Exit codes:
    0  accepted / transitioned
    1  rejected
    2  terminal state
    >2 infrastructure error

Examples:
  kitsoki turn app.yaml --state foyer --intent go_west
  kitsoki turn app.yaml --state foyer --intent open --slots '{"door":"red"}'
  kitsoki turn --app stories/cloak/app.yaml --trace /tmp/cloak.jsonl --intent go --slot direction=west`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Route to the appropriate mode.
			if traceFlag != "" || appFlag != "" {
				return runTraceTurn(cmd, appFlag, traceFlag, intentName, slotPairs)
			}

			// Stateless probe: positional app.yaml + --state required.
			if len(args) == 0 {
				return fmt.Errorf("provide <app.yaml> as a positional argument or use --app with --trace")
			}
			appPath := args[0]

			if (intentName == "") == (inputText == "") {
				return fmt.Errorf("exactly one of --intent or --input must be set")
			}
			if state == "" {
				return fmt.Errorf("--state is required")
			}

			worldVars, err := decodeJSONFlag(worldFlag, "world")
			if err != nil {
				return err
			}
			slotVals, err := decodeJSONFlag(slotsFlag, "slots")
			if err != nil {
				return err
			}

			// loadAppWithEnv publishes KITSOKI_APP_DIR before Load so
			// the loader's env-var validator can resolve references in
			// env-expanded fields (cwd, etc.) during validation.
			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			// Default world: schema defaults, then user overrides.
			defaultWorld := machine.WorldFromSchema(def.World)
			merged := make(map[string]any, len(defaultWorld.Vars)+len(worldVars))
			for k, v := range defaultWorld.Vars {
				merged[k] = v
			}
			for k, v := range worldVars {
				merged[k] = v
			}

			m, err := machine.New(def)
			if err != nil {
				return fmt.Errorf("build machine: %w", err)
			}

			// In-memory store: required by orchestrator.New, never written.
			s, err := store.OpenMemory()
			if err != nil {
				return fmt.Errorf("open in-memory store: %w", err)
			}
			defer func() { _ = s.Close() }()

			resolvedHarnessType := harnessType
			if resolvedHarnessType == "" && intentName == "" {
				var selectErr error
				resolvedHarnessType, selectErr = autoSelectHarness("")
				if selectErr != nil {
					return selectErr
				}
			}

			h, err := buildTurnHarness(resolvedHarnessType, recordingPath, def, intentName != "")
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)
			host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
			if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
				return fmt.Errorf("validate hosts: %w", err)
			}

			// Wire the chats store on the same in-memory DB so app YAMLs
			// that invoke host.chat.* can be exercised via `kitsoki turn`.
			// The DB is discarded on return — every turn starts empty.
			chatStore, err := chats.NewStore(s.DB())
			if err != nil {
				return fmt.Errorf("init chats store: %w", err)
			}
			chatAdapter := chathost.NewAdapter(chatStore)

			// Build the agent plugin registry from the app's agent_plugins
			// declarations. Wiring this lets rooms use external agent transports
			// (subprocess, mcp_http) via the agent: field on effects.
			agentReg, agentRegErr := agent.BuildRegistryFromDef(def, h)
			if agentRegErr != nil {
				return fmt.Errorf("build agent registry: %w", agentRegErr)
			}
			defer func() { _ = agentReg.Close() }()

			orchOpts := []orchestrator.Option{
				orchestrator.WithHostRegistry(hostReg),
				orchestrator.WithChatStore(chatAdapter),
				orchestrator.WithAgentRegistry(agentReg),
			}
			orchOpts = append(orchOpts, turnSemanticRoutingOptions(resolvedHarnessType, recordingPath, intentName != "")...)
			orch := orchestrator.New(def, m, s, h, orchOpts...)

			result, err := orch.OneShot(cmd.Context(), orchestrator.OneShotInput{
				State:  app.StatePath(state),
				World:  merged,
				Intent: intentName,
				Slots:  slotVals,
				Input:  inputText,
			})
			if err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(turnOutputView(result))
		},
	}

	// Stateless probe flags.
	cmd.Flags().StringVar(&state, "state", "", "starting state path (stateless probe only, required without --trace)")
	cmd.Flags().StringVar(&worldFlag, "world", "", `world overrides as JSON or @file (stateless probe only)`)
	cmd.Flags().StringVar(&slotsFlag, "slots", "", `intent slots as JSON or @file (stateless probe only; use --slot for the trace path)`)
	cmd.Flags().StringVar(&inputText, "input", "", "free-text input routed through the harness (stateless probe only)")
	cmd.Flags().StringVar(&harnessType, "harness", "", "harness type for --input: claude|live|replay (default auto)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "recording YAML for --harness replay; omit to fail closed if routing reaches the harness")

	// Shared.
	cmd.Flags().StringVar(&intentName, "intent", "", "intent name to invoke directly (both modes)")

	// Trace-backed persistent turn flags.
	cmd.Flags().StringVar(&appFlag, "app", "", "path to app.yaml (trace-backed turn; omit with --trace to reconstruct the story from the trace itself; use positional arg for stateless probe)")
	cmd.Flags().StringVar(&traceFlag, "trace", "", "JSONL trace file (trace-backed turn; required with --app)")
	cmd.Flags().StringArrayVar(&slotPairs, "slot", nil, "slot key=value (repeatable; trace-backed turn)")

	return cmd
}

// runTraceTurn implements the trace-backed persistent turn path.
//
// It opens or creates the JSONL trace, builds a JSONL-backed orchestrator,
// runs one direct-intent turn, and echoes the new events to stdout.
// Exit codes are signalled via turnExitError.
//
// Exit code contract (finding 2.2/2.4):
//
//	0 — intent accepted
//	1 — intent rejected (machine-semantic: wrong state, guard failed, etc.)
//	2 — terminal state reached
//	3 — infrastructure error (missing app file, malformed slot, open failure, …)
func runTraceTurn(cmd *cobra.Command, appPath, tracePath, intentName string, slotPairs []string) error {
	if tracePath == "" {
		return infraError("--trace is required (use --app with --trace)")
	}
	if intentName == "" {
		return infraError("--intent is required for the trace-backed turn path")
	}

	// Parse --slot k=v pairs.
	slots := make(map[string]any, len(slotPairs))
	for _, pair := range slotPairs {
		idx := strings.Index(pair, "=")
		if idx <= 0 {
			return infraError("--slot %q: must be in key=value form", pair)
		}
		slots[pair[:idx]] = pair[idx+1:]
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Shared trace+story+session bootstrap (also used by `kitsoki drive`).
	// The direct-intent path never routes free text, so a noRunHarness is
	// wired in: any harness invocation is a bug and errors loudly.
	ts, err := setupTraceSession(ctx, appPath, tracePath, &noRunHarness{})
	if err != nil {
		return err
	}
	defer ts.Close()

	// Capture the event count after the snapshot so we echo only the turn's
	// events, not the (large) story snapshot.
	histBefore := len(ts.Sink.History())

	// Run one direct-intent turn against the JSONL-backed session.
	outcome, err := ts.Orch.SubmitDirect(ctx, ts.SID, intentName, slots)
	if err != nil {
		return infraError("submit direct: %v", err)
	}

	// Echo newly appended events to stdout as JSONL.
	histAfter := ts.Sink.History()
	enc := json.NewEncoder(cmd.OutOrStdout())
	for _, ev := range histAfter[histBefore:] {
		if encErr := enc.Encode(ev); encErr != nil {
			return fmt.Errorf("encode event: %w", encErr)
		}
	}

	// Determine exit code based on outcome mode.
	switch outcome.Mode {
	case orchestrator.ModeRejected:
		return turnExitError{code: turnExitRejected}
	case orchestrator.ModeCompleted:
		return turnExitError{code: turnExitTerminal}
	default:
		return nil // accepted / transitioned
	}
}

// turnOutput is the JSON shape printed by `kitsoki turn`. It wraps OneShotResult
// so we can fold the OutcomeMode (an int) into a stable string field.
type turnOutput struct {
	Mode string `json:"mode"`
	*orchestrator.OneShotResult
}

// turnOutputView constructs the JSON-friendly shape from a OneShotResult.
// `Mode` shadows OneShotResult.Mode (which is an int) by being declared
// first in turnOutput; encoding/json picks up the outer field.
func turnOutputView(r *orchestrator.OneShotResult) turnOutput {
	return turnOutput{Mode: r.Mode.String(), OneShotResult: r}
}

// decodeJSONFlag parses a flag whose value is either inline JSON (e.g.
// `{"a":1}`) or `@path` to a JSON file. Empty input returns nil.
func decodeJSONFlag(raw, fieldName string) (map[string]any, error) {
	if raw == "" {
		return nil, nil
	}
	data := []byte(raw)
	if len(raw) > 0 && raw[0] == '@' {
		path := raw[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --%s file %q: %w", fieldName, path, err)
		}
		data = b
	}
	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse --%s JSON: %w", fieldName, err)
	}
	return out, nil
}

// buildTurnHarness chooses a harness for `kitsoki turn`. When --intent is set,
// or when --harness replay is used without a recording, the harness must never
// be invoked. Return a no-run harness so exact deterministic routes can still be
// probed while any LLM-tier fallthrough fails closed before spending.
func buildTurnHarness(harnessType, recordingPath string, def *app.AppDef, directIntent bool) (harness.Harness, error) {
	if directIntent {
		return &noRunHarness{}, nil
	}
	if harnessType == "" {
		var err error
		harnessType, err = autoSelectHarness("")
		if err != nil {
			return nil, err
		}
	}
	switch harnessType {
	case "replay":
		if recordingPath == "" {
			return &noRunHarness{}, nil
		}
		return harness.NewReplay(recordingPath)
	case "claude":
		return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{Exec: host.RunClaudeOneShotForHarness})
	case "live":
		client, _, err := newLiveClient()
		if err != nil {
			return nil, err
		}
		return harness.NewLive(&client, "", def)
	default:
		return nil, fmt.Errorf("unknown --harness %q", harnessType)
	}
}

// turnSemanticRoutingOptions returns the semantic-routing options for the
// stateless turn command. Replay-without-recording remains a no-LLM probe: exact
// deterministic routes can resolve, while anything that needs the model fails
// closed through noRunHarness.
func turnSemanticRoutingOptions(harnessType, recordingPath string, directIntent bool) []orchestrator.Option {
	return semanticRoutingOptions()
}

// noRunHarness is a fail-closed placeholder harness for no-LLM probe paths. It
// never gets called on deterministic/semantic/direct routes, but the
// orchestrator constructor requires a non-nil harness.
type noRunHarness struct{}

func (n *noRunHarness) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("noRunHarness: RunTurn called unexpectedly (no-LLM probe reached the harness)")
}
func (n *noRunHarness) Close() error { return nil }

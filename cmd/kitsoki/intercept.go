// intercept.go — implements the `kitsoki intercept` pre-LLM gate.
//
// `kitsoki intercept` classifies a single free-text prompt through the no-LLM
// routing tiers (Orchestrator.Classify: deterministic display/example,
// semantic synonym/template, optional embedding) against a bound (app, room),
// then applies a conservative gate. A match that clears the gate is executed
// directly via Orchestrator.OneShot (no LLM, no persistence); anything the gate
// is unsure about passes through so the prompt reaches the LLM untouched.
//
// This is the engine half of the Stage-3 Claude Code UserPromptSubmit hook: the
// hook pipes Claude's prompt JSON to stdin, and the exit code tells the hook
// whether the prompt was intercepted (and how it landed) or should proceed to
// the LLM.
//
// Exit codes:
//
//	0  — intercepted; transition fired
//	1  — intercepted; intent rejected (guard failed / not allowed)
//	2  — intercepted; landed in a terminal state
//	3  — infrastructure error (missing app file, bad world JSON, no binding, …)
//	10 — pass-through; no confident no-LLM match (the prompt should reach the LLM)
//
// The gate is intentionally conservative (principle of least surprise):
// pass-through is the default and only a high-confidence, fully-slotted,
// unambiguous match is executed. See THE GATE in interceptCmd.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/semroute"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/webconfig"
	"kitsoki/internal/world"
)

// interceptExitPassThrough is the exit code when the gate declines to intercept
// and the prompt should proceed to the LLM untouched. Distinct from turn.go's
// 1/2/3 (which mean an intent was executed and rejected/terminal/errored): exit
// 10 means nothing was executed at all.
const interceptExitPassThrough = 10

// interceptExitError is a sentinel that carries the pass-through exit code
// through cobra's error return without triggering cobra's "Error:" banner
// (handled by SilenceErrors on the command and by main()). Unlike turnExitError,
// a pass-through is a normal, expected outcome — main() must NOT print an
// "error:" line for it.
type interceptExitError struct {
	code int
	msg  string
}

func (e interceptExitError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("intercept exit %d", e.code)
}

// IsInterceptExitError reports whether err is an intercept-exit sentinel and, if
// so, returns the exit code. Used by main() to translate the pass-through exit
// (10) without printing an "error: …" line (pass-through is not a failure).
func IsInterceptExitError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if ie, ok := err.(interceptExitError); ok { //nolint:errorlint
		return ie.code, true
	}
	return 0, false
}

// interceptOutput is the JSON document `kitsoki intercept` writes to stdout. It
// describes the gate decision in a form the Stage-3 hook (or a flow-test author)
// can inspect without re-running the classification.
type interceptOutput struct {
	// Matched is true only on an INTERCEPT (the gate executed the verdict).
	Matched bool `json:"matched"`
	// Intent is the resolved intent id (set on INTERCEPT).
	Intent string `json:"intent,omitempty"`
	// Confidence is the verdict's confidence band.
	Confidence float64 `json:"confidence,omitempty"`
	// TopConfidence is the best no-LLM confidence seen on a PASS-THROUGH (0 on
	// a no-match). Mutually exclusive with Confidence in practice.
	TopConfidence float64 `json:"top_confidence,omitempty"`
	// MatchReason is the matcher's machine-readable explanation (set on INTERCEPT).
	MatchReason string `json:"match_reason,omitempty"`
	// Reason is the PASS-THROUGH cause: no_match | tie | below_bar | missing_slot.
	Reason string `json:"reason,omitempty"`
	// GateBar is the effective confidence bar the gate applied.
	GateBar float64 `json:"gate_bar"`
	// Exit is the process exit code this outcome maps to.
	Exit int `json:"exit"`
	// Result is the full OneShot outcome (set on a stateless-fast-path INTERCEPT).
	Result *orchestrator.OneShotResult `json:"result,omitempty"`
	// MultiTurn is true when the match belongs to a binding whose app declares a
	// room flagged intercept_drive: rest: the command would be DRIVEN to rest on
	// a persisted session (Orchestrator.DriveToRest), not one-shotted. The CLI
	// reports this but does NOT drive — driving needs a real harness, so it is the
	// Claude hook's job. See docs/architecture/prompt-intercept.md §"Multi-turn".
	MultiTurn bool `json:"multi_turn,omitempty"`
}

// interceptEngineInput is the resolved binding + prompt the core engine
// classifies and (on a confident match) executes. The CLI's RunE and the
// Stage-3 hook shim both build this and call runInterceptEngine — neither
// re-implements the rig wiring, the gate, or the OneShot mapping.
type interceptEngineInput struct {
	// AppPath is the path to the story app.yaml (already resolved from
	// flags/config by the caller).
	AppPath string
	// Room is the starting state path whose allowed intents form the gate's
	// alphabet.
	Room string
	// Input is the free-text prompt to classify.
	Input string
	// Bar is the confidence bar a match must clear. A negative value means
	// "use the 0.90 default"; a non-negative value is honoured verbatim (the
	// caller collapses flag/config precedence into this single field).
	Bar float64
	// World carries optional world-var overrides on top of the schema defaults.
	World map[string]any
	// Logger receives the intercept.* trace events. A nil logger is tolerated
	// (the engine substitutes a discard logger).
	Logger *slog.Logger
}

// interceptResult is the structured outcome of runInterceptEngine — everything
// the CLI needs to emit JSON + map an exit code, and everything the hook shim
// needs to decide block-vs-passthrough and compose a report. It deliberately
// carries no I/O: the engine never prints and never returns a sentinel
// exit-error (only a real error on infra failure), so both front-ends format
// the same struct their own way.
type interceptResult struct {
	// Matched is true only on an INTERCEPT (the gate executed the verdict).
	Matched bool
	// Intent is the resolved intent id (set on INTERCEPT).
	Intent string
	// Confidence is the verdict's confidence band. On a pass-through this is
	// the best no-LLM confidence seen (0 on a no-match).
	Confidence float64
	// MatchReason is the matcher's machine-readable explanation (set on INTERCEPT).
	MatchReason string
	// GateBar is the effective confidence bar the gate applied.
	GateBar float64
	// Reason is the PASS-THROUGH cause: no_match | tie | below_bar |
	// missing_slot (empty on an INTERCEPT).
	Reason string
	// Exit is the process exit code this outcome maps to (0/1/2/10).
	Exit int
	// OneShot is the full OneShot outcome (set only when the gate executed the
	// stateless fast path — i.e. MultiTurn is false).
	OneShot *orchestrator.OneShotResult
	// MultiTurn is true when the matched command belongs to a binding whose app
	// declares a room flagged intercept_drive: rest (conflict-capable intercept):
	// the gate does NOT run the stateless OneShot but signals the caller to drive
	// a persisted session to rest instead (Orchestrator.DriveToRest). Intent and
	// Slots carry what to drive; OneShot stays nil. See
	// docs/architecture/prompt-intercept.md §"Multi-turn commands".
	MultiTurn bool
	// Slots are the resolved verdict slots, carried for the MultiTurn drive path.
	Slots map[string]any
}

// runInterceptEngine resolves the rig, classifies the prompt through the no-LLM
// tiers against (AppPath, Room), applies the conservative gate, and — on a
// confident, fully-slotted, unambiguous match — executes the verdict directly
// via Orchestrator.OneShot (no LLM, no persistence). It returns the structured
// interceptResult that both the `kitsoki intercept` CLI and the Stage-3 hook
// shim format. A non-nil error is an infrastructure failure (exit 3 territory):
// missing app, bad world, classify/execute failure — the caller decides whether
// to surface it (CLI) or fail open (hook).
func runInterceptEngine(ctx context.Context, in interceptEngineInput) (interceptResult, error) {
	logger := in.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	// Build the stateless probe rig exactly like turn.go's probe path:
	// in-memory store, noRunHarness, host builtins, agent registry. Direct-
	// intent execution after classification never invokes the harness, so the
	// noRunHarness is correct here.
	def, err := loadAppWithEnv(in.AppPath)
	if err != nil {
		return interceptResult{}, infraError("%v", err)
	}

	m, err := machine.New(def)
	if err != nil {
		return interceptResult{}, infraError("build machine: %v", err)
	}

	s, err := store.OpenMemory()
	if err != nil {
		return interceptResult{}, infraError("open in-memory store: %v", err)
	}
	defer func() { _ = s.Close() }()

	h := &noRunHarness{}

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
	if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
		return interceptResult{}, infraError("validate hosts: %v", err)
	}

	chatStore, err := chats.NewStore(s.DB())
	if err != nil {
		return interceptResult{}, infraError("init chats store: %v", err)
	}
	chatAdapter := chathost.NewAdapter(chatStore)

	agentReg, agentRegErr := agent.BuildRegistryFromDef(def, h)
	if agentRegErr != nil {
		return interceptResult{}, infraError("build agent registry: %v", agentRegErr)
	}
	defer func() { _ = agentReg.Close() }()

	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithChatStore(chatAdapter),
		orchestrator.WithAgentRegistry(agentReg),
		orchestrator.WithLogger(logger),
	)

	// Resolve state + world (schema defaults first, overrides win).
	state := app.StatePath(in.Room)
	defaultWorld := machine.WorldFromSchema(def.World)
	worldMap := make(map[string]any, len(defaultWorld.Vars)+len(in.World))
	for k, v := range defaultWorld.Vars {
		worldMap[k] = v
	}
	for k, v := range in.World {
		worldMap[k] = v
	}

	// Classify (side-effect-free; no LLM, no persistence).
	verdict, matched, err := orch.Classify(ctx, state, world.World{Vars: worldMap}, in.Input)
	if err != nil {
		return interceptResult{}, infraError("classify: %v", err)
	}

	// THE GATE — conservative; pass-through is the default. A negative Bar
	// means "use the 0.90 default".
	bar := 0.90
	if in.Bar >= 0 {
		bar = in.Bar
	}

	passThrough := func(reason string) (interceptResult, error) {
		logger.Debug(trace.EvInterceptPassed,
			slog.String("input", in.Input),
			slog.Float64("top_confidence", verdict.Confidence),
			slog.String("reason", reason),
		)
		return interceptResult{
			Matched:    false,
			Confidence: verdict.Confidence,
			Reason:     reason,
			GateBar:    bar,
			Exit:       interceptExitPassThrough,
		}, nil
	}

	switch {
	case !matched:
		return passThrough("no_match")
	case verdict.Confidence == semroute.ConfidenceTie || len(verdict.Candidates) > 0:
		// A tie (0.50) or any surfaced candidates is ambiguous — never pick
		// one silently; let the LLM (or the user) disambiguate.
		return passThrough("tie")
	case verdict.Confidence < bar:
		// A deterministic hit is 1.00 and always clears; a synonym hit (0.90)
		// clears the default bar exactly. Below the bar passes through.
		return passThrough("below_bar")
	case len(verdict.MissingSlots) > 0 ||
		orchestrator.RequiresUnfilledSlot(def, state, verdict.Intent, verdict.Slots):
		// A match that needs a slot the matcher could not fill is not
		// executable as-is — pass through rather than guess the slot.
		return passThrough("missing_slot")
	}

	// INTERCEPT.
	logger.Debug(trace.EvInterceptMatched,
		slog.String("input", in.Input),
		slog.String("intent", verdict.Intent),
		slog.Float64("confidence", verdict.Confidence),
		slog.String("match_reason", verdict.MatchReason),
		slog.Float64("gate_bar", bar),
		slog.Bool("executed", true),
	)

	// Multi-turn binding (conflict-capable intercept): the bound app declares a
	// room flagged intercept_drive: rest, so the matched command may enter a
	// multi-turn, agent-in-the-loop sub-flow that the stateless OneShot cannot
	// drive (and abandoning it could strand the tree). Don't OneShot — signal the
	// caller to drive a persisted session to rest (DriveToRest) under its own
	// budget. Single-command matches in such a binding settle fast there too; the
	// no-LLM promise still holds for them (the agent only runs on a real
	// conflict). See docs/architecture/prompt-intercept.md §"Multi-turn commands".
	if orch.HasInterceptDriveRoom() {
		return interceptResult{
			Matched:     true,
			MultiTurn:   true,
			Intent:      verdict.Intent,
			Slots:       verdict.Slots,
			Confidence:  verdict.Confidence,
			MatchReason: verdict.MatchReason,
			GateBar:     bar,
			Exit:        0,
		}, nil
	}

	// Stateless fast path — execute the verdict directly (no LLM, no persistence).
	result, err := orch.OneShot(ctx, orchestrator.OneShotInput{
		State:  state,
		World:  worldMap,
		Intent: verdict.Intent,
		Slots:  verdict.Slots,
	})
	if err != nil {
		return interceptResult{}, infraError("execute intercepted intent %q: %v", verdict.Intent, err)
	}

	// Map the OneShot outcome to an exit code, reusing turn.go's 0/1/2
	// semantics. ModeClarify is defensive — the missing_slot gate above should
	// have passed through before we reach OneShot.
	exitCode := 0
	switch result.Mode {
	case orchestrator.ModeTransitioned:
		exitCode = 0
	case orchestrator.ModeCompleted:
		exitCode = turnExitTerminal
	case orchestrator.ModeRejected:
		exitCode = turnExitRejected
	case orchestrator.ModeClarify:
		exitCode = interceptExitPassThrough
	default:
		return interceptResult{}, infraError("unexpected outcome mode %s", result.Mode)
	}

	return interceptResult{
		Matched:     true,
		Intent:      verdict.Intent,
		Confidence:  verdict.Confidence,
		MatchReason: verdict.MatchReason,
		GateBar:     bar,
		Exit:        exitCode,
		OneShot:     result,
	}, nil
}

func interceptCmd() *cobra.Command {
	var (
		inputFlag  string
		appFlag    string
		roomFlag   string
		barFlag    float64
		worldFlag  string
		configFlag string
		traceFlag  string
	)

	cmd := &cobra.Command{
		Use:           "intercept",
		Short:         "Pre-LLM gate: classify a prompt and execute a confident no-LLM match (else pass through)",
		SilenceErrors: true, // pass-through (exit 10) is not a failure; keep stdout clean JSON
		SilenceUsage:  true,
		Long: `Classify a single free-text prompt through kitsoki's no-LLM routing tiers
(deterministic display/example, semantic synonym/template, optional embedding)
against a bound (app, room), then apply a conservative gate. A high-confidence,
fully-slotted, unambiguous match is executed directly (no LLM); anything else
passes through so the prompt reaches the LLM untouched.

This is the engine half of the Stage-3 Claude Code UserPromptSubmit hook: pipe
Claude's prompt JSON to stdin and read the exit code.

Binding resolution (per field): --app/--room/--bar win; any unset field falls
back to the intercept: block in .kitsoki.yaml (see --config).

Input resolution: --input wins; otherwise all of stdin is read — if it parses
as JSON with a "prompt" string field that field is used, else the trimmed raw
stdin is the input.

Exit codes:
  0   intercepted; transition fired
  1   intercepted; intent rejected
  2   intercepted; terminal state
  3   infrastructure error
  10  pass-through (the prompt should reach the LLM)

Examples:
  kitsoki intercept --app app.yaml --room start --input "go north"
  echo '{"prompt":"go north"}' | kitsoki intercept   # binding from .kitsoki.yaml
  kitsoki intercept --input "head west" --trace /tmp/intercept.jsonl`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Resolve the binding: flags win, config fills the gaps.
			appPath, room, cfgBar, err := resolveInterceptBinding(appFlag, roomFlag, barFlag, configFlag)
			if err != nil {
				return err
			}

			// 2. Resolve the input: --input wins; else read stdin (JSON {"prompt"}
			//    or raw trimmed text).
			input, err := resolveInterceptInput(cmd, inputFlag)
			if err != nil {
				return err
			}

			worldOverrides, err := decodeJSONFlag(worldFlag, "world")
			if err != nil {
				return infraError("%v", err)
			}

			// The two intercept.* events always flow through this logger; when
			// --trace is set they land in a JSONL file, otherwise the handler is
			// io.Discard (the events still "emit", they just go nowhere).
			logger := interceptLogger(traceFlag)
			if closer, ok := logger.Handler().(io.Closer); ok {
				defer func() { _ = closer.Close() }()
			}

			// Collapse the --bar/config-bar precedence into the engine's single
			// Bar field (negative ⇒ default). --bar wins; an unset --bar falls
			// back to a positive config bar; otherwise the engine's 0.90 default.
			bar := -1.0
			switch {
			case barFlag >= 0:
				bar = barFlag
			case cfgBar > 0:
				bar = cfgBar
			}

			// 3. Run the shared engine: rig + classify + gate + execute.
			res, err := runInterceptEngine(cmd.Context(), interceptEngineInput{
				AppPath: appPath,
				Room:    room,
				Input:   input,
				Bar:     bar,
				World:   worldOverrides,
				Logger:  logger,
			})
			if err != nil {
				return err
			}

			// 4. Format the structured result as JSON + map Exit → sentinel.
			if !res.Matched {
				return emitInterceptJSON(cmd, interceptOutput{
					Matched:       false,
					TopConfidence: res.Confidence,
					Reason:        res.Reason,
					GateBar:       res.GateBar,
					Exit:          res.Exit,
				}, interceptExitError{code: res.Exit})
			}

			return emitInterceptJSON(cmd, interceptOutput{
				Matched:     true,
				MultiTurn:   res.MultiTurn,
				Intent:      res.Intent,
				Confidence:  res.Confidence,
				MatchReason: res.MatchReason,
				GateBar:     res.GateBar,
				Exit:        res.Exit,
				Result:      res.OneShot, // nil on the MultiTurn path
			}, interceptExitForCode(res.Exit))
		},
	}

	cmd.Flags().StringVar(&inputFlag, "input", "", "prompt to classify (default: read stdin — JSON {\"prompt\":…} or raw text)")
	cmd.Flags().StringVar(&appFlag, "app", "", "path to app.yaml (default: intercept.app in --config)")
	cmd.Flags().StringVar(&roomFlag, "room", "", "starting state path (default: intercept.room in --config)")
	cmd.Flags().Float64Var(&barFlag, "bar", -1, "confidence bar a match must clear to intercept (default: intercept.confidence_bar in --config, else 0.90)")
	cmd.Flags().StringVar(&worldFlag, "world", "", "world overrides as JSON or @file")
	cmd.Flags().StringVar(&configFlag, "config", webconfig.DefaultConfigFile, "config file carrying the intercept: binding")
	cmd.Flags().StringVar(&traceFlag, "trace", "", "optional JSONL path for intercept.* trace events")

	return cmd
}

// resolveInterceptBinding resolves (app, room, bar) per-field: flags win, and
// any unset field falls back to the intercept: block in the config file. The
// returned bar is the config bar (0 when no usable config block) — the caller
// still applies the flag/default precedence so a negative flag means "default".
// app or room still empty after the fallback is an infra error (exit 3).
func resolveInterceptBinding(appFlag, roomFlag string, barFlag float64, configFlag string) (appPath, room string, cfgBar float64, err error) {
	appPath, room = appFlag, roomFlag

	// Only consult the config when something is actually missing (so an absent
	// file is harmless when all three are passed on the command line).
	if appPath == "" || room == "" || barFlag < 0 {
		cfg, loadErr := webconfig.Load(configFlag)
		if loadErr != nil {
			return "", "", 0, infraError("load config %q: %v", configFlag, loadErr)
		}
		if ic := cfg.Intercept; ic != nil {
			if appPath == "" {
				appPath = ic.App
			}
			if room == "" {
				room = ic.Room
			}
			cfgBar = ic.ConfidenceBar
		}
	}

	if appPath == "" {
		return "", "", 0, infraError("no app: pass --app or set intercept.app in %s", configFlag)
	}
	if room == "" {
		return "", "", 0, infraError("no room: pass --room or set intercept.room in %s", configFlag)
	}
	return appPath, room, cfgBar, nil
}

// resolveInterceptInput resolves the prompt: --input wins; otherwise all of
// stdin is read. If stdin parses as JSON with a "prompt" string field that
// field is used (the Stage-3 hook pipes Claude's UserPromptSubmit JSON straight
// in); otherwise the trimmed raw stdin is the input.
func resolveInterceptInput(cmd *cobra.Command, inputFlag string) (string, error) {
	if inputFlag != "" {
		return inputFlag, nil
	}
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", infraError("read stdin: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", infraError("no input: pass --input or pipe a prompt on stdin")
	}
	// Try the UserPromptSubmit JSON shape first; fall back to raw text.
	var probe struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err == nil && probe.Prompt != "" {
		return probe.Prompt, nil
	}
	return trimmed, nil
}

// interceptLogger builds the logger the intercept.* events flow through. With a
// --trace path it writes JSON lines to that file at Debug; otherwise the handler
// discards (the events still emit, they just go nowhere — so --trace is the only
// way to capture them, by design).
func interceptLogger(tracePath string) *slog.Logger {
	if tracePath == "" {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	f, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// A bad --trace path is non-fatal: degrade to discard so the gate still
		// runs. The events are diagnostic, not load-bearing.
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// emitInterceptJSON writes the interceptOutput to stdout (indented) and returns
// the exit-carrying error. A JSON encode failure is itself an infra error.
func emitInterceptJSON(cmd *cobra.Command, out interceptOutput, exitErr error) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return infraError("encode output: %v", err)
	}
	return exitErr
}

// interceptExitForCode maps an INTERCEPT exit code (from runInterceptEngine) to
// the sentinel error the CLI returns so main()/cobra translate it without
// printing an "Error:" banner. 0 is success (nil); 1/2 reuse turn.go's
// rejected/terminal sentinels; the defensive ModeClarify→10 path reuses the
// pass-through sentinel. Pass-through (Matched==false) is handled by the caller
// directly, so it is not reached here.
func interceptExitForCode(code int) error {
	switch code {
	case 0:
		return nil
	case turnExitRejected, turnExitTerminal:
		return turnExitError{code: code}
	case interceptExitPassThrough:
		return interceptExitError{code: code}
	default:
		return infraError("unexpected intercept exit code %d", code)
	}
}

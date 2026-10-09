// drive.go — the `kitsoki drive` subcommand: an interactive, headless driver.
//
// drive runs the REAL orchestrator turn loop against a persistent JSONL trace,
// accepting one free-text line per turn (from stdin or --script) and routing it
// through orch.Turn (the same path the TUI uses) via a live OR replay harness,
// with python-vcr-style record/playback modes over the routing cassette. Each
// turn emits one JSON object on stdout carrying the slice-1 Frame (the full
// human-fidelity screen, reused from the TUI composer — NOT re-derived) plus the
// routed intent, confidence, exit class, and host calls.
//
// It is `turn --trace` with the real harness wired in instead of noRunHarness,
// the slice-1 Frame on the way out, and VCR cassette modes. See
// docs/proposals/qa-drive-command.md.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/tui"
	"kitsoki/internal/webconfig"
)

// driveFrame is the per-turn JSON object `kitsoki drive` writes to stdout, one
// line per turn. It bundles the slice-1 Frame (the human-fidelity screen) with
// the interpretive routing decision and the deterministic downstream effects so
// an agent can read it, decide, and submit the next line.
type driveFrame struct {
	// Frame is the composed slice-1 screen (Text/ANSI twins + typed metadata).
	Frame tui.Frame `json:"frame"`
	// RoutedIntent is the intent the harness routed the free text to ("" on a
	// rejection that never resolved an intent).
	RoutedIntent string `json:"routed_intent"`
	// Confidence is the routing confidence the harness reported (0 when the
	// harness does not report one).
	Confidence float64 `json:"confidence"`
	// Exit classifies the turn: accepted | rejected | terminal | error.
	Exit string `json:"exit"`
	// HostCalls summarises the host.* invocations this turn made (empty when none).
	HostCalls []driveHostCall `json:"host_calls,omitempty"`
	// OperationDrive summarises an automatic background operation drive, or an
	// explicit --drive-operation drive.
	OperationDrive *driveOperationFrame `json:"operation_drive,omitempty"`
}

// driveHostCall is a compact summary of one host.* invocation observed in the
// turn's trace events, projected for the per-turn JSONL.
type driveHostCall struct {
	Namespace string `json:"namespace"`
	State     string `json:"state,omitempty"`
}

// driveOperationFrame is additive metadata emitted when a background operation
// auto-runs after an operator/scripted turn, or when --drive-operation asks the
// operation driver to continue an active autonomous/supervised operation.
type driveOperationFrame struct {
	Turns      int    `json:"turns"`
	StopReason string `json:"stop_reason,omitempty"`
	LastIntent string `json:"last_intent,omitempty"`
}

const (
	driveExitAccepted = "accepted"
	driveExitRejected = "rejected"
	driveExitTerminal = "terminal"
	driveExitError    = "error"
)

func driveCmd() *cobra.Command {
	var (
		tracePath      string
		harnessType    string
		cassette       string
		recordMode     string
		cols           int
		rows           int
		scriptPath     string
		profileName    string
		configPath     string
		warpBasisPath  string
		driveOperation bool
	)

	cmd := &cobra.Command{
		Use:           "drive <app.yaml>",
		Short:         "Drive a persistent trace with free-text input per turn (headless)",
		SilenceErrors: true,
		SilenceUsage:  true,
		Long: `Run the real orchestrator turn loop against a persistent JSONL trace,
reading one free-text line per turn (stdin or --script) and routing it through
the live or replay harness. Each turn emits one JSON object on stdout: the
slice-1 Frame plus { routed_intent, confidence, exit, host_calls }. Background
operation handles auto-drive after accepted turns; --drive-operation force-drives
any active autonomous/supervised operation handle.

VCR record modes (over the routing cassette):
  none  replay on hit; ERROR on miss (pure deterministic, no live calls)
  once  replay on hit; live+append on miss only while the cassette is empty/new
  new   replay on hit; live+append every novel miss (exploratory QA)
  all   ignore the cassette; always call live and (re)record

Examples:
  kitsoki drive testdata/apps/cloak/app.yaml --trace /tmp/cloak.jsonl \
     --harness replay --cassette testdata/apps/cloak/recording.yaml --record none
  echo "go south" | kitsoki drive app.yaml --trace /tmp/t.jsonl \
     --harness replay --cassette rec.yaml --record none`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := ""
			if len(args) == 1 {
				appPath = args[0]
			}
			return runDrive(cmd, driveCmdConfig{
				appPath:        appPath,
				tracePath:      tracePath,
				harnessType:    harnessType,
				cassette:       cassette,
				recordMode:     recordMode,
				cols:           cols,
				rows:           rows,
				scriptPath:     scriptPath,
				profileName:    profileName,
				configPath:     configPath,
				warpBasisPath:  warpBasisPath,
				driveOperation: driveOperation,
			})
		},
	}

	cmd.Flags().StringVar(&tracePath, "trace", "", "JSONL trace file (created if absent; the durable session)")
	cmd.Flags().StringVar(&harnessType, "harness", "replay", "routing harness: live | replay")
	cmd.Flags().StringVar(&cassette, "cassette", "", "recording.yaml to replay and/or record into")
	cmd.Flags().StringVar(&recordMode, "record", "none", "VCR mode: none | once | all | new")
	cmd.Flags().IntVar(&cols, "cols", 100, "frame width in columns")
	cmd.Flags().IntVar(&rows, "rows", 30, "frame height in rows")
	cmd.Flags().StringVar(&scriptPath, "script", "", "optional batch input file (newline-separated; CI smoke)")
	cmd.Flags().StringVar(&profileName, "profile", "", "harness profile from .kitsoki.yaml/.kitsoki.local.yaml; supplies backend, model, quota, and provider env")
	cmd.Flags().StringVar(&configPath, "config", webconfig.DefaultConfigFile, "config file used with --profile")
	cmd.Flags().StringVar(&warpBasisPath, "warp", "", "path to a warp-basis YAML (state + world overrides); applied as the first action after session create. Same file the TUI's /warp file:<path> loads.")
	cmd.Flags().BoolVar(&driveOperation, "drive-operation", false, "after each accepted turn, force-drive any active autonomous/supervised operation until it rests")

	return cmd
}

// driveCmdConfig carries the parsed flags for runDrive.
type driveCmdConfig struct {
	appPath        string
	tracePath      string
	harnessType    string
	cassette       string
	recordMode     string
	cols           int
	rows           int
	scriptPath     string
	profileName    string
	configPath     string
	warpBasisPath  string
	driveOperation bool

	// harnessOverride, when non-nil, replaces buildDriveHarness — the DI seam
	// tests use to inject a stub live harness (or a failing live harness behind
	// a replay cassette) without real LLM credentials. Always nil in production.
	harnessOverride harness.Harness
}

func runDrive(cmd *cobra.Command, cfg driveCmdConfig) error {
	if cfg.tracePath == "" {
		return infraError("--trace is required")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	profiles, defaultProfile, activeProfile, err := resolveDriveProfiles(cfg)
	if err != nil {
		return err
	}

	// Build the routing harness BEFORE the trace session so a bad
	// harness/cassette config fails before any session bootstrap. The harness
	// is handed to setupTraceSession, which owns its Close. Tests inject a stub
	// via harnessOverride to avoid real LLM credentials.
	h := cfg.harnessOverride
	if h == nil {
		var err error
		h, err = buildDriveHarness(cfg, activeProfile)
		if err != nil {
			return err
		}
	}

	// setupTraceSession takes ownership of h: on a returned error h is already
	// closed; on success ts.Close tears it down.
	var orchOpts []orchestrator.Option
	if len(profiles) > 0 {
		orchOpts = append(orchOpts, orchestrator.WithHarnessProfiles(profiles, defaultProfile))
		if cfg.profileName != "" {
			orchOpts = append(orchOpts, func(o *orchestrator.Orchestrator) {
				_ = o.SetSelection(cfg.profileName, "", "")
			})
		}
	}
	ts, err := setupTraceSession(ctx, cfg.appPath, cfg.tracePath, h, orchOpts...)
	if err != nil {
		return err
	}
	defer ts.Close()

	// Run the initial state's on_enter chain so the first frame and the
	// session world match what a fresh TUI session would show. Skipped when the
	// trace already carries turns (a resumed session): re-running on_enter would
	// double-apply entry effects.
	if isFreshTrace(ts.Sink) {
		if cfg.warpBasisPath != "" {
			resolved, basis, basisErr := tui.LoadWarpBasis(cfg.warpBasisPath, cfg.appPath)
			if basisErr != nil {
				ts.Close()
				return infraError("--warp %q: %v", cfg.warpBasisPath, basisErr)
			}
			if basis.State == "" {
				ts.Close()
				return infraError("--warp %s: missing required `state:` field", resolved)
			}
			_, warpErr := ts.Orch.Teleport(ctx, ts.SID, inbox.TeleportTarget{
				State: app.StatePath(basis.State),
				Slots: basis.World,
			})
			if warpErr != nil {
				ts.Close()
				return infraError("--warp %s: teleport: %v", resolved, warpErr)
			}
		} else {
			if err := ts.Orch.RunInitialOnEnter(ctx, ts.SID); err != nil {
				ts.Close()
				return infraError("run initial on_enter: %v", err)
			}
		}
	}

	// Seed the TUI model used purely as the slice-1 Frame composer. The model
	// is driven headlessly: each turn's outcome is folded in via
	// ApplyTurnOutcome (the same handleTurnOutcome path the live TUI runs), then
	// ComposeFrame paints the still at the requested geometry.
	model, err := newDriveModel(ts)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(cmd.OutOrStdout())

	// Input source: --script file or stdin.
	var reader io.Reader = cmd.InOrStdin()
	if cfg.scriptPath != "" {
		f, ferr := os.Open(cfg.scriptPath)
		if ferr != nil {
			return infraError("open --script %q: %v", cfg.scriptPath, ferr)
		}
		defer func() { _ = f.Close() }()
		reader = f
	}

	scanner := bufio.NewScanner(reader)
	// Allow long free-text lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}

		histBefore := len(ts.Sink.History())
		outcome, turnErr := ts.Orch.Turn(ctx, ts.SID, line)
		model = model.ApplyTurnOutcome(outcome, line, turnErr)
		finalOutcome := outcome
		finalErr := turnErr
		var opFrame *driveOperationFrame

		if shouldDriveOperationAfterTurn(outcome, turnErr) {
			var drive *orchestrator.OperationDriveOutcome
			var driveErr error
			if cfg.driveOperation {
				drive, driveErr = ts.Orch.DriveOperation(ctx, ts.SID)
			} else {
				drive, driveErr = ts.Orch.DriveBackgroundOperation(ctx, ts.SID)
			}
			if drive != nil {
				opFrame = &driveOperationFrame{
					Turns:      drive.Turns,
					StopReason: drive.StopReason,
					LastIntent: drive.LastIntent,
				}
				if drive.Final != nil {
					model = model.ApplyTurnOutcome(drive.Final, "Drive operation", driveErr)
					finalOutcome = drive.Final
				}
			}
			if driveErr != nil {
				finalErr = driveErr
			}
		}

		histAfter := ts.Sink.History()
		newEvents := histAfter[histBefore:]
		frame := tui.ComposeFrame(&model, cfg.cols, cfg.rows)

		df := driveFrame{
			Frame:          frame,
			RoutedIntent:   routedIntentFromEvents(newEvents),
			Confidence:     driveConfidence(h),
			Exit:           driveExit(finalOutcome, finalErr),
			HostCalls:      hostCallsFromEvents(newEvents),
			OperationDrive: opFrame,
		}
		if encErr := enc.Encode(df); encErr != nil {
			return fmt.Errorf("encode frame: %w", encErr)
		}

		// Stop driving once the story reaches a terminal state — there is no
		// further turn to take.
		if finalOutcome != nil && finalOutcome.Mode == orchestrator.ModeCompleted {
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return infraError("read input: %v", scanErr)
	}

	return nil
}

func shouldDriveOperationAfterTurn(out *orchestrator.TurnOutcome, err error) bool {
	if err != nil || out == nil {
		return false
	}
	return out.Mode == orchestrator.ModeTransitioned
}

// buildDriveHarness constructs the routing harness for drive: a LiveHarness or
// ReplayHarness, wrapped in a VCRHarness whenever a cassette + record mode call
// for recording on a miss. The four VCR modes are enforced inside the
// VCRHarness; here we only wire the right inner/live harness.
//
// Mode precedence:
//   - --record none with --harness replay and a cassette: pure ReplayHarness
//     wrapped in a none-mode VCR (replay on hit, hard error on miss — zero
//     live calls). With no cassette this is an error.
//   - any recording mode (once|new|all): a VCRHarness over the cassette with a
//     LiveHarness fall-through. Requires a cassette path and live credentials.
//   - --harness live with --record none and no cassette: a bare LiveHarness
//     (every turn live; nothing recorded).
func buildDriveHarness(cfg driveCmdConfig, activeProfile *orchestrator.HarnessProfile) (harness.Harness, error) {
	mode, err := harness.ParseVCRMode(cfg.recordMode)
	if err != nil {
		return nil, infraError("%v", err)
	}

	// A cassette + a record mode (or replay harness) routes through the VCR
	// harness, which owns replay+record over the single recording.yaml shape.
	if cfg.cassette != "" {
		var live harness.Harness
		if mode != harness.VCRModeNone {
			// Recording modes need a live fall-through.
			live, err = newDriveLiveHarness(cfg, activeProfile)
			if err != nil {
				return nil, err
			}
		}
		vcr, vErr := harness.NewVCR(mode, cfg.cassette, live)
		if vErr != nil {
			if live != nil {
				_ = live.Close()
			}
			return nil, infraError("%v", vErr)
		}
		return vcr, nil
	}

	// No cassette: only a bare live harness makes sense (and only when a
	// record mode would never demand a cassette to write to).
	switch cfg.harnessType {
	case "live":
		return newDriveLiveHarness(cfg, activeProfile)
	case "replay":
		return nil, infraError("--harness replay requires --cassette")
	default:
		return nil, infraError("unknown --harness %q (want live | replay)", cfg.harnessType)
	}
}

// newDriveLiveHarness builds a LiveHarness from on-disk credentials. It loads
// the app def to give the harness its prompt context. Returns a clear infra
// error when credentials or the app are unavailable so a headless run fails
// loudly rather than hanging.
func newDriveLiveHarness(cfg driveCmdConfig, activeProfile *orchestrator.HarnessProfile) (harness.Harness, error) {
	if cfg.appPath == "" {
		return nil, infraError("--harness live requires the app.yaml positional argument for prompt context")
	}
	def, err := loadAppWithEnv(cfg.appPath)
	if err != nil {
		return nil, infraError("%v", err)
	}
	env := map[string]string(nil)
	model := ""
	if activeProfile != nil {
		env = activeProfile.Env
		model = activeProfile.Model
		// A named native-Claude profile is subscription-backed: route through
		// the Claude CLI, which owns that login, rather than requiring a direct
		// Anthropic API credential before the CLI is ever invoked.
		if activeProfile.Backend == "claude" && len(env) == 0 {
			profile := host.ActiveProfile{
				Name:     activeProfile.Name,
				Provider: host.Provider{Backend: activeProfile.Backend, Model: model},
			}
			claudeExec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
				return host.RunClaudeOneShotForHarness(host.WithActiveProfile(ctx, profile), bin, args, stdin, workingDir)
			}
			return harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{Model: model, Exec: claudeExec})
		}
	}
	client, _, err := newLiveClientWithEnv(env)
	if err != nil {
		return nil, infraError("%v", err)
	}
	lh, err := harness.NewLive(&client, model, def)
	if err != nil {
		return nil, infraError("%v", err)
	}
	return lh, nil
}

func resolveDriveProfiles(cfg driveCmdConfig) (map[string]orchestrator.HarnessProfile, string, *orchestrator.HarnessProfile, error) {
	if strings.TrimSpace(cfg.profileName) == "" {
		return nil, "", nil, nil
	}
	configPath := cfg.configPath
	if strings.TrimSpace(configPath) == "" {
		configPath = webconfig.DefaultConfigFile
	}
	webCfg, err := webconfig.Load(configPath)
	if err != nil {
		return nil, "", nil, infraError("load --config %q: %v", configPath, err)
	}
	profiles, defaultProfile := harnessProfilesFromConfig(webCfg)
	profile, ok := profiles[cfg.profileName]
	if !ok {
		return nil, "", nil, infraError("unknown --profile %q", cfg.profileName)
	}
	return profiles, defaultProfile, &profile, nil
}

// newDriveModel builds the headless TUI model used solely as the slice-1 Frame
// composer. It seeds the initial typed view so the first frame (and the room
// body after each turn) renders through the same path the live TUI uses.
func newDriveModel(ts *traceSession) (tui.RootModel, error) {
	j, err := ts.Orch.LoadJourney(ts.SID)
	if err != nil {
		return tui.RootModel{}, infraError("load journey: %v", err)
	}
	initialView, typedView, env, rr, err := ts.Orch.InitialViewTyped(j.World)
	if err != nil {
		return tui.RootModel{}, infraError("render initial view: %v", err)
	}
	m := tui.NewRootModel(ts.Orch, ts.SID, "", initialView,
		tui.WithInitialTypedView(typedView, env, rr),
	)
	return m, nil
}

// isFreshTrace reports whether the trace carries no turn events yet (only the
// story snapshot at turn 0). A fresh trace needs its initial on_enter run; a
// resumed one already has it.
func isFreshTrace(sink *store.JSONLSink) bool {
	for _, ev := range sink.History() {
		if ev.Turn > 0 {
			return false
		}
		switch ev.Kind {
		case store.TransitionApplied, store.StateEntered, store.TurnStarted:
			// Turn-0 on_enter already ran.
			return false
		}
	}
	return true
}

// driveExit classifies a turn outcome into the JSONL `exit` field.
func driveExit(out *orchestrator.TurnOutcome, err error) string {
	if err != nil {
		return driveExitError
	}
	if out == nil {
		return driveExitError
	}
	switch out.Mode {
	case orchestrator.ModeCompleted:
		return driveExitTerminal
	case orchestrator.ModeRejected, orchestrator.ModeCancelled:
		return driveExitRejected
	default:
		// Transitioned, Clarify, OffPath — the turn was accepted/processed.
		return driveExitAccepted
	}
}

// driveConfidence reads the routing confidence from the harness when it
// implements ConfidenceReporter; otherwise 0.
func driveConfidence(h harness.Harness) float64 {
	if cr, ok := h.(harness.ConfidenceReporter); ok {
		return cr.LastConfidence()
	}
	return 0
}

// routedIntentFromEvents extracts the routed intent name from a turn's newly
// appended events. The machine's TransitionApplied (or the harness's
// LLMToolCall breadcrumb) carries the routed intent name.
func routedIntentFromEvents(events []store.Event) string {
	// Prefer the authoritative TransitionApplied; fall back to the LLM tool call.
	for _, ev := range events {
		if ev.Kind == store.TransitionApplied {
			if name := intentFromPayload(ev.Payload); name != "" {
				return name
			}
		}
	}
	for _, ev := range events {
		if ev.Kind == store.LLMToolCall {
			if name := intentFromPayload(ev.Payload); name != "" {
				return name
			}
		}
	}
	return ""
}

// intentFromPayload pulls the "intent" string field out of an event payload.
func intentFromPayload(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	if v, ok := m["intent"].(string); ok {
		return v
	}
	return ""
}

// hostCallsFromEvents projects the HostInvoked events of a turn into the
// compact driveHostCall summaries for the per-turn JSONL.
func hostCallsFromEvents(events []store.Event) []driveHostCall {
	var out []driveHostCall
	for _, ev := range events {
		if ev.Kind != store.HostInvoked {
			continue
		}
		hc := driveHostCall{State: string(ev.StatePath)}
		var m map[string]any
		if err := json.Unmarshal(ev.Payload, &m); err == nil {
			if ns, ok := m["namespace"].(string); ok {
				hc.Namespace = ns
			} else if ns, ok := m["host"].(string); ok {
				hc.Namespace = ns
			}
		}
		out = append(out, hc)
	}
	return out
}

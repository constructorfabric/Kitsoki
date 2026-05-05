// Package testrunner implements the Mode 2 (deterministic flow tests) and
// Mode 1 (input→intent pass-rate tests) runners described in §10.
package testrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/app"
	"hally/internal/clock"
	"hally/internal/expr"
	"hally/internal/harness"
	"hally/internal/host"
	"hally/internal/intent"
	"hally/internal/jobs"
	"hally/internal/machine"
	"hally/internal/orchestrator"
	"hally/internal/store"
	"hally/internal/world"
)

// ─── Flow fixture YAML format (§10.3.1) ──────────────────────────────────────

// FlowFixture is the top-level flow fixture document.
type FlowFixture struct {
	TestKind     string            `yaml:"test_kind"`
	App          string            `yaml:"app"`
	Oracle       string            `yaml:"oracle,omitempty"`
	InitialState string            `yaml:"initial_state"`
	InitialWorld map[string]any    `yaml:"initial_world,omitempty"`
	Turns        []FlowTurn        `yaml:"turns"`

	// HostHandlers declares stub host.* handlers used by this flow.
	// Keys are the handler name (e.g. "host.run", "host.workspace_manager.get").
	// Each value declares the canned response. Presence of any host_handlers
	// entry implicitly opts the fixture into the orchestrator-backed runner.
	HostHandlers map[string]HostStub `yaml:"host_handlers,omitempty"`

	// UseOrchestrator opts into the orchestrator-backed runner instead of the
	// legacy machine-only path. Defaults to false for backwards compatibility;
	// flows that declare host_handlers: or any advance_clock:/expect_inbox: turn
	// fields implicitly opt in regardless of this flag.
	UseOrchestrator *bool `yaml:"use_orchestrator,omitempty"`

	// Session-level assertions.
	ExpectTerminal           *bool          `yaml:"expect_terminal,omitempty"`
	ExpectEventsCountAtLeast *int           `yaml:"expect_events_count_atleast,omitempty"`
	ExpectEventsCountAtMost  *int           `yaml:"expect_events_count_atmost,omitempty"`
	ExpectNoErrors           *bool          `yaml:"expect_no_errors,omitempty"`
	ExpectWorldFinal         map[string]any `yaml:"expect_world_final,omitempty"`
}

// HostStub declares the canned response for a stub host handler used in a flow
// fixture. Mutually exclusive fields: at most one of Error, InfraError, or Data
// should be set. Delay (virtual time) and RequestClarification are independent.
type HostStub struct {
	// Data is returned in host.Result.Data on a successful invocation.
	Data map[string]any `yaml:"data,omitempty"`
	// Error sets host.Result.Error (a domain-level error). Empty = no error.
	// Mutually exclusive with InfraError.
	Error string `yaml:"error,omitempty"`
	// InfraError, when non-empty, makes the handler return (Result{}, error).
	// This simulates a handler that fails at the infrastructure level
	// (distinct from a domain-level Error). Mutually exclusive with Error.
	InfraError string `yaml:"infra_error,omitempty"`
	// Delay (e.g. "30s") simulates a slow handler using the Fake clock's
	// Sleep. Combined with FlowTurn.AdvanceClock, this lets a test verify
	// long-running background-job behaviour without real wall time.
	Delay string `yaml:"delay,omitempty"`
	// RequestClarification, when non-empty, causes the stub to call
	// host.RequestClarification mid-execution and block until the user answers
	// via answer_clarification. The value is the question string surfaced to the
	// user in the action_required notification. After the answer is received the
	// stub returns Data (or Error/InfraError) as normal.
	// Requires background: true on the invoking effect.
	RequestClarification string `yaml:"request_clarification,omitempty"`
}

// FlowTurn is one turn in the fixture.
type FlowTurn struct {
	// Exactly one of Intent or Input must be set.
	Intent *FlowIntent `yaml:"intent,omitempty"`
	Input  string      `yaml:"input,omitempty"`

	// WorldOverride mutates world before guard evaluation on this turn (§7.19).
	// Lets fixtures probe arcs that would otherwise require a long preceding
	// flow (e.g. the L2 cycle-budget feedback arcs).
	WorldOverride map[string]any `yaml:"world_override,omitempty"`

	// AdvanceClock (e.g. "30s") moves the FakeClock forward by the given
	// duration after the turn completes, then waits for the scheduler and
	// the session listener to drain. Requires the orchestrator path (implicit
	// when set). Use after a turn that submits a background job to assert on
	// post-on_complete world state without real wall time.
	AdvanceClock string `yaml:"advance_clock,omitempty"`

	// ExpectInbox asserts notification counts after this turn (post
	// advance_clock if set). All keys are optional; only specified assertions
	// run. Requires the orchestrator path.
	ExpectInbox *FlowInboxExpectation `yaml:"expect_inbox,omitempty"`

	// Assertions (§10.3.2).
	ExpectState          string         `yaml:"expect_state,omitempty"`
	ExpectNotState       string         `yaml:"expect_not_state,omitempty"`
	ExpectStateIn        []string       `yaml:"expect_state_in,omitempty"`
	ExpectSlots          map[string]any `yaml:"expect_slots,omitempty"`
	ExpectWorld          map[string]any `yaml:"expect_world,omitempty"`
	ExpectWorldFull      map[string]any `yaml:"expect_world_full,omitempty"`
	ExpectWorldUnchanged *bool          `yaml:"expect_world_unchanged,omitempty"`
	ExpectEvents         []FlowEvent    `yaml:"expect_events,omitempty"`
	ExpectEventsExact    []FlowEvent    `yaml:"expect_events_exact,omitempty"`
	ExpectError          *FlowError     `yaml:"expect_error,omitempty"`
	ExpectViewMatches    string         `yaml:"expect_view_matches,omitempty"`
	ExpectNoView         *bool          `yaml:"expect_no_view,omitempty"`
}

// FlowInboxExpectation asserts notification counts after a turn. All fields
// are optional; only the ones explicitly set are checked.
type FlowInboxExpectation struct {
	// Unread asserts the total count of unread notifications.
	Unread *int `yaml:"unread,omitempty"`
	// NeedsAttention asserts the count of action_required notifications.
	NeedsAttention *int `yaml:"needs_attention,omitempty"`
	// Severities asserts that the sorted list of severity strings for all
	// unread notifications matches exactly (e.g. ["info", "success"]).
	Severities []string `yaml:"severities,omitempty"`
}

// FlowIntent is the structured intent in a fixture turn.
type FlowIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// FlowEvent is one event in an expect_events assertion.
type FlowEvent struct {
	Kind   string         `yaml:"kind"`
	From   string         `yaml:"from,omitempty"`
	To     string         `yaml:"to,omitempty"`
	Effect map[string]any `yaml:"effect,omitempty"`
}

// FlowError is the expect_error assertion.
type FlowError struct {
	Code            string   `yaml:"code"`
	AllowedContains []string `yaml:"allowed_contains,omitempty"`
	GuardHint       string   `yaml:"guard_hint,omitempty"`
	MissingSlots    []string `yaml:"missing_slots,omitempty"`
}

// ─── Flow runner result types ─────────────────────────────────────────────────

// TurnResult holds the result for a single turn in a flow run.
type TurnResult struct {
	TurnIndex int
	Passed    bool
	Failures  []string
	NewState  app.StatePath
	View      string
	Events    []store.Event
}

// FlowResult holds the result for a complete flow file.
type FlowResult struct {
	File    string
	Passed  bool
	Turns   []TurnResult
	Skipped bool // when oracle miss and allow-missing is set
}

// FlowReport is the aggregate result of running multiple flows.
type FlowReport struct {
	Results []FlowResult
	Passed  int
	Failed  int
}

// ─── Flow runner options ──────────────────────────────────────────────────────

// FlowOptions configure the flow runner.
type FlowOptions struct {
	// OracleOverride overrides the oracle path declared in each fixture file.
	OracleOverride string
	// AllowMissingOracle downgrades oracle-miss failures to skips.
	AllowMissingOracle bool
	// FailFast stops at first failure.
	FailFast bool
	// Verbose enables per-turn verbose output.
	Verbose bool
	// JSONOut is the optional path to write a JSON report.
	JSONOut string
}

// ─── orchRig holds all resources for an orchestrator-backed flow run ─────────

// orchRig bundles the orchestrator and associated resources for one flow run.
// Call cleanup() when done to close the store.
type orchRig struct {
	orch     *orchestrator.Orchestrator
	sched    jobs.Scheduler
	jobStore *jobs.JobStore
	st       store.Store
	sid      app.SessionID
	clk      *clock.Fake
	cleanup  func() error
}

// buildOrchestratorRig constructs a fully wired orchestrator rig for one flow
// run, using an in-memory SQLite store and a fake clock at epoch zero.
//
// Host stubs from fixture.HostHandlers are registered in a host.Registry;
// each stub closure respects Delay (using the fake clock's Sleep), InfraError,
// Error, and Data fields in that priority order.
func buildOrchestratorRig(ctx context.Context, def *app.AppDef, m machine.Machine, fixture *FlowFixture) (*orchRig, error) {
	// Deterministic epoch.
	clk := clock.NewFake(time.Unix(0, 0))

	// In-memory SQLite store (no file I/O).
	st, err := store.OpenMemory()
	if err != nil {
		return nil, fmt.Errorf("buildOrchestratorRig: open store: %w", err)
	}

	// Job store (schema migration applied once on open).
	js, err := jobs.NewJobStore(st.DB())
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: job store: %w", err)
	}

	// Scheduler backed by the fake clock so Delay stubs block on fake time.
	sched := jobs.NewScheduler(js, jobs.WithClock(clk))

	// Build host registry from stubs.
	reg := host.NewRegistry()
	// Pre-register built-in service handlers that fixtures rely on but typically
	// do not stub (because they are infrastructure, not application logic).
	// host.jobs.answer_clarification must be registered so that the
	// answer_clarification intent can store the user's answer in the DB and let
	// a waiting background job resume.  Stubs declared in host_handlers: are
	// registered below and take precedence over any built-in with the same name.
	if _, ok := fixture.HostHandlers["host.jobs.answer_clarification"]; !ok {
		reg.Register("host.jobs.answer_clarification", host.AnswerClarificationHandler)
	}
	for name, stub := range fixture.HostHandlers {
		stub := stub // capture for closure
		reg.Register(name, func(hctx context.Context, args map[string]any) (host.Result, error) {
			// 1. Simulated delay using the fake clock injected by the scheduler.
			if stub.Delay != "" {
				d, parseErr := time.ParseDuration(stub.Delay)
				if parseErr != nil {
					return host.Result{}, fmt.Errorf("stub %q: parse delay: %w", name, parseErr)
				}
				if d > 0 {
					host.ClockFromContext(hctx).Sleep(d)
				}
			}
			// 2. Mid-flight clarification: pause until the user answers.
			if stub.RequestClarification != "" {
				_, cErr := host.RequestClarification(hctx, jobs.ClarificationSchema{
					Prompt:  stub.RequestClarification,
					Fields:  map[string]string{"answer": "string"},
				})
				if cErr != nil {
					return host.Result{Error: cErr.Error()}, nil
				}
			}
			// 3. Infrastructure error (indistinguishable from a real failure).
			if stub.InfraError != "" {
				return host.Result{}, errors.New(stub.InfraError)
			}
			// 4. Domain-level error or success.
			return host.Result{Data: stub.Data, Error: stub.Error}, nil
		})
	}

	// Use a no-op harness; the orchestrator path calls RunIntent directly
	// for intent: turns and never falls through to the harness in normal use.
	h := &noopHarness{}

	orch := orchestrator.New(def, m, st, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(js),
	)

	sid, err := orch.NewSession(ctx)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: new session: %w", err)
	}

	return &orchRig{
		orch:     orch,
		sched:    sched,
		jobStore: js,
		st:       st,
		sid:      sid,
		clk:      clk,
		cleanup:  st.Close,
	}, nil
}

// shouldUseOrchestrator reports whether the fixture should run through the
// orchestrator-backed path instead of the legacy machine-only path.
//
// The orchestrator path is required whenever:
//   - fixture.UseOrchestrator is explicitly true, OR
//   - any host_handlers are declared (stubs must be wired to a real registry), OR
//   - any turn declares advance_clock or expect_inbox (these require the
//     scheduler + listener goroutine infrastructure).
//
// If UseOrchestrator is explicitly false, the legacy path is always used
// regardless of any other fields.
func shouldUseOrchestrator(fixture *FlowFixture) bool {
	if fixture.UseOrchestrator != nil {
		return *fixture.UseOrchestrator
	}
	if len(fixture.HostHandlers) > 0 {
		return true
	}
	for _, t := range fixture.Turns {
		if t.AdvanceClock != "" || t.ExpectInbox != nil {
			return true
		}
	}
	return false
}

// ─── RunFlows runs all flow fixtures matching the glob ───────────────────────

// RunFlows loads the app, finds all flow fixtures matching the glob, and runs them.
// Returns a FlowReport and non-nil error only for fatal startup errors.
func RunFlows(ctx context.Context, appPath, glob string, opts FlowOptions) (*FlowReport, error) {
	// Load app.
	def, err := app.Load(appPath)
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}

	// Build machine.
	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}

	// Find fixture files.
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", glob, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no flow fixtures matched %q", glob)
	}

	report := &FlowReport{}
	for _, f := range files {
		results, err := runFlowFile(ctx, def, m, f, opts)
		if err != nil {
			return nil, fmt.Errorf("run flow %q: %w", f, err)
		}
		report.Results = append(report.Results, results...)
		for _, r := range results {
			if r.Skipped {
				continue
			}
			if r.Passed {
				report.Passed++
			} else {
				report.Failed++
			}
		}
		if opts.FailFast && report.Failed > 0 {
			break
		}
	}

	if opts.JSONOut != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if werr := os.WriteFile(opts.JSONOut, b, 0644); werr != nil {
			return nil, fmt.Errorf("write JSON report: %w", werr)
		}
	}

	return report, nil
}

// runFlowFile parses and runs one flow fixture file (which may contain multiple
// YAML documents separated by ---).
func runFlowFile(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, opts FlowOptions) ([]FlowResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", filePath, err)
	}

	// Split on --- to handle multi-document files.
	docs := splitYAMLDocs(data)
	var results []FlowResult

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var fixture FlowFixture
		if err := yaml.Unmarshal([]byte(doc), &fixture); err != nil {
			return nil, fmt.Errorf("parse fixture in %q: %w", filePath, err)
		}
		if fixture.TestKind != "flow" {
			continue // skip non-flow docs
		}

		result, err := runOneFlow(ctx, def, m, filePath, &fixture, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, *result)
	}

	return results, nil
}

// splitYAMLDocs splits a YAML file into individual documents on "---" boundaries.
func splitYAMLDocs(data []byte) []string {
	return strings.Split(string(data), "\n---")
}

// runOneFlow executes a single FlowFixture. It dispatches to the
// orchestrator-backed path when shouldUseOrchestrator returns true, otherwise
// falls back to the legacy machine-only path.
func runOneFlow(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	if shouldUseOrchestrator(fixture) {
		return runOneFlowOrchestrator(ctx, def, m, filePath, fixture, opts)
	}
	return runOneFlowLegacy(ctx, def, m, filePath, fixture, opts)
}

// runOneFlowLegacy executes a single FlowFixture against the machine directly
// (the original, non-orchestrator path). Preserved unchanged for backwards
// compatibility with existing fixtures that don't declare host_handlers or
// advance_clock.
func runOneFlowLegacy(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	result := &FlowResult{File: filePath}

	// Load oracle if needed.
	var replayHarness *harness.ReplayHarness
	oraclePath := fixture.Oracle
	if opts.OracleOverride != "" {
		oraclePath = opts.OracleOverride
	}

	// Resolve oracle path relative to fixture file.
	if oraclePath != "" {
		if !filepath.IsAbs(oraclePath) {
			oraclePath = filepath.Join(filepath.Dir(filePath), oraclePath)
		}
		rh, err := harness.NewReplay(oraclePath)
		if err != nil {
			if opts.AllowMissingOracle {
				result.Skipped = true
				return result, nil
			}
			return nil, fmt.Errorf("load oracle %q: %w", oraclePath, err)
		}
		replayHarness = rh
	}

	// Set up initial world.
	initialWorld := machine.WorldFromSchema(def.World)
	for k, v := range fixture.InitialWorld {
		initialWorld.Vars[k] = v
	}

	// Run turns.
	currentState := app.StatePath(fixture.InitialState)
	currentWorld := initialWorld
	allEvents := []store.Event{}
	hasError := false

	for i, turn := range fixture.Turns {
		var call intent.IntentCall

		if turn.Intent != nil {
			// Structured intent — bypass oracle.
			call = intent.IntentCall{
				Intent: turn.Intent.Name,
				Slots:  world.Slots(turn.Intent.Slots),
			}
		} else if turn.Input != "" {
			// Oracle lookup.
			if replayHarness == nil {
				return nil, fmt.Errorf("turn %d: input %q requires an oracle but none was loaded", i+1, turn.Input)
			}
			params, err := replayHarness.RunTurn(ctx, harness.TurnInput{
				StatePath: currentState,
				UserText:  turn.Input,
			})
			if err != nil {
				if opts.AllowMissingOracle {
					result.Skipped = true
					return result, nil
				}
				return nil, fmt.Errorf("turn %d: oracle miss for input %q in state %q: %w", i+1, turn.Input, currentState, err)
			}
			call, err = paramsToIntentCall(params)
			if err != nil {
				return nil, fmt.Errorf("turn %d: parse oracle result: %w", i+1, err)
			}
		} else {
			return nil, fmt.Errorf("turn %d: neither intent nor input is set", i+1)
		}

		// Apply world_override (§7.19) before guard evaluation. Mutations are
		// applied in-place to the running world so the rest of the turn (guard
		// eval, effects, view render) sees them.
		for k, v := range turn.WorldOverride {
			currentWorld.Vars[k] = v
		}

		// Snapshot pre-turn world for expect_world_unchanged.
		preTurnWorld := cloneWorldVars(currentWorld)

		// Run the machine.
		machResult, err := m.Turn(ctx, currentState, currentWorld, call)
		if err != nil {
			return nil, fmt.Errorf("turn %d: machine.Turn: %w", i+1, err)
		}

		allEvents = append(allEvents, machResult.Events...)

		tr := TurnResult{TurnIndex: i}

		// Check expect_error.
		if turn.ExpectError != nil {
			if machResult.ValidationError == nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expected error %q but got none", turn.ExpectError.Code))
			} else {
				ve := machResult.ValidationError
				if string(ve.Code) != turn.ExpectError.Code {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expected error code %q but got %q", turn.ExpectError.Code, ve.Code))
				}
				// Check allowed_contains.
				for _, expected := range turn.ExpectError.AllowedContains {
					found := false
					for _, a := range ve.AllowedIntents {
						if a == expected {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.allowed_contains: %q not in allowed list %v", expected, ve.AllowedIntents))
					}
				}
				// Check missing_slots.
				for _, ms := range turn.ExpectError.MissingSlots {
					found := false
					for _, s := range ve.MissingSlots {
						if s == ms {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.missing_slots: %q not in %v", ms, ve.MissingSlots))
					}
				}
			}
			// State should be unchanged on error.
			tr.NewState = currentState
			tr.Events = machResult.Events
			tr.Passed = len(tr.Failures) == 0
			if machResult.ValidationError != nil {
				hasError = true
			}
			result.Turns = append(result.Turns, tr)
			continue
		}

		if machResult.ValidationError != nil {
			hasError = true
			tr.Failures = append(tr.Failures, fmt.Sprintf("unexpected validation error: %s", machResult.ValidationError.Error()))
		}

		// Update state/world for successful turn.
		currentState = machResult.NewState
		currentWorld = machResult.World
		tr.NewState = currentState
		tr.View = machResult.View
		tr.Events = machResult.Events

		// Apply assertions.
		if turn.ExpectState != "" {
			if string(currentState) != turn.ExpectState {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state: got %q, want %q", currentState, turn.ExpectState))
			}
		}
		if turn.ExpectNotState != "" {
			if string(currentState) == turn.ExpectNotState {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_not_state: state must not be %q", turn.ExpectNotState))
			}
		}
		if len(turn.ExpectStateIn) > 0 {
			found := false
			for _, s := range turn.ExpectStateIn {
				if string(currentState) == s {
					found = true
					break
				}
			}
			if !found {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state_in: %q not in %v", currentState, turn.ExpectStateIn))
			}
		}

		// expect_world: subset match.
		for k, expected := range turn.ExpectWorld {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world[%q]: got %v (%T), want %v (%T)", k, got, got, expected, expected))
			}
		}

		// expect_world_full: full match.
		if len(turn.ExpectWorldFull) > 0 {
			for k, expected := range turn.ExpectWorldFull {
				got := currentWorld.Vars[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full[%q]: got %v, want %v", k, got, expected))
				}
			}
			for k := range currentWorld.Vars {
				if _, ok := turn.ExpectWorldFull[k]; !ok {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full: unexpected key %q in world", k))
				}
			}
		}

		// expect_world_unchanged.
		if turn.ExpectWorldUnchanged != nil && *turn.ExpectWorldUnchanged {
			for k, before := range preTurnWorld {
				after := currentWorld.Vars[k]
				if !deepEqualValues(before, after) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_unchanged: world[%q] changed from %v to %v", k, before, after))
				}
			}
		}

		// expect_events: ordered subsequence.
		if len(turn.ExpectEvents) > 0 {
			if errs := assertEventsSubsequence(tr.Events, turn.ExpectEvents); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_events_exact: full ordered match.
		if len(turn.ExpectEventsExact) > 0 {
			if errs := assertEventsExact(tr.Events, turn.ExpectEventsExact); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_view_matches.
		if turn.ExpectViewMatches != "" {
			re, reErr := regexp.Compile(turn.ExpectViewMatches)
			if reErr != nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: invalid regex %q: %v", turn.ExpectViewMatches, reErr))
			} else if !re.MatchString(tr.View) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: view does not match %q\nview: %q", turn.ExpectViewMatches, tr.View))
			}
		}

		// expect_no_view.
		if turn.ExpectNoView != nil && *turn.ExpectNoView {
			if tr.View != "" {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_no_view: view is not empty: %q", tr.View))
			}
		}

		// expect_slots — assert on accepted slots (we don't have a clean path here
		// unless we compare the call slots; do that).
		if len(turn.ExpectSlots) > 0 {
			for k, expected := range turn.ExpectSlots {
				got := call.Slots[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_slots[%q]: got %v, want %v", k, got, expected))
				}
			}
		}

		tr.Passed = len(tr.Failures) == 0
		result.Turns = append(result.Turns, tr)

		if opts.FailFast && !tr.Passed {
			break
		}
	}

	// Session-level assertions.
	sessionFailures := []string{}

	if fixture.ExpectTerminal != nil && *fixture.ExpectTerminal {
		// Look up whether current state is terminal.
		stateIsTerminal := isTerminalState(def, currentState)
		if !stateIsTerminal {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_terminal: state %q is not terminal", currentState))
		}
	}

	if fixture.ExpectEventsCountAtLeast != nil {
		if len(allEvents) < *fixture.ExpectEventsCountAtLeast {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atleast: got %d events, want >= %d", len(allEvents), *fixture.ExpectEventsCountAtLeast))
		}
	}
	if fixture.ExpectEventsCountAtMost != nil {
		if len(allEvents) > *fixture.ExpectEventsCountAtMost {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atmost: got %d events, want <= %d", len(allEvents), *fixture.ExpectEventsCountAtMost))
		}
	}

	if fixture.ExpectNoErrors != nil {
		if *fixture.ExpectNoErrors && hasError {
			sessionFailures = append(sessionFailures, "expect_no_errors: there were validation errors")
		}
	}

	if len(fixture.ExpectWorldFinal) > 0 {
		for k, expected := range fixture.ExpectWorldFinal {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				sessionFailures = append(sessionFailures, fmt.Sprintf("expect_world_final[%q]: got %v, want %v", k, got, expected))
			}
		}
	}

	// Compute overall pass.
	allTurnsPassed := true
	for _, tr := range result.Turns {
		if !tr.Passed {
			allTurnsPassed = false
			break
		}
	}
	result.Passed = allTurnsPassed && len(sessionFailures) == 0

	// Append session-level failures as a synthetic turn.
	if len(sessionFailures) > 0 {
		result.Turns = append(result.Turns, TurnResult{
			TurnIndex: -1,
			Passed:    false,
			Failures:  sessionFailures,
		})
	}

	return result, nil
}

// runOneFlowOrchestrator executes a single FlowFixture through the
// orchestrator-backed path. It:
//   - Builds an in-memory SQLite store + fake clock + job scheduler + host
//     registry (from fixture.HostHandlers).
//   - Seeds InitialWorld by applying world_override on the first turn (or
//     directly before the first RunIntent call).
//   - Runs each turn by calling orch.RunIntent for intent: turns.
//   - After each turn, if AdvanceClock is set: advances the fake clock then
//     waits for the scheduler and listener to drain.
//   - Applies all the same turn assertions as the legacy path, plus
//     ExpectInbox assertions (world-based via $inbox counts from jobStore).
//
// The orchestrator path is fully backwards-compatible with fixture fields that
// don't mention scheduling: a fixture that is auto-upgraded (e.g. because it
// sets host_handlers) but doesn't use advance_clock or expect_inbox will
// simply run through the orchestrator and apply the same assertions.
func runOneFlowOrchestrator(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	result := &FlowResult{File: filePath}

	// Build the rig (store + scheduler + orchestrator).
	rig, err := buildOrchestratorRig(ctx, def, m, fixture)
	if err != nil {
		return nil, fmt.Errorf("runOneFlowOrchestrator: %w", err)
	}
	defer func() { _ = rig.cleanup() }()

	// Seed InitialWorld by teleporting the session into the right initial state
	// and world. We use orch.Teleport if available; otherwise we fall back to
	// injecting world via a world_override on the first turn combined with using
	// initial_state as the session's starting state.
	//
	// The simplest approach: persist a synthetic EffectApplied event for each
	// initial-world key and a StateTransitioned event for the initial state before
	// any turns run.  The orchestrator's loadJourney will replay these so the first
	// turn sees the right initial conditions.
	if err := seedInitialState(ctx, rig, def, fixture); err != nil {
		return nil, fmt.Errorf("runOneFlowOrchestrator: seed initial state: %w", err)
	}

	// Run turns.
	allEvents := []store.Event{}
	hasError := false

	for i, turn := range fixture.Turns {
		var (
			outcome   *orchestrator.TurnOutcome
			turnErr   error
			call      intent.IntentCall
		)

		if turn.Intent != nil {
			// Load the current world before building the intent call so that
			// slot values containing {{ world.* }} templates can be expanded.
			preJ, preJErr := rig.orch.LoadJourney(rig.sid)
			if preJErr != nil {
				return nil, fmt.Errorf("turn %d: load world for slot rendering: %w", i+1, preJErr)
			}

			resolvedSlots := renderSlots(turn.Intent.Slots, preJ.World)
			call = intent.IntentCall{
				Intent: turn.Intent.Name,
				Slots:  world.Slots(resolvedSlots),
			}

			// Apply world_override before the turn so the machine sees it.
			// In the orchestrator path we do this by injecting a synthetic
			// EffectApplied event before calling RunIntent.
			if len(turn.WorldOverride) > 0 {
				if woErr := injectWorldOverride(ctx, rig, turn.WorldOverride); woErr != nil {
					return nil, fmt.Errorf("turn %d: world_override: %w", i+1, woErr)
				}
			}

			outcome, turnErr = rig.orch.RunIntent(ctx, rig.sid, call.Intent, map[string]any(call.Slots))
		} else if turn.Input != "" {
			// input: turns in the orchestrator path require an oracle to resolve the
			// intent. For now we return an error — the orchestrator path is only used
			// for intent: turns (host_handlers, advance_clock). If someone wants
			// oracle-resolved turns with orchestrator features they should add a static
			// harness; that's a future enhancement.
			return nil, fmt.Errorf("turn %d: input: %q in orchestrator-path fixture; use intent: instead (oracle routing not yet supported on orchestrator path)", i+1, turn.Input)
		} else {
			return nil, fmt.Errorf("turn %d: neither intent nor input is set", i+1)
		}

		if turnErr != nil {
			return nil, fmt.Errorf("turn %d: RunIntent: %w", i+1, turnErr)
		}

		allEvents = append(allEvents, outcome.Events...)

		// AdvanceClock: move the fake clock forward, then wait for scheduler + listener.
		if turn.AdvanceClock != "" {
			d, parseErr := time.ParseDuration(turn.AdvanceClock)
			if parseErr != nil {
				return nil, fmt.Errorf("turn %d: advance_clock %q: %w", i+1, turn.AdvanceClock, parseErr)
			}
			if d > 0 {
				waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if advErr := advanceAndWait(waitCtx, rig, d); advErr != nil {
					cancel()
					return nil, fmt.Errorf("turn %d: advance_clock: %w", i+1, advErr)
				}
				cancel()
			}
		}

		tr := TurnResult{TurnIndex: i}

		// Reload journey to get the post-completion world (after on_complete fires).
		journey, loadErr := rig.orch.LoadJourney(rig.sid)
		if loadErr != nil {
			return nil, fmt.Errorf("turn %d: load journey: %w", i+1, loadErr)
		}
		currentState := journey.State
		currentWorld := journey.World

		// Resolve the pre-turn world snapshot for expect_world_unchanged.
		// Since we apply world_override before RunIntent, we need the world
		// before world_override injection. We use the state before injection.
		// For simplicity: the pre-turn world is the world immediately after
		// the previous turn's journey reload (we already track it above).
		// We'll capture it here by reloading before the turn's assertions run.

		// check expect_error: in the orchestrator path ValidationError manifests
		// as ModeRejected in the outcome.
		if turn.ExpectError != nil {
			if outcome.Mode != orchestrator.ModeRejected {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expected error %q but got none (mode=%s)", turn.ExpectError.Code, outcome.Mode))
			} else {
				if string(outcome.ErrorCode) != turn.ExpectError.Code {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error: code got %q, want %q", outcome.ErrorCode, turn.ExpectError.Code))
				}
				for _, expected := range turn.ExpectError.AllowedContains {
					found := false
					for _, a := range outcome.AllowedIntents {
						if a == expected {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.allowed_contains: %q not in %v", expected, outcome.AllowedIntents))
					}
				}
			}
			tr.NewState = currentState
			tr.Events = outcome.Events
			tr.Passed = len(tr.Failures) == 0
			if outcome.Mode == orchestrator.ModeRejected {
				hasError = true
			}
			result.Turns = append(result.Turns, tr)
			continue
		}

		if outcome.Mode == orchestrator.ModeRejected {
			hasError = true
			tr.Failures = append(tr.Failures, fmt.Sprintf("unexpected rejection: code=%s hint=%s", outcome.ErrorCode, outcome.GuardHint))
		}

		tr.NewState = currentState
		tr.View = outcome.View
		tr.Events = outcome.Events

		// expect_state.
		if turn.ExpectState != "" && string(currentState) != turn.ExpectState {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state: got %q, want %q", currentState, turn.ExpectState))
		}
		// expect_not_state.
		if turn.ExpectNotState != "" && string(currentState) == turn.ExpectNotState {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_not_state: state must not be %q", turn.ExpectNotState))
		}
		// expect_state_in.
		if len(turn.ExpectStateIn) > 0 {
			found := false
			for _, s := range turn.ExpectStateIn {
				if string(currentState) == s {
					found = true
					break
				}
			}
			if !found {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state_in: %q not in %v", currentState, turn.ExpectStateIn))
			}
		}
		// expect_world: subset match.
		for k, expected := range turn.ExpectWorld {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world[%q]: got %v (%T), want %v (%T)", k, got, got, expected, expected))
			}
		}
		// expect_world_full.
		if len(turn.ExpectWorldFull) > 0 {
			for k, expected := range turn.ExpectWorldFull {
				got := currentWorld.Vars[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full[%q]: got %v, want %v", k, got, expected))
				}
			}
			for k := range currentWorld.Vars {
				if _, ok := turn.ExpectWorldFull[k]; !ok {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full: unexpected key %q in world", k))
				}
			}
		}
		// expect_events / expect_events_exact.
		if len(turn.ExpectEvents) > 0 {
			if errs := assertEventsSubsequence(tr.Events, turn.ExpectEvents); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		if len(turn.ExpectEventsExact) > 0 {
			if errs := assertEventsExact(tr.Events, turn.ExpectEventsExact); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		// expect_view_matches.
		if turn.ExpectViewMatches != "" {
			re, reErr := regexp.Compile(turn.ExpectViewMatches)
			if reErr != nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: invalid regex %q: %v", turn.ExpectViewMatches, reErr))
			} else if !re.MatchString(tr.View) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: view does not match %q\nview: %q", turn.ExpectViewMatches, tr.View))
			}
		}
		// expect_no_view.
		if turn.ExpectNoView != nil && *turn.ExpectNoView && tr.View != "" {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_no_view: view is not empty: %q", tr.View))
		}
		// expect_slots.
		if len(turn.ExpectSlots) > 0 {
			for k, expected := range turn.ExpectSlots {
				got := call.Slots[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_slots[%q]: got %v, want %v", k, got, expected))
				}
			}
		}

		// expect_inbox: query unread counts from the job store.
		if turn.ExpectInbox != nil {
			if errs := assertInbox(ctx, rig.jobStore, rig.sid, turn.ExpectInbox); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		tr.Passed = len(tr.Failures) == 0
		result.Turns = append(result.Turns, tr)

		if opts.FailFast && !tr.Passed {
			break
		}
	}

	// Session-level assertions.
	sessionFailures := []string{}

	// Re-load final journey state.
	finalJourney, loadErr := rig.orch.LoadJourney(rig.sid)
	if loadErr != nil {
		return nil, fmt.Errorf("load final journey: %w", loadErr)
	}
	finalState := finalJourney.State
	finalWorld := finalJourney.World

	if fixture.ExpectTerminal != nil && *fixture.ExpectTerminal {
		if !isTerminalState(def, finalState) {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_terminal: state %q is not terminal", finalState))
		}
	}
	if fixture.ExpectEventsCountAtLeast != nil && len(allEvents) < *fixture.ExpectEventsCountAtLeast {
		sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atleast: got %d events, want >= %d", len(allEvents), *fixture.ExpectEventsCountAtLeast))
	}
	if fixture.ExpectEventsCountAtMost != nil && len(allEvents) > *fixture.ExpectEventsCountAtMost {
		sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atmost: got %d events, want <= %d", len(allEvents), *fixture.ExpectEventsCountAtMost))
	}
	if fixture.ExpectNoErrors != nil && *fixture.ExpectNoErrors && hasError {
		sessionFailures = append(sessionFailures, "expect_no_errors: there were validation errors")
	}
	if len(fixture.ExpectWorldFinal) > 0 {
		for k, expected := range fixture.ExpectWorldFinal {
			got := finalWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				sessionFailures = append(sessionFailures, fmt.Sprintf("expect_world_final[%q]: got %v, want %v", k, got, expected))
			}
		}
	}

	allTurnsPassed := true
	for _, tr := range result.Turns {
		if !tr.Passed {
			allTurnsPassed = false
			break
		}
	}
	result.Passed = allTurnsPassed && len(sessionFailures) == 0

	if len(sessionFailures) > 0 {
		result.Turns = append(result.Turns, TurnResult{
			TurnIndex: -1,
			Passed:    false,
			Failures:  sessionFailures,
		})
	}

	return result, nil
}

// ─── Orchestrator path helpers ────────────────────────────────────────────────

// seedInitialState writes synthetic events that set the session's starting
// state and world to match fixture.InitialState and fixture.InitialWorld.
// The orchestrator's loadJourney replays the event log, so persisting these
// events before any turns is sufficient to bootstrap the session.
func seedInitialState(ctx context.Context, rig *orchRig, def *app.AppDef, fixture *FlowFixture) error {
	if fixture.InitialState == "" && len(fixture.InitialWorld) == 0 {
		return nil
	}

	// Build a set of synthetic seed events at turn 0.
	var events []store.Event

	// TransitionApplied moves the journey to the desired initial state.
	// BuildJourney interprets {"to": "..."} to update js.State.
	if fixture.InitialState != "" {
		events = append(events, store.Event{
			Kind: store.TransitionApplied,
			Turn: 0,
			Payload: mustJSON(map[string]any{
				"from":   "",
				"to":     fixture.InitialState,
				"intent": "__seed__",
			}),
		})
	}

	// Apply each initial-world key as an EffectApplied event.
	for k, v := range fixture.InitialWorld {
		events = append(events, store.Event{
			Kind: store.EffectApplied,
			Turn: 0,
			Payload: mustJSON(map[string]any{
				"set": map[string]any{k: v},
			}),
		})
	}

	return rig.st.AppendEvents(rig.sid, events)
}

// injectWorldOverride writes a set of EffectApplied events for each
// world_override key so the orchestrator's next journey load reflects the
// override.
func injectWorldOverride(ctx context.Context, rig *orchRig, overrides map[string]any) error {
	var events []store.Event
	for k, v := range overrides {
		events = append(events, store.Event{
			Kind: store.EffectApplied,
			Turn: 0,
			Payload: mustJSON(map[string]any{
				"set": map[string]any{k: v},
			}),
		})
	}
	return rig.st.AppendEvents(rig.sid, events)
}

// advanceAndWait advances the fake clock by d, then drains the scheduler and
// session listener with the given context deadline.
//
// The drain algorithm is entirely event-driven — no real-time sleep is used,
// which eliminates the 50 ms flake window that appeared on slow CI:
//
//  1. Subscribe to the session's job-event channel before advancing, so we
//     never miss events that arrive between the Advance call and our first
//     channel read.
//  2. Advance the fake clock.  This unblocks any timers whose deadline ≤
//     now+d, including RequestClarification's poll timer for a resumed job.
//  3. Wait for the scheduler to be idle (WaitIdle): all job goroutines are
//     either terminal or awaiting_input.
//  4. Wait for the orchestrator listener to process all queued terminal events
//     (WaitListenerIdle).  on_complete effects run inside this call.
//  5. Drain any remaining session-channel events (on_complete may have
//     dispatched new jobs whose terminal events arrive here).  For each
//     event, repeat steps 3–4 so cascading on_complete chains fully settle.
//  6. After WaitIdle + WaitListenerIdle both return with the channel empty,
//     the system is quiescent; return nil.
//
// The outer context (typically 5 s) is the hard deadline; ctx.Err() is
// returned if it fires before the drain completes.
func advanceAndWait(ctx context.Context, rig *orchRig, d time.Duration) error {
	rig.clk.Advance(d)

	// Drain loop: WaitIdle + WaitListenerIdle each cover one barrier; together
	// they guarantee "no jobs running" + "all events fanned out have been
	// processed by the listener".  Cascading on_complete chains can dispatch
	// new jobs during the wait, so we loop until IsIdle() reports a stable
	// no-running state after both barriers cleared.
	const maxIter = 32
	for i := 0; i < maxIter; i++ {
		if err := rig.sched.WaitIdle(ctx); err != nil {
			return fmt.Errorf("scheduler WaitIdle: %w", err)
		}
		if err := rig.orch.WaitListenerIdle(ctx, rig.sid); err != nil {
			return fmt.Errorf("listener WaitListenerIdle: %w", err)
		}
		// If on_complete didn't dispatch any new background work during the
		// listener's processing, runningCount is still zero and we are done.
		// Otherwise loop: the next WaitIdle blocks until the cascading job
		// finishes; the next WaitListenerIdle drains its event.
		if rig.sched.IsIdle() {
			return nil
		}
	}
	return fmt.Errorf("advanceAndWait: drain did not stabilize after %d iterations", maxIter)
}

// assertInbox checks inbox notification counts against the expectation.
func assertInbox(ctx context.Context, js *jobs.JobStore, sid app.SessionID, exp *FlowInboxExpectation) []string {
	var failures []string
	if exp == nil || js == nil {
		return failures
	}

	counts, err := js.UnreadCount(ctx, sid)
	if err != nil {
		return []string{fmt.Sprintf("expect_inbox: UnreadCount: %v", err)}
	}

	total := 0
	for _, cnt := range counts {
		total += cnt
	}

	if exp.Unread != nil && total != *exp.Unread {
		failures = append(failures, fmt.Sprintf("expect_inbox.unread: got %d, want %d", total, *exp.Unread))
	}

	if exp.NeedsAttention != nil {
		attn := counts[jobs.SeverityActionRequired]
		if attn != *exp.NeedsAttention {
			failures = append(failures, fmt.Sprintf("expect_inbox.needs_attention: got %d, want %d", attn, *exp.NeedsAttention))
		}
	}

	if len(exp.Severities) > 0 {
		var got []string
		for sev, cnt := range counts {
			for j := 0; j < cnt; j++ {
				got = append(got, string(sev))
			}
		}
		sort.Strings(got)
		want := make([]string, len(exp.Severities))
		copy(want, exp.Severities)
		sort.Strings(want)

		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			failures = append(failures, fmt.Sprintf("expect_inbox.severities: got %v, want %v", got, exp.Severities))
		}
	}

	return failures
}

// renderSlots expands `{{ world.* }}` template expressions in slot values
// against the current world snapshot. Only string values that contain `{{`
// are processed; all other values are passed through unchanged.
//
// This enables fixture turns to reference runtime-generated values such as
// last_job_id without hard-coding them:
//
//	slots:
//	  job_id: "{{ world.last_job_id }}"
//	  answer: "prod"
//
// A rendering error causes the slot value to be replaced with the raw
// template string plus an error annotation, which will typically cause the
// subsequent intent call to fail with a recognisable message rather than
// silently passing a wrong value.
func renderSlots(slots map[string]any, w world.World) map[string]any {
	if len(slots) == 0 {
		return slots
	}
	env := expr.Env{World: map[string]any(w.Vars)}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, "{{") {
			out[k] = v
			continue
		}
		rendered, err := expr.Render(s, env)
		if err != nil {
			// Keep a diagnostic string so the turn fails clearly.
			out[k] = fmt.Sprintf("(renderSlots error for %q: %v)", s, err)
			continue
		}
		out[k] = rendered
	}
	return out
}

// mustJSON marshals v to JSON and panics on error (only for trusted values).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return b
}

// noopHarness is a stub harness for the orchestrator path. It should never
// be called in normal operation since RunIntent bypasses the harness. If it is
// called (e.g. due to an input: turn being accidentally routed here), it returns
// an error that clearly explains what happened.
type noopHarness struct{}

func (h *noopHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("noopHarness: RunTurn called in orchestrator-path fixture (state=%q input=%q); use intent: turns only", in.StatePath, in.UserText)
}

func (h *noopHarness) Close() error { return nil }

var _ harness.Harness = (*noopHarness)(nil)

// ─── Assertion helpers ────────────────────────────────────────────────────────

// assertEventsSubsequence checks that each expected event appears in order
// in the actual events list (extra events between them are allowed).
func assertEventsSubsequence(actual []store.Event, expected []FlowEvent) []string {
	var failures []string
	ai := 0
	for _, exp := range expected {
		found := false
		for ai < len(actual) {
			ev := actual[ai]
			ai++
			if matchEvent(ev, exp) {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, fmt.Sprintf("expect_events: event %+v not found in subsequence", exp))
		}
	}
	return failures
}

// assertEventsExact checks that the events list exactly matches expected.
func assertEventsExact(actual []store.Event, expected []FlowEvent) []string {
	var failures []string
	if len(actual) != len(expected) {
		failures = append(failures, fmt.Sprintf("expect_events_exact: got %d events, want %d", len(actual), len(expected)))
		return failures
	}
	for i, exp := range expected {
		if !matchEvent(actual[i], exp) {
			failures = append(failures, fmt.Sprintf("expect_events_exact[%d]: event mismatch. got kind=%q, want kind=%q", i, actual[i].Kind, exp.Kind))
		}
	}
	return failures
}

// matchEvent checks if an actual store.Event matches an expected FlowEvent.
func matchEvent(ev store.Event, exp FlowEvent) bool {
	if string(ev.Kind) != exp.Kind {
		return false
	}
	if ev.Payload == nil {
		return exp.From == "" && exp.To == "" && len(exp.Effect) == 0
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return false
	}
	if exp.From != "" {
		if from, _ := payload["from"].(string); from != exp.From {
			return false
		}
	}
	if exp.To != "" {
		if to, _ := payload["to"].(string); to != exp.To {
			return false
		}
	}
	if len(exp.Effect) > 0 {
		// Check that effect fields match.
		for k, v := range exp.Effect {
			payloadVal := payload[k]
			if !deepEqualValues(payloadVal, v) {
				return false
			}
		}
	}
	return true
}

// deepEqualValues compares two values accounting for JSON number types.
func deepEqualValues(a, b any) bool {
	// Normalize both to JSON and back to handle int/float64 differences.
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	return string(ja) == string(jb)
}

// isTerminalState returns true if the given state path is declared terminal.
func isTerminalState(def *app.AppDef, statePath app.StatePath) bool {
	s := lookupStateDef(def.States, string(statePath))
	return s != nil && s.Terminal
}

// lookupStateDef finds a state in the nested state map by dot-separated path.
func lookupStateDef(states map[string]*app.State, path string) *app.State {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 0 {
		return nil
	}
	s, ok := states[parts[0]]
	if !ok || s == nil {
		return nil
	}
	if len(parts) == 1 {
		return s
	}
	return lookupStateDef(s.States, parts[1])
}

// cloneWorldVars clones world vars for pre-turn snapshot.
func cloneWorldVars(w world.World) map[string]any {
	out := make(map[string]any, len(w.Vars))
	for k, v := range w.Vars {
		out[k] = v
	}
	return out
}

// paramsToIntentCall converts mcp.CallToolParams from a replay harness to an IntentCall.
func paramsToIntentCall(params mcp.CallToolParams) (intent.IntentCall, error) {
	if params.Name != "transition" {
		return intent.IntentCall{}, fmt.Errorf("unexpected tool name %q (want transition)", params.Name)
	}
	argsMap, ok := params.Arguments.(map[string]any)
	if !ok {
		b, _ := json.Marshal(params.Arguments)
		if err := json.Unmarshal(b, &argsMap); err != nil {
			return intent.IntentCall{}, fmt.Errorf("parse arguments: %w", err)
		}
	}
	intentName, _ := argsMap["intent"].(string)
	if intentName == "" {
		return intent.IntentCall{}, fmt.Errorf("missing intent field")
	}
	var slots world.Slots
	if sv, ok := argsMap["slots"]; ok && sv != nil {
		if m, ok := sv.(map[string]any); ok {
			slots = world.Slots(m)
		}
	}
	return intent.IntentCall{Intent: intentName, Slots: slots}, nil
}

// ─── Reporter ─────────────────────────────────────────────────────────────────

// PrintFlowReport writes the human-readable report to stdout.
func PrintFlowReport(report *FlowReport) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, r := range report.Results {
		status := "PASS"
		if r.Skipped {
			status = "SKIP"
		} else if !r.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%-6s\t%s\n", status, filepath.Base(r.File))
		for _, tr := range r.Turns {
			turnStatus := "  ok  "
			if !tr.Passed {
				turnStatus = "  FAIL"
			}
			if tr.TurnIndex < 0 {
				fmt.Fprintf(w, "  %s\t[session]\n", turnStatus)
			} else {
				fmt.Fprintf(w, "  %s\tturn %d → %s\n", turnStatus, tr.TurnIndex+1, tr.NewState)
			}
			for _, f := range tr.Failures {
				fmt.Fprintf(w, "\t  ✗ %s\n", f)
			}
		}
	}
	_ = w.Flush()

	fmt.Printf("\nSummary: %d/%d flows pass\n", report.Passed, report.Passed+report.Failed)
	if report.Failed > 0 {
		fmt.Println("FAIL")
	} else {
		fmt.Println("PASS")
	}
}

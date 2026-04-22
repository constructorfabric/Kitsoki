// Package testrunner implements the Mode 2 (deterministic flow tests) and
// Mode 1 (input→intent pass-rate tests) runners described in §10.
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/intent"
	"hally/internal/machine"
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

	// Session-level assertions.
	ExpectTerminal           *bool `yaml:"expect_terminal,omitempty"`
	ExpectEventsCountAtLeast *int  `yaml:"expect_events_count_atleast,omitempty"`
	ExpectEventsCountAtMost  *int  `yaml:"expect_events_count_atmost,omitempty"`
	ExpectNoErrors           *bool `yaml:"expect_no_errors,omitempty"`
	ExpectWorldFinal         map[string]any `yaml:"expect_world_final,omitempty"`
}

// FlowTurn is one turn in the fixture.
type FlowTurn struct {
	// Exactly one of Intent or Input must be set.
	Intent *FlowIntent `yaml:"intent,omitempty"`
	Input  string      `yaml:"input,omitempty"`

	// Assertions (§10.3.2).
	ExpectState        string         `yaml:"expect_state,omitempty"`
	ExpectNotState     string         `yaml:"expect_not_state,omitempty"`
	ExpectStateIn      []string       `yaml:"expect_state_in,omitempty"`
	ExpectSlots        map[string]any `yaml:"expect_slots,omitempty"`
	ExpectWorld        map[string]any `yaml:"expect_world,omitempty"`
	ExpectWorldFull    map[string]any `yaml:"expect_world_full,omitempty"`
	ExpectWorldUnchanged *bool        `yaml:"expect_world_unchanged,omitempty"`
	ExpectEvents       []FlowEvent    `yaml:"expect_events,omitempty"`
	ExpectEventsExact  []FlowEvent    `yaml:"expect_events_exact,omitempty"`
	ExpectError        *FlowError     `yaml:"expect_error,omitempty"`
	ExpectViewMatches  string         `yaml:"expect_view_matches,omitempty"`
	ExpectNoView       *bool          `yaml:"expect_no_view,omitempty"`
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

// runOneFlow executes a single FlowFixture against the machine.
func runOneFlow(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
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

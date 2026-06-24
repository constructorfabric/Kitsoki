// Mode 1 (input→intent pass-rate) runner.
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// ─── Recording YAML types (local copies matching harness.recordingFile) ─────────────

// recordingFile is the top-level recording YAML document.
type recordingFile struct {
	Kind          string           `yaml:"kind"`
	AppID         string           `yaml:"app_id"`
	AppVersion    string           `yaml:"app_version"`
	GeneratedAt   string           `yaml:"generated_at"`
	Generator     string           `yaml:"generator"`
	MinConfidence float64          `yaml:"min_confidence"`
	Entries       []recordingEntry `yaml:"entries"`
}

// recordingEntry is one row in the recording YAML.
type recordingEntry struct {
	State      string          `yaml:"state"`
	Input      string          `yaml:"input"`
	Intent     recordingIntent `yaml:"intent"`
	Confidence float64         `yaml:"confidence"`
	MajorityOf int             `yaml:"majority_of"`
}

// recordingIntent holds the intent name and slot map in a recording entry.
type recordingIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// ─── Intent fixture YAML format ──────────────────────────────────────────────

// IntentFixtureFile is the top-level document in an intent fixture file.
type IntentFixtureFile struct {
	TestKind string          `yaml:"test_kind"`
	App      string          `yaml:"app"`
	State    string          `yaml:"state"`
	Defaults IntentDefaults  `yaml:"defaults"`
	Fixtures []IntentFixture `yaml:"fixtures"`
}

// IntentDefaults holds per-file defaults for runs, pass rate, and temperature.
type IntentDefaults struct {
	Runs        int     `yaml:"runs"`
	MinPassRate float64 `yaml:"min_pass_rate"`
	Temperature float64 `yaml:"temperature"`
}

// IntentFixture is one fixture group inside an intent file.
type IntentFixture struct {
	ID                string         `yaml:"id"`
	Intent            *IntentExpect  `yaml:"intent,omitempty"`
	Slots             map[string]any `yaml:"slots,omitempty"`
	Inputs            []string       `yaml:"inputs"`
	Runs              int            `yaml:"runs,omitempty"` // per-fixture override
	MinPassRate       float64        `yaml:"min_pass_rate,omitempty"`
	ExpectFailure     *ExpectFailure `yaml:"expect_failure,omitempty"`
	ExpectFallthrough bool           `yaml:"expect_fallthrough,omitempty"`
}

// IntentExpect holds the expected intent name and slots.
type IntentExpect struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// ExpectFailure declares that the fixture should produce a failure.
type ExpectFailure struct {
	AnyOf []string `yaml:"any_of"`
}

// ─── Intent runner result types ───────────────────────────────────────────────

// InputResult holds the result for a single (input, state) pair.
type InputResult struct {
	Input             string
	Runs              int
	Passed            int
	PassRate          float64
	BelowMin          bool
	DurationMillis    int64
	CostUSD           float64
	FirstUsage        map[string]any `json:",omitempty"`
	FirstError        string         `json:",omitempty"`
	FirstActualIntent string         `json:",omitempty"`
	FirstActualSlots  map[string]any `json:",omitempty"`
}

// FixtureResult holds the aggregate result for one fixture group.
type FixtureResult struct {
	ID          string
	State       string
	MinPassRate float64
	Inputs      []InputResult
	TotalRuns   int
	TotalPassed int
	PassRate    float64
	Passed      bool
}

// IntentReport is the aggregate result of running all intent fixtures.
type IntentReport struct {
	Fixtures         []FixtureResult
	TotalPassed      int
	TotalFailed      int
	TotalInputs      int
	TotalRuns        int
	DurationMillis   int64
	CostUSD          float64
	HarnessType      string `json:",omitempty"`
	HarnessModel     string `json:",omitempty"`
	AgentBackend     string `json:",omitempty"`
	ProfileName      string `json:",omitempty"`
	Regressions      []string
	RecordingEmitted bool
}

// ─── Intent runner options ────────────────────────────────────────────────────

// IntentOptions configure the intent runner.
type IntentOptions struct {
	// Glob is the glob pattern for intent fixture files.
	Glob string
	// Runs overrides the default runs per input globally.
	Runs int
	// DryRun prints plan without running.
	DryRun bool
	// MaxCostUSD is the estimated cost cap (0 = no limit).
	MaxCostUSD float64
	// OnlyState filters to matching state.
	OnlyState string
	// EmitRecording writes the recording YAML to this path.
	EmitRecording string
	// BaselinePath is the baseline JSON for regression tracking.
	BaselinePath string
	// UpdateBaseline writes a new baseline after running.
	UpdateBaseline bool
	// RegressionThreshold is the allowed drop from baseline (default 5%).
	RegressionThreshold float64
	// JSONOut is the optional path to write a JSON report.
	JSONOut string
	// HarnessType selects the harness: "live", "static".
	HarnessType string
	// HarnessModel records the selected model in JSON reports for benchmark provenance.
	HarnessModel string
	// AgentBackend records the selected CLI backend for benchmark provenance.
	AgentBackend string
	// ProfileName records the harness profile used for benchmark provenance.
	ProfileName string
	// LiveHarnessFactory builds a model-backed harness after the story has
	// loaded. The app definition is required because the routing schema and
	// prompt are story-specific.
	LiveHarnessFactory func(*app.AppDef) (harness.Harness, error)
	// StaticHarnessImpl is used when HarnessType == "static".
	StaticHarnessImpl harness.Harness
	// SkipOnRecordingMiss skips inputs that are not in the recording rather than
	// counting them as failures. Useful when running with the static harness
	// against a recording that doesn't cover all fixture inputs.
	SkipOnRecordingMiss bool
	// ImportResolver is the injected ImportResolver (DI) through which an
	// `@kitsoki/<name>` import in the app under test resolves against the
	// `--kitsoki-repo` override or the embedded story library — letting
	// `kitsoki test intents` run a vendored instance in a foreign repo. nil
	// keeps the legacy error-on-missing behaviour.
	ImportResolver app.ImportResolver
}

// ─── Baseline format ─────────────────────────────────────────────────────────

// Baseline is the persisted regression-tracking file.
type Baseline struct {
	GeneratedAt string             `json:"generated_at"`
	AppID       string             `json:"app_id"`
	AppVersion  string             `json:"app_version"`
	Fixtures    map[string]float64 `json:"fixtures"` // key: "state::fixture-id"
}

// ─── RunIntents ───────────────────────────────────────────────────────────────

// RunIntents runs Mode 1 pass-rate tests against all matching intent fixtures.
func RunIntents(ctx context.Context, appPath string, opts IntentOptions) (*IntentReport, error) {
	// Publish KITSOKI_APP_DIR before Load so env-expanded fields in
	// the app yaml validate against the live var (bug 2 ordering fix
	// — see flows.go for the canonical comment).
	publishAppDirForTestrunner(appPath)

	// Load app.
	def, err := app.LoadWithResolver(appPath, nil, opts.ImportResolver)
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}

	// Build machine for validation.
	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}

	// Find fixture files.
	files, err := filepath.Glob(opts.Glob)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", opts.Glob, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no intent fixtures matched %q", opts.Glob)
	}

	// Load harness.
	h := opts.StaticHarnessImpl
	if h == nil && opts.LiveHarnessFactory != nil {
		h, err = opts.LiveHarnessFactory(def)
		if err != nil {
			return nil, fmt.Errorf("build %s harness: %w", opts.HarnessType, err)
		}
	}
	if h == nil {
		return nil, fmt.Errorf("no harness provided (use --harness static or --harness live)")
	}
	defer func() { _ = h.Close() }()

	// Collect all fixtures across all files.
	type stateFixtureGroup struct {
		file    string
		state   string
		def     *app.AppDef
		fixture IntentFixture
		deflt   IntentDefaults
	}

	var allFixtures []stateFixtureGroup

	for _, f := range files {
		docs, err := loadIntentFixtureFile(f)
		if err != nil {
			return nil, fmt.Errorf("load %q: %w", f, err)
		}
		for _, doc := range docs {
			if opts.OnlyState != "" && doc.State != opts.OnlyState {
				continue
			}
			for _, fix := range doc.Fixtures {
				allFixtures = append(allFixtures, stateFixtureGroup{
					file:    f,
					state:   doc.State,
					def:     def,
					fixture: fix,
					deflt:   doc.Defaults,
				})
			}
		}
	}

	if opts.DryRun {
		totalCalls := 0
		totalInputs := 0
		for _, g := range allFixtures {
			runs := effectiveRuns(g.fixture, g.deflt, opts.Runs)
			totalCalls += runs * len(g.fixture.Inputs)
			totalInputs += len(g.fixture.Inputs)
		}
		fmt.Printf("Dry run: %d fixtures, %d inputs, %d total calls (est. cost not calculated for static harness)\n",
			len(allFixtures), totalInputs, totalCalls)
		return &IntentReport{}, nil
	}

	// Run all fixtures.
	reportStarted := time.Now()
	report := &IntentReport{
		HarnessType:  opts.HarnessType,
		HarnessModel: opts.HarnessModel,
		AgentBackend: opts.AgentBackend,
		ProfileName:  opts.ProfileName,
	}
	recordingEntries := []recordingEntry{}

	// Load baseline.
	var baseline *Baseline
	if opts.BaselinePath != "" {
		b, err := loadBaseline(opts.BaselinePath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("load baseline: %w", err)
		}
		baseline = b
	}

	threshold := opts.RegressionThreshold
	if threshold == 0 {
		threshold = 0.05
	}

	for _, g := range allFixtures {
		runs := effectiveRuns(g.fixture, g.deflt, opts.Runs)
		minPassRate := effectiveMinPassRate(g.fixture, g.deflt)

		fr := FixtureResult{
			ID:          g.fixture.ID,
			State:       g.state,
			MinPassRate: minPassRate,
		}

		for _, input := range g.fixture.Inputs {
			ir := InputResult{Input: input, Runs: runs}
			skipped := false
			for run := 0; run < runs; run++ {
				started := time.Now()
				result, err := runOneIntent(ctx, h, m, g.def, g.state, input, g.fixture)
				ir.DurationMillis += time.Since(started).Milliseconds()
				if err != nil {
					if opts.SkipOnRecordingMiss && isRecordingMissError(err) {
						skipped = true
						break
					}
					if ir.FirstError == "" {
						ir.FirstError = err.Error()
					}
					// Count as fail, don't abort.
				} else {
					if ir.FirstActualIntent == "" {
						ir.FirstActualIntent = result.Intent
						ir.FirstActualSlots = result.Slots
						ir.FirstUsage = result.Usage
					}
					ir.CostUSD += result.CostUSD
					report.CostUSD += result.CostUSD
					if result.Passed {
						ir.Passed++
					}
				}
				if !skipped {
					report.TotalRuns++
				}
			}
			if skipped {
				ir.Runs = 0       // indicate skipped
				ir.PassRate = 1.0 // treat as pass for rate computation
				ir.BelowMin = false
				fr.Inputs = append(fr.Inputs, ir)
				continue
			}
			ir.RunCount(runs)
			ir.PassRate = float64(ir.Passed) / float64(runs)
			ir.BelowMin = ir.PassRate < minPassRate
			fr.Inputs = append(fr.Inputs, ir)
			fr.TotalRuns += runs
			fr.TotalPassed += ir.Passed
			report.TotalInputs++

			// Collect majority-vote recording entry.
			if ir.Passed > runs/2 && g.fixture.Intent != nil {
				recordingEntries = append(recordingEntries, recordingEntry{
					State: g.state,
					Input: input,
					Intent: recordingIntent{
						Name:  g.fixture.Intent.Name,
						Slots: g.fixture.Intent.Slots,
					},
					Confidence: float64(ir.Passed) / float64(runs),
					MajorityOf: runs,
				})
			}
		}

		if fr.TotalRuns > 0 {
			fr.PassRate = float64(fr.TotalPassed) / float64(fr.TotalRuns)
		} else {
			// All inputs were skipped (recording miss). Treat the fixture as
			// fully skipped — count as pass so CI doesn't block unnecessarily.
			fr.PassRate = 1.0
		}
		fr.Passed = fr.TotalRuns == 0 || fr.PassRate >= minPassRate

		// Regression check.
		if baseline != nil {
			key := fmt.Sprintf("%s::%s", g.state, g.fixture.ID)
			if prev, ok := baseline.Fixtures[key]; ok {
				drop := prev - fr.PassRate
				if drop > threshold {
					report.Regressions = append(report.Regressions,
						fmt.Sprintf("regression: %s dropped %.1f%% (was %.1f%%, now %.1f%%)",
							key, drop*100, prev*100, fr.PassRate*100))
				}
			}
		}

		report.Fixtures = append(report.Fixtures, fr)
		if fr.Passed {
			report.TotalPassed++
		} else {
			report.TotalFailed++
		}
	}

	// Emit recording if requested.
	if opts.EmitRecording != "" && len(recordingEntries) > 0 {
		of := recordingFile{
			Kind:          "recording",
			AppID:         def.App.ID,
			AppVersion:    def.App.Version,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			Generator:     "kitsoki test intents",
			MinConfidence: 0.80,
			Entries:       recordingEntries,
		}
		b, _ := yaml.Marshal(of)
		if err := os.WriteFile(opts.EmitRecording, b, 0644); err != nil {
			return nil, fmt.Errorf("write recording: %w", err)
		}
		report.RecordingEmitted = true
	}

	// Update baseline if requested.
	if opts.UpdateBaseline {
		newBaseline := Baseline{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			AppID:       def.App.ID,
			AppVersion:  def.App.Version,
			Fixtures:    make(map[string]float64),
		}
		for _, fr := range report.Fixtures {
			key := fmt.Sprintf("%s::%s", fr.State, fr.ID)
			newBaseline.Fixtures[key] = fr.PassRate
		}
		path := opts.BaselinePath
		if path == "" {
			path = ".kitsoki/intents-baseline.json"
		}
		b, _ := json.MarshalIndent(newBaseline, "", "  ")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err == nil {
			_ = os.WriteFile(path, b, 0644)
		}
	}

	report.DurationMillis = time.Since(reportStarted).Milliseconds()
	if opts.JSONOut != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if err := os.MkdirAll(filepath.Dir(opts.JSONOut), 0o755); err != nil {
			return nil, fmt.Errorf("create json report dir: %w", err)
		}
		if err := os.WriteFile(opts.JSONOut, b, 0o644); err != nil {
			return nil, fmt.Errorf("write json report: %w", err)
		}
	}

	return report, nil
}

// RunCount stamps the run count (called after the loop).
func (ir *InputResult) RunCount(runs int) {
	if ir.Runs == 0 {
		ir.Runs = runs
	}
}

type intentRunResult struct {
	Passed  bool
	Intent  string
	Slots   map[string]any
	Usage   map[string]any
	CostUSD float64
}

// runOneIntent runs a single intent routing check and returns true if it passed.
func runOneIntent(ctx context.Context, h harness.Harness, m machine.Machine, def *app.AppDef, state, input string, fix IntentFixture) (intentRunResult, error) {
	initialWorld := machine.WorldFromSchema(def.World)
	allowed := allowedIntentNames(m, app.StatePath(state), initialWorld)
	if len(allowed) == 0 {
		return intentRunResult{}, fmt.Errorf("state %q has no allowed intents; fixture may reference a stale or invalid state", state)
	}
	runCtx := host.WithAgentUsageBox(ctx)
	params, err := h.RunTurn(runCtx, harness.TurnInput{
		StatePath:      app.StatePath(state),
		UserText:       input,
		World:          initialWorld,
		AllowedIntents: allowed,
	})
	if err != nil {
		// A harness error might itself be an expected failure.
		if fix.ExpectFailure != nil {
			return intentRunResult{Passed: true}, nil
		}
		return intentRunResult{}, err
	}

	call, err := paramsToIntentCall(params)
	if err != nil {
		if fix.ExpectFailure != nil {
			return intentRunResult{Passed: true}, nil
		}
		return intentRunResult{}, err
	}
	result := intentRunResult{
		Intent:  call.Intent,
		Slots:   call.Slots,
		Usage:   host.AgentUsageFrom(runCtx),
		CostUSD: host.AgentCostFrom(runCtx),
	}

	// Run validation on the machine.
	vr := m.Validate(app.StatePath(state), initialWorld, call)

	if fix.ExpectFailure != nil {
		// We expect a failure.
		if !vr.OK {
			// Check it's one of the expected codes.
			for _, code := range fix.ExpectFailure.AnyOf {
				if code == "clarify" || string(vr.Err.Code) == code {
					result.Passed = true
					return result, nil
				}
			}
			return result, nil
		}
		// No error when we expected one.
		return result, nil
	}

	if fix.ExpectFallthrough {
		// We expect the intent to fall through to a wildcard.
		// The intent routes to some intent, and the machine accepts it (wildcard).
		result.Passed = vr.OK
		return result, nil
	}

	// Positive fixture: check intent name and slots.
	if !vr.OK {
		return result, nil
	}
	if fix.Intent != nil {
		if call.Intent != fix.Intent.Name {
			return result, nil
		}
		for k, v := range fix.Intent.Slots {
			if !deepEqualValues(call.Slots[k], v) {
				return result, nil
			}
		}
	}
	result.Passed = true
	return result, nil
}

func allowedIntentNames(m machine.Machine, state app.StatePath, w world.World) []string {
	allowed := m.AllowedIntents(state, w)
	names := make([]string, 0, len(allowed))
	for _, ai := range allowed {
		names = append(names, ai.Name)
	}
	return names
}

// ─── Loaders ─────────────────────────────────────────────────────────────────

// loadIntentFixtureFile parses one fixture file (may contain multiple YAML docs).
func loadIntentFixtureFile(filePath string) ([]IntentFixtureFile, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	docs := splitYAMLDocs(data)
	var out []IntentFixtureFile
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var f IntentFixtureFile
		if err := yaml.Unmarshal([]byte(doc), &f); err != nil {
			return nil, err
		}
		if f.TestKind != "intents" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// loadBaseline loads a baseline JSON file.
func loadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// isRecordingMissError returns true if the error is from a static/replay harness
// recording miss (no entry for this (state, input) pair).
func isRecordingMissError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no entry for state=") ||
		strings.Contains(msg, "recording miss for state=")
}

func effectiveRuns(fix IntentFixture, deflt IntentDefaults, globalOverride int) int {
	if globalOverride > 0 {
		return globalOverride
	}
	if fix.Runs > 0 {
		return fix.Runs
	}
	if deflt.Runs > 0 {
		return deflt.Runs
	}
	return 1
}

func effectiveMinPassRate(fix IntentFixture, deflt IntentDefaults) float64 {
	if fix.MinPassRate > 0 {
		return fix.MinPassRate
	}
	if deflt.MinPassRate > 0 {
		return deflt.MinPassRate
	}
	return 0.90
}

// ─── Reporter ─────────────────────────────────────────────────────────────────

// PrintIntentReport writes the human-readable Mode 1 report to stdout.
func PrintIntentReport(report *IntentReport) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, fr := range report.Fixtures {
		status := "PASS"
		if !fr.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%-6s\tstate:%-20s\t%-30s\t%.0f/%.0f (%.1f%%)\t(threshold %.0f%%)\n",
			status, fr.State, fr.ID,
			float64(fr.TotalPassed), float64(fr.TotalRuns), fr.PassRate*100,
			fr.MinPassRate*100)
		for _, ir := range fr.Inputs {
			irStatus := "ok"
			if ir.BelowMin {
				irStatus = "LOW"
			}
			fmt.Fprintf(w, "\t  %-6s  %-40s\t%d/%d (%.0f%%)\n", irStatus, `"`+ir.Input+`"`, ir.Passed, ir.Runs, ir.PassRate*100)
		}
	}
	_ = w.Flush()

	fmt.Printf("\nSummary: %d/%d fixtures pass\n", report.TotalPassed, report.TotalPassed+report.TotalFailed)
	if len(report.Regressions) > 0 {
		fmt.Println("REGRESSIONS:")
		for _, r := range report.Regressions {
			fmt.Printf("  %s\n", r)
		}
	}
	if report.TotalFailed > 0 || len(report.Regressions) > 0 {
		fmt.Println("FAIL")
	} else {
		fmt.Println("PASS")
	}
}

// Ensure world import is used (needed for world.Slots conversion).
var _ = world.Slots(nil)
var _ = intent.IntentCall{}

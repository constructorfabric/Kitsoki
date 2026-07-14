package testrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/storyauthoring"
)

// FlowCoverageOptions configures static flow fixture coverage analysis.
type FlowCoverageOptions struct {
	// FlowsGlob is the glob for flow fixture files. Required.
	FlowsGlob string
	// JSONOut is the optional path to write a machine-readable report.
	JSONOut string
	// MinBranchCoverage fails the gate when branch coverage is below this
	// percentage. Zero disables the threshold.
	MinBranchCoverage float64
	// RequireAllBranches fails the gate when any authored transition branch is
	// uncovered.
	RequireAllBranches bool
	// MinEffectCoverage fails the gate when authored effect coverage is below
	// this percentage. Zero disables the threshold.
	MinEffectCoverage float64
	// RequireAllEffects fails the gate when any authored effect is uncovered.
	RequireAllEffects bool
	// RequireHostAssertions fails the gate when a covered invoke effect is not
	// backed by expect_host_calls or a host cassette.
	RequireHostAssertions bool
	// StarlarkCoverage runs deterministic flow fixtures with host.starlark.run
	// instrumentation and reports statement/branch coverage for .star scripts.
	StarlarkCoverage bool
	// MinStarlarkStatementCoverage fails when instrumented statement coverage is
	// below this percentage. Zero disables the threshold.
	MinStarlarkStatementCoverage float64
	// MinStarlarkBranchCoverage fails when instrumented Starlark branch-outcome
	// coverage is below this percentage. Zero disables the threshold.
	MinStarlarkBranchCoverage float64
	// RequireAllStarlarkBranches fails when any authored Starlark if/elif branch
	// outcome lacks coverage.
	RequireAllStarlarkBranches bool
	// RequireRealStarlark fails when a covered host.starlark.run effect was only
	// stubbed at the handler layer instead of executing the real script.
	RequireRealStarlark bool
	// MaxCombinations caps enum slot product expansion per state+intent. Larger
	// products are reported as skipped so authors can opt into a narrower check.
	MaxCombinations int
	// ImportResolver is the injected app import resolver used by kitsoki test.
	ImportResolver app.ImportResolver
}

// FlowCoverageReport is the static coverage ledger for one app's flow fixtures.
type FlowCoverageReport struct {
	App                       string                        `json:"app"`
	AppPath                   string                        `json:"app_path"`
	FlowsGlob                 string                        `json:"flows_glob"`
	BranchCoverage            CoverageSummary               `json:"branch_coverage"`
	EffectCoverage            CoverageSummary               `json:"effect_coverage"`
	StarlarkStatementCoverage CoverageSummary               `json:"starlark_statement_coverage,omitempty"`
	StarlarkBranchCoverage    CoverageSummary               `json:"starlark_branch_coverage,omitempty"`
	Branches                  []FlowBranchCoverage          `json:"branches"`
	Effects                   []FlowEffectCoverage          `json:"effects,omitempty"`
	StarlarkScripts           []starlarkhost.ScriptCoverage `json:"starlark_scripts,omitempty"`
	ParameterChecks           []FlowParameterCoverage       `json:"parameter_checks,omitempty"`
	FlowFiles                 []string                      `json:"flow_files"`
	Problems                  []string                      `json:"problems,omitempty"`
	Passed                    bool                          `json:"passed"`
}

// CoverageSummary is a compact pass/fail metric for one coverage dimension.
type CoverageSummary struct {
	Covered int     `json:"covered"`
	Total   int     `json:"total"`
	Percent float64 `json:"percent"`
}

// FlowBranchCoverage describes one authored transition branch.
type FlowBranchCoverage struct {
	ID        string   `json:"id"`
	State     string   `json:"state"`
	Intent    string   `json:"intent"`
	Index     int      `json:"index"`
	Target    string   `json:"target"`
	When      string   `json:"when,omitempty"`
	Default   bool     `json:"default,omitempty"`
	Covered   bool     `json:"covered"`
	FlowFiles []string `json:"flow_files,omitempty"`
}

// FlowEffectCoverage describes one authored state-machine effect site.
type FlowEffectCoverage struct {
	ID             string         `json:"id"`
	State          string         `json:"state"`
	Origin         string         `json:"origin"`
	Intent         string         `json:"intent,omitempty"`
	BranchIndex    int            `json:"branch_index,omitempty"`
	EffectIndex    string         `json:"effect_index"`
	Kind           string         `json:"kind"`
	When           string         `json:"when,omitempty"`
	Invoke         string         `json:"invoke,omitempty"`
	HostRun        *HostRunEffect `json:"host_run,omitempty"`
	StarlarkScript string         `json:"starlark_script,omitempty"`
	Covered        bool           `json:"covered"`
	HostAsserted   bool           `json:"host_asserted,omitempty"`
	FlowFiles      []string       `json:"flow_files,omitempty"`
}

// HostRunEffect records the high-risk shape of a host.run call site.
type HostRunEffect struct {
	Cmd  any `json:"cmd,omitempty"`
	Args any `json:"args,omitempty"`
	Cwd  any `json:"cwd,omitempty"`
	Bind any `json:"bind,omitempty"`
}

// FlowParameterCoverage describes bounded enum-slot coverage for one
// state+intent pair.
type FlowParameterCoverage struct {
	State                string              `json:"state"`
	Intent               string              `json:"intent"`
	EnumSlots            map[string][]string `json:"enum_slots,omitempty"`
	ObservedValues       map[string][]string `json:"observed_values,omitempty"`
	MissingValues        map[string][]string `json:"missing_values,omitempty"`
	RequiredCombinations int                 `json:"required_combinations,omitempty"`
	ObservedCombinations int                 `json:"observed_combinations,omitempty"`
	MissingCombinations  []map[string]string `json:"missing_combinations,omitempty"`
	SkippedReason        string              `json:"skipped_reason,omitempty"`
	Passed               bool                `json:"passed"`
}

// RunFlowCoverage statically compares authored story transitions with the
// flow fixtures that claim to drive them. It does not execute flows; pair this
// with RunFlows for behavioral correctness.
func RunFlowCoverage(ctx context.Context, appPath string, opts FlowCoverageOptions) (*FlowCoverageReport, error) {
	if opts.FlowsGlob == "" {
		return nil, fmt.Errorf("flow coverage: FlowsGlob is required")
	}
	if opts.MaxCombinations <= 0 {
		opts.MaxCombinations = 64
	}

	// loadAppForRun holds appDirLoadMu across the setenv+Load span so a
	// concurrent RunFlows/RunIntents/RunFlowCoverage call in the same
	// process can't clobber KITSOKI_APP_DIR mid-Load (see flows.go).
	def, err := loadAppForRun(appPath, opts.ImportResolver)
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}
	files, err := ExpandGlobList(opts.FlowsGlob)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	branches := collectFlowBranches(def)
	byKey := make(map[string]*FlowBranchCoverage, len(branches))
	for i := range branches {
		byKey[branches[i].ID] = &branches[i]
	}
	effects := collectFlowEffects(def)
	starlarkRecorder := starlarkhost.NewCoverageRecorder()
	effectsByBranch := make(map[string][]*FlowEffectCoverage)
	onEnterByState := make(map[string][]*FlowEffectCoverage)
	var problems []string
	for i := range effects {
		eff := &effects[i]
		if eff.Invoke == "host.starlark.run" && eff.StarlarkScript != "" {
			resolved, rerr := resolveStarlarkCoverageScript(def, eff.StarlarkScript)
			if rerr != nil {
				problems = append(problems, rerr.Error())
			} else {
				eff.StarlarkScript = resolved
			}
		}
		switch eff.Origin {
		case "transition":
			key := branchID(eff.State, eff.Intent, eff.BranchIndex)
			effectsByBranch[key] = append(effectsByBranch[key], eff)
		case "on_enter":
			onEnterByState[eff.State] = append(onEnterByState[eff.State], eff)
		}
	}

	param := newParameterLedger(def, opts.MaxCombinations)
	registerStarlarkScripts(starlarkRecorder, effects, &problems)
	for _, file := range files {
		fixtures, ferr := loadFlowCoverageFixtures(file)
		if ferr != nil {
			return nil, ferr
		}
		for _, fixture := range fixtures {
			state := fixture.InitialState
			if state == "" {
				problems = append(problems, fmt.Sprintf("%s: flow fixture missing initial_state", file))
				continue
			}
			cassetteHandlers, cerr := fixtureCassetteHandlers(file, fixture)
			if cerr != nil {
				problems = append(problems, cerr.Error())
			}
			markOnEnterEffects(onEnterByState, state, file, cassetteHandlers)
			for i, turn := range fixture.Turns {
				if turn.Intent == nil {
					if turn.ExpectState != "" {
						state = turn.ExpectState
						markOnEnterEffects(onEnterByState, state, file, cassetteHandlers)
					}
					continue
				}
				intentName := turn.Intent.Name
				param.observe(state, intentName, turn.Intent.Slots)

				candidates := branchesForStateIntent(branches, state, intentName)
				if len(candidates) == 0 {
					problems = append(problems, fmt.Sprintf("%s turn %d: no authored branch for %s + %s", file, i+1, state, intentName))
					if turn.ExpectState != "" {
						state = turn.ExpectState
					}
					continue
				}
				covered := inferCoveredBranches(candidates, state, turn)
				for _, branch := range covered {
					if got := byKey[branch.ID]; got != nil {
						got.Covered = true
						got.FlowFiles = appendUnique(got.FlowFiles, file)
					}
					markBranchEffects(effectsByBranch, branch.ID, file, turn, cassetteHandlers)
				}

				nextState := inferNextState(candidates, state, turn)
				if nextState != "" {
					state = nextState
					markOnEnterEffects(onEnterByState, state, file, cassetteHandlers)
				}
			}
		}
	}

	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	covered := 0
	for i := range branches {
		sort.Strings(branches[i].FlowFiles)
		if branches[i].Covered {
			covered++
		}
	}
	summary := CoverageSummary{Covered: covered, Total: len(branches)}
	if summary.Total > 0 {
		summary.Percent = float64(summary.Covered) * 100 / float64(summary.Total)
	}
	sort.Slice(effects, func(i, j int) bool { return effects[i].ID < effects[j].ID })
	effectCovered := 0
	hostAssertionMissing := false
	for i := range effects {
		sort.Strings(effects[i].FlowFiles)
		if effects[i].Covered {
			effectCovered++
		}
		if effects[i].Covered && effects[i].Invoke != "" && !effects[i].HostAsserted {
			hostAssertionMissing = true
		}
	}
	effectSummary := CoverageSummary{Covered: effectCovered, Total: len(effects)}
	if effectSummary.Total > 0 {
		effectSummary.Percent = float64(effectSummary.Covered) * 100 / float64(effectSummary.Total)
	}
	if opts.StarlarkCoverage || opts.MinStarlarkStatementCoverage > 0 || opts.MinStarlarkBranchCoverage > 0 || opts.RequireAllStarlarkBranches || opts.RequireRealStarlark {
		flowReport, ferr := RunFlows(ctx, appPath, opts.FlowsGlob, FlowOptions{
			ImportResolver:   opts.ImportResolver,
			StarlarkCoverage: starlarkRecorder,
		})
		if ferr != nil {
			problems = append(problems, fmt.Sprintf("starlark coverage flow run failed: %v", ferr))
		} else if flowReport.Failed > 0 {
			problems = append(problems, fmt.Sprintf("starlark coverage flow run had %d failing fixture(s)", flowReport.Failed))
		}
	}
	starlarkScripts := starlarkRecorder.Snapshot()
	starlarkStatementSummary, starlarkBranchSummary := summarizeStarlarkCoverage(starlarkScripts)

	paramChecks := param.results()
	passed := true
	if opts.RequireAllBranches && summary.Covered != summary.Total {
		passed = false
	}
	if opts.MinBranchCoverage > 0 && summary.Percent+1e-9 < opts.MinBranchCoverage {
		passed = false
	}
	if opts.RequireAllEffects && effectSummary.Covered != effectSummary.Total {
		passed = false
	}
	if opts.MinEffectCoverage > 0 && effectSummary.Percent+1e-9 < opts.MinEffectCoverage {
		passed = false
	}
	if opts.RequireHostAssertions && hostAssertionMissing {
		passed = false
	}
	if opts.MinStarlarkStatementCoverage > 0 && starlarkStatementSummary.Percent+1e-9 < opts.MinStarlarkStatementCoverage {
		passed = false
	}
	if opts.MinStarlarkBranchCoverage > 0 && starlarkBranchSummary.Percent+1e-9 < opts.MinStarlarkBranchCoverage {
		passed = false
	}
	if opts.RequireAllStarlarkBranches && starlarkBranchSummary.Covered != starlarkBranchSummary.Total {
		passed = false
	}
	if opts.RequireRealStarlark && coveredStarlarkEffectMissingRealRun(effects, starlarkScripts) {
		passed = false
	}

	report := &FlowCoverageReport{
		App:                       def.App.ID,
		AppPath:                   appPath,
		FlowsGlob:                 opts.FlowsGlob,
		BranchCoverage:            summary,
		EffectCoverage:            effectSummary,
		StarlarkStatementCoverage: starlarkStatementSummary,
		StarlarkBranchCoverage:    starlarkBranchSummary,
		Branches:                  branches,
		Effects:                   effects,
		StarlarkScripts:           starlarkScripts,
		ParameterChecks:           paramChecks,
		FlowFiles:                 files,
		Problems:                  problems,
		Passed:                    passed,
	}
	if len(problems) > 0 && (opts.RequireAllBranches || opts.RequireAllEffects || opts.RequireHostAssertions || opts.StarlarkCoverage || opts.MinStarlarkStatementCoverage > 0 || opts.MinStarlarkBranchCoverage > 0 || opts.RequireAllStarlarkBranches || opts.RequireRealStarlark) {
		report.Passed = false
	}
	if opts.JSONOut != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if dir := filepath.Dir(opts.JSONOut); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create JSON report dir: %w", err)
			}
		}
		if err := os.WriteFile(opts.JSONOut, append(b, '\n'), 0o644); err != nil {
			return nil, fmt.Errorf("write JSON report: %w", err)
		}
	}
	return report, nil
}

func loadFlowCoverageFixtures(file string) ([]FlowFixture, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", file, err)
	}
	var fixtures []FlowFixture
	for _, doc := range splitYAMLDocs(data) {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var fixture FlowFixture
		if err := yaml.Unmarshal([]byte(doc), &fixture); err != nil {
			return nil, fmt.Errorf("parse fixture in %q: %w", file, err)
		}
		if fixture.TestKind == "flow" {
			fixtures = append(fixtures, fixture)
		}
	}
	return fixtures, nil
}

func collectFlowBranches(def *app.AppDef) []FlowBranchCoverage {
	var out []FlowBranchCoverage
	walkStates("", def.States, func(path string, st *app.State) {
		for intentName, transitions := range st.On {
			if isBuiltinStoryAuthoringCoverageSite(path, intentName) {
				continue
			}
			for idx, tr := range transitions {
				target := normalizeTarget(path, tr.Target)
				id := branchID(path, intentName, idx)
				out = append(out, FlowBranchCoverage{
					ID:      id,
					State:   path,
					Intent:  intentName,
					Index:   idx,
					Target:  target,
					When:    tr.When,
					Default: tr.Default,
				})
			}
		}
	})
	return out
}

func collectFlowEffects(def *app.AppDef) []FlowEffectCoverage {
	var out []FlowEffectCoverage
	walkStates("", def.States, func(path string, st *app.State) {
		if path != storyauthoring.RoomState {
			out = append(out, collectEffectSites(path, "on_enter", "", 0, st.OnEnter, "on_enter")...)
		}
		for intentName, transitions := range st.On {
			if isBuiltinStoryAuthoringCoverageSite(path, intentName) {
				continue
			}
			for branchIdx, tr := range transitions {
				origin := fmt.Sprintf("%s#%s[%d]", path, intentName, branchIdx)
				out = append(out, collectEffectSites(path, "transition", intentName, branchIdx, tr.Effects, origin)...)
			}
		}
	})
	return out
}

func isBuiltinStoryAuthoringCoverageSite(state, intentName string) bool {
	return storyauthoring.IsFrameworkTransition(state, intentName)
}

func collectEffectSites(state, originKind, intent string, branchIdx int, effects []app.Effect, origin string) []FlowEffectCoverage {
	var out []FlowEffectCoverage
	var walk func([]app.Effect, string)
	walk = func(list []app.Effect, prefix string) {
		for idx, eff := range list {
			effectIndex := fmt.Sprintf("%s%d", prefix, idx)
			id := fmt.Sprintf("%s:%s.effects[%s]", origin, originKind, effectIndex)
			site := FlowEffectCoverage{
				ID:          id,
				State:       state,
				Origin:      originKind,
				Intent:      intent,
				BranchIndex: branchIdx,
				EffectIndex: effectIndex,
				Kind:        effectKind(eff),
				When:        eff.When,
				Invoke:      eff.Invoke,
			}
			if eff.Invoke == "host.run" {
				site.HostRun = &HostRunEffect{
					Cmd:  eff.With["cmd"],
					Args: eff.With["args"],
					Cwd:  eff.With["cwd"],
					Bind: eff.Bind,
				}
			}
			if eff.Invoke == "host.starlark.run" {
				if script, ok := eff.With["script"].(string); ok {
					site.StarlarkScript = script
				}
			}
			out = append(out, site)
			if len(eff.OnComplete) > 0 {
				walk(eff.OnComplete, effectIndex+".on_complete.")
			}
			if len(eff.Effects) > 0 {
				walk(eff.Effects, effectIndex+".effects.")
			}
		}
	}
	walk(effects, "")
	return out
}

func effectKind(eff app.Effect) string {
	var kinds []string
	if eff.Invoke != "" {
		kinds = append(kinds, "invoke")
	}
	if len(eff.Set) > 0 {
		kinds = append(kinds, "set")
	}
	if len(eff.Increment) > 0 {
		kinds = append(kinds, "increment")
	}
	if eff.Say != "" {
		kinds = append(kinds, "say")
	}
	if eff.Emit != "" {
		kinds = append(kinds, "emit")
	}
	if eff.EmitIntent != "" {
		kinds = append(kinds, "emit_intent")
	}
	if eff.Target != "" {
		kinds = append(kinds, "target")
	}
	if len(eff.OnComplete) > 0 {
		kinds = append(kinds, "on_complete")
	}
	if len(eff.Effects) > 0 {
		kinds = append(kinds, "effects")
	}
	if len(kinds) == 0 {
		return "noop"
	}
	return strings.Join(kinds, "+")
}

func fixtureCassetteHandlers(flowFile string, fixture FlowFixture) (map[string]bool, error) {
	if fixture.HostCassette == "" {
		return nil, nil
	}
	cassettePath := fixture.HostCassette
	if !filepath.IsAbs(cassettePath) {
		cassettePath = filepath.Join(filepath.Dir(flowFile), cassettePath)
	}
	cas, err := LoadCassette(cassettePath)
	if err != nil {
		return nil, fmt.Errorf("%s: load host_cassette %q: %w", flowFile, fixture.HostCassette, err)
	}
	handlers := make(map[string]bool)
	for _, episode := range cas.Episodes {
		if handler, ok := episode.Match["handler"]; ok {
			handlers[fmt.Sprint(handler)] = true
		}
	}
	return handlers, nil
}

func resolveStarlarkCoverageScript(def *app.AppDef, script string) (string, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return "", fmt.Errorf("host.starlark.run effect has empty script")
	}
	if strings.Contains(script, "{{") {
		return "", fmt.Errorf("host.starlark.run script %q is templated; starlark coverage cannot statically register it", script)
	}
	if filepath.IsAbs(script) {
		return filepath.Clean(script), nil
	}
	if def.BaseDir != "" {
		return filepath.Clean(filepath.Join(def.BaseDir, script)), nil
	}
	return filepath.Clean(script), nil
}

func registerStarlarkScripts(recorder *starlarkhost.CoverageRecorder, effects []FlowEffectCoverage, problems *[]string) {
	seen := map[string]bool{}
	for _, eff := range effects {
		if eff.Invoke != "host.starlark.run" || eff.StarlarkScript == "" || seen[eff.StarlarkScript] {
			continue
		}
		seen[eff.StarlarkScript] = true
		src, err := os.ReadFile(eff.StarlarkScript)
		if err != nil {
			*problems = append(*problems, fmt.Sprintf("read starlark script %q: %v", eff.StarlarkScript, err))
			continue
		}
		if err := recorder.RegisterScript(eff.StarlarkScript, src); err != nil {
			*problems = append(*problems, fmt.Sprintf("parse starlark script %q for coverage: %v", eff.StarlarkScript, err))
		}
	}
}

func summarizeStarlarkCoverage(scripts []starlarkhost.ScriptCoverage) (CoverageSummary, CoverageSummary) {
	var statements CoverageSummary
	var branches CoverageSummary
	for _, script := range scripts {
		for _, stmt := range script.Statements {
			if strings.HasPrefix(stmt.Kind, "instrumentation_error:") {
				continue
			}
			statements.Total++
			if stmt.Covered {
				statements.Covered++
			}
		}
		for _, branch := range script.Branches {
			branches.Total += 2
			if branch.TrueHits > 0 {
				branches.Covered++
			}
			if branch.FalseHits > 0 {
				branches.Covered++
			}
		}
	}
	if statements.Total > 0 {
		statements.Percent = float64(statements.Covered) * 100 / float64(statements.Total)
	}
	if branches.Total > 0 {
		branches.Percent = float64(branches.Covered) * 100 / float64(branches.Total)
	}
	return statements, branches
}

func coveredStarlarkEffectMissingRealRun(effects []FlowEffectCoverage, scripts []starlarkhost.ScriptCoverage) bool {
	ran := map[string]bool{}
	for _, script := range scripts {
		if len(script.FlowFiles) > 0 {
			ran[script.Script] = true
		}
	}
	for _, eff := range effects {
		if eff.Invoke == "host.starlark.run" && eff.Covered && eff.StarlarkScript != "" && !ran[eff.StarlarkScript] {
			return true
		}
	}
	return false
}

func markBranchEffects(byBranch map[string][]*FlowEffectCoverage, branchID, file string, turn FlowTurn, cassetteHandlers map[string]bool) {
	for _, eff := range byBranch[branchID] {
		markEffect(eff, file, turnHostAsserted(turn, eff.Invoke) || cassetteHandlers[eff.Invoke])
	}
}

func markOnEnterEffects(byState map[string][]*FlowEffectCoverage, state, file string, cassetteHandlers map[string]bool) {
	for _, eff := range byState[state] {
		markEffect(eff, file, cassetteHandlers[eff.Invoke])
	}
}

func markEffect(eff *FlowEffectCoverage, file string, hostAsserted bool) {
	eff.Covered = true
	eff.FlowFiles = appendUnique(eff.FlowFiles, file)
	if eff.Invoke != "" && hostAsserted {
		eff.HostAsserted = true
	}
}

func turnHostAsserted(turn FlowTurn, invoke string) bool {
	if invoke == "" {
		return false
	}
	for _, call := range turn.ExpectHostCalls {
		if call.Handler == invoke {
			return true
		}
	}
	for _, ev := range turn.ExpectEvents {
		if eventMatchesHandler(ev, invoke) {
			return true
		}
	}
	for _, ev := range turn.ExpectEventsExact {
		if eventMatchesHandler(ev, invoke) {
			return true
		}
	}
	return false
}

func eventMatchesHandler(ev FlowEvent, handler string) bool {
	if ev.Kind != "" && ev.Kind != "HostDispatched" {
		return false
	}
	if ev.Effect == nil {
		return false
	}
	for _, key := range []string{"handler", "invoke"} {
		if got, ok := ev.Effect[key]; ok && fmt.Sprint(got) == handler {
			return true
		}
	}
	return false
}

func walkStates(prefix string, states map[string]*app.State, visit func(string, *app.State)) {
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		st := states[name]
		visit(path, st)
		if len(st.States) > 0 {
			walkStates(path, st.States, visit)
		}
	}
}

func branchID(state, intent string, idx int) string {
	return fmt.Sprintf("%s#%s[%d]", state, intent, idx)
}

func branchesForStateIntent(branches []FlowBranchCoverage, state, intent string) []FlowBranchCoverage {
	var out []FlowBranchCoverage
	for _, branch := range branches {
		if branch.State == state && branch.Intent == intent {
			out = append(out, branch)
		}
	}
	return out
}

func inferCoveredBranches(candidates []FlowBranchCoverage, state string, turn FlowTurn) []FlowBranchCoverage {
	if turn.ExpectError != nil {
		return nil
	}
	if turn.ExpectState != "" {
		var out []FlowBranchCoverage
		for _, branch := range candidates {
			if branch.Target == turn.ExpectState {
				out = append(out, branch)
			}
		}
		return out
	}
	if len(turn.ExpectStateIn) > 0 {
		allowed := make(map[string]bool, len(turn.ExpectStateIn))
		for _, s := range turn.ExpectStateIn {
			allowed[s] = true
		}
		var out []FlowBranchCoverage
		for _, branch := range candidates {
			if allowed[branch.Target] {
				out = append(out, branch)
			}
		}
		return out
	}
	if len(candidates) == 1 {
		return candidates
	}
	if len(candidates) > 0 && candidates[0].Target == state && allSameTarget(candidates) {
		return candidates
	}
	return nil
}

func inferNextState(candidates []FlowBranchCoverage, state string, turn FlowTurn) string {
	if turn.ExpectState != "" {
		return turn.ExpectState
	}
	covered := inferCoveredBranches(candidates, state, turn)
	if len(covered) == 1 {
		return covered[0].Target
	}
	if len(covered) > 1 && allSameTarget(covered) {
		return covered[0].Target
	}
	return ""
}

func allSameTarget(branches []FlowBranchCoverage) bool {
	if len(branches) == 0 {
		return false
	}
	target := branches[0].Target
	for _, branch := range branches[1:] {
		if branch.Target != target {
			return false
		}
	}
	return true
}

func normalizeTarget(from, target string) string {
	if target == "" || target == "." {
		return from
	}
	if strings.HasPrefix(target, "@") || strings.Contains(target, ".") {
		return target
	}
	if i := strings.LastIndex(from, "."); i >= 0 {
		return from[:i+1] + target
	}
	return target
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

type parameterLedger struct {
	def             *app.AppDef
	maxCombinations int
	observed        map[string][]map[string]any
}

func newParameterLedger(def *app.AppDef, maxCombinations int) *parameterLedger {
	return &parameterLedger{
		def:             def,
		maxCombinations: maxCombinations,
		observed:        map[string][]map[string]any{},
	}
}

func (l *parameterLedger) observe(state, intent string, slots map[string]any) {
	key := state + "#" + intent
	copied := make(map[string]any, len(slots))
	for k, v := range slots {
		copied[k] = v
	}
	l.observed[key] = append(l.observed[key], copied)
}

func (l *parameterLedger) results() []FlowParameterCoverage {
	var out []FlowParameterCoverage
	walkStates("", l.def.States, func(path string, st *app.State) {
		for intentName := range st.On {
			intentDef, ok := l.intentForState(st, intentName)
			if !ok {
				continue
			}
			enumSlots := enumSlotValues(intentDef)
			if len(enumSlots) == 0 {
				continue
			}
			key := path + "#" + intentName
			check := FlowParameterCoverage{
				State:          path,
				Intent:         intentName,
				EnumSlots:      enumSlots,
				ObservedValues: map[string][]string{},
				MissingValues:  map[string][]string{},
				Passed:         true,
			}
			observedCombos := map[string]bool{}
			for _, slots := range l.observed[key] {
				combo := map[string]string{}
				for slot := range enumSlots {
					if raw, ok := slots[slot]; ok {
						value := fmt.Sprint(raw)
						check.ObservedValues[slot] = appendUnique(check.ObservedValues[slot], value)
						combo[slot] = value
					}
				}
				if len(combo) == len(enumSlots) {
					observedCombos[comboKey(combo)] = true
				}
			}
			for slot, values := range enumSlots {
				sort.Strings(check.ObservedValues[slot])
				seen := make(map[string]bool, len(check.ObservedValues[slot]))
				for _, v := range check.ObservedValues[slot] {
					seen[v] = true
				}
				for _, want := range values {
					if !seen[want] {
						check.MissingValues[slot] = append(check.MissingValues[slot], want)
						check.Passed = false
					}
				}
			}
			combos, skipped := expandEnumCombinations(enumSlots, l.maxCombinations)
			check.RequiredCombinations = len(combos)
			check.ObservedCombinations = len(observedCombos)
			if skipped != "" {
				check.SkippedReason = skipped
			} else {
				for _, combo := range combos {
					if !observedCombos[comboKey(combo)] {
						check.MissingCombinations = append(check.MissingCombinations, combo)
						check.Passed = false
					}
				}
			}
			out = append(out, check)
		}
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].State == out[j].State {
			return out[i].Intent < out[j].Intent
		}
		return out[i].State < out[j].State
	})
	return out
}

func (l *parameterLedger) intentForState(st *app.State, intentName string) (app.Intent, bool) {
	if st.Intents != nil {
		if intentDef, ok := st.Intents[intentName]; ok {
			return intentDef, true
		}
	}
	if l.def.Intents != nil {
		if intentDef, ok := l.def.Intents[intentName]; ok {
			return intentDef, true
		}
	}
	return app.Intent{}, false
}

func enumSlotValues(intentDef app.Intent) map[string][]string {
	out := map[string][]string{}
	for name, slot := range intentDef.Slots {
		if len(slot.Values) == 0 {
			continue
		}
		values := append([]string(nil), slot.Values...)
		sort.Strings(values)
		out[name] = values
	}
	return out
}

func expandEnumCombinations(slots map[string][]string, max int) ([]map[string]string, string) {
	keys := make([]string, 0, len(slots))
	total := 1
	for k, values := range slots {
		keys = append(keys, k)
		total *= len(values)
		if total > max {
			return nil, fmt.Sprintf("combination product %d exceeds max %d", total, max)
		}
	}
	sort.Strings(keys)
	var out []map[string]string
	var rec func(int, map[string]string)
	rec = func(idx int, cur map[string]string) {
		if idx == len(keys) {
			combo := make(map[string]string, len(cur))
			for k, v := range cur {
				combo[k] = v
			}
			out = append(out, combo)
			return
		}
		key := keys[idx]
		for _, value := range slots[key] {
			cur[key] = value
			rec(idx+1, cur)
		}
		delete(cur, key)
	}
	rec(0, map[string]string{})
	return out, ""
}

func comboKey(combo map[string]string) string {
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+combo[k])
	}
	return strings.Join(parts, "\x00")
}

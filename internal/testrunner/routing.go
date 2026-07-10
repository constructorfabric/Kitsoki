// Mode 0 (pure no-LLM routing) runner.
//
// `test intents` (intents.go) benchmarks the FULL harness stack — including
// the LLM tiers — against a StaticHarness recording lookup. That is the
// right tool for tuning phrasing against a live model, but it cannot prove
// anything about the deterministic/semantic tiers on their own: a phrase
// with no recording entry is silently SKIPPED (counted as a pass), so a
// fixture file full of "these resolve without the LLM" claims can pass
// green while never exercising semroute at all.
//
// RunRoutingFixtures closes that gap. It builds a real orchestrator over the
// app's actual (state, world) and calls [orchestrator.Orchestrator.Classify]
// directly — the side-effect-free, no-LLM routing entrypoint (deterministic
// display/example match -> semantic synonym/template -> optional embedding
// tier; see internal/orchestrator/classify.go). No harness is ever invoked:
// there is no LLM in this path, full stop, so a fixture that names a
// harness-only phrasing FAILS here rather than silently skipping.
//
// Fixture format is the same `test_kind: intents` YAML the Mode 1 runner
// uses (state + fixtures: [{id, intent, inputs}]), plus one field Mode 1
// does not need: `defers_to_interpreter: true`, which asserts the OPPOSITE
// of a resolved verdict — that Classify returns ok=false for that phrase
// (content-bearing free text these tiers correctly decline to guess at).
// Note the name is scoped precisely to what Classify covers: ok=false here
// means only that the deterministic/semantic/embedding tiers didn't resolve
// it — the live Turn pipeline may still route it no-LLM via a LATER tier
// Classify doesn't model (a state's `default_intent` sink or the app's
// `free_form_fallback`), or it may fall all the way to the LLM. Fixtures
// should reserve `defers_to_interpreter` for genuinely content-bearing free
// text (a bug description, a free-form request) rather than affirmations/
// continuations that a `default_intent` sink actually resolves
// deterministically one tier down — see docs/testing/routing-tuning.md.
package testrunner

import (
	"context"
	"fmt"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// RoutingOptions configures RunRoutingFixtures.
type RoutingOptions struct {
	// Glob is the glob pattern for intent fixture files (same format as
	// `test intents`).
	Glob string
	// OnlyState filters to fixture docs whose `state:` matches exactly.
	OnlyState string
	// ImportResolver threads through to app.LoadWithResolver (base-story
	// `@kitsoki/...` imports).
	ImportResolver app.ImportResolver
}

// RoutingFixtureResult is the outcome for one fixture group (one `id:`, all
// its `inputs:`).
type RoutingFixtureResult struct {
	ID     string
	State  string
	Passed bool
	Inputs []RoutingInputResult
}

// RoutingInputResult is the outcome for one input phrase.
type RoutingInputResult struct {
	Input          string
	Passed         bool
	Deferred       bool   // Classify returned ok=false
	ActualIntent   string // "" when Deferred
	ExpectedIntent string // "" when the fixture expects a defer
	Reason         string // populated on failure
}

// RoutingReport aggregates every fixture group's result.
type RoutingReport struct {
	Fixtures    []RoutingFixtureResult
	TotalPassed int
	TotalFailed int
}

// RunRoutingFixtures loads appPath, builds a real (never-invoked) harness
// orchestrator, and asserts every fixture's expected no-LLM routing outcome.
// It never constructs a live LLM harness and never calls RunTurn — Classify
// is pure and side-effect-free (see internal/orchestrator/classify.go).
func RunRoutingFixtures(ctx context.Context, appPath string, opts RoutingOptions) (*RoutingReport, error) {
	def, err := loadAppForRun(appPath, opts.ImportResolver)
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}

	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}

	s, err := store.OpenMemory()
	if err != nil {
		return nil, fmt.Errorf("open in-memory store: %w", err)
	}
	defer func() { _ = s.Close() }()

	// A StaticHarness with no entries: if Classify ever fell through and
	// something (incorrectly) invoked the harness, it errors loudly instead
	// of silently answering — belt-and-suspenders on top of Classify's own
	// "never calls the harness" contract.
	poison := &StaticHarness{}
	orch := orchestrator.New(def, m, s, poison)

	files, err := filepath.Glob(opts.Glob)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", opts.Glob, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no intent fixtures matched %q", opts.Glob)
	}

	report := &RoutingReport{}

	for _, f := range files {
		docs, err := loadIntentFixtureFile(f)
		if err != nil {
			return nil, fmt.Errorf("load %q: %w", f, err)
		}
		for _, doc := range docs {
			if opts.OnlyState != "" && doc.State != opts.OnlyState {
				continue
			}
			initialWorld := machine.WorldFromSchema(def.World)
			for _, fix := range doc.Fixtures {
				fr := RoutingFixtureResult{ID: fix.ID, State: doc.State}
				fr.Passed = true
				for _, input := range fix.Inputs {
					ir := RoutingInputResult{Input: input}
					verdict, ok, cerr := orch.Classify(ctx, app.StatePath(doc.State), initialWorld, input)
					if cerr != nil {
						ir.Passed = false
						ir.Reason = fmt.Sprintf("classify error: %v", cerr)
					} else if fix.DefersToInterpreter {
						ir.Deferred = !ok
						ir.Passed = !ok
						if ok {
							ir.ActualIntent = verdict.Intent
							ir.Reason = fmt.Sprintf("expected defer-to-interpreter, but a no-LLM tier resolved %q", verdict.Intent)
						}
					} else if fix.Intent != nil {
						ir.ExpectedIntent = fix.Intent.Name
						if !ok {
							ir.Passed = false
							ir.Reason = "no no-LLM tier matched (expected a deterministic/semantic resolution)"
						} else {
							ir.ActualIntent = verdict.Intent
							ir.Passed = verdict.Intent == fix.Intent.Name
							if !ir.Passed {
								ir.Reason = fmt.Sprintf("resolved %q, expected %q", verdict.Intent, fix.Intent.Name)
							} else if slotsErr := slotsSubsetMatch(fix.Intent.Slots, verdict.Slots); slotsErr != "" {
								ir.Passed = false
								ir.Reason = slotsErr
							}
						}
					} else {
						// Fixture declares neither an expected intent nor a
						// defer — nothing to assert; treat as a pass so
						// authors can use this file purely for shared
						// documentation-style groups without inputs.
						ir.Passed = true
					}
					if !ir.Passed {
						fr.Passed = false
					}
					fr.Inputs = append(fr.Inputs, ir)
				}
				report.Fixtures = append(report.Fixtures, fr)
				if fr.Passed {
					report.TotalPassed++
				} else {
					report.TotalFailed++
				}
			}
		}
	}

	return report, nil
}

// slotsSubsetMatch reports a non-empty reason string when any key in
// `expected` is missing from or mismatched in `actual`. Extra keys in
// `actual` are ignored — fixtures assert what they care about.
func slotsSubsetMatch(expected, actual map[string]any) string {
	for k, v := range expected {
		av, ok := actual[k]
		if !ok {
			return fmt.Sprintf("missing expected slot %q", k)
		}
		if fmt.Sprintf("%v", av) != fmt.Sprintf("%v", v) {
			return fmt.Sprintf("slot %q = %v, expected %v", k, av, v)
		}
	}
	return ""
}

// PrintRoutingReport writes a human-readable summary to stdout.
func PrintRoutingReport(r *RoutingReport) {
	for _, fr := range r.Fixtures {
		status := "PASS"
		if !fr.Passed {
			status = "FAIL"
		}
		fmt.Printf("%-4s  state:%-24s  %-40s\n", status, fr.State, fr.ID)
		for _, ir := range fr.Inputs {
			istatus := "ok"
			if !ir.Passed {
				istatus = "FAIL"
			}
			label := ir.ActualIntent
			if ir.Deferred {
				label = "(deferred to interpreter)"
			}
			fmt.Printf("        %-6s %-60q -> %s\n", istatus, ir.Input, label)
			if !ir.Passed && ir.Reason != "" {
				fmt.Printf("               %s\n", ir.Reason)
			}
		}
	}
	fmt.Printf("\nSummary: %d/%d fixtures pass\n", r.TotalPassed, r.TotalPassed+r.TotalFailed)
}

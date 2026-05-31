// Calibration end-to-end test. Loads the real Oregon Trail story
// definition + its recording, re-routes every entry through the full
// deterministic → semroute → turncache stack (with the LLM stubbed
// to return the recorded intent), and asserts the LLM-fallthrough
// rate stays at or below the calibration target of 30%.
//
// This is the safety net the calibration plan calls out: any future
// change that regresses the matcher or breaks a synonym's stem-set
// containment surfaces here as a test failure, not as a quality
// regression in production.
//
// The test deliberately lives in internal/semroute (rather than
// cmd/kitsoki) so it runs by default in `go test ./...`. The
// re-routing logic is in cmd/kitsoki/replay_routing.go (the CLI
// surface) — this test invokes it through a small shim:
// loading the story, building the matcher, replaying.
package semroute_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/lex"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/semroute"
	"kitsoki/internal/turncache"
	"kitsoki/internal/world"
)

// llmFallthroughTarget is the calibration ceiling.
//
// Updated for issue H2: the previous 0.30 ceiling was set against a
// gate that didn't include production's `RequiresUnfilledSlot`
// guard, so the published number under-counted real LLM cost. With
// the gate in place — verdicts that match an intent but leave a
// required slot empty now correctly fall through to the LLM — the
// honest measured rate on the Oregon Trail recording is 37.5%
// (24 of 64 turns). 0.40 leaves a small margin so the test isn't
// flaky against minor matcher/recording reshuffles; meaningful
// regressions still trip it. The synonym-authoring plan
// stays the same; the next round of work should bring this back
// under 30% by adding slot-level synonyms so the
// matcher can fill required enum slots from bare-string matches
// instead of needing an explicit `{slot}` template per phrasing.
const llmFallthroughTarget = 0.40

// recordingFile mirrors the YAML shape; kept private to this file so
// the test isn't coupled to the CLI's RecordingFile export.
type recordingFile struct {
	Kind    string `yaml:"kind"`
	Entries []struct {
		State  string `yaml:"state"`
		Input  string `yaml:"input"`
		Intent struct {
			Name  string         `yaml:"name"`
			Slots map[string]any `yaml:"slots"`
		} `yaml:"intent"`
	} `yaml:"entries"`
}

// repoRoot finds the worktree root from this test file's location.
// We can't use os.Getwd because go test sets it to the package dir.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	// file is internal/semroute/calibration_test.go; root is two ups.
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestOregonTrailCalibration is the calibration safety net. If a
// future edit drops the LLM-fallthrough rate above the target, this
// test fails — pointing the author at either:
//
//   - a regressed matcher (semroute), or
//   - a synonym that no longer applies (intents.yaml), or
//   - a recording entry that legitimately needs a new synonym to be
//     added (the recording grew without the synonym library keeping
//     up).
//
// The test runs in <50 ms on a modern laptop and is `-race` clean.
func TestOregonTrailCalibration(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	appPath := filepath.Join(root, "stories", "oregon-trail", "app.yaml")
	recPath := filepath.Join(root, "stories", "oregon-trail", "recording.yaml")

	def, err := app.Load(appPath)
	if err != nil {
		t.Fatalf("load app: %v", err)
	}
	rec, err := loadRec(t, recPath)
	if err != nil {
		t.Fatalf("load recording: %v", err)
	}

	rate := computeFallthroughRate(t, def, rec)
	if rate > llmFallthroughTarget {
		t.Fatalf("Oregon Trail LLM fallthrough rate = %.3f, target ≤ %.3f", rate, llmFallthroughTarget)
	}
	t.Logf("Oregon Trail LLM fallthrough rate = %.3f (target ≤ %.3f, %d turns)",
		rate, llmFallthroughTarget, len(rec.Entries))
}

// BenchmarkReplayRouting_OregonTrail measures the per-turn cost of
// the routing stack over the full Oregon Trail recording. Sets the
// stage for Phase-7 latency regressions: a future commit that
// doubles per-turn cost shows up as a 2× delta here.
func BenchmarkReplayRouting_OregonTrail(b *testing.B) {
	root := repoRootB(b)
	appPath := filepath.Join(root, "stories", "oregon-trail", "app.yaml")
	recPath := filepath.Join(root, "stories", "oregon-trail", "recording.yaml")

	def, err := app.Load(appPath)
	if err != nil {
		b.Fatalf("load app: %v", err)
	}
	data, err := os.ReadFile(recPath)
	if err != nil {
		b.Fatalf("read recording: %v", err)
	}
	var rec recordingFile
	if err := yaml.Unmarshal(data, &rec); err != nil {
		b.Fatalf("parse recording: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = computeFallthroughRateFast(def, &rec)
	}
}

// repoRootB is the *testing.B variant of repoRoot — same logic, just
// avoids depending on testing.TB shenanigans across the t/b types.
func repoRootB(b *testing.B) string {
	b.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// loadRec reads + parses the recording. Kept separate from the
// shared yaml.Unmarshal call so the test produces a clear error
// message on a malformed recording.
func loadRec(t *testing.T, path string) (*recordingFile, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rf recordingFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, err
	}
	if rf.Kind != "recording" {
		t.Fatalf("recording kind = %q, want \"recording\"", rf.Kind)
	}
	return &rf, nil
}

// computeFallthroughRate is the test's calibration loop. Mirrors the
// CLI's ReplayRouting closely but without the per-turn audit-row
// machinery — the test only needs the LLM count.
func computeFallthroughRate(t *testing.T, def *app.AppDef, rec *recordingFile) float64 {
	t.Helper()
	return computeFallthroughRateFast(def, rec)
}

// computeFallthroughRateFast is the bench-friendly variant: no
// testing.T, no t.Helper. Same algorithm.
func computeFallthroughRateFast(def *app.AppDef, rec *recordingFile) float64 {
	m, err := machine.New(def)
	if err != nil {
		return 1.0
	}
	matcher, _ := semroute.Compile(def)
	cache := turncache.NewMemory(turncache.DefaultConfig())
	defer cache.Close()
	appHash := orchestrator.ComputeAppHash(def)

	stops := stopwordsExtra(def)
	w := machine.WorldFromSchema(def.World)

	// Mirror production's high bar (semantic.go::semanticBars). Apps
	// that override the bar must see the same number this test sees.
	highBar := app.DefaultRoutingConfig().SemanticHighBar
	if def.Routing != nil {
		highBar = def.Routing.SemanticHighBar
	}

	var llmCount int
	for _, entry := range rec.Entries {
		state := app.StatePath(entry.State)

		// Tier 1 — deterministic. Match against menu display + intent
		// examples for the state.
		if matchDeterministic(def, m, state, w, entry.Input) {
			continue
		}

		// Tier 2 — semroute. We apply the same RequiresUnfilledSlot
		// guard the production orchestrator does (semantic.go) so the
		// calibration number reflects real production LLM cost rather
		// than over-counting bare-synonym matches that production
		// would have dropped to the LLM.
		if matcher != nil && !matcher.IsEmpty() {
			allowed := m.AllowedIntents(state, w)
			names := make([]string, len(allowed))
			for i, ai := range allowed {
				names[i] = ai.Name
			}
			verdict, _ := matcher.Match(context.Background(), entry.State, names, entry.Input)
			if verdict.Confidence >= highBar &&
				!orchestrator.RequiresUnfilledSlot(def, state, verdict.Intent, verdict.Slots) {
				continue
			}
		}

		// Tier 3 — cache. First occurrence of (state, sig) misses;
		// subsequent occurrences would hit but each recording entry
		// here is treated independently in the test loop.
		key := turncache.Key{
			App:       def.App.ID,
			AppHash:   appHash,
			StatePath: entry.State,
			Signature: lex.Signature(entry.Input, stops),
		}
		if _, found, _ := cache.Get(context.Background(), key); found {
			continue
		}

		// Tier 4 — LLM. Write back so a repeat in the loop counts as
		// a cache hit (mirrors the CLI's behaviour, even though our
		// recording doesn't repeat).
		llmCount++
		_ = cache.Put(context.Background(), key, turncache.CachedVerdict{
			Intent:    entry.Intent.Name,
			SlotsJSON: "{}",
		})
	}
	if len(rec.Entries) == 0 {
		return 0
	}
	return float64(llmCount) / float64(len(rec.Entries))
}

// matchDeterministic is the test's slim re-implementation of the
// orchestrator's TryDeterministic. Returns true when input matches a
// menu entry's display OR a unique intent example.
func matchDeterministic(def *app.AppDef, m machine.Machine, state app.StatePath, w world.World, input string) bool {
	menu := m.Menu(state, w)
	if len(menu.Primary) == 0 {
		return false
	}
	norm := normalise(input)
	for _, entry := range menu.Primary {
		if normalise(entry.Display) == norm {
			return true
		}
	}
	var hits int
	for _, entry := range menu.Primary {
		if def.Intents == nil {
			continue
		}
		intentDef, ok := def.Intents[entry.Intent]
		if !ok {
			continue
		}
		for _, ex := range intentDef.Examples {
			if normalise(ex) == norm {
				hits++
				break
			}
		}
	}
	return hits == 1
}

// normalise mirrors the orchestrator's deterministic normalisation:
// strings.ToLower + strings.TrimSpace.
func normalise(s string) string {
	out := make([]byte, 0, len(s))
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	for i := start; i < end; i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// stopwordsExtra mirrors the helper in cmd/kitsoki/replay_routing.go.
func stopwordsExtra(def *app.AppDef) []string {
	if def == nil || def.Routing == nil || len(def.Routing.StopwordsExtra) == 0 {
		return nil
	}
	return def.Routing.StopwordsExtra
}

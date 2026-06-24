// Tests for issue H1: TrySemantic must call RecordSynonymHit on every
// successful semroute hit so the inspect surfaces
// (--unused-synonyms, --routing-stats, --synonym-suggestions) see
// real production data.
//
// We exercise three sub-cases — bare synonym, implicit-example, and
// template — because the matcher distinguishes the three Kinds and
// each one needs to round-trip through the SynonymStats table.
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/turncache"
)

// newSemanticHitFixture builds an orchestrator with both an in-memory
// store and an in-memory turncache so we can read SynonymStats after
// each turn. The returned app has three intents:
//
//   - go_west declares a bare synonym "wade" (Kind = "bare")
//   - go_south uses Intent.Examples for the example path
//     (Kind = "example")
//   - buy declares a template "buy {item}" (Kind = "template")
//
// The state-machine is intentionally tiny: every intent goes from
// `start` to `ended`. The buy intent's item slot is optional so the
// template path doesn't get gated by RequiresUnfilledSlot.
func newSemanticHitFixture(t *testing.T) (*orchestrator.Orchestrator, *app.AppDef, turncache.Cache, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: semroute-hits-test
  version: 0.1.0

world: {}

intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
    synonyms: ["wade"]
  go_south:
    title: "Go south"
    examples: ["go south"]
  buy:
    title: "Buy"
    examples: ["buy stuff"]
    synonyms:
      - "buy {item}"
    slots:
      item:
        type: string
        required: false
        examples: ["food"]

root: start

states:
  start:
    view: "start"
    on:
      go_west:
        - target: ended
      go_south:
        - target: ended
      buy:
        - target: ended

  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	cache := turncache.NewMemory(turncache.DefaultConfig())
	t.Cleanup(func() { _ = cache.Close() })

	h := &countingHarness{fall: staticHarness{intentName: "go_west"}}
	orch := orchestrator.New(def, m, s, h, orchestrator.WithTurnCache(cache))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	return orch, def, cache, sid
}

// TestRecordSynonymHit_BareSynonym pins the H1 wiring for a bare
// synonym match. "wade" is a declared synonym on go_west; one Turn
// should produce one synonym_hits row with Kind="bare" and HitCount=1.
func TestRecordSynonymHit_BareSynonym(t *testing.T) {
	// No t.Parallel(): the orchestrator's machine package shares a
	// package-level `eventSeq` counter (machine.go:1426) that races
	// when many parallel Turn() calls race through. The race is
	// pre-existing and out of scope here; we serialise these tests
	// so adding them doesn't make the orchestrator package's -race
	// signal noisier.
	orch, def, cache, sid := newSemanticHitFixture(t)
	ctx := context.Background()

	_, err := orch.Turn(ctx, sid, "wade")
	require.NoError(t, err)

	stats, err := cache.SynonymStats(ctx, orchestrator.ComputeAppHash(def))
	require.NoError(t, err)
	require.Len(t, stats, 1,
		"want 1 synonym_hits row after one bare-synonym turn, got %d (%+v)", len(stats), stats)
	got := stats[0]
	if got.Intent != "go_west" || got.Pattern != "wade" || got.Kind != "bare" || got.HitCount != 1 {
		t.Errorf("synonym hit: want intent=go_west pattern=wade kind=bare hit_count=1, got intent=%s pattern=%s kind=%s hit_count=%d",
			got.Intent, got.Pattern, got.Kind, got.HitCount)
	}
}

// TestRecordSynonymHit_RepeatIncrements pins HitCount==2 after two
// identical hits. Catches a regression that always inserts a fresh
// row instead of upserting.
func TestRecordSynonymHit_RepeatIncrements(t *testing.T) {
	// Serialised — see TestRecordSynonymHit_BareSynonym comment.
	// New session per turn so the orchestrator doesn't transition to
	// terminal "ended" and refuse the second turn.
	orch, def, cache, _ := newSemanticHitFixture(t)
	ctx := context.Background()

	sid1, err := orch.NewSession(ctx)
	require.NoError(t, err)
	_, err = orch.Turn(ctx, sid1, "wade")
	require.NoError(t, err)

	sid2, err := orch.NewSession(ctx)
	require.NoError(t, err)
	_, err = orch.Turn(ctx, sid2, "wade")
	require.NoError(t, err)

	stats, err := cache.SynonymStats(ctx, orchestrator.ComputeAppHash(def))
	require.NoError(t, err)
	require.Len(t, stats, 1, "want one row, got %d (%+v)", len(stats), stats)
	if stats[0].HitCount != 2 {
		t.Errorf("HitCount: want 2 after two identical turns, got %d", stats[0].HitCount)
	}
}

// TestRecordSynonymHit_Example pins the Kind="example" path: a turn
// that resolves through Intent.Examples (implicit synonym) must
// record the hit with Kind="example" so --unused-synonyms can
// distinguish authored synonyms from menu-derived ones.
func TestRecordSynonymHit_Example(t *testing.T) {
	// No t.Parallel(): the orchestrator's machine package shares a
	// package-level `eventSeq` counter (machine.go:1426) that races
	// when many parallel Turn() calls race through. The race is
	// pre-existing and out of scope here; we serialise these tests
	// so adding them doesn't make the orchestrator package's -race
	// signal noisier.
	orch, def, cache, sid := newSemanticHitFixture(t)
	ctx := context.Background()

	// "go south" is in go_south's Examples and not anywhere else.
	// It also matches deterministically against the menu display
	// for that intent — to be SURE it's the semroute path we use
	// a tokenised phrasing the deterministic tier won't catch.
	_, err := orch.Turn(ctx, sid, "south go")
	require.NoError(t, err)

	stats, err := cache.SynonymStats(ctx, orchestrator.ComputeAppHash(def))
	require.NoError(t, err)
	require.Len(t, stats, 1,
		"want 1 synonym_hits row after example turn, got %d (%+v)", len(stats), stats)
	got := stats[0]
	if got.Kind != "example" {
		t.Errorf("Kind: want example, got %s (full=%+v)", got.Kind, got)
	}
	if got.Intent != "go_south" {
		t.Errorf("Intent: want go_south, got %s", got.Intent)
	}
}

// TestRecordSynonymHit_Template pins the Kind="template" path: a
// matched template synonym must record the verbatim template source
// as the Pattern (e.g. "buy {item}") and Kind="template". This is
// the data --routing-stats uses to break down hits by surface.
func TestRecordSynonymHit_Template(t *testing.T) {
	// No t.Parallel(): the orchestrator's machine package shares a
	// package-level `eventSeq` counter (machine.go:1426) that races
	// when many parallel Turn() calls race through. The race is
	// pre-existing and out of scope here; we serialise these tests
	// so adding them doesn't make the orchestrator package's -race
	// signal noisier.
	orch, def, cache, sid := newSemanticHitFixture(t)
	ctx := context.Background()

	// "buy food" matches template "buy {item}" with item="food".
	_, err := orch.Turn(ctx, sid, "buy food")
	require.NoError(t, err)

	stats, err := cache.SynonymStats(ctx, orchestrator.ComputeAppHash(def))
	require.NoError(t, err)
	require.Len(t, stats, 1,
		"want 1 synonym_hits row after template turn, got %d (%+v)", len(stats), stats)
	got := stats[0]
	if got.Kind != "template" {
		t.Errorf("Kind: want template, got %s (full=%+v)", got.Kind, got)
	}
	if got.Pattern != "buy {item}" {
		t.Errorf("Pattern: want %q, got %q", "buy {item}", got.Pattern)
	}
	if got.Intent != "buy" {
		t.Errorf("Intent: want buy, got %s", got.Intent)
	}
}

// TestRecordSynonymHit_NilCacheSafe pins the nil-cache guard. An
// orchestrator constructed WITHOUT WithTurnCache must not panic on a
// successful semroute hit even though there's nowhere to record the
// hit. Mirrors the same defensive style as tryTurnCache.
func TestRecordSynonymHit_NilCacheSafe(t *testing.T) {
	// Serialised — see TestRecordSynonymHit_BareSynonym comment.
	const appYAML = `
app:
  id: semroute-nil-cache
  version: 0.1.0

world: {}

intents:
  go_west:
    title: "Go west"
    examples: ["go west"]
    synonyms: ["wade"]

root: start

states:
  start:
    view: "start"
    on:
      go_west:
        - target: ended
  ended:
    terminal: true
    view: "done"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h := &countingHarness{fall: staticHarness{intentName: "go_west"}}
	orch := orchestrator.New(def, m, s, h) // NOTE: no WithTurnCache.
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// No panic; outcome is the usual "wade" → go_west → ended path.
	out, err := orch.Turn(ctx, sid, "wade")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode)
}

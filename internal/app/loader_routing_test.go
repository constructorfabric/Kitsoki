// Tests for the semantic-routing Phase 0 schema surface:
//
//   - Intent.Synonyms / Slot.Synonyms YAML round-trip.
//   - RoutingConfig / AppDef.Routing YAML round-trip.
//   - validateRouting() loader-side errors.
//   - mergeInto() collision behaviour for synonym-bearing intents.
//
// Fixtures live under testdata/routing_*.yaml. The routing surface is
// documented in docs/architecture/semantic-routing.md; the cases
// below cover every YAML branch the loader has to traverse.
package app_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
)

// ====================== helpers ======================

// loadFixture is a t.Helper-wrapped Load that puts the fixture name in
// the failure message so a single-line `require.NoError(t, err)` is
// enough at the call site.
func loadFixture(t *testing.T, name string) *app.AppDef {
	t.Helper()
	path := filepath.Join("testdata", name)
	def, err := app.Load(path)
	if err != nil {
		t.Fatalf("Load(%q): unexpected error %v", path, err)
	}
	if def == nil {
		t.Fatalf("Load(%q): want non-nil AppDef", path)
	}
	return def
}

// requireLoadError asserts Load fails and the message contains every
// substring in mustContain. Centralised so future error-message refactors
// only need to update the single assertion shape.
func requireLoadError(t *testing.T, name string, mustContain ...string) error {
	t.Helper()
	path := filepath.Join("testdata", name)
	_, err := app.Load(path)
	if err == nil {
		t.Fatalf("Load(%q): want error, got nil", path)
	}
	msg := err.Error()
	for _, sub := range mustContain {
		if !strings.Contains(msg, sub) {
			t.Errorf("Load(%q) error: want substring %q, got %q", path, sub, msg)
		}
	}
	return err
}

// ====================== synonyms round-trip ======================

func TestRouting_SynonymsRoundTrip(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_synonyms_ok.yaml")

	ford, ok := def.Intents["ford"]
	require.True(t, ok, "ford intent must be present")
	require.Equal(t, []string{"wade", "walk it", "buy {items} for {total_cost}"}, ford.Synonyms,
		"intent.synonyms must round-trip")

	pick, ok := def.Intents["pick_profession"]
	require.True(t, ok, "pick_profession intent must be present")
	prof, ok := pick.Slots["profession"]
	require.True(t, ok, "profession slot must be present")
	require.Equal(t, "enum", prof.Type)

	tests := []struct {
		name string
		key  string
		want []string
	}{
		{"banker", "banker", []string{"rich guy", "money man"}},
		{"carpenter", "carpenter", []string{"builder", "woodworker"}},
		{"farmer", "farmer", []string{"farmhand", "agricultural"}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := prof.Synonyms[tc.key]
			require.Equal(t, tc.want, got, "slot.synonyms[%q]", tc.key)
		})
	}
}

// TestRouting_DuplicateSynonymsAcceptedSilently pins the current
// behaviour: a duplicate entry in `intent.synonyms` is preserved
// verbatim by the loader. Dedupe is left to the
// semroute compile step.
func TestRouting_DuplicateSynonymsAcceptedSilently(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_dup_synonyms.yaml")
	ford := def.Intents["ford"]
	require.Equal(t, []string{"wade", "wade"}, ford.Synonyms,
		"loader must accept duplicate synonyms; dedupe is downstream")
}

// ====================== synonyms validation errors ======================

// TestRouting_LoaderValidationErrors runs the negative-fixture cases
// table-driven; each fixture is paired with the substrings that must
// appear in the error message. Centralises the error-shape assertions
// so a future error-message refactor surfaces in one place.
func TestRouting_LoaderValidationErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		fixture     string
		mustContain []string
	}{
		{
			name:    "synonyms_on_non_enum_slot",
			fixture: "routing_synonyms_nonenum.yaml",
			mustContain: []string{
				"synonyms:", "only valid on enum", "total_cost",
			},
		},
		{
			name:    "synonyms_key_not_in_values",
			fixture: "routing_synonyms_bad_key.yaml",
			mustContain: []string{
				`synonyms key "purple"`, "colour",
			},
		},
		{
			name:    "empty_synonym_string",
			fixture: "routing_empty_synonym.yaml",
			mustContain: []string{
				"synonyms[1] is empty",
			},
		},
		{
			name:    "high_bar_must_exceed_mid_bar",
			fixture: "routing_bar_order.yaml",
			mustContain: []string{
				"semantic_high_bar", "must be greater than", "semantic_mid_bar",
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireLoadError(t, tc.fixture, tc.mustContain...)
		})
	}
}

// ====================== routing block round-trip ======================

func TestRouting_NoRoutingBlock(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_no_block.yaml")
	require.Nil(t, def.Routing, "missing routing: block must leave Routing nil so callers fall back to defaults")

	// Pin the default values: any drift here means the routing
	// defaults are silently changing.
	d := app.DefaultRoutingConfig()
	require.True(t, d.Enabled, "DefaultRoutingConfig.Enabled")
	require.InDelta(t, 0.80, d.SemanticHighBar, 1e-9, "DefaultRoutingConfig.SemanticHighBar")
	require.InDelta(t, 0.65, d.SemanticMidBar, 1e-9, "DefaultRoutingConfig.SemanticMidBar")
	require.True(t, d.CacheEnabled, "DefaultRoutingConfig.CacheEnabled")
	require.Equal(t, 30*24*time.Hour, d.CacheMaxAge.AsTimeDuration(), "DefaultRoutingConfig.CacheMaxAge")
	require.Equal(t, 10000, d.CacheCap, "DefaultRoutingConfig.CacheCap")
	require.InDelta(t, 0.10, d.CacheTrimFraction, 1e-9, "DefaultRoutingConfig.CacheTrimFraction")
	require.Equal(t, 3, d.RevalidateStrikes, "DefaultRoutingConfig.RevalidateStrikes")
	require.False(t, d.ConfidenceDecay, "DefaultRoutingConfig.ConfidenceDecay")
	require.False(t, d.ExtractLLMOnNoMatch, "DefaultRoutingConfig.ExtractLLMOnNoMatch must default off (opt-in)")
}

// TestRouting_EmptyBlockGetsDefaults pins the documented behaviour for
// `routing: {}` — an entirely empty mapping is NOT a parse error;
// UnmarshalYAML seeds DefaultRoutingConfig before decoding so every
// field lands on its default.
func TestRouting_EmptyBlockGetsDefaults(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_empty_block.yaml")
	require.NotNil(t, def.Routing, "routing: {} must produce a non-nil *RoutingConfig (not a parse error)")
	got := *def.Routing
	want := app.DefaultRoutingConfig()
	require.Equal(t, want, got, "empty routing: {} must equal DefaultRoutingConfig()")
}

func TestRouting_PartialBlockTakesDefaults(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_partial_block.yaml")
	require.NotNil(t, def.Routing)
	r := def.Routing

	// Author-set field survives.
	require.False(t, r.Enabled, "author wrote enabled:false; must not be overwritten by default-fill")

	// Every other field takes its default. Table-driven so the failure
	// message names which field drifted.
	tests := []struct {
		name string
		want any
		got  any
	}{
		{"CacheEnabled", true, r.CacheEnabled},
		{"SemanticHighBar", 0.80, r.SemanticHighBar},
		{"SemanticMidBar", 0.65, r.SemanticMidBar},
		{"CacheMaxAge", 30 * 24 * time.Hour, r.CacheMaxAge.AsTimeDuration()},
		{"CacheCap", 10000, r.CacheCap},
		{"CacheTrimFraction", 0.10, r.CacheTrimFraction},
		{"RevalidateStrikes", 3, r.RevalidateStrikes},
		{"ConfidenceDecay", false, r.ConfidenceDecay},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.got, "partial block: %s drifted from default", tc.name)
		})
	}
}

func TestRouting_FullBlockRoundTrip(t *testing.T) {
	t.Parallel()
	def := loadFixture(t, "routing_full_block.yaml")
	require.NotNil(t, def.Routing)
	r := def.Routing

	tests := []struct {
		name string
		want any
		got  any
	}{
		{"Enabled", true, r.Enabled},
		{"SemanticHighBar", 0.85, r.SemanticHighBar},
		{"SemanticMidBar", 0.55, r.SemanticMidBar},
		{"CacheEnabled", false, r.CacheEnabled},
		{"CacheMaxAge", 7 * 24 * time.Hour, r.CacheMaxAge.AsTimeDuration()},
		{"StopwordsExtra", []string{"yall", "wagon"}, r.StopwordsExtra},
		{"CacheCap", 500, r.CacheCap},
		{"CacheTrimFraction", 0.25, r.CacheTrimFraction},
		{"RevalidateStrikes", 5, r.RevalidateStrikes},
		{"ConfidenceDecay", true, r.ConfidenceDecay},
		{"ExtractLLMOnNoMatch", true, r.ExtractLLMOnNoMatch},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.got, "full block: %s did not round-trip", tc.name)
		})
	}
}

// ====================== import-merge collisions ======================

// TestRouting_IncludeCollision_IntentSynonyms confirms two stories
// declaring the same intent (with synonyms on one side) collide loudly
// rather than silently unioning. The exact error-message shape is
// pinned so a future refactor of mergeInto can't quietly change it.
func TestRouting_IncludeCollision_IntentSynonyms(t *testing.T) {
	t.Parallel()
	requireLoadError(t,
		filepath.Join("routing_intent_collision", "main.yaml"),
		`intent "ford" is already declared`,
	)
}

// TestRouting_IncludeCollision_SlotSynonyms confirms two stories that
// declare the same intent with overlapping slot.synonyms collide
// loudly. Same rule as above — slot synonyms live inside the intent
// value, so the intent-collision check governs them.
func TestRouting_IncludeCollision_SlotSynonyms(t *testing.T) {
	t.Parallel()
	requireLoadError(t,
		filepath.Join("routing_slot_collision", "main.yaml"),
		`intent "pick" is already declared`,
	)
}

// ====================== DefaultRoutingConfig invariants ======================

func TestDefaultRoutingConfig_WithDefaultsIdempotent(t *testing.T) {
	t.Parallel()
	d := app.DefaultRoutingConfig()
	require.Equal(t, d, d.WithDefaults(), "WithDefaults must be a no-op on a value already filled from DefaultRoutingConfig")
}

// TestDefaultRoutingConfig_WithDefaultsFillsZeroes is a one-call shape
// check: starting from the zero value, WithDefaults must replace every
// numeric/duration field with its default. (Bool fields are documented
// to pass through unchanged.)
func TestDefaultRoutingConfig_WithDefaultsFillsZeroes(t *testing.T) {
	t.Parallel()
	zero := app.RoutingConfig{}
	got := zero.WithDefaults()
	want := app.DefaultRoutingConfig()

	// Numeric / duration fields must match the defaults.
	require.InDelta(t, want.SemanticHighBar, got.SemanticHighBar, 1e-9)
	require.InDelta(t, want.SemanticMidBar, got.SemanticMidBar, 1e-9)
	require.Equal(t, want.CacheMaxAge, got.CacheMaxAge)
	require.Equal(t, want.CacheCap, got.CacheCap)
	require.InDelta(t, want.CacheTrimFraction, got.CacheTrimFraction, 1e-9)
	require.Equal(t, want.RevalidateStrikes, got.RevalidateStrikes)

	// Bool fields are documented to pass through — confirm WithDefaults
	// does NOT seed them from the zero value (that's UnmarshalYAML's job).
	require.False(t, got.Enabled, "WithDefaults must NOT flip Enabled from false")
	require.False(t, got.CacheEnabled, "WithDefaults must NOT flip CacheEnabled from false")
}

package studio

// session_digest_internal_test.go — proves the world_digest carried on an
// advancing turn's frame metadata is bounded so it cannot blow the MCP
// tool-result cap (the P2 bug: deep-import rooms embedded ~350 alias-prefixed
// keys / 78k+ chars, spilling the state-transition result to a file).
//
//   - TestProjectWorldDigest_DefaultBoundsBloatedDigest: the zero projection
//     (no flag) drops empty keys and truncates long values, shrinking a
//     deep-import-shaped digest from tens of thousands of chars to a small,
//     readable payload while preserving every non-empty key.
//   - TestProjectWorldDigest_OmitDropsDigest: omit:true returns nil.
//   - TestProjectWorldDigest_ExplicitMaxValueLen: an explicit maxLen overrides
//     the default truncation.
//   - TestTurnResponse_BoundedFrameDigest: the full turnResponse → frame
//     projection keeps the marshalled advancing-turn payload under the cap.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui"
)

// bloatedDigest builds a digest shaped like a deep-import room (stories/
// kitsoki-dev core.bf.*): ~360 alias-prefixed keys, a mix of empty values,
// short values, and a handful of long content-bearing values (rendered diffs /
// prompts). Sized to reproduce the real bug, where a bf__accept turn's full
// digest measured ~78k chars and spilled the tool-result to a file.
func bloatedDigest() map[string]any {
	longVal := "diff --git a/internal/app/foo.go b/internal/app/foo.go\n" +
		strings.Repeat("+ a fairly long rendered line of a diff or prompt body\n", 60)
	d := make(map[string]any, 360)
	prefixes := []string{"core__bf__", "core__cyp__", "core__impl__", "core__gitops__"}
	for _, p := range prefixes {
		for i := 0; i < 90; i++ {
			key := p + "field" + string(rune('a'+i%26)) + itoa(i)
			switch {
			case i%3 == 0:
				d[key] = "" // empty-valued: pure key-count bloat
			case i%15 == 1:
				d[key] = longVal // a few long content-bearing values
			default:
				d[key] = "a moderate world value like a state name or short prompt"
			}
		}
	}
	return d
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func digestSize(t *testing.T, d map[string]any) int {
	t.Helper()
	b, err := json.Marshal(d)
	require.NoError(t, err)
	return len(b)
}

// TestProjectWorldDigest_DefaultBoundsBloatedDigest is the core regression: the
// default (zero) projection must shrink a deep-import-shaped digest dramatically
// while keeping every non-empty key, and every value bounded by the default
// truncation.
func TestProjectWorldDigest_DefaultBoundsBloatedDigest(t *testing.T) {
	full := bloatedDigest()
	before := digestSize(t, full)

	bounded := projectWorldDigest(full, digestProjection{}) // no flag → safe default
	after := digestSize(t, bounded)

	t.Logf("world_digest payload: before=%d chars  after=%d chars  (default projection)", before, after)

	// The unbounded digest is over the kind of size that spilled to a file; the
	// bounded one is a fraction of it and below the real measured-failure size
	// (a bf__accept turn measured ~78k chars before this fix).
	require.Greater(t, before, 78_000, "fixture should reproduce the measured bloat (>78k)")
	assert.Less(t, after, before/3, "default projection must cut the payload to <1/3")
	assert.Less(t, after, 50_000, "bounded payload must drop well below the measured-failure size")

	// Every empty-valued key is dropped; every non-empty key is preserved.
	emptyKeys, nonEmptyKeys := 0, 0
	for k, v := range full {
		if s, ok := v.(string); ok && s == "" {
			emptyKeys++
			_, present := bounded[k]
			assert.False(t, present, "empty-valued key %q must be dropped", k)
		} else {
			nonEmptyKeys++
			_, present := bounded[k]
			assert.True(t, present, "non-empty key %q must be preserved", k)
		}
	}
	require.Positive(t, emptyKeys, "fixture must contain empty keys")
	assert.Len(t, bounded, nonEmptyKeys, "bounded digest keeps exactly the non-empty keys")

	// No value exceeds the default truncation (+ ellipsis rune).
	for k, v := range bounded {
		s, ok := v.(string)
		require.True(t, ok, "bounded values are strings; key %q is %T", k, v)
		assert.LessOrEqual(t, len([]rune(strings.TrimSuffix(s, "…"))), defaultDigestValueLen,
			"value for %q exceeds default truncation", k)
	}
}

// TestProjectWorldDigest_OmitDropsDigest confirms omit:true drops the digest.
func TestProjectWorldDigest_OmitDropsDigest(t *testing.T) {
	assert.Nil(t, projectWorldDigest(bloatedDigest(), digestProjection{omit: true}))
}

// TestProjectWorldDigest_ExplicitMaxValueLen confirms an explicit maxLen
// overrides the default truncation.
func TestProjectWorldDigest_ExplicitMaxValueLen(t *testing.T) {
	const maxLen = 8
	bounded := projectWorldDigest(bloatedDigest(), digestProjection{maxLen: maxLen})
	for k, v := range bounded {
		s := v.(string)
		assert.LessOrEqual(t, len([]rune(strings.TrimSuffix(s, "…"))), maxLen,
			"value for %q exceeds explicit max_value_len", k)
	}
}

// TestTurnResponse_BoundedFrameDigest exercises the full turnResponse → frame
// projection an advancing call returns and asserts the marshalled payload is
// bounded under the default, omitted when requested, and full under no
// projection only via the read-only frameResult path is NOT what advancing uses.
func TestTurnResponse_BoundedFrameDigest(t *testing.T) {
	frame := tui.Frame{
		Text:  "screen",
		Width: 80, Height: 24,
		Metadata: tui.FrameMeta{
			State:       "review",
			WorldDigest: bloatedDigest(),
		},
	}
	out := &orchestrator.TurnOutcome{NewState: "review"}

	// Default projection (what drive/submit/continue pass with no flag).
	def := turnResponse(out, frame, nil, digestProjection{})
	defBytes, err := json.Marshal(def)
	require.NoError(t, err)

	// Unbounded baseline for comparison (the old behavior — full digest).
	fullBytes, err := json.Marshal(struct {
		WD map[string]any `json:"world_digest"`
	}{frame.Metadata.WorldDigest})
	require.NoError(t, err)

	t.Logf("advancing TurnResponse payload: full_digest≈%d chars  default_projection=%d chars",
		len(fullBytes), len(defBytes))

	assert.Less(t, len(defBytes), len(fullBytes)/3,
		"default-projected turn payload must be far smaller than the full digest")
	assert.Less(t, len(defBytes), 78_000,
		"default-projected turn payload drops below the measured-failure size")

	// omit_world:true drops the digest entirely — the escape hatch for the very
	// largest worlds where even the truncated digest is unwanted.
	omitted := turnResponse(out, frame, nil, digestProjection{omit: true})
	assert.Nil(t, omitted.Frame.Metadata.WorldDigest, "omit_world must drop the frame digest")
	omitBytes, err := json.Marshal(omitted)
	require.NoError(t, err)
	t.Logf("omit_world advancing payload: %d chars", len(omitBytes))
	assert.Less(t, len(omitBytes), 2_000, "omit_world payload is tiny regardless of world size")
}

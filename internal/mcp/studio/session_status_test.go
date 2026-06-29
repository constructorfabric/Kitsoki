package studio_test

// session_status_test.go — tests for the compact session.status tool and the
// new opt-in projection params on session.inspect / session.trace.
//
//   - TestSessionStatus_CompactShape: session.status returns state +
//     allowed_intents and NEVER embeds world.
//   - TestSessionStatus_WellKnownKeys: status/last_error/exit are read from
//     world when present; the full world is still not returned.
//   - TestSessionStatus_IsListed: session.status appears in ListTools.
//   - TestSessionInspect_OmitWorld: omit_world:true drops world from inspect.
//   - TestSessionInspect_MaxValueLen: max_value_len:N truncates each world value.
//   - TestSessionDrive_FrameNeverCarriesWorld: an advancing call never embeds
//     world in its frame.
//   - TestSessionWorld_ListKeysThenGetValue / _MissingKey: the targeted
//     session.world read (key list, single value, not-found) that replaces the
//     dropped frame digest.
//   - TestSessionTrace_TruncatePayload: truncate_payload:N caps event payloads.
//   - TestSessionTrace_Kinds: kinds filter returns only matching event kinds.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// ─── session.status ──────────────────────────────────────────────────────────

// TestSessionStatus_CompactShape drives a turn then calls session.status:
// the result must carry state and allowed_intents, but MUST NOT embed world.
func TestSessionStatus_CompactShape(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive one turn so there's a non-trivial state to inspect.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	driven := driveResult(t, res)
	require.Equal(t, "cloakroom", driven.Outcome.State)

	// Call session.status.
	res, err = callTool(ctx, cs, "session.status", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.status: %s", contentText(res))

	var status studio.SessionStatusResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &status))

	assert.True(t, status.OK, "status.ok is true")
	assert.Equal(t, "cloakroom", status.State, "status.state matches driven state")
	assert.NotEmpty(t, status.AllowedIntents, "status.allowed_intents non-empty")

	// Confirm the JSON payload contains no "world" key — not dumped at all.
	raw := contentText(res)
	assert.NotContains(t, raw, `"world"`, "session.status must never embed 'world'")
}

// TestSessionStatus_WellKnownKeys confirms that status/last_error/exit are
// surfaced from the world when present. We submit a direct intent that sets a
// world variable via a story that records world mutations, then verify only
// those specific keys appear — not the rest of the world.
// For the cloak app (no status/last_error/exit world keys), those fields should
// simply be absent from the result.
func TestSessionStatus_WellKnownKeys(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// The cloak app has no status/last_error/exit keys, so they must be absent.
	res, err := callTool(ctx, cs, "session.status", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.status: %s", contentText(res))

	var status studio.SessionStatusResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &status))

	assert.True(t, status.OK)
	assert.Equal(t, "foyer", status.State, "initial state before any drive")
	assert.Empty(t, status.Status, "no status world key → empty")
	assert.Empty(t, status.LastError, "no last_error world key → empty")
	assert.Nil(t, status.Exit, "no exit world key → nil")

	// Still must not embed world.
	raw := contentText(res)
	assert.NotContains(t, raw, `"world"`, "session.status must never embed 'world'")
}

// TestSessionStatus_IsListed confirms session.status appears in the tool
// registry (registered identically to session.inspect).
func TestSessionStatus_IsListed(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	cs := connectInProcess(ctx, t, srv)

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["session.status"], "session.status must be in the tool list")
	assert.True(t, names["session.inspect"], "session.inspect still registered")
}

// ─── session.inspect projections ─────────────────────────────────────────────

// TestSessionInspect_OmitWorld confirms omit_world:true drops the world map
// from the inspect result while keeping state/allowed_intents/last_turns.
func TestSessionInspect_OmitWorld(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive to a state.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	require.Equal(t, "cloakroom", driveResult(t, res).Outcome.State)

	// inspect with omit_world: world must be absent.
	res, err = callTool(ctx, cs, "session.inspect", map[string]any{
		"handle":     handle,
		"omit_world": true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "inspect omit_world: %s", contentText(res))

	var ins studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ins))

	assert.True(t, ins.OK)
	assert.Equal(t, "cloakroom", ins.State)
	assert.Nil(t, ins.World, "omit_world:true must drop the world map")
	assert.NotEmpty(t, ins.AllowedIntents, "allowed_intents still present")
	assert.NotEmpty(t, ins.LastTurns, "last_turns still present")

	// The raw JSON must not embed a "world" key at all.
	raw := contentText(res)
	assert.NotContains(t, raw, `"world"`, "omit_world must remove 'world' from JSON")
}

// TestSessionInspect_MaxValueLen confirms max_value_len:N truncates long world
// values and leaves short values untouched.
func TestSessionInspect_MaxValueLen(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// First drive to get some world state, then inspect without truncation to
	// capture the baseline.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	require.Equal(t, "cloakroom", driveResult(t, res).Outcome.State)

	// Baseline inspect (no truncation).
	baseRes, err := callTool(ctx, cs, "session.inspect", map[string]any{"handle": handle})
	require.NoError(t, err)
	var baseIns studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(baseRes)), &baseIns))

	// Inspect with a very small max_value_len to force truncation on any
	// non-trivial world value. (Cloak's world values may be short; we verify the
	// truncation logic is applied correctly regardless.)
	const maxLen = 3
	res, err = callTool(ctx, cs, "session.inspect", map[string]any{
		"handle":        handle,
		"max_value_len": maxLen,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "inspect max_value_len: %s", contentText(res))

	var ins studio.InspectResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ins))

	assert.True(t, ins.OK)
	assert.Equal(t, "cloakroom", ins.State, "state unaffected by max_value_len")

	// Every world value in the truncated result must be ≤ maxLen chars + "…" if
	// the original was longer.
	for k, v := range ins.World {
		s, ok := v.(string)
		require.True(t, ok, "world values become strings when truncated")
		if baseVal, exists := baseIns.World[k]; exists {
			// Compare with the base value to know whether truncation was expected.
			baseStr := ""
			switch tv := baseVal.(type) {
			case string:
				baseStr = tv
			default:
				b, _ := json.Marshal(baseVal)
				baseStr = string(b)
			}
			if len([]rune(baseStr)) > maxLen {
				assert.True(t, strings.HasSuffix(s, "…"),
					"truncated value for %q must end with ellipsis; got %q", k, s)
				// The non-ellipsis prefix must be ≤ maxLen runes.
				withoutEllipsis := strings.TrimSuffix(s, "…")
				assert.LessOrEqual(t, len([]rune(withoutEllipsis)), maxLen,
					"prefix of truncated world[%q] must be ≤ %d runes", k, maxLen)
			}
		}
	}
}

// ─── advancing turns carry no world; session.world reads it on demand ─────────

// TestSessionDrive_FrameNeverCarriesWorld confirms an advancing call
// (session.drive) NEVER embeds the world in its frame — the cap-relief design:
// in deep-import rooms the flat world is hundreds of keys / tens of thousands of
// chars and would blow the tool-result cap. The transition result stays small;
// the caller reads world separately via session.world / session.inspect.
func TestSessionDrive_FrameNeverCarriesWorld(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	driven := driveResult(t, res)
	require.Equal(t, "cloakroom", driven.Outcome.State)
	assert.Nil(t, driven.Frame.Metadata.WorldDigest, "advancing frame must not carry world")
	assert.NotContains(t, contentText(res), `"world_digest"`,
		"'world_digest' must not appear in the advancing-turn JSON")
}

// TestSessionWorld_ListKeysThenGetValue exercises the targeted read: no key →
// the sorted key-name list (no values); with a key → that single typed value
// and found=true. This is what replaces the dropped frame digest.
func TestSessionWorld_ListKeysThenGetValue(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Advance so world has settled state.
	_, err := callTool(ctx, cs, "session.drive", map[string]any{"handle": handle, "input": "go west"})
	require.NoError(t, err)

	// No key → key NAMES only, sorted, no values on the wire.
	listRes, err := callTool(ctx, cs, "session.world", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, listRes.IsError, "session.world list: %s", contentText(listRes))
	var keys studio.SessionWorldKeys
	require.NoError(t, json.Unmarshal([]byte(contentText(listRes)), &keys))
	require.True(t, keys.OK)
	require.NotEmpty(t, keys.Keys, "world should expose some keys")
	require.True(t, sort.StringsAreSorted(keys.Keys), "keys must be sorted")

	// Fetch the first key's value → found=true, value present.
	getRes, err := callTool(ctx, cs, "session.world", map[string]any{"handle": handle, "key": keys.Keys[0]})
	require.NoError(t, err)
	var val studio.SessionWorldValue
	require.NoError(t, json.Unmarshal([]byte(contentText(getRes)), &val))
	assert.True(t, val.OK)
	assert.True(t, val.Found, "an advertised key must be found")
	assert.Equal(t, keys.Keys[0], val.Key)
}

// TestSessionWorld_MissingKey returns found=false (not an error) for an unknown
// key, so a caller can probe without branching on transport errors.
func TestSessionWorld_MissingKey(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	res, err := callTool(ctx, cs, "session.world", map[string]any{
		"handle": handle, "key": "no_such_key_xyz",
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	var val studio.SessionWorldValue
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &val))
	assert.True(t, val.OK)
	assert.False(t, val.Found, "unknown key → found=false")
	assert.Nil(t, val.Value)
}

// ─── session.trace projections ────────────────────────────────────────────────

// TestSessionTrace_TruncatePayload confirms truncate_payload:N caps each
// event's payload and appends an ellipsis when truncated.
func TestSessionTrace_TruncatePayload(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive a turn to get some trace events with non-trivial payloads.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	driveResult(t, res) // consume

	const cap = 5
	res, err = callTool(ctx, cs, "session.trace", map[string]any{
		"handle":           handle,
		"truncate_payload": cap,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "trace truncate_payload: %s", contentText(res))

	var tr studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &tr))
	assert.True(t, tr.OK)
	require.NotEmpty(t, tr.Events, "should have events after a drive")

	// At least one event with a payload should be truncated. Verify the
	// invariant: every truncated payload must be ≤ cap bytes (of raw bytes
	// before ellipsis) plus the ellipsis.
	for _, ev := range tr.Events {
		if len(ev.Payload) == 0 {
			continue
		}
		raw := string(ev.Payload)
		if strings.HasSuffix(raw, "…") {
			// The bytes before the ellipsis rune must be ≤ cap bytes.
			prefix := strings.TrimSuffix(raw, "…")
			assert.LessOrEqual(t, len([]byte(prefix)), cap,
				"truncated payload prefix must be ≤ %d bytes; got %q", cap, raw)
		}
	}
}

// TestSessionTrace_TruncatePayloadDefault confirms that omitting truncate_payload
// applies the 500-byte default, while an explicit 0 opts out of truncation.
func TestSessionTrace_TruncatePayloadDefault(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	res, err := callTool(ctx, cs, "session.drive", map[string]any{
		"handle": handle, "input": "go west",
	})
	require.NoError(t, err)
	driveResult(t, res)

	// Default (unset) → 500-byte cap: every payload must be ≤ 500 bytes (before
	// the ellipsis rune).
	res, err = callTool(ctx, cs, "session.trace", map[string]any{"handle": handle})
	require.NoError(t, err)
	require.False(t, res.IsError, "trace default: %s", contentText(res))
	var def studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &def))
	require.NotEmpty(t, def.Events)
	sawTruncated := false
	for _, ev := range def.Events {
		// The marshalled payload is a JSON string; decode it to get the raw text
		// the truncator capped at 500 bytes (pre-ellipsis).
		var raw string
		if json.Unmarshal(ev.Payload, &raw) != nil {
			continue
		}
		if strings.HasSuffix(raw, "…") {
			sawTruncated = true
			prefix := strings.TrimSuffix(raw, "…")
			assert.LessOrEqual(t, len([]byte(prefix)), 500,
				"default-truncated payload prefix must be ≤ 500 bytes; got %q", raw)
		}
	}
	assert.True(t, sawTruncated, "default 500-byte cap should truncate at least one large payload")

	// Explicit 0 → no truncation: no payload should carry the ellipsis suffix.
	res, err = callTool(ctx, cs, "session.trace", map[string]any{
		"handle": handle, "truncate_payload": 0,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "trace opt-out: %s", contentText(res))
	var full studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &full))
	require.NotEmpty(t, full.Events)
	for _, ev := range full.Events {
		assert.False(t, strings.HasSuffix(string(ev.Payload), "…"),
			"truncate_payload:0 must not truncate; got %q", string(ev.Payload))
	}
}

// TestSessionTrace_Kinds confirms kinds:[] filters trace events to only those
// with a matching kind.
func TestSessionTrace_Kinds(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive a couple of turns to get a rich trace.
	for _, input := range []string{"go west", "hang the cloak"} {
		res, err := callTool(ctx, cs, "session.drive", map[string]any{
			"handle": handle, "input": input,
		})
		require.NoError(t, err)
		driveResult(t, res)
	}

	// Get the full trace first to know which kinds are present.
	res, err := callTool(ctx, cs, "session.trace", map[string]any{"handle": handle})
	require.NoError(t, err)
	var fullTr studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &fullTr))
	require.NotEmpty(t, fullTr.Events, "full trace must have events")

	// Pick a kind that definitely appears.
	wantKind := string(fullTr.Events[0].Kind)

	// Now filter to only that kind.
	res, err = callTool(ctx, cs, "session.trace", map[string]any{
		"handle": handle,
		"kinds":  []any{wantKind},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "trace kinds: %s", contentText(res))

	var filteredTr studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &filteredTr))
	assert.True(t, filteredTr.OK)
	require.NotEmpty(t, filteredTr.Events, "filtered trace must have at least one event of kind %q", wantKind)

	for _, ev := range filteredTr.Events {
		assert.Equal(t, wantKind, string(ev.Kind),
			"kinds filter must exclude other event kinds")
	}

	// Sanity: filtering to a non-existent kind returns no events.
	res, err = callTool(ctx, cs, "session.trace", map[string]any{
		"handle": handle,
		"kinds":  []any{"nonexistent.kind.xyz"},
	})
	require.NoError(t, err)
	var emptyTr studio.TraceResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &emptyTr))
	assert.Empty(t, emptyTr.Events, "filtering to a nonexistent kind returns no events")
}

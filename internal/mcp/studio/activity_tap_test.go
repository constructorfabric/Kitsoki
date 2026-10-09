package studio

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// The tap must report the live in-flight state and the active agent while a
// turn runs (never-silent): pollers watching session.status decide whether a
// run is healthy from exactly these fields.
func TestActivityTap_RecordsLiveStateAndStickyAgent(t *testing.T) {
	tap := &activityTap{}

	_, seen := tap.snapshot()
	require.False(t, seen, "fresh tap has no activity")

	agentPayload, err := json.Marshal(map[string]any{"verb": "task", "agent": "bf__reproducer"})
	require.NoError(t, err)

	base := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	tap.record(store.Event{Kind: store.TurnStarted, StatePath: app.StatePath("bf.idle"), Ts: base})
	tap.record(store.Event{Kind: store.AgentCalled, StatePath: app.StatePath("bf.reproducing"), Ts: base.Add(time.Second), Payload: agentPayload})
	tap.record(store.Event{Kind: store.AgentStreamEvent, StatePath: app.StatePath("bf.reproducing"), Ts: base.Add(2 * time.Second)})

	act, seen := tap.snapshot()
	require.True(t, seen)
	assert.Equal(t, "bf.reproducing", act.statePath, "live in-flight state, not the resting state")
	assert.Equal(t, string(store.AgentStreamEvent), act.kind)
	assert.Equal(t, base.Add(2*time.Second), act.ts)
	assert.Equal(t, "bf__reproducer", act.agent, "agent stays sticky across stream events")

	tap.record(store.Event{Kind: store.AgentReturned, StatePath: app.StatePath("bf.reproducing"), Ts: base.Add(3 * time.Second)})
	act, _ = tap.snapshot()
	assert.Empty(t, act.agent, "agent clears once the call completes")
	assert.Equal(t, "bf.reproducing", act.statePath)
}

// decorateRunning projects the tap onto the wire struct; a tap that has seen
// nothing must leave the RunningDrive untouched.
func TestDecorateRunning_FillsInFlightFields(t *testing.T) {
	rt := &sessionRuntime{tap: &activityTap{}}
	running := &RunningDrive{Handle: "s1", Poll: "session.status"}

	rt.decorateRunning(running)
	assert.Empty(t, running.InFlightState, "no activity yet — no in-flight claims")

	ts := time.Date(2026, 7, 5, 12, 0, 5, 0, time.UTC)
	rt.tap.record(store.Event{Kind: store.AgentStreamEvent, StatePath: app.StatePath("bf.reproducing"), Ts: ts})
	rt.decorateRunning(running)
	assert.Equal(t, "bf.reproducing", running.InFlightState)
	assert.Equal(t, string(store.AgentStreamEvent), running.LastEventKind)
	assert.Equal(t, ts.UnixMicro(), running.LastEventAtUnixMicro)
}

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// loader_write_mode_test.go — load-time invariants for the write_mode: room
// posture (agent-write-mode-opt-in.md "Load-time invariants").

// write_mode: read_only loads cleanly on an agent-dispatch room (a host.agent.task
// effect), and the posture is preserved on the State.
func TestWriteMode_ReadOnly_OnTaskRoom(t *testing.T) {
	yaml := `app:
  id: wm-ok
  version: 0.1.0
hosts:
  - host.agent.task
intents:
  go:
    title: Go
root: workbench
states:
  workbench:
    write_mode: read_only
    on:
      go:
        - target: workbench
          effects:
            - invoke: host.agent.task
              with:
                agent: builder
                acceptance: { schema: schemas/out.json }
agents:
  builder:
    system_prompt: "build things"
    external_side_effect: false
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	require.Equal(t, WriteModeReadOnly, def.States["workbench"].WriteMode)
}

// write_mode: read_only on a conversational room (which drives converse) loads.
func TestWriteMode_ReadOnly_OnConversationalRoom(t *testing.T) {
	yaml := `app:
  id: wm-conv
  version: 0.1.0
intents:
  go:
    title: Go
root: chat
states:
  chat:
    mode: conversational
    write_mode: read_only
    on:
      go: [{ target: chat }]
`
	_, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
}

// An invalid write_mode value is rejected.
func TestWriteMode_InvalidValue(t *testing.T) {
	yaml := `app:
  id: wm-bad
  version: 0.1.0
intents:
  go:
    title: Go
root: chat
states:
  chat:
    mode: conversational
    write_mode: yolo
    on:
      go: [{ target: chat }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "write_mode")
	require.Contains(t, err.Error(), "invalid")
}

// write_mode: read_only on a NON-agent room (no task/converse/off-ramp) is
// rejected — it would silently do nothing.
func TestWriteMode_ReadOnly_OnNonAgentRoom(t *testing.T) {
	yaml := `app:
  id: wm-noagent
  version: 0.1.0
intents:
  go:
    title: Go
root: plain
states:
  plain:
    view: "just a room"
    write_mode: read_only
    on:
      go: [{ target: plain }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "dispatches an agent")
}

// write_mode: read_only contradicting a statically write-capable agent
// (external_side_effect: true) is rejected.
func TestWriteMode_ReadOnly_StaticWriteContradiction(t *testing.T) {
	yaml := `app:
  id: wm-contradiction
  version: 0.1.0
hosts:
  - host.agent.task
intents:
  go:
    title: Go
root: workbench
states:
  workbench:
    write_mode: read_only
    on:
      go:
        - target: workbench
          effects:
            - invoke: host.agent.task
              with:
                agent: writer
                acceptance: { schema: schemas/out.json }
agents:
  writer:
    system_prompt: "writes freely"
    external_side_effect: true
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "external_side_effect: true")
}

// A story that `set:`s the engine-reserved write_mode_scope key is rejected (a
// story must not self-grant write mode).
func TestWriteMode_RejectsStorySetOfScopeKey(t *testing.T) {
	yaml := `app:
  id: wm-forge
  version: 0.1.0
world:
  some_var: { type: string }
intents:
  go:
    title: Go
root: chat
states:
  chat:
    mode: conversational
    on:
      go:
        - target: chat
          effects:
            - set: { write_mode_scope: session }
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "write_mode_scope")
	require.Contains(t, err.Error(), "engine-reserved")
}

func TestWriteMode_RejectsStorySetOfOperationRunKey(t *testing.T) {
	yaml := `app:
  id: op-run-forge
  version: 0.1.0
intents:
  go:
    title: Go
root: chat
states:
  chat:
    mode: conversational
    on:
      go:
        - target: chat
          effects:
            - set:
                operation_run:
                  status: completed
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), OperationRunWorldKey)
	require.Contains(t, err.Error(), "engine-reserved")
}

// Absent write_mode is the default-off path: a plain room loads with WriteMode "".
func TestWriteMode_AbsentIsDefaultOff(t *testing.T) {
	yaml := `app:
  id: wm-absent
  version: 0.1.0
intents:
  go:
    title: Go
root: plain
states:
  plain:
    view: "a room"
    on:
      go: [{ target: plain }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	require.Equal(t, "", def.States["plain"].WriteMode)
}

// write_mode_scope is a recognised world key for references (it is reserved) even
// when not declared, but `set:` is still rejected (covered above). This guards
// the reserved-key membership.
func TestWriteMode_ScopeKeyIsReserved(t *testing.T) {
	_, ok := ReservedWorldKeys[WriteModeScopeWorldKey]
	require.True(t, ok, "write_mode_scope must be an engine-reserved world key")
	require.False(t, strings.Contains(WriteModeScopeWorldKey, " "))

	_, ok = ReservedWorldKeys[OperationRunWorldKey]
	require.True(t, ok, "operation_run must be an engine-reserved world key")
	require.False(t, strings.Contains(OperationRunWorldKey, " "))
}

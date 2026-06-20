package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// loader_intercept_drive_test.go — load-time invariants for the intercept_drive:
// room flag (conflict-capable intercept; see prompt-intercept.md §"Multi-turn
// commands"). The only valid value is "rest", and it is only meaningful on a
// top-level room.

// intercept_drive: rest loads cleanly on a top-level room and is preserved.
func TestInterceptDrive_Rest_OnTopLevelRoom(t *testing.T) {
	yaml := `app:
  id: id-ok
  version: 0.1.0
intents:
  go:
    title: Go
root: work
states:
  work:
    intercept_drive: rest
    on:
      go: [{ target: work }]
`
	def, err := LoadBytes([]byte(yaml))
	require.NoError(t, err)
	require.Equal(t, InterceptDriveRest, def.States["work"].InterceptDrive)
}

// An intercept_drive value other than "rest" is rejected at load time so a typo
// can't silently disable the multi-turn drive.
func TestInterceptDrive_InvalidValue(t *testing.T) {
	yaml := `app:
  id: id-bad
  version: 0.1.0
intents:
  go:
    title: Go
root: work
states:
  work:
    intercept_drive: drive
    on:
      go: [{ target: work }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "intercept_drive")
	require.Contains(t, err.Error(), "the only valid value is \"rest\"")
}

// intercept_drive on a NESTED state is rejected: the gate binds to and drives
// whole rooms, so a nested-leaf flag would never fire.
func TestInterceptDrive_RejectedOnNestedState(t *testing.T) {
	yaml := `app:
  id: id-nested
  version: 0.1.0
intents:
  go:
    title: Go
root: outer
states:
  outer:
    type: compound
    initial: inner
    states:
      inner:
        intercept_drive: rest
        on:
          go: [{ target: inner }]
`
	_, err := LoadBytes([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "only meaningful on a top-level room")
}

// The real git-ops conflict room carries intercept_drive: rest — the flag the
// gate keys its escalation on. A guard so the binding can't silently drop it.
func TestInterceptDrive_GitOpsConflictRoomFlagged(t *testing.T) {
	def, err := Load("../../stories/git-ops/app.yaml")
	require.NoError(t, err)
	conflict, ok := def.States["conflict"]
	require.True(t, ok, "git-ops must declare a top-level conflict room")
	require.Equal(t, InterceptDriveRest, conflict.InterceptDrive,
		"the conflict room must be flagged intercept_drive: rest so the gate drives it")
}

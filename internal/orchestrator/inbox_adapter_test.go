package orchestrator_test

// Integration test for the P1-C fix from the dev-story-bugfix-unify
// Opus review: host.inbox.add must persist a notification through
// the orchestrator's JobStore-backed adapter, NOT silently report
// persisted:false.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_InboxAdd_WritesToJobStore — a state's on_enter
// invokes host.inbox.add.  The orchestrator must install its
// JobStore-backed InboxAdder so the resulting Notification actually
// persists in the jobs store and is visible to ListNotifications.
//
// Before P1-C the adapter was unwired in production: every
// host.inbox.add call returned persisted:false ("no inbox adapter
// installed; notification dropped").
func TestOrchestrator_InboxAdd_WritesToJobStore(t *testing.T) {
	const yamlSrc = `
app:
  id: inbox-adapter-test
  version: 0.1.0
hosts:
  - host.inbox.add
intents:
  start: {}
root: idle
states:
  idle:
    on:
      start:
        - target: noted
  noted:
    on_enter:
      - invoke: host.inbox.add
        with:
          title: "Checkpoint: review me"
          body: "## What\nA reproducible artifact landed."
          kind: "checkpoint"
          thread: "issues/test-thread"
          state: "noted"
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jobStore, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)

	// Register the real host.inbox.add handler so the orchestrator's
	// adapter injection runs end-to-end.
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)

	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("noted"), out.NewState)

	// The adapter must have written one notification for this session.
	notifs, err := jobStore.ListNotifications(ctx, sid, 0)
	require.NoError(t, err)
	require.Len(t, notifs, 1,
		"host.inbox.add must persist exactly one notification via the JobStore-backed adapter")

	n := notifs[0]
	require.Equal(t, "Checkpoint: review me", n.Title)
	require.Contains(t, n.Body, "reproducible artifact")
	require.Equal(t, "noted", n.TeleportState,
		"InboxNotification.State must flow through to Notification.TeleportState")
	require.Equal(t, "host_call", n.OriginKind,
		"adapter writes origin_kind=host_call to distinguish from job-driven notifications")
	require.Equal(t, "issues/test-thread", n.OriginRef,
		"thread argument must flow through to origin_ref")
	require.Equal(t, jobs.SeverityActionRequired, n.Severity,
		"kind=checkpoint must map to SeverityActionRequired")

	// Unread count must also reflect the write.
	counts, err := jobStore.UnreadCount(ctx, sid)
	require.NoError(t, err)
	require.GreaterOrEqual(t, counts[jobs.SeverityActionRequired], 1,
		"unread count for the action_required tier must include the freshly-added notification")
}

// TestOrchestrator_InboxAdd_NoJobStore is the negative case: when no
// JobStore is wired (deterministic flow-test posture), host.inbox.add
// reports persisted:false but the turn still succeeds — the
// always-on contract holds.
func TestOrchestrator_InboxAdd_NoJobStore(t *testing.T) {
	const yamlSrc = `
app:
  id: inbox-no-store-test
  version: 0.1.0
hosts:
  - host.inbox.add
intents:
  start: {}
root: idle
states:
  idle:
    on:
      start:
        - target: noted
  noted:
    on_enter:
      - invoke: host.inbox.add
        with:
          title: "x"
          body: "y"
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)

	// No WithJobStore — adapter remains unwired.
	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{},
		orchestrator.WithHostRegistry(reg),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("noted"), out.NewState,
		"the turn must succeed even without an inbox adapter (always-on contract)")
}

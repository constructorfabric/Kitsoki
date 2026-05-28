package orchestrator_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// TestTimeout_TraceArmCancelFire verifies that arming, cancelling, and
// firing a timeout each emit their respective structured slog event kind.
func TestTimeout_TraceArmCancelFire(t *testing.T) {
	t.Parallel()

	def, err := app.Load("testdata/timeout/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)
	clk := clock.NewFake(time.Unix(0, 0))

	orch := orchestrator.New(def, m, s, &timeoutNoopHarness{},
		orchestrator.WithClock(clk),
		orchestrator.WithLogger(logger),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Teleport into waiting → arm timeout.
	_, err = orch.Teleport(ctx, sid, inbox.TeleportTarget{State: "waiting"})
	require.NoError(t, err)

	require.True(t, handler.hasMsg(trace.EvTimeoutArmed),
		"expected timeout.armed after Teleport→waiting; captured msgs=%v", handler.msgs())

	// Advance the clock past the 10-day deadline → fire timeout.
	clk.Advance(11 * 24 * time.Hour)
	require.Eventually(t, func() bool {
		j, lerr := orch.LoadJourney(sid)
		if lerr != nil {
			return false
		}
		return j.State == app.StatePath("traveled")
	}, 2*time.Second, 5*time.Millisecond)

	require.True(t, handler.hasMsg(trace.EvTimeoutFired),
		"expected timeout.fired after clock advance; captured msgs=%v", handler.msgs())
}

// TestTimeout_TraceCancelled verifies that an explicit cancel emits a
// timeout.cancelled event. We exit the waiting state via Teleport, which
// triggers armTimeoutForState's cancel branch.
func TestTimeout_TraceCancelled(t *testing.T) {
	t.Parallel()

	def, err := app.Load("testdata/timeout/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)
	clk := clock.NewFake(time.Unix(0, 0))

	orch := orchestrator.New(def, m, s, &timeoutNoopHarness{},
		orchestrator.WithClock(clk),
		orchestrator.WithLogger(logger),
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Teleport(ctx, sid, inbox.TeleportTarget{State: "waiting"})
	require.NoError(t, err)
	require.True(t, handler.hasMsg(trace.EvTimeoutArmed))

	// Teleport elsewhere → cancels the pending waiting timeout.
	_, err = orch.Teleport(ctx, sid, inbox.TeleportTarget{State: "traveled"})
	require.NoError(t, err)

	require.True(t, handler.hasMsg(trace.EvTimeoutCancelled),
		"expected timeout.cancelled after exit; captured msgs=%v", handler.msgs())
}

package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_HostErrorObjectOnRedirect proves that an `on_error:`
// redirect populates the reserved global `host_error` world var (structured:
// namespace + message, plus stderr/exit_code surfaced from the failing host
// result's Data) alongside the bare `last_error` string — and that both land
// in the persisted EffectApplied event log so a pure replay reconstructs them.
//
// This is the visibility fix: previously only last_error (string) was set, and
// the documented host_error slot was never actually populated by any code.
func TestOrchestrator_HostErrorObjectOnRedirect(t *testing.T) {
	def, err := app.Load("testdata/hosterror/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{
			Error: "deliberate failure",
			Data: map[string]any{
				"stderr":    "boom on the wire",
				"exit_code": float64(2),
			},
		}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	// Replay the persisted event log from scratch — the resume / restart path.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	js, err := store.BuildJourney(def, orch.InitialState(), orch.InitialWorld(), history)
	require.NoError(t, err)

	// Bare last_error string still set.
	require.Equal(t, "deliberate failure", js.World.Vars["last_error"])

	// Structured host_error object reconstructed from the event log.
	raw, ok := js.World.Vars["host_error"]
	require.True(t, ok, "host_error must be set and replayable from the event log")
	herr, ok := raw.(map[string]any)
	require.True(t, ok, "host_error must be a map, got %T", raw)

	require.Equal(t, "host.fail", herr["namespace"])
	require.Equal(t, "deliberate failure", herr["message"])
	// stderr/exit_code surfaced from the failing result's Data.
	require.Equal(t, "boom on the wire", herr["stderr"])
	require.Equal(t, float64(2), herr["exit_code"])
	// Full data payload also carried.
	require.NotNil(t, herr["data"])
}

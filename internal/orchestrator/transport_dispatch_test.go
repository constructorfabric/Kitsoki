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
	"kitsoki/internal/transport"
)

// TestOrchestrator_TransportRegistryInjected verifies that the orchestrator
// installs the transport.Registry into ctx so the host.transport.post bridge
// handler can dispatch through it.
//
// We stand up a stub app whose probe state on_enter invokes
// host.transport.post; with WithTransportRegistry wired, the message lands
// in the TUITransport buffer.
func TestOrchestrator_TransportRegistryInjected(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)

	// Replace probe.on_enter at runtime with a transport.post invocation.
	probe := def.States["probe"]
	require.NotNil(t, probe)
	probe.OnEnter = []app.Effect{{
		Invoke: "host.transport.post",
		With: map[string]any{
			"transport": "tui",
			"thread":    "S-1",
			"phase_id":  "phase_test",
			"title":     "Hello",
			"body":      "world",
		},
	}}
	def.Hosts = append(def.Hosts, "host.transport.post")

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	transportReg := transport.NewRegistry()
	tt := transport.NewTUITransport()
	transportReg.Register(tt)
	t.Cleanup(func() { _ = transportReg.Close() })

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithTransportRegistry(transportReg),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	posts := tt.Drain()
	require.Len(t, posts, 1, "exactly one transport.post should have fired")
	require.Equal(t, "phase_test", posts[0].Msg.PhaseID)
	require.Equal(t, "Hello", posts[0].Msg.Title)
	require.Equal(t, "world", posts[0].Msg.Body)
	require.Equal(t, "tui", posts[0].Key.Transport)
	require.Equal(t, "S-1", posts[0].Key.Thread)
}

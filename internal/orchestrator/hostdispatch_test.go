package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_HostDispatchBindsAndRefreshesView covers the orchestrator's
// post-machine host-call dispatch path: after a state's on_enter invokes a
// host.*, the binding lands in world and the returned view reflects it on the
// same turn (not the next one).
func TestOrchestrator_HostDispatchBindsAndRefreshesView(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"message": "hello world"}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("probe"), out.NewState)
	require.True(t, strings.Contains(out.View, "hello world"),
		"expected refreshed view to include bound value, got: %q", out.View)
}

// TestOrchestrator_HostDispatchDisabledWhenNoRegistry verifies the orchestrator
// is safe to run without a host registry: host calls are ignored, bindings do
// not land, and the view still renders (with the pre-host world).
func TestOrchestrator_HostDispatchDisabledWhenNoRegistry(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Note: no WithHostRegistry — deterministic flow-test posture.
	orch := orchestrator.New(def, m, s, noopHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("probe"), out.NewState)
	require.False(t, strings.Contains(out.View, "hello world"),
		"host binding should be skipped when no registry is wired")
}

// TestOrchestrator_HostDispatchOnError_RoutesToErrorState verifies that
// when an on_enter `invoke:` step has an `on_error:` arc, a non-empty
// Result.Error from the host handler routes the session to the named
// error state — instead of leaving it stuck in the success target.
//
// Regression for the bugfix room's phase_6_5 verifier hang: the verifier
// returned exit 1 but kitsoki still advanced to the success state because
// the orchestrator captured `last_error` in world without consulting
// hc.OnError to actually transition.
func TestOrchestrator_HostDispatchOnError_RoutesToErrorState(t *testing.T) {
	def, err := app.Load("testdata/hosterror/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: "deliberate failure"}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("probe_error"), out.NewState,
		"on_error must route to the named error state on host failure; got %q", out.NewState)
	require.True(t, strings.Contains(out.View, "error_branch"),
		"expected error-state on_enter to fire, got view: %q", out.View)
}

// TestOrchestrator_WithChatStore_InjectsStoreIntoContext verifies that when
// a ChatStore is wired via orchestrator.WithChatStore, it is injected into
// the handler context so ChatStoreFromContext returns it inside the handler.
func TestOrchestrator_WithChatStore_InjectsStoreIntoContext(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Minimal ChatStore that records whether it was called.
	var storeSeen bool
	cs := &chatStoreProbe{onGet: func() { storeSeen = true }}

	reg := host.NewRegistry()
	reg.Register("host.probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		got := host.ChatStoreFromContext(ctx)
		if got == cs {
			storeSeen = true
		}
		return host.Result{Data: map[string]any{"message": "ok"}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithChatStore(cs),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.True(t, storeSeen, "expected ChatStore to be present in handler context")
}

// chatStoreProbe is a minimal ChatStore that calls a callback on any method
// to confirm it was injected into context.
type chatStoreProbe struct {
	onGet func()
}

func (p *chatStoreProbe) Get(_ context.Context, _ string) (*host.ChatRecord, error) {
	p.onGet()
	return nil, nil
}
func (p *chatStoreProbe) Resolve(_ context.Context, _, _, _, _ string) (*host.ChatRecord, bool, error) {
	return nil, false, nil
}
func (p *chatStoreProbe) Create(_ context.Context, _, _, _, _ string) (*host.ChatRecord, error) {
	return nil, nil
}
func (p *chatStoreProbe) List(_ context.Context, _, _, _ string) ([]host.ChatRecord, error) {
	return nil, nil
}
func (p *chatStoreProbe) Fork(_ context.Context, _, _ string) (*host.ChatRecord, error) {
	return nil, nil
}
func (p *chatStoreProbe) Archive(_ context.Context, _ string) error              { return nil }
func (p *chatStoreProbe) Rename(_ context.Context, _, _ string) error            { return nil }
func (p *chatStoreProbe) SetClaudeSessionID(_ context.Context, _, _ string) error { return nil }
func (p *chatStoreProbe) AppendMessage(_ context.Context, _, _, _ string, _ map[string]any) (host.ChatMessage, error) {
	return host.ChatMessage{}, nil
}
func (p *chatStoreProbe) Transcript(_ context.Context, _ string, _ int) ([]host.ChatMessage, error) {
	return nil, nil
}
func (p *chatStoreProbe) LatestSeq(_ context.Context, _ string) (int, error) { return -1, nil }
func (p *chatStoreProbe) WithLock(_ context.Context, _ string, fn func(context.Context) error) error {
	return fn(context.Background())
}

// noopHarness is a zero-behavior Harness for SubmitDirect tests. RunTurn is
// never invoked by SubmitDirect, so a stub is sufficient.
type noopHarness struct{}

func (noopHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (noopHarness) Close() error { return nil }

package orchestrator_test

import (
	"context"
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

type oracleTransitionHarness struct{}

func (oracleTransitionHarness) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{Name: "transition", Arguments: map[string]any{
		"intent": "do",
		"slots":  map[string]any{},
	}}, nil
}

func (oracleTransitionHarness) Close() error { return nil }

// TestRepro_OnErrorRedirectSurfacesFailure is an independent oracle for the
// operator-visible half of an on_error redirect. The destination room does not
// interpolate world.last_error itself, so the runtime must add a failure banner
// rather than silently showing an ordinary bounced screen.
func TestRepro_OnErrorRedirectSurfacesFailure(t *testing.T) {
	def, err := app.LoadBytes([]byte(`
app: { id: on-error-banner-oracle, version: 0.1.0 }
hosts: [host.test.fail]
world:
  last_error: { type: string, default: "" }
intents:
  do:
    title: Do the thing
root: start
states:
  start:
    view: "Start"
    on:
      do:
        - target: working
  working:
    view: "Working"
    on_enter:
      - invoke: host.test.fail
        on_error: bounced
  bounced:
    terminal: true
    view: "Back at the lobby. Try again when ready."
`))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.test.fail", func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{Error: "simulated host failure"}, nil
	})
	orch := orchestrator.New(def, m, s, oracleTransitionHarness{}, orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	out, err := orch.Turn(context.Background(), sid, "do")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("bounced"), out.NewState)
	require.Contains(t, out.View, "Action failed: simulated host failure",
		"an on_error destination that hides last_error must still tell the operator why it bounced")
}

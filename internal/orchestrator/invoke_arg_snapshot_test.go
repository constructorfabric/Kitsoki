package orchestrator_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestInvokeArgNotClobberedByLaterSet guards the effect-ordering fix in
// dispatchHostCalls: an `invoke:`'s `with:` args are re-rendered at dispatch
// time so a downstream invoke in the same chain can read an earlier invoke's
// bind. That re-render must use the world AS OF the invoke's position
// (machine.HostInvocation.WorldSnapshot) overlaid with accumulated binds — NOT
// the final post-chain world. Otherwise a `set:` positioned AFTER the invoke
// clobbers the value the invoke was supposed to receive.
//
// Regression of record: the dev-story proposal/restart ("start over") arc
// archived the discovery chat with `chat_id: "{{ world.proposal_chat_id }}"`
// and THEN cleared `proposal_chat_id` with a following `set:`. The dispatch-time
// re-render read the post-`set:` world, so archive received an empty chat_id
// and failed ("chat_id argument is required"); on_enter then never minted a
// fresh chat and subsequent converse turns ran with an empty chat_id — the
// conversation "forgot" everything.
func TestInvokeArgNotClobberedByLaterSet(t *testing.T) {
	var (
		mu       sync.Mutex
		gotToken string
	)
	reg := host.NewRegistry()
	reg.Register("host.test.capture", func(_ context.Context, args map[string]any) (host.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		gotToken, _ = args["token"].(string)
		return host.Result{}, nil
	})

	def := &app.AppDef{
		App:   app.AppMeta{ID: "invoke-arg-snapshot-test"},
		Root:  "init",
		Hosts: []string{"host.test.capture"},
		World: map[string]app.VarDef{
			"token": {Type: "string", Default: "live-token"},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On:   map[string][]app.Transition{"enter": {{Target: "room"}}},
			},
			"room": {
				View: app.LegacyView("room"),
				OnEnter: []app.Effect{
					// Invoke reads the live token...
					{
						Invoke: "host.test.capture",
						With:   map[string]any{"token": "{{ world.token }}"},
					},
					// ...then a LATER set clears it. Pre-fix, the re-render
					// read this cleared value and the handler saw "".
					{Set: map[string]any{"token": ""}},
				},
			},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	ctx := context.Background()
	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer s.Close()

	orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"},
		orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "live-token", gotToken,
		"invoke must see the world value as of its own position, not the post-chain value cleared by a later set:")
}

// TestLaterInvokeSeesEarlierBind is the complementary guard: the snapshot fix
// must NOT break the 2-step `on_enter:` composition the re-render exists for —
// a downstream invoke's `with:` arg referencing a key bound by an earlier
// invoke in the same chain must still resolve to that bound value.
func TestLaterInvokeSeesEarlierBind(t *testing.T) {
	var (
		mu     sync.Mutex
		gotCtx string
	)
	reg := host.NewRegistry()
	reg.Register("host.test.produce", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"value": "bound-by-step-1"}}, nil
	})
	reg.Register("host.test.consume", func(_ context.Context, args map[string]any) (host.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		gotCtx, _ = args["ctx"].(string)
		return host.Result{}, nil
	})

	def := &app.AppDef{
		App:   app.AppMeta{ID: "invoke-bind-compose-test"},
		Root:  "init",
		Hosts: []string{"host.test.produce", "host.test.consume"},
		World: map[string]app.VarDef{
			"step1_out": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On:   map[string][]app.Transition{"enter": {{Target: "room"}}},
			},
			"room": {
				View: app.LegacyView("room"),
				OnEnter: []app.Effect{
					{
						Invoke: "host.test.produce",
						Bind:   map[string]string{"step1_out": "value"},
					},
					{
						Invoke: "host.test.consume",
						With:   map[string]any{"ctx": "{{ world.step1_out }}"},
					},
				},
			},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	ctx := context.Background()
	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer s.Close()

	orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"},
		orchestrator.WithHostRegistry(reg))
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "bound-by-step-1", gotCtx,
		"a downstream invoke must still see the value an earlier invoke bound in the same chain")
}

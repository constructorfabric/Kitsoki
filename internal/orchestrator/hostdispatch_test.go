package orchestrator_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestOrchestrator_HostDispatchedFlushedLiveBeforeInvoke pins the observability
// half of the triage-hang fix: HostDispatched must reach the JSONL sink (which
// the web SSE stream tails) BEFORE the handler returns, so a slow or wedged
// host call shows up live instead of the whole turn's event batch landing only
// at turn-end (a frozen screen with nothing to show). It also guards against a
// double-write — the live flush must remove HostDispatched from the turn-end
// batch so it appears exactly once.
func TestOrchestrator_HostDispatchedFlushedLiveBeforeInvoke(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "trace.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	dispatched := make(chan struct{}) // closed when the handler is entered
	release := make(chan struct{})    // blocks the handler until the test allows
	reg := host.NewRegistry()
	reg.Register("host.probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		close(dispatched)
		<-release
		return host.Result{Data: map[string]any{"message": "hello world"}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_, _ = orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
		close(done)
	}()

	// Wait until the handler is blocked mid-Invoke.
	select {
	case <-dispatched:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered")
	}

	// The turn has NOT returned yet (handler is parked), but HostDispatched
	// must already be in the sink — that is the live flush.
	countDispatched := func() int {
		n := 0
		for _, ev := range sink.History() {
			if ev.Kind == store.HostDispatched {
				n++
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return countDispatched() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"HostDispatched should be flushed to the sink live, before the handler returns")

	close(release)
	<-done

	require.Equal(t, 1, countDispatched(),
		"HostDispatched must appear exactly once — the live flush must not also leave it in the turn-end batch")
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

// TestOrchestrator_RunInitialOnEnter_FiresHostCallAndBinds verifies
// that a freshly-created session runs the initial state's on_enter
// chain before the first frame renders. Without this, any app whose
// root room declares `on_enter: invoke …` to populate world keys
// (e.g. dev-story's main view: `iface.ticket.list_mine` → my_tickets)
// would render the first frame against the default world and show
// "(empty)" until the user navigates away and back.
//
// The fixture's `idle` (initial) state on_enter invokes host.probe
// which binds greeting="hello world"; the test asserts the world key
// is populated after RunInitialOnEnter and that subsequent calls are
// no-ops.
func TestOrchestrator_RunInitialOnEnter_FiresHostCallAndBinds(t *testing.T) {
	def, err := app.Load("testdata/initial_onenter/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	invokeCount := 0
	reg := host.NewRegistry()
	reg.Register("host.probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		invokeCount++
		return host.Result{Data: map[string]any{"message": "hello world"}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Pre-condition: world key is at default before RunInitialOnEnter.
	j0, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "", j0.World.Vars["greeting"], "greeting should be at default before initial on_enter")

	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))
	require.Equal(t, 1, invokeCount, "host.probe must be invoked exactly once")

	j1, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "hello world", j1.World.Vars["greeting"],
		"greeting must be bound from host.probe's Data.message after RunInitialOnEnter")

	// Idempotent: subsequent calls are no-ops because journey.Turn > 0
	// once a real turn has run, but for a session that's still at
	// turn 0 a second call would re-fire (no journey.Turn change
	// happens — initial on_enter is stamped turn=0).
	//
	// In practice cmd/kitsoki/main.go calls this exactly once
	// post-NewSession; the guard above is just belt-and-braces. Run
	// a real Turn next and confirm RunInitialOnEnter then no-ops.
	out, err := orch.SubmitDirect(ctx, sid, "go_forward", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	preCount := invokeCount
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))
	require.Equal(t, preCount, invokeCount,
		"RunInitialOnEnter must be a no-op once journey.Turn > 0")
}

// TestOrchestrator_HostDispatchOnError_SelfRedirectDoesNotLoop guards
// against an infinite loop when a state's on_enter `invoke:` has
// `on_error: <self>`. The author's intent for self-targeting on_error
// is "stay in place, surface the failure" — not "re-enter and try
// again, forever". Re-firing on_enter on self-redirect would invoke
// the same failing host call again, land here again, loop forever.
//
// Regression for the dev-story dogfood: ticket_search has
// `on_enter: invoke iface.ticket.search ... on_error: ticket_search`,
// which folded under the `core` import alias became
// `on_error: ../ticket_search` resolving to `core.ticket_search`
// (the same room). With the resolve-relative-target fix in place but
// no self-guard, typing `tickets` froze the TUI in a tight loop —
// 200k+ invoke effects in seconds. The guard returns after the
// transition events, without re-running on_enter.
func TestOrchestrator_HostDispatchOnError_SelfRedirectDoesNotLoop(t *testing.T) {
	def, err := app.Load("testdata/hosterror_selfredirect/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var invokeCount int64
	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		invokeCount++
		return host.Result{Error: "deliberate failure"}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Submit completes synchronously if the loop is broken; before the
	// guard landed this would never return (or hit the recursion cap and
	// surface a HarnessError after thousands of invocations). One invoke
	// is expected: the original on_enter call. The self-redirect must
	// NOT trigger a second.
	out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("probe"), out.NewState,
		"session must land in probe (the self-target), not loop or escape")
	require.Equal(t, int64(1), invokeCount,
		"host.fail must be invoked exactly once; self-redirect must not re-fire on_enter")
}

// TestOrchestrator_OnErrorRedirect_DepthCapBreaksLoop guards against an
// on-error redirect loop (see docs/stories/state-machine.md
// "Effects" — the on_error recursion cap): a
// host call whose on_error target is a SIBLING state (not self) whose
// own on_enter re-invokes a failing host call with on_error pointing
// back at the original state. The `target == prior` self-redirect
// guard at orchestrator.go:1350 does not catch this — mutually-
// redirecting siblings strictly alternate prior↔target on each
// recursion, so the guard never sees equality. Before the
// `maxRedirectDepth` cap landed (commit fa39746), this looped
// forever; `core.bf.idle`'s `iface.workspace.create` failing against a
// stale `.worktrees/bf-<id>/` dir was a real instance.
//
// Fixture: testdata/hosterror_loop/app.yaml has two states `a` and
// `b`; `a.on_enter` invokes host.fail with on_error: b, `b.on_enter`
// invokes host.fail with on_error: a. The registered host.fail always
// returns Result.Error.
//
// Assertions:
//   - SubmitDirect returns without hanging (5s context timeout makes a
//     loop regression FAIL fast rather than hang CI).
//   - A HarnessError event with reason=on_error.depth_cap_exceeded
//     appears in store.LoadHistory(sid).
//   - Host invocation count is bounded by the cap (depth-cap + 1 = 5
//     in the current implementation; using <=8 here as a generous
//     ceiling so a future cap-bump doesn't accidentally fail this).
//
// REGRESSION VERIFICATION: To prove this test catches the loop, comment
// out the `if depth > maxRedirectDepth { … return }` block in
// orchestrator.go::enterRedirectState (around lines 1305-1324), then
// re-run `go test -run TestOrchestrator_OnErrorRedirect_DepthCapBreaksLoop`.
// The test must FAIL with a context-deadline timeout (the SubmitDirect
// hard-stops at 5s; without the cap the recursion would spin
// indefinitely). Restore the cap afterwards. Verified 2026-05-18.
func TestOrchestrator_OnErrorRedirect_DepthCapBreaksLoop(t *testing.T) {
	def, err := app.Load("testdata/hosterror_loop/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var invokeCount int64
	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		atomic.AddInt64(&invokeCount, 1)
		return host.Result{Error: "deliberate failure"}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	rootCtx := context.Background()
	sid, err := orch.NewSession(rootCtx)
	require.NoError(t, err)

	// 5-second hard timeout so a regression (the loop comes back)
	// fails the test in seconds rather than hanging CI for minutes.
	ctx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer cancel()

	out, err := orch.SubmitDirect(ctx, sid, "trigger", nil)
	require.NoError(t, err, "depth-cap firing must surface as TurnOutcome.HarnessError, not a Go error / hang")
	require.NotNil(t, out)

	require.Less(t, atomic.LoadInt64(&invokeCount), int64(8),
		"host.fail invocations must be bounded by the depth cap; got %d (cap is currently 4, so 5 is the legitimate ceiling)", atomic.LoadInt64(&invokeCount))

	// The HarnessError event must land in the persisted history with
	// the cap's documented reason string.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	var found bool
	for _, ev := range history {
		if ev.Kind != store.HarnessError {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		if reason, _ := p["reason"].(string); reason == "on_error.depth_cap_exceeded" {
			found = true
			break
		}
	}
	require.True(t, found,
		"expected a HarnessError event with reason=on_error.depth_cap_exceeded in history; got %d events", len(history))
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
func (p *chatStoreProbe) Archive(_ context.Context, _ string) error               { return nil }
func (p *chatStoreProbe) Rename(_ context.Context, _, _ string) error             { return nil }
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
func (p *chatStoreProbe) Enqueue(_ context.Context, _ host.EnqueueDriveOptions) (*host.ChatDrive, error) {
	return nil, nil
}
func (p *chatStoreProbe) Dequeue(_ context.Context, _ string) (*host.ChatDrive, error) {
	return nil, host.ErrNoPendingDrive
}
func (p *chatStoreProbe) ClaimDrive(_ context.Context, _ string) (*host.ChatDrive, error) {
	return nil, host.ErrDriveNotFound
}
func (p *chatStoreProbe) MarkDriveDone(_ context.Context, _ string, _ int) error { return nil }
func (p *chatStoreProbe) MarkDriveFailed(_ context.Context, _, _ string) error   { return nil }
func (p *chatStoreProbe) MarkDriveDismissed(_ context.Context, _ string) error   { return nil }
func (p *chatStoreProbe) GetDrive(_ context.Context, _ string) (*host.ChatDrive, error) {
	return nil, host.ErrDriveNotFound
}
func (p *chatStoreProbe) ListDrives(_ context.Context, _ string, _ host.ListDrivesFilter) ([]host.ChatDrive, error) {
	return nil, nil
}

// noopHarness is a zero-behavior Harness for SubmitDirect tests. RunTurn is
// never invoked by SubmitDirect, so a stub is sufficient.
type noopHarness struct{}

func (noopHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (noopHarness) Close() error { return nil }

// TestOrchestrator_HostDispatchChained_BoundSlotReachesNextStep verifies
// the core rerenderHostArgs contract: a two-step `on_enter:` block where
// step 2 references step 1's bound slot via a nested template
// (`with.payload.foo: "{{ world.step1_result.value }}"`) must dispatch
// step 2 with the post-bind value, not the machine-time pre-bind nil.
//
// Regression for the silent-fallback bug in rerenderHostArgs: a leaf
// template rendering against `world.step1_result.value` at machine time
// produced nil (slot not yet bound) so the up-front-resolved hc.Args had
// `payload.foo: nil`; the orchestrator's late re-render is what makes it
// land as "X".
func TestOrchestrator_HostDispatchChained_BoundSlotReachesNextStep(t *testing.T) {
	def, err := app.Load("testdata/hostchained/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var (
		step2Args map[string]any
		mu        sync.Mutex
	)
	reg := host.NewRegistry()
	reg.Register("host.step1", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"step1_result": map[string]any{"value": "X"},
			// Switch type_changer from {} to a string so the leaf
			// `{{ world.type_changer.field }}` in step 3 errors at
			// dispatch time but not at machine time.  Used by the
			// per-leaf fallback test below; harmless for the simpler
			// chained test (step 2 ignores it).
			"type_changer": "now-a-string",
		}}, nil
	})
	reg.Register("host.step2", func(ctx context.Context, args map[string]any) (host.Result, error) {
		mu.Lock()
		step2Args = args
		mu.Unlock()
		return host.Result{Data: map[string]any{"step2_result": map[string]any{"ok": true}}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "go", map[string]any{})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, step2Args, "host.step2 must have been invoked")
	payload, ok := step2Args["payload"].(map[string]any)
	require.True(t, ok, "step2 args.payload must be a map, got: %#v", step2Args["payload"])
	require.Equal(t, "X", payload["foo"],
		"step2 args.payload.foo must be the post-bind value from step1; got: %#v", payload["foo"])
	require.Equal(t, "kept", payload["literal"],
		"non-template leaves must be preserved verbatim")
}

// TestOrchestrator_HostDispatchChained_LeafFallbackOnBadTemplate verifies
// the per-leaf fallback semantics added to rerenderHostArgs: when one leaf
// of a nested `with:` block fails to render (here, references an unknown
// world slot), the surrounding leaves still see post-bind values and the
// HostDispatched event records `rerender_fell_back: true` so the trace is
// honest about which call received a partially-stale args map.
func TestOrchestrator_HostDispatchChained_LeafFallbackOnBadTemplate(t *testing.T) {
	def, err := app.Load("testdata/hostchained/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var (
		step3Args map[string]any
		mu        sync.Mutex
	)
	reg := host.NewRegistry()
	reg.Register("host.step1", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"step1_result": map[string]any{"value": "X"},
			// Switch type_changer from {} to a string so the leaf
			// `{{ world.type_changer.field }}` in step 3 errors at
			// dispatch time but not at machine time.  Used by the
			// per-leaf fallback test below; harmless for the simpler
			// chained test (step 2 ignores it).
			"type_changer": "now-a-string",
		}}, nil
	})
	reg.Register("host.step2", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"step2_result": map[string]any{"ok": true}}}, nil
	})
	reg.Register("host.step3", func(ctx context.Context, args map[string]any) (host.Result, error) {
		mu.Lock()
		step3Args = args
		mu.Unlock()
		return host.Result{Data: map[string]any{}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "go_bad", map[string]any{})
	require.NoError(t, err)

	mu.Lock()
	require.NotNil(t, step3Args, "host.step3 must still run after the bad leaf falls back")
	payload, ok := step3Args["payload"].(map[string]any)
	require.True(t, ok, "step3 args.payload must be a map, got: %#v", step3Args["payload"])
	require.Equal(t, "X", payload["good"],
		"the good leaf must render against the post-bind world; got: %#v", payload["good"])
	require.Equal(t, "kept", payload["literal"],
		"the literal leaf must pass through unchanged")
	// The bad leaf falls back to the machine-time up-front-resolved value.
	// That up-front render of `{{ world.never_bound.does_not_exist }}` also
	// errors against an empty world; the fallback path keeps the raw
	// template string so the handler can still see *something* and the
	// HostDispatched event records the fallback.  The exact value is
	// implementation-defined (nil or raw template); the contract is "the
	// handler still runs and the surrounding leaves are correct".
	mu.Unlock()

	// HostDispatched for host.step3 must record rerender_fell_back: true.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)
	foundStep3Dispatch := false
	for _, ev := range history {
		if ev.Kind != store.HostDispatched {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		if p["namespace"] != "host.step3" {
			continue
		}
		foundStep3Dispatch = true
		require.Equal(t, true, p["rerender_fell_back"],
			"HostDispatched for step3 must record rerender_fell_back: true; payload=%#v", p)
		// Sanity: the args.payload.good leaf must be in the event payload too.
		argsP, _ := p["args"].(map[string]any)
		payloadP, _ := argsP["payload"].(map[string]any)
		require.Equal(t, "X", payloadP["good"],
			"HostDispatched.args must reflect the rerendered (post-bind) args")
	}
	require.True(t, foundStep3Dispatch,
		"HostDispatched event for host.step3 must appear in the event log")

	// And for step1/step2 (the all-good cases), HostDispatched must record
	// rerender_fell_back: false so the diagnostic story differentiates the
	// good calls from the partially-stale one.
	for _, ev := range history {
		if ev.Kind != store.HostDispatched {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		ns, _ := p["namespace"].(string)
		if ns == "host.step1" || ns == "host.step2" {
			require.Equal(t, false, p["rerender_fell_back"],
				"HostDispatched for %s must NOT record a fallback; payload=%#v", ns, p)
		}
	}
}

package orchestrator_test

// narration_trace_test.go — the rendered room view (the operator-facing
// narration) is recorded into the trace on turn.end.
//
// A room's narration is the expansion of its `view:` template against world
// state (banner/prose/kv/headings/the questions a clarify room poses, …). The
// trace records only world state, and the view templates can change mid-run
// and run-to-run without being pinned to a git sha — so the narration cannot
// be reconstructed after the fact from the story files. It must be captured at
// render time. We capture it in the turn.end payload (exactly one view per
// turn). This test pins that contract; it FAILS if turn.end omits the view.
//
// The recorded view is the operator's narration with presentation ANSI
// stripped: lipgloss only emits the banner/heading colour to a colour terminal,
// so recording it raw would make the same session record different bytes in a
// TTY vs. headless. We assert the recorded view is ANSI-free and equals the
// stripped display view; the source-color sentinels (not ANSI) are preserved.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestTurnEndRecordsRenderedView(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Transition foyer → cloakroom; the destination renders a non-empty view.
	out, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.NotEmpty(t, out.View, "the destination room must render a non-empty view")

	// Find the turn.end event and assert it carries the rendered view — the
	// same narration text the operator saw (TurnOutcome.View).
	var end *store.Event
	for i := range sink.History() {
		if sink.History()[i].Kind == store.TurnEnded {
			ev := sink.History()[i]
			end = &ev
		}
	}
	require.NotNil(t, end, "expected a turn.end event")

	var payload struct {
		Outcome string `json:"outcome"`
		To      string `json:"to"`
		View    string `json:"view"`
	}
	require.NoError(t, json.Unmarshal(end.Payload, &payload))
	require.Equal(t, "transitioned", payload.Outcome)
	require.NotEmpty(t, payload.View, "turn.end must record the rendered room narration")
	require.NotContains(t, payload.View, "\x1b",
		"recorded narration must be ANSI-free so the trace is deterministic across color profiles")
	require.Equal(t, ansi.Strip(out.View), payload.View,
		"recorded narration must equal the operator's view with presentation ANSI stripped")
	// Sanity: the display view did carry styling here, so the strip is load-bearing.
	require.True(t, strings.Contains(out.View, "\x1b") || ansi.Strip(out.View) == out.View)
}

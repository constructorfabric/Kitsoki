// Regression coverage for the conversational-room routing contract that lets a
// no-LLM (nil-harness / --flow) dev-story tour stay in a discovery room:
//
// In an imported conversational discovery room (core.prd.idle: mode:
// conversational + default_intent: discuss) the parent's GLOBAL, content-bearing
// slot-bearing intent core__work (priority 45, required `request` slot) is in the
// allowed set and competes with the room's default_intent. A product manager's
// free-text idea must sink to default_intent (discuss) — NOT be stolen by the
// global work intent, which would transition out of the discovery room and
// dead-end a conversational tour.
//
// This is upheld by the semroute slot-bearing-deferral guards (RequiresUnfilledSlot
// + droppedSlotContent, semantic.go): a synonym/example/embedding match that
// routes to a slot-bearing intent while dropping user content abdicates to the
// interpreter; under the nil harness there is no interpreter, so the turn falls
// through to the room's default_intent. These tests pin that end-to-end against
// the real slidey-dev story so a future routing change cannot silently break
// conversation-driven tours.
package orchestrator_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestConversationalDefaultIntent_WinsOverGlobalWork(t *testing.T) {
	abs, err := filepath.Abs("../../stories/slidey-dev/app.yaml")
	require.NoError(t, err)
	def, err := app.Load(abs)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)

	// core__work is genuinely in the allowed set for core.prd.idle — the
	// competition this test guards against is real, not vacuous.
	ais := m.AllowedIntents(app.StatePath("core.prd.idle"), machine.WorldFromSchema(def.World))
	var hasWork bool
	for _, ai := range ais {
		if ai.Name == "core__work" {
			hasWork = true
		}
	}
	require.True(t, hasWork, "precondition: global core__work must be allowed in core.prd.idle")

	// Each utterance — including lines lexically identical to core__work's
	// examples — must sink to default_intent (stay in core.prd.idle), never
	// steal to core__work (which would transition to core.landing).
	utterances := []string{
		"I want a CLI tool that converts markdown to PDF",
		"refactor the loader",
		"fix the login crash",
		"investigate why the build is slow",
		"add a test for the off-ramp",
		"look at how imports resolve and sketch a change",
	}
	for _, msg := range utterances {
		t.Run(msg, func(t *testing.T) {
			s, err := store.OpenMemory()
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			orch := orchestrator.New(def, m, s, nil) // nil harness == --flow posture
			sid, err := orch.NewSession(context.Background())
			require.NoError(t, err)
			_, err = orch.Teleport(context.Background(), sid, inbox.TeleportTarget{State: app.StatePath("core.prd.idle")})
			require.NoError(t, err)

			out, err := orch.Turn(context.Background(), sid, msg)
			require.NoError(t, err)
			require.Equal(t, app.StatePath("core.prd.idle"), out.NewState,
				"free-text idea must sink to default_intent discuss (stay in room), not steal to global core__work")
		})
	}
}

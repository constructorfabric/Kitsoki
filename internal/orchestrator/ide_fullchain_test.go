package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestIDE_FullChain_SelectionReachesConversePrompt is the keystone regression
// the original e2e lacked: it crosses every boundary from "operator typed free
// text with an editor selection on the turn ctx" to "the prompt bytes the model
// receives". The pieces were each unit-tested in isolation while the feature
// shipped broken end-to-end (the selection never reached any prompt, and free
// text mis-routed). This test fails if ANY link in that chain breaks:
//
//	free text  →  default_intent routing tier  →  discuss/converse effect
//	           →  host.agent.converse  →  appendIDEAmbient  →  prompt to model
//
// The editor selection is injected onto the turn ctx exactly as the TUI does
// (host.WithIDEAmbient), and a capturing ClaudeRunner records the prompt the
// converse verb dispatches — so the assertion is on the real bytes, not an
// intermediate struct.
func TestIDE_FullChain_SelectionReachesConversePrompt(t *testing.T) {
	t.Parallel()
	const appYAML = `
app:
  id: ide-fullchain
  version: 0.1.0
world: {}
intents:
  discuss:
    slots:
      message: { type: string, required: true }
root: chat
states:
  chat:
    mode: conversational
    default_intent: discuss
    on:
      discuss:
        - target: .
          effects:
            - invoke: host.agent.converse
              with:
                question: "{{ slots.message }}"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	orch := orchestrator.New(def, m, s, nil, orchestrator.WithHostRegistry(reg))

	// Capture the prompt the converse verb dispatches (args + stdin).
	var captured string
	runner := func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		captured = strings.Join(args, " ") + "\n" + stdin
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	// The editor selection on the turn ctx — exactly the seam the TUI fills.
	ctx := host.WithClaudeRunner(context.Background(), runner)
	ctx = host.WithIDEAmbient(ctx, host.IDEAmbient{
		File:      "/repo/internal/foo.go",
		Selection: "func answer() int { return 42 }",
		Lines:     1,
		Range:     "10:0-10:31",
	})

	out, err := orch.Turn(ctx, sid, "make this idempotent")
	require.NoError(t, err)
	require.NotNil(t, out)

	require.Contains(t, captured, "make this idempotent",
		"the operator's text must reach the converse prompt")
	require.Contains(t, captured, "## Active editor selection (via /ide)",
		"the editor selection block must ride the prompt the model receives")
	require.Contains(t, captured, "func answer() int { return 42 }",
		"the selected code must be in the prompt")
	require.Contains(t, captured, "/repo/internal/foo.go")
}

// TestIDE_FullChain_NoEditorIsByteIdentical guards the other side: a turn with
// no editor context produces a prompt with no IDE block — so the feature is a
// true no-op when /ide is off.
func TestIDE_FullChain_NoEditorIsByteIdentical(t *testing.T) {
	t.Parallel()
	const appYAML = `
app:
  id: ide-fullchain-off
  version: 0.1.0
world: {}
intents:
  discuss:
    slots:
      message: { type: string, required: true }
root: chat
states:
  chat:
    mode: conversational
    default_intent: discuss
    on:
      discuss:
        - target: .
          effects:
            - invoke: host.agent.converse
              with:
                question: "{{ slots.message }}"
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	orch := orchestrator.New(def, m, s, nil, orchestrator.WithHostRegistry(reg))

	var captured string
	runner := func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		captured = strings.Join(args, " ") + "\n" + stdin
		return host.ClaudeRun{Stdout: "ok"}, nil
	}

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	// No WithIDEAmbient — the /ide link is off.
	ctx := host.WithClaudeRunner(context.Background(), runner)
	_, err = orch.Turn(ctx, sid, "make this idempotent")
	require.NoError(t, err)

	require.Contains(t, captured, "make this idempotent")
	require.NotContains(t, captured, "Active editor",
		"no editor context must mean no IDE block in the prompt")
}

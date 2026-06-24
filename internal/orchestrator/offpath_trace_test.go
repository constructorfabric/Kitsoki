package orchestrator_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// setupOffPathOrchWithLogger mirrors setupOffPathOrch but threads a
// capturing slog handler through every component so the test can assert on
// the structured trace events emitted by the off-path code path. Kept
// separate from setupOffPathOrch to avoid disturbing the existing tests
// that rely on its exact return-shape.
func setupOffPathOrchWithLogger(t *testing.T) (*orchestrator.Orchestrator, *capturingHandler, app.SessionID) {
	t.Helper()
	t.Setenv(host.AgentBinEnv, fakeAgentPath(t))

	def := minimalOffPathApp()

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithLogger(logger),
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, handler, sid
}

// TestOffPathTraceEvents asserts that the off-path code path emits the
// full Ev{OffPathEnter, OffPathAskStart, OffPathChatResolved, OffPathAskDone,
// OffPathExit} sequence in order — the previously-silent activity that the
// trace pretty-printer now renders as OFFPATH-tagged chips.
func TestOffPathTraceEvents(t *testing.T) {
	orch, handler, sid := setupOffPathOrchWithLogger(t)
	ctx := context.Background()

	require.NoError(t, orch.MarkOffPathEntered(sid, "start"))

	answer, err := orch.AskOffPath(ctx, sid, "what is 2+2?")
	require.NoError(t, err)
	require.NotEmpty(t, answer)

	require.NoError(t, orch.MarkOffPathExited(sid, "start"))

	msgs := handler.msgs()
	t.Logf("captured %d events: %v", len(msgs), msgs)

	require.True(t, handler.hasMsg(trace.EvOffPathEnter), "expected offpath.enter")
	require.True(t, handler.hasMsg(trace.EvOffPathAskStart), "expected offpath.ask.start")
	require.True(t, handler.hasMsg(trace.EvOffPathChatResolved), "expected offpath.chat.resolved")
	require.True(t, handler.hasMsg(trace.EvOffPathAskDone), "expected offpath.ask.done")
	require.True(t, handler.hasMsg(trace.EvOffPathExit), "expected offpath.exit")

	enterIdx, askStartIdx, askDoneIdx, exitIdx := -1, -1, -1, -1
	for i, m := range msgs {
		switch m {
		case trace.EvOffPathEnter:
			if enterIdx < 0 {
				enterIdx = i
			}
		case trace.EvOffPathAskStart:
			if askStartIdx < 0 {
				askStartIdx = i
			}
		case trace.EvOffPathAskDone:
			askDoneIdx = i
		case trace.EvOffPathExit:
			exitIdx = i
		}
	}
	require.GreaterOrEqual(t, enterIdx, 0)
	require.GreaterOrEqual(t, askStartIdx, 0)
	require.GreaterOrEqual(t, askDoneIdx, 0)
	require.GreaterOrEqual(t, exitIdx, 0)
	require.Less(t, enterIdx, askStartIdx, "enter must precede ask.start")
	require.Less(t, askStartIdx, askDoneIdx, "ask.start must precede ask.done")
	require.Less(t, askDoneIdx, exitIdx, "ask.done must precede exit")
}

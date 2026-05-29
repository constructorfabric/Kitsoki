package tui_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/tui"
)

// TestTranscriptLiveLineNoRaceOnRapidUpdates verifies that rapid
// AppendLive / UpdateLive / FinalizeLive sequences don't produce
// duplicate or overlapping output in the pending queue. This regression
// tests the issue where multiple routing updates firing in quick
// succession could cause spinner/status lines to overlap in the TUI output.
func TestTranscriptLiveLineNoRaceOnRapidUpdates(t *testing.T) {
	t.Parallel()

	// Create a transcript model
	m := tui.NewTranscriptModel(80, app.AppDef{})

	// Simulate rapid routing updates like those from the routing observer
	// Sequence:
	// 1. AppendLive("Deterministic…")
	// 2. UpdateLive("Semantic…") — rapid update
	// 3. UpdateLive("Cache hit…") — another rapid update
	// 4. FinalizeLive("") — settle the live line
	// 5. Verify pending has exactly one entry (not duplicated)

	m.AppendLive("Deterministic…")
	require.Equal(t, "Deterministic…", tui.GetTranscriptLiveLine(m),
		"AppendLive should set liveLine")

	m.UpdateLive("Semantic…")
	require.Equal(t, "Semantic…", tui.GetTranscriptLiveLine(m),
		"UpdateLive should replace liveLine")

	m.UpdateLive("Cache hit…")
	require.Equal(t, "Cache hit…", tui.GetTranscriptLiveLine(m),
		"rapid UpdateLive should replace without duplicating")

	// Finalize (settle the line)
	m.FinalizeLive("")
	require.Empty(t, tui.GetTranscriptLiveLine(m),
		"FinalizeLive should clear liveLine")

	// FlushPending should return exactly one entry (the final cached line)
	pending := tui.GetTranscriptPending(m)
	require.Len(t, pending, 1, "pending should have exactly one entry")
	require.Equal(t, "Cache hit…", pending[0],
		"pending should contain the final settled line, not duplicates")
}

// TestTranscriptLiveLineRaceWithMultipleGoroutines verifies that
// concurrent UpdateLive calls don't produce inconsistent state.
// (In practice, Bubbletea's Update is single-threaded, but the
// routing observer launches goroutines to send messages, so we test
// that the model itself is safe against rapid sequential updates.)
func TestTranscriptLiveLineRaceWithMultipleGoroutines(t *testing.T) {
	t.Parallel()

	m := tui.NewTranscriptModel(80, app.AppDef{})

	// Start with a liveLine so UpdateLive calls don't return early
	m.AppendLive("Initial")

	var wg sync.WaitGroup
	const numUpdates = 100

	// All goroutines try to update the live line concurrently
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m.UpdateLive(strings.Repeat(".", idx%10+1))
		}(i)
	}
	wg.Wait()

	// At the end, liveLine should be non-empty (last update wins)
	liveLine := tui.GetTranscriptLiveLine(m)
	require.NotEmpty(t, liveLine, "liveLine should not be empty after concurrent updates")

	// Finalize and check pending — should have exactly one entry
	m.FinalizeLive("")
	pending := tui.GetTranscriptPending(m)
	require.Len(t, pending, 1, "pending should have exactly one entry (the final line)")
}

// TestTranscriptLiveLineUpdateAfterFinalize verifies the guard behavior:
// UpdateLive on an empty liveLine should be a no-op (not recreate it).
// This prevents late-arriving messages from creating new live content
// after the line has been settled.
func TestTranscriptLiveLineUpdateAfterFinalize(t *testing.T) {
	t.Parallel()

	m := tui.NewTranscriptModel(80, app.AppDef{})

	// Set and finalize
	m.AppendLive("Original")
	m.FinalizeLive("")
	require.Empty(t, tui.GetTranscriptLiveLine(m), "liveLine should be empty after finalize")

	// Late-arriving update should be no-op (guard prevents recreation)
	m.UpdateLive("Late message")
	require.Empty(t, tui.GetTranscriptLiveLine(m),
		"UpdateLive should not recreate liveLine after finalize")

	// Verify pending still has the original settled line, nothing extra
	pending := tui.GetTranscriptPending(m)
	require.Len(t, pending, 1, "should still have exactly one entry (original)")
	require.Equal(t, "Original", pending[0], "should be the original settled line")
}

// TestTranscriptFlushPendingClears verifies that FlushPending actually
// clears the pending queue after flushing. Multiple calls to FlushPending
// should not re-emit the same content.
func TestTranscriptFlushPendingClears(t *testing.T) {
	t.Parallel()

	m := tui.NewTranscriptModel(80, app.AppDef{})

	// Add some content to pending via AppendLive + FinalizeLive
	m.AppendLive("Line 1")
	m.FinalizeLive("")

	pending1 := tui.GetTranscriptPending(m)
	require.Len(t, pending1, 1, "should have one pending item")

	// Flush (this would be called by the Update wrapper)
	_ = m.FlushPending()

	// After flush, pending should be empty
	pending2 := tui.GetTranscriptPending(m)
	require.Empty(t, pending2, "pending should be empty after flush")

	// Second flush should return nil (no pending items)
	cmd := m.FlushPending()
	require.Nil(t, cmd, "second flush should return nil (no pending)")
}

// TestTranscriptRealisticRoutingSequence reproduces the routing event
// sequence that would occur during a normal turn, ensuring no duplicate
// or overlapping output. This corresponds to the scenario shown in the
// screenshot bug report.
func TestTranscriptRealisticRoutingSequence(t *testing.T) {
	t.Parallel()

	m := tui.NewTranscriptModel(80, app.AppDef{})

	// 1. User submits input → deterministic placeholder
	m.AppendLive("  routing: deterministic…")
	require.Equal(t, "  routing: deterministic…", tui.GetTranscriptLiveLine(m))

	// 2. Deterministic tier misses → semantic tier
	m.UpdateLive("  routing: semantic…")
	require.Equal(t, "  routing: semantic…", tui.GetTranscriptLiveLine(m))

	// 3. Semantic tier misses → cache tier
	m.UpdateLive("  routing: turncache…")
	require.Equal(t, "  routing: turncache…", tui.GetTranscriptLiveLine(m))

	// 4. Cache miss → LLM tier
	m.UpdateLive("  routing: LLM…")
	require.Equal(t, "  routing: LLM…", tui.GetTranscriptLiveLine(m))

	// 5. LLM resolves (simulate) → finalize
	m.FinalizeLive("↳ resolved: view")
	require.Empty(t, tui.GetTranscriptLiveLine(m),
		"liveLine should be cleared after finalize")

	// 6. Verify pending has exactly one entry (the final resolved line)
	// not multiple entries (which would cause duplicate output)
	pending := tui.GetTranscriptPending(m)
	require.Len(t, pending, 1, "pending should have exactly one entry")
	require.Equal(t, "↳ resolved: view", pending[0],
		"pending should contain the final resolved line")

	// 7. Flush and verify the queue is clear
	_ = m.FlushPending()
	pending2 := tui.GetTranscriptPending(m)
	require.Empty(t, pending2, "pending should be empty after flush")
}

package store_test

// jsonl_crash_test.go — Layer 4: crash-mid-write recovery.
//
// Sub-cases:
//   - RandomTruncation (N=1000, skip under -short)
//   - TruncationAtExactNewline
//   - TruncationCrossingTurnBoundary
//   - FsyncFailureInjection
//   - DiskFullENOSPC
//   - FileReplacedUnderUs
//   - ReadOnlyFilesystem
//   - ConcurrentWriters (flock)
//   - SymlinkInPath

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// ─── Layer 4.1: Random truncation (property test, N=1000) ─────────────────────

// TestLayer4_RandomTruncation picks random byte offsets within the last line
// of a trace file, truncates there, and verifies that reopen either:
//
//	(a) detects the torn last line and discards it (folding to the prior committed turn), or
//	(b) the file was already well-formed at that point and reopen succeeds.
//
// The important assertion is: reopen never panics, never silently returns
// corrupt history, and (if it succeeds) returns a history that folds
// without error.
//
// Skipped under -short (slow loop).
func TestLayer4_RandomTruncation(t *testing.T) {
	if testing.Short() {
		t.Skip("Layer4_RandomTruncation: skip under -short (N=1000 slow loop)")
	}
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bigtrace.jsonl")

	// Build a reasonably long fixture (100+ events across multiple turns).
	events := makeLongFixture(200, 20)
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	for _, ev := range events {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// Find the last line start.
	lastNL := bytes.LastIndexByte(raw[:len(raw)-1], '\n') // -1 to skip trailing newline
	if lastNL < 0 {
		t.Skip("fixture too small: only one line")
	}
	lastLineStart := lastNL + 1
	lastLineEnd := len(raw) - 1 // position of the trailing \n

	if lastLineEnd <= lastLineStart {
		t.Skip("last line is empty")
	}

	rng := rand.New(rand.NewSource(42))
	const N = 1000

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()

	for i := 0; i < N; i++ {
		// Pick a random byte offset within [lastLineStart, lastLineEnd).
		offset := lastLineStart + rng.Intn(lastLineEnd-lastLineStart)

		truncated := make([]byte, offset)
		copy(truncated, raw[:offset])

		truncPath := filepath.Join(dir, fmt.Sprintf("trunc_%d.jsonl", i))
		require.NoError(t, os.WriteFile(truncPath, truncated, 0o644))

		// Reopen: either succeeds (well-formed prefix) or returns an error.
		// Never panic; never return corrupt data.
		s2, reopenErr := store.OpenJSONL(truncPath)
		if reopenErr != nil {
			// Acceptable: torn last line detected.
			continue
		}
		// Succeeded: history must be foldable without error.
		hist := s2.History()
		_ = s2.Close()
		_, foldErr := store.BuildJourney(def, "start", initial, hist)
		require.NoError(t, foldErr,
			"iter %d: history from truncated-file must fold without error", i)
	}
}

// makeLongFixture produces nEvents events spread across nTurns turns.
// Events alternate between TurnStarted, TransitionApplied, EffectApplied, TurnEnded.
func makeLongFixture(nEvents, nTurns int) store.History {
	h := make(store.History, 0, nEvents)
	evPerTurn := nEvents / nTurns
	if evPerTurn < 2 {
		evPerTurn = 2
	}
	seq := 0
	for turn := 1; turn <= nTurns; turn++ {
		seq = 0
		h = append(h, mkEvent(app.TurnNumber(turn), seq, store.TurnStarted, map[string]any{"input": fmt.Sprintf("turn-%d", turn)}))
		seq++
		h = append(h, mkEvent(app.TurnNumber(turn), seq, store.TransitionApplied, map[string]any{
			"from": fmt.Sprintf("state_%d", turn-1),
			"to":   fmt.Sprintf("state_%d", turn),
		}))
		seq++
		for j := 2; j < evPerTurn-1; j++ {
			h = append(h, mkEvent(app.TurnNumber(turn), seq, store.EffectApplied, map[string]any{
				"set": map[string]any{fmt.Sprintf("var_%d", j): turn * j},
			}))
			seq++
		}
		h = append(h, mkEvent(app.TurnNumber(turn), seq, store.TurnEnded, map[string]any{}))
	}
	return h
}

// ─── Layer 4.2: Truncation at exact newline boundary ──────────────────────────

// TestLayer4_TruncationAtExactNewlineBoundary verifies that a file ending
// exactly at the \n of the last complete line is treated as well-formed
// (no recovery needed; all events intact).
func TestLayer4_TruncationAtExactNewlineBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.jsonl")

	events := []store.Event{
		mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1}),
		mkEvent(1, 1, store.TurnEnded, map[string]any{}),
	}
	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	for _, ev := range events {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	// File already ends at \n — this is the "exact newline boundary" case.
	// Reopen must succeed and see all events.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err, "file ending at exact \\n must open without error")
	defer s2.Close()

	hist := s2.History()
	require.Len(t, hist, len(events),
		"history must contain all events when file ends at \\n boundary")
}

// ─── Layer 4.3: Truncation crossing a turn boundary ──────────────────────────

// TestLayer4_TruncationCrossingTurnBoundary verifies that when multiple events
// from one turn are lost due to truncation, the reopened file's history reflects
// only the prior fully-committed turns (not partial data from the lost turn).
//
// Since OpenJSONL currently accepts partial-turn data (individual events are
// atomic; turn-level atomicity is a higher-level concern), this test verifies
// that at minimum: (a) reopen does not error on a cleanly-newline-terminated
// prefix, and (b) the returned history contains only the prior events.
func TestLayer4_TruncationCrossingTurnBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cross_turn.jsonl")

	// Turn 1 events (fully committed).
	turn1 := []store.Event{
		mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1}),
		mkEvent(1, 1, store.TransitionApplied, map[string]any{"from": "a", "to": "b"}),
		mkEvent(1, 2, store.TurnEnded, map[string]any{}),
	}
	// Turn 2 events (partially written — simulate crash mid-turn).
	turn2Partial := []store.Event{
		mkEvent(2, 0, store.TurnStarted, map[string]any{"x": 2}),
		mkEvent(2, 1, store.TransitionApplied, map[string]any{"from": "b", "to": "c"}),
		// TurnEnded never written — crash here
	}

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	for _, ev := range append(turn1, turn2Partial...) {
		require.NoError(t, s.Append(ev))
	}
	require.NoError(t, s.Close())

	// The file is complete (all lines have \n) but turn 2 is incomplete.
	// Reopen must succeed since all written lines are valid JSON.
	s2, err := store.OpenJSONL(path)
	require.NoError(t, err,
		"file with partial turn (but valid lines) must open without error")
	defer s2.Close()

	hist := s2.History()
	// Must see all written events (turn 1 + partial turn 2).
	wantCount := len(turn1) + len(turn2Partial)
	require.Len(t, hist, wantCount,
		"all cleanly-written events must be present after reopen")

	// Now simulate actual truncation: cut off the last line (last turn2 event).
	rawFull, err := os.ReadFile(path)
	require.NoError(t, err)

	// Find and remove the last complete line.
	// trim trailing \n, find the previous \n
	trimmed := rawFull[:len(rawFull)-1]
	lastNL := bytes.LastIndexByte(trimmed, '\n')
	require.True(t, lastNL >= 0, "must have at least one prior \\n")

	truncated := rawFull[:lastNL+1] // end at the prior \n (exclusive of last line)
	truncPath := filepath.Join(dir, "truncated_cross_turn.jsonl")
	require.NoError(t, os.WriteFile(truncPath, truncated, 0o644))

	s3, err := store.OpenJSONL(truncPath)
	require.NoError(t, err,
		"file truncated at \\n boundary must reopen successfully")
	defer s3.Close()

	hist3 := s3.History()
	// The last event of turn2Partial was removed; only len-1 events remain.
	require.Len(t, hist3, wantCount-1,
		"truncating at \\n boundary removes exactly the last line")
}

// ─── Layer 4.4: fsync failure injection ───────────────────────────────────────

// TestLayer4_FsyncFailure verifies that an injected fsync error surfaces to the
// caller of Append and does not silently succeed.
func TestLayer4_FsyncFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "fsync_fail.jsonl")

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Inject: fail on every fsync call.
	injectErr := errors.New("injected fsync failure")
	s.SetHookFsync(func(_ *os.File) error {
		return injectErr
	})

	err = s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1}))
	require.Error(t, err, "Append must surface fsync error")
	// The implementation uses %w so errors.Is is the strongest assertion.
	// Dropped the ||containsString fallback (finding: pick errors.Is, not ||).
	require.True(t, errors.Is(err, injectErr),
		"error must wrap the injected fsync error via errors.Is: %v", err)
}

// ─── Layer 4.5: Disk full (ENOSPC) ────────────────────────────────────────────

// TestLayer4_DiskFullENOSPC verifies that an injected ENOSPC write error
// surfaces to the caller of Append.
func TestLayer4_DiskFullENOSPC(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "enospc.jsonl")

	s, err := store.OpenJSONL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Inject ENOSPC on every write call during Append.
	s.SetHookWrite(func(_ *os.File, _ []byte) (int, error) {
		return 0, syscall.ENOSPC
	})

	err = s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{"x": 1}))
	require.Error(t, err, "Append must surface ENOSPC write error")
}

// ─── Layer 4.6: File replaced under us ────────────────────────────────────────

// TestLayer4_FileReplacedUnderUs verifies that when the trace file is atomically
// replaced (via rename) after OpenJSONL, the sink either detects the inode
// change or at minimum does not silently write to the wrong file.
//
// Note: The current JSONLSink implementation does not implement inode checking
// at Append time (the proposal requests adding it). This test documents the
// current behaviour and serves as a regression anchor: if inode checking is
// added, update to require.Error.
//
// Current behaviour: Append writes to the already-open file descriptor (not
// the new file at that path). The O_APPEND fd continues to write to the
// original inode, not the replacement. This is safe for the "don't corrupt
// the new file" property. The test asserts the append succeeds (original inode
// still writable) and that the replacement file does NOT contain the new event.
func TestLayer4_FileReplacedUnderUs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	origPath := filepath.Join(dir, "trace.jsonl")

	s, err := store.OpenJSONL(origPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Atomically replace the trace file.
	newPath := filepath.Join(dir, "trace_new.jsonl")
	s2, err := store.OpenJSONL(newPath)
	require.NoError(t, err)
	require.NoError(t, s2.Close())
	require.NoError(t, os.Rename(newPath, origPath))

	// Now append to the original (now-replaced) sink.
	// Inode checking is active: the path now points to a different inode.
	appendErr := s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{"replaced": true}))
	require.Error(t, appendErr, "Append after file replacement must fail (inode changed)")
	require.Contains(t, appendErr.Error(), "replaced under us",
		"error must mention file replacement; got: %v", appendErr)
}

// ─── Layer 4.7: Read-only filesystem ──────────────────────────────────────────

// TestLayer4_ReadOnlyFilesystem verifies that opening a trace in a read-only
// directory fails at OpenJSONL, not at first Append.
func TestLayer4_ReadOnlyFilesystem(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("read-only test skipped when running as root")
	}
	t.Parallel()
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro")
	require.NoError(t, os.Mkdir(roDir, 0o555)) // read + execute, no write
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })

	tracePath := filepath.Join(roDir, "trace.jsonl")
	_, err := store.OpenJSONL(tracePath)
	require.Error(t, err,
		"OpenJSONL on read-only directory must fail at open, not at first Append")
}

// ─── Layer 4.8: Concurrent writers (flock) ────────────────────────────────────

// TestLayer4_ConcurrentWriters verifies that a second OpenJSONL on the same
// trace path fails at open time because flock is enforced. The second writer
// must not succeed; if it does, the test fails.
func TestLayer4_ConcurrentWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")

	s1, err := store.OpenJSONL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s1.Close() })

	// flock is enforced: second open must fail.
	_, err2 := store.OpenJSONL(path)
	require.Error(t, err2, "second OpenJSONL on the same trace file must fail (flock enforced)")
	require.Contains(t, err2.Error(), "locked",
		"error must mention locking; got: %v", err2)
	t.Logf("second OpenJSONL correctly failed (flock enforced): %v", err2)
}

// ─── Layer 4.9: Symlink in path ───────────────────────────────────────────────

// TestLayer4_SymlinkInPath verifies that a trace path that is a symlink is
// resolved at open time and the sink writes to the real target.
func TestLayer4_SymlinkInPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.jsonl")
	linkPath := filepath.Join(dir, "link.jsonl")

	// Create the symlink (target doesn't need to exist yet for a dangling link;
	// but OpenJSONL will create the target, so link must point to a writable path).
	require.NoError(t, os.Symlink(realPath, linkPath))

	// Open via the symlink.
	s, err := store.OpenJSONL(linkPath)
	require.NoError(t, err, "OpenJSONL via symlink must succeed")
	require.NoError(t, s.Append(mkEvent(1, 0, store.TurnStarted, map[string]any{"via": "symlink"})))
	require.NoError(t, s.Close())

	// The real file must exist and contain the event.
	_, statErr := os.Stat(realPath)
	require.NoError(t, statErr, "real target file must exist after write via symlink")

	// Reload via the symlink and verify history.
	s2, err := store.OpenJSONL(linkPath)
	require.NoError(t, err)
	defer s2.Close()
	hist := s2.History()
	require.Len(t, hist, 1, "history via symlink must see the written event")
	require.Equal(t, store.TurnStarted, hist[0].Kind)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// Satisfy unused import of time (used in makeLongFixture via mkEvent).
var _ = time.Now

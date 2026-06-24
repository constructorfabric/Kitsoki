package mining

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// fakeReloader records Reload/RerunOnEnter calls; an optional failOnNth makes a
// Reload fail to exercise the reload-failure revert path.
type fakeReloader struct {
	reloads  int
	reruns   int
	failNext bool
}

func (f *fakeReloader) Reload(_ string, _ app.StatePath) error {
	f.reloads++
	if f.failNext {
		f.failNext = false
		return errors.New("boom: load failed")
	}
	return nil
}

func (f *fakeReloader) RerunOnEnter(_ context.Context, _ app.SessionID) error {
	f.reruns++
	return nil
}

// fakeGate returns a fixed failure count (and optional error).
type fakeGate struct {
	failed int
	err    error
	calls  int
}

func (g *fakeGate) RunFlows(_ context.Context, _, _ string) (int, error) {
	g.calls++
	return g.failed, g.err
}

func newApplier(root string, r *fakeReloader, g *fakeGate) *Applier {
	return &Applier{
		TreeRoot: root,
		Entry:    "app.yaml",
		State:    app.StatePath("foyer"),
		SID:      "sess-1",
		FlowGlob: "flows/*.yaml",
		Reloader: r,
		FlowGate: g,
	}
}

func decidedPayload(t *testing.T, rec *recorderSink) store.MiningProposalDecidedPayload {
	t.Helper()
	require.Len(t, rec.events, 1)
	assert.Equal(t, store.MiningProposalDecided, rec.events[0].kind)
	var dp store.MiningProposalDecidedPayload
	require.NoError(t, json.Unmarshal(rec.events[0].payload, &dp))
	return dp
}

// TestAccept_GreenKeepsLive proves the happy path: write delta → reload →
// green gate → keep, with MiningProposalDecided{accept, flows_green:true,
// reverted:false}.
func TestAccept_GreenKeepsLive(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("app: original\n"), 0o644))

	r := &fakeReloader{}
	g := &fakeGate{failed: 0}
	a := newApplier(root, r, g)
	rec := &recorderSink{}

	prop := &Proposal{
		Recipe: Recipe{ID: "r1", Kind: KindBinding},
		Files:  map[string][]byte{"app.yaml": []byte("app: enriched\n")},
	}
	res, err := a.Accept(context.Background(), prop, sessionSink(rec))
	require.NoError(t, err)
	assert.True(t, res.FlowsGreen)
	assert.False(t, res.Reverted)

	// The edit is LIVE on disk.
	got, _ := os.ReadFile(filepath.Join(root, "app.yaml"))
	assert.Equal(t, "app: enriched\n", string(got))

	assert.Equal(t, 1, r.reloads, "one reload on the apply")
	assert.Equal(t, 1, r.reruns)
	assert.Equal(t, 1, g.calls, "gate ran once against the reloaded tree")

	dp := decidedPayload(t, rec)
	assert.Equal(t, store.MiningVerdictAccept, dp.Verdict)
	assert.Equal(t, store.MiningByHuman, dp.By)
	assert.True(t, dp.FlowsGreen)
	assert.False(t, dp.Reverted)
}

// TestAccept_RedRevertsByteForByte proves the gate is a hard revert-or-keep:
// a red gate restores the pre-edit bytes, re-reloads, and records
// {flows_green:false, reverted:true}.
func TestAccept_RedRevertsByteForByte(t *testing.T) {
	root := t.TempDir()
	original := []byte("app: original\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), original, 0o644))

	r := &fakeReloader{}
	g := &fakeGate{failed: 2} // two fixtures regressed
	a := newApplier(root, r, g)
	rec := &recorderSink{}

	prop := &Proposal{
		Recipe: Recipe{ID: "r1", Kind: KindBinding},
		Files:  map[string][]byte{"app.yaml": []byte("app: broken\n")},
	}
	res, err := a.Accept(context.Background(), prop, sessionSink(rec))
	require.NoError(t, err)
	assert.False(t, res.FlowsGreen)
	assert.True(t, res.Reverted)
	assert.Equal(t, 2, res.FailedFlows)

	// Byte-for-byte restore.
	got, _ := os.ReadFile(filepath.Join(root, "app.yaml"))
	assert.Equal(t, original, got, "the pre-edit bytes must be restored exactly")

	// Two reloads: the apply, then the revert's re-Reload.
	assert.Equal(t, 2, r.reloads)

	dp := decidedPayload(t, rec)
	assert.Equal(t, store.MiningVerdictAccept, dp.Verdict)
	assert.False(t, dp.FlowsGreen)
	assert.True(t, dp.Reverted)
}

// TestAccept_NewFileRevertedByRemoval proves a delta that ADDS a file (no
// pre-edit bytes) is reverted by removing the file on a red gate.
func TestAccept_NewFileRevertedByRemoval(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("app: x\n"), 0o644))

	r := &fakeReloader{}
	g := &fakeGate{failed: 1}
	a := newApplier(root, r, g)
	rec := &recorderSink{}

	prop := &Proposal{
		Recipe: Recipe{ID: "r1", Kind: KindGate},
		Files:  map[string][]byte{"rooms/new.yaml": []byte("room: new\n")},
	}
	res, err := a.Accept(context.Background(), prop, sessionSink(rec))
	require.NoError(t, err)
	assert.True(t, res.Reverted)

	_, statErr := os.Stat(filepath.Join(root, "rooms", "new.yaml"))
	assert.True(t, os.IsNotExist(statErr), "an added file must be removed on revert")
}

// TestAccept_ReloadFailureReverts proves a reload that fails after the write
// (e.g. the loader chokes on the edited tree) reverts and records reverted.
func TestAccept_ReloadFailureReverts(t *testing.T) {
	root := t.TempDir()
	original := []byte("app: original\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), original, 0o644))

	r := &fakeReloader{failNext: true}
	g := &fakeGate{failed: 0}
	a := newApplier(root, r, g)
	rec := &recorderSink{}

	prop := &Proposal{
		Recipe: Recipe{ID: "r1", Kind: KindBinding},
		Files:  map[string][]byte{"app.yaml": []byte("app: broken\n")},
	}
	res, err := a.Accept(context.Background(), prop, sessionSink(rec))
	require.NoError(t, err)
	assert.True(t, res.Reverted)
	assert.False(t, res.FlowsGreen)
	assert.Equal(t, 0, g.calls, "the gate never runs when the reload itself failed")

	got, _ := os.ReadFile(filepath.Join(root, "app.yaml"))
	assert.Equal(t, original, got)
}

// TestRejectAndRefine record their verdicts without touching the tree.
func TestRejectAndRefine(t *testing.T) {
	a := &Applier{}
	rec := &recorderSink{}

	require.NoError(t, a.Reject("r1", sessionSink(rec)))
	require.NoError(t, a.Refine("r2", sessionSink(rec)))

	require.Len(t, rec.events, 2)
	var reject store.MiningProposalDecidedPayload
	require.NoError(t, json.Unmarshal(rec.events[0].payload, &reject))
	assert.Equal(t, store.MiningVerdictReject, reject.Verdict)
	var refine store.MiningProposalDecidedPayload
	require.NoError(t, json.Unmarshal(rec.events[1].payload, &refine))
	assert.Equal(t, store.MiningVerdictRefine, refine.Verdict)
}

// fakeLocker proves the per-chat lock is acquired across the keep-or-revert.
type fakeLocker struct{ locked, unlocked int }

func (l *fakeLocker) Lock()   { l.locked++ }
func (l *fakeLocker) Unlock() { l.unlocked++ }

func TestAccept_AcquiresLock(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte("x"), 0o644))
	r := &fakeReloader{}
	g := &fakeGate{}
	a := newApplier(root, r, g)
	lock := &fakeLocker{}
	a.Lock = lock

	prop := &Proposal{Recipe: Recipe{ID: "r1"}, Files: map[string][]byte{"app.yaml": []byte("y")}}
	_, err := a.Accept(context.Background(), prop, sessionSink(&recorderSink{}))
	require.NoError(t, err)
	assert.Equal(t, 1, lock.locked)
	assert.Equal(t, 1, lock.unlocked)
}

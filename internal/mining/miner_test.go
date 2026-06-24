package mining

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
)

// fakeRunner is an injected PipelineRunner that records the request it was
// handed and returns a canned result (or blocks until released, for the
// cancelled-pass case). It NEVER spends LLM — the whole point of the seam.
type fakeRunner struct {
	mu       sync.Mutex
	gotReqs  []PassRequest
	result   PassResult
	block    chan struct{} // when non-nil, RunPass blocks until ctx done or closed
	released bool
}

func (r *fakeRunner) RunPass(ctx context.Context, req PassRequest) (PassResult, error) {
	r.mu.Lock()
	r.gotReqs = append(r.gotReqs, req)
	block := r.block
	r.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return PassResult{}, ctx.Err()
		case <-block:
		}
	}
	return r.result, nil
}

func (r *fakeRunner) requests() []PassRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PassRequest(nil), r.gotReqs...)
}

// newTestMiner wires a miner over an in-memory scheduler, a fixture transcript
// dir under home, an in-memory watermark store, and the given fake runner.
func newTestMiner(t *testing.T, runner PipelineRunner, marks WatermarkStore) (*Miner, jobs.Scheduler, string) {
	t.Helper()
	home := t.TempDir()
	repo := "/Users/brad/code/Kitsoki"
	// A fixture slug dir with one transcript so Resolve returns it (and
	// newestMtime has something to read in the exec path — irrelevant here since
	// the runner is faked).
	slugDir := filepath.Join(home, ".claude", "projects", Slug(repo))
	require.NoError(t, os.MkdirAll(slugDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(slugDir, "s1.jsonl"), []byte("{}"), 0o644))

	sched := jobs.NewInMemoryScheduler()
	m := &Miner{
		Resolver: TranscriptResolver{HomeDir: home},
		Sched:    sched,
		Pipeline: runner,
		Marks:    marks,
		Cfg:      Config{Enabled: true, Cadence: 10 * time.Millisecond, FirstPassSample: 5},
	}
	return m, sched, repo
}

// TestMiner_FirstLaunchSeed: an empty watermark for the slug ⇒ Start fires a
// SEED pass (the human-backlog history seed), and a completed pass advances the
// watermark. First-launch detection is exactly "watermark absent".
func TestMiner_FirstLaunchSeed(t *testing.T) {
	runner := &fakeRunner{result: PassResult{
		Recipes:      []Recipe{{ID: "x#1", Priority: 3, Kind: KindBinding}},
		Sessions:     2,
		NewWatermark: 1700,
	}}
	marks := NewMapWatermarkStore(nil)
	m, sched, repo := newTestMiner(t, runner, marks)

	// Empty watermark ⇒ first launch.
	_, mined := marks.Get(Slug(absClean(repo)))
	require.False(t, mined, "watermark must be empty before first launch")

	require.NoError(t, m.Start(context.Background(), app.SessionID("s"), repo))
	require.NoError(t, sched.WaitIdle(context.Background()))
	waitWatermark(t, marks, Slug(absClean(repo)), 1700)

	reqs := runner.requests()
	require.Len(t, reqs, 1, "exactly one seed pass on first launch")
	assert.Equal(t, TriggerSeed, reqs[0].Trigger, "first launch is a SEED pass")

	mtime, mined := marks.Get(Slug(absClean(repo)))
	assert.True(t, mined, "completed pass marks the slug mined")
	assert.Equal(t, int64(1700), mtime, "watermark advances to the pass's newest mtime")
}

// TestMiner_SecondLaunchNoSeed: a slug with a watermark present ⇒ Start fires NO
// seed (the seed is first-launch-only). Live work drives Notify thereafter.
func TestMiner_SecondLaunchNoSeed(t *testing.T) {
	runner := &fakeRunner{}
	marks := NewMapWatermarkStore(map[string]int64{
		Slug(absClean("/Users/brad/code/Kitsoki")): 1500,
	})
	m, sched, repo := newTestMiner(t, runner, marks)

	require.NoError(t, m.Start(context.Background(), app.SessionID("s"), repo))
	require.NoError(t, sched.WaitIdle(context.Background()))

	assert.Empty(t, runner.requests(), "an already-mined slug fires no seed pass")
}

// TestMiner_CancelledLeavesWatermarkUntouched: a pass that is cancelled before
// it completes must leave the watermark untouched so the next pass re-picks the
// same transcripts. The watermark advances ONLY on a completed pass.
func TestMiner_CancelledLeavesWatermarkUntouched(t *testing.T) {
	runner := &fakeRunner{
		block:  make(chan struct{}),
		result: PassResult{NewWatermark: 9999},
	}
	marks := NewMapWatermarkStore(nil)
	m, sched, repo := newTestMiner(t, runner, marks)
	slug := Slug(absClean(repo))

	require.NoError(t, m.Start(context.Background(), app.SessionID("s"), repo))

	// Wait until the blocking pass is in flight (its JobID is recorded in the
	// captured PassRequest), then cancel its job.
	var id jobs.JobID
	require.Eventually(t, func() bool {
		reqs := runner.requests()
		if len(reqs) == 1 && reqs[0].JobID != "" {
			id = jobs.JobID(reqs[0].JobID)
			return true
		}
		return false
	}, time.Second, 5*time.Millisecond, "a mine job must be running")
	require.NoError(t, sched.Cancel(context.Background(), id))
	require.NoError(t, sched.WaitIdle(context.Background()))
	// Release the blocked runner so its goroutine unwinds cleanly.
	close(runner.block)

	// Give the miner's terminal-subscription goroutine a moment to observe the
	// cancellation; it must NOT advance the watermark.
	assertWatermarkStaysUnmined(t, marks, slug)
	_, mined := marks.Get(slug)
	assert.False(t, mined, "a cancelled pass must leave the slug UNMINED")
}

// TestMiner_DisabledStartsNothing: a disabled miner runs no pass and records no
// watermark — the flow/test posture. Notify on a disabled miner is a no-op pass
// (records a paused event via the sink, which is nil here ⇒ harmless).
func TestMiner_DisabledStartsNothing(t *testing.T) {
	runner := &fakeRunner{}
	marks := NewMapWatermarkStore(nil)
	m, sched, repo := newTestMiner(t, runner, marks)
	m.Cfg.Enabled = false

	require.NoError(t, m.Start(context.Background(), app.SessionID("s"), repo))
	require.NoError(t, sched.WaitIdle(context.Background()))
	m.Notify(context.Background())
	require.NoError(t, sched.WaitIdle(context.Background()))

	assert.Empty(t, runner.requests(), "a disabled miner spends nothing")
	_, mined := marks.Get(Slug(absClean(repo)))
	assert.False(t, mined)
}

// TestMiner_RecipesHandedToProposer confirms the on-complete recipe hand-off:
// a completed pass delivers its emitted recipes to the injected RecipeHandler
// (the proposer's seam) on the miner's background goroutine.
func TestMiner_RecipesHandedToProposer(t *testing.T) {
	want := []Recipe{{ID: "r#1", Priority: 4, Kind: KindGate}}
	runner := &fakeRunner{result: PassResult{Recipes: want, Sessions: 1, NewWatermark: 42}}
	marks := NewMapWatermarkStore(nil)
	m, sched, repo := newTestMiner(t, runner, marks)

	var (
		mu  sync.Mutex
		got []Recipe
		sid app.SessionID
	)
	m.Recipes = func(_ context.Context, s app.SessionID, recipes []Recipe) {
		mu.Lock()
		defer mu.Unlock()
		sid = s
		got = recipes
	}

	require.NoError(t, m.Start(context.Background(), app.SessionID("sess-7"), repo))
	require.NoError(t, sched.WaitIdle(context.Background()))
	waitWatermark(t, marks, Slug(absClean(repo)), 42)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	}, time.Second, 5*time.Millisecond, "recipes must reach the proposer seam")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, want, got)
	assert.Equal(t, app.SessionID("sess-7"), sid)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func waitWatermark(t *testing.T, marks WatermarkStore, slug string, want int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		mt, mined := marks.Get(slug)
		return mined && mt == want
	}, time.Second, 5*time.Millisecond, "watermark must advance to %d", want)
}

func assertWatermarkStaysUnmined(t *testing.T, marks WatermarkStore, slug string) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case <-deadline:
			return
		default:
			if _, mined := marks.Get(slug); mined {
				t.Fatalf("watermark advanced on a cancelled pass (slug %q)", slug)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

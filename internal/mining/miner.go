package mining

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/store"
)

// PassTrigger is why a mining pass fired: the first-launch history seed or a
// debounced live pass over new transcripts.
type PassTrigger string

const (
	// TriggerSeed is the bounded first-pass over a project's EXISTING transcript
	// history — fires exactly once per slug, when its watermark is empty. It mines
	// the human's interactive backlog (entrypoint=cli) and DROPS dispatched
	// headless agent/agent sessions (prep.py default).
	TriggerSeed PassTrigger = PassTrigger(store.MiningTriggerSeed)
	// TriggerLive is a debounced pass over the NEW transcripts the live free-form
	// work produced (mtime > watermark). It KEEPS dispatched agent sessions
	// (prep.py --keep-agent-sessions) because those host.agent.task turns are
	// exactly what work happened.
	TriggerLive PassTrigger = PassTrigger(store.MiningTriggerLive)
)

// PassRequest is the resolved input to one pipeline pass — what the injected
// PipelineRunner needs to run prep.py → the one agent pass → ground/score/emit.
// It is deterministic; the runner reads it and returns the emitted recipes.
type PassRequest struct {
	// Slug is the Claude Code projects slug of the repo (mining.Slug).
	Slug string
	// Trigger is seed|live (drives --keep-agent-sessions and the sample mode).
	Trigger PassTrigger
	// TranscriptDirs are the resolved dirs to mine (primary + extras), already
	// presence-checked by TranscriptResolver.Resolve.
	TranscriptDirs []string
	// Watermark is the per-slug newest-mined mtime (unix seconds). A live pass
	// selects transcripts with mtime > Watermark; a seed pass ignores it (it is
	// empty by definition when the seed fires).
	Watermark int64
	// Sample is the recency cap for the seed pass (prep.py --sample recency --max
	// Sample). Ignored for live (live selects by mtime).
	Sample int
	// JobID is the background job id the pass runs under (for the trace event).
	JobID string
}

// PassResult is what a PipelineRunner returns from a completed pass.
type PassResult struct {
	// Recipes are the scored capture candidates emit.py produced (analysis.json),
	// already parsed into the proposer's consumable shape.
	Recipes []Recipe
	// Sessions is the count of transcript sessions folded into the sample (for
	// the MiningPassRan trace event).
	Sessions int
	// NewWatermark is the newest transcript mtime (unix seconds) in this pass's
	// sample. The miner advances mined_through[slug] to this ONLY on completion.
	// Zero leaves the watermark untouched (nothing new mined).
	NewWatermark int64
}

// PipelineRunner is the injected seam that wraps tools/session-mining: it runs
// prep.py(--job, sample, keep-agent-sessions) → intents.workflow.js (THE ONE
// agent pass, cassette-backed when real) → ground.py/tag_score.py/emit.py and
// parses analysis.json into recipes. It is the only path that may spend LLM, and
// only step B does; every test injects a fake. The package never reimplements
// the analyzer — this seam invokes it.
type PipelineRunner interface {
	RunPass(ctx context.Context, req PassRequest) (PassResult, error)
}

// WatermarkStore is the per-slug dedup ledger seam. Get returns the slug's
// newest-mined mtime and whether it has ever been mined (false ⇒ empty ⇒ the
// seed fires). Set persists an advanced watermark. In production this is backed
// by the .kitsoki.yaml `mining.mined_through` map (a sibling state file per the
// proposal's lean); tests use an in-memory map.
type WatermarkStore interface {
	Get(slug string) (mtime int64, mined bool)
	Set(slug string, mtime int64) error
}

// RecipeHandler receives the recipes a completed pass emitted. The orchestrator
// wires this to the proposer (mining-proposal-loop); a nil handler drops them
// (the pass still advances the watermark and emits MiningPassRan). It runs on
// the miner's background goroutine, off any turn.
type RecipeHandler func(ctx context.Context, sid app.SessionID, recipes []Recipe)

// Config is the miner's tunable knobs, sourced from webconfig.MiningConfig.
type Config struct {
	// Enabled gates the whole service. When false, Start is a no-op and Notify
	// records a paused MiningPassRan rather than running a pass.
	Enabled bool
	// Cadence is the debounce window for live passes.
	Cadence time.Duration
	// FirstPassSample is the recency cap for the history seed.
	FirstPassSample int
}

// Miner is the always-on, session-scoped ambient session miner. It seeds from a
// project's existing transcript history on first launch, then mines the NEW
// transcripts live free-form work produces on every debounce window, handing the
// emitted recipes to the proposer. It owns a watermark so no session is ever
// re-mined, and runs every pass as a detached jobs.Scheduler job so it survives
// across turns and never blocks input. See docs/architecture/ambient-mining.md.
//
// Dependency injection is the spine: Resolver, Sched, Pipeline, Marks, Sink,
// Recipes and Clk are all seams. No path here calls a live LLM — the single
// agent pass lives behind the injected PipelineRunner, cassette-backed when
// real.
type Miner struct {
	Resolver TranscriptResolver
	Sched    jobs.Scheduler
	Pipeline PipelineRunner
	Marks    WatermarkStore
	// Sink records MiningPassRan (nil ⇒ no trace event). Bound per session.
	Sink *SessionSink
	// Recipes receives emitted recipes (nil ⇒ dropped after the watermark
	// advance). The orchestrator wires this to the proposer.
	Recipes RecipeHandler
	// Clk is the time source (defaults to clock.Real()).
	Clk clock.Clock
	// Cfg is the resolved config.
	Cfg Config

	mu       sync.Mutex
	sid      app.SessionID
	slug     string
	dirs     []string
	started  bool
	enabled  bool
	debounce clock.Timer
	gen      uint64 // debounce generation; bumped on every (re)arm to coalesce
}

// clk returns the miner's clock, defaulting to real.
func (m *Miner) clk() clock.Clock {
	if m.Clk == nil {
		return clock.Real()
	}
	return m.Clk
}

// absClean resolves repoPath to an absolute, cleaned path (recap.sh runs
// `cd "$DIR" && pwd`).
func absClean(repoPath string) string {
	if !filepath.IsAbs(repoPath) {
		if a, err := filepath.Abs(repoPath); err == nil {
			repoPath = a
		}
	}
	return filepath.Clean(repoPath)
}

// Start binds the miner to a session + repo and, when enabled, fires the
// first-launch seed pass (iff the slug's watermark is empty). It is idempotent
// per session and never blocks: the seed runs as a detached background job. A
// disabled miner records nothing and starts no pass. Subsequent live work is fed
// via Notify. Returns an error only on a resolver/infra failure; "no transcript
// history" is a benign no-op (the seed simply finds nothing to mine).
func (m *Miner) Start(ctx context.Context, sid app.SessionID, repoPath string) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.sid = sid
	m.enabled = m.Cfg.Enabled
	m.slug = Slug(absClean(repoPath))

	dirs, err := m.Resolver.Resolve(repoPath, nil)
	if err != nil {
		m.started = false
		m.mu.Unlock()
		return err
	}
	m.dirs = dirs
	enabled := m.enabled
	slug := m.slug
	m.mu.Unlock()

	if !enabled {
		return nil
	}
	// First launch for this slug ⇒ seed pass over existing history. Thereafter
	// skip the seed; live work drives Notify.
	if _, mined := m.Marks.Get(slug); !mined {
		m.submitPass(context.WithoutCancel(ctx), TriggerSeed)
	}
	return nil
}

// Notify signals that new transcripts may exist (a turn finished, a dispatched
// host.agent.task landed). It debounces a live pass by Cadence: repeated calls
// within the window coalesce into one pass. A disabled miner records a paused
// MiningPassRan and runs nothing. Safe to call from any goroutine, off any turn.
func (m *Miner) Notify(ctx context.Context) {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	if !m.enabled {
		sink, sid, slug := m.Sink, m.sid, m.slug
		m.mu.Unlock()
		recordPaused(sink, sid, slug)
		return
	}
	cadence := m.Cfg.Cadence
	if cadence <= 0 {
		cadence = 30 * time.Second
	}
	// Coalesce: stop any pending debounce and bump the generation so a
	// previously-armed timer goroutine that already read its channel is ignored.
	if m.debounce != nil {
		m.debounce.Stop()
	}
	m.gen++
	gen := m.gen
	t := m.clk().NewTimer(cadence)
	m.debounce = t
	passCtx := context.WithoutCancel(ctx)
	m.mu.Unlock()

	go func() {
		<-t.C()
		m.mu.Lock()
		fire := m.started && m.enabled && m.gen == gen
		if fire {
			m.debounce = nil
		}
		m.mu.Unlock()
		if fire {
			m.submitPass(passCtx, TriggerLive)
		}
	}()
}

// SetEnabled flips the miner's enabled flag (the /mine pause/resume hook). A
// pause stops any pending debounce so no further pass fires; resume re-arms
// (the next Notify schedules a pass). Returns the new state.
func (m *Miner) SetEnabled(enabled bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
	if !enabled && m.debounce != nil {
		m.debounce.Stop()
		m.debounce = nil
		m.gen++ // invalidate any in-flight debounce goroutine
	}
	return m.enabled
}

// Enabled reports the live enabled state.
func (m *Miner) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// submitPass resolves the request, submits the pipeline pass as a detached
// jobs.Scheduler job (Kind "mine"), and starts a goroutine that awaits the
// terminal result. On a COMPLETED pass it advances the watermark, emits
// MiningPassRan, and hands the recipes to the proposer; a cancelled/failed pass
// leaves the watermark UNTOUCHED so the next pass re-picks the same transcripts.
func (m *Miner) submitPass(ctx context.Context, trigger PassTrigger) {
	m.mu.Lock()
	slug, dirs, sid := m.slug, append([]string(nil), m.dirs...), m.sid
	sample := m.Cfg.FirstPassSample
	if sample <= 0 {
		sample = 12
	}
	m.mu.Unlock()

	watermark, _ := m.Marks.Get(slug)

	// res is captured by the handler closure and read on the awaiting goroutine
	// after JobDone — the scheduler runs the handler to completion before
	// emitting the terminal event, so there is no read/write race across the two.
	var res PassResult
	spec := jobs.JobSpec{
		SessionID: sid,
		Kind:      "mine",
		Payload: map[string]any{
			"slug":    slug,
			"trigger": string(trigger),
		},
		Handler: func(hctx context.Context, args map[string]any) (host.Result, error) {
			jobID, _ := args["__job_id"].(string)
			r, err := m.Pipeline.RunPass(hctx, PassRequest{
				Slug:           slug,
				Trigger:        trigger,
				TranscriptDirs: dirs,
				Watermark:      watermark,
				Sample:         sample,
				JobID:          jobID,
			})
			if err != nil {
				return host.Result{}, err
			}
			res = r
			data, _ := toMap(mineResultData{Recipes: len(r.Recipes), Sessions: r.Sessions})
			return host.Result{Data: data}, nil
		},
	}

	id, err := m.Sched.Submit(ctx, spec)
	if err != nil {
		return
	}

	ch, unsub := m.Sched.Subscribe(id)
	go func() {
		defer unsub()
		for ev := range ch {
			switch ev.Status {
			case jobs.JobDone:
				m.onPassComplete(ctx, sid, slug, trigger, string(id), res)
				return
			case jobs.JobFailed, jobs.JobCancelled:
				// Watermark untouched → next pass re-picks the same transcripts.
				return
			}
		}
	}()
}

// onPassComplete advances the watermark (only here, only on a completed pass),
// emits MiningPassRan, and hands the recipes to the proposer.
func (m *Miner) onPassComplete(ctx context.Context, sid app.SessionID, slug string, trigger PassTrigger, jobID string, res PassResult) {
	if res.NewWatermark > 0 {
		if cur, _ := m.Marks.Get(slug); res.NewWatermark > cur {
			_ = m.Marks.Set(slug, res.NewWatermark)
		}
	}
	_ = appendPayload(m.Sink, store.MiningPassRan, store.MiningPassRanPayload{
		Trigger:  string(trigger),
		Slug:     slug,
		Sessions: res.Sessions,
		Recipes:  len(res.Recipes),
		JobID:    jobID,
	})
	if m.Recipes != nil && len(res.Recipes) > 0 {
		m.Recipes(ctx, sid, res.Recipes)
	}
}

// recordPaused emits a paused MiningPassRan so the trace shows a pass that would
// have fired while disabled.
func recordPaused(sink *SessionSink, _ app.SessionID, slug string) {
	_ = appendPayload(sink, store.MiningPassRan, store.MiningPassRanPayload{
		Trigger: string(TriggerLive),
		Slug:    slug,
		Paused:  true,
	})
}

// mineResultData is the typed payload a mine job's handler returns; it is
// marshaled into host.Result.Data so the count rides the job-terminal channel
// for the trace.
type mineResultData struct {
	Recipes  int `json:"recipes"`
	Sessions int `json:"sessions"`
}

// toMap round-trips a struct through JSON into a map[string]any (the shape
// host.Result.Data carries).
func toMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

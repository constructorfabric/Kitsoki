// trace_session.go — shared setup for the trace-backed persistent surfaces.
//
// Both `kitsoki turn --trace` (a single direct-intent turn) and `kitsoki drive`
// (an interactive free-text loop) open a JSONL trace, resolve/reconstruct the
// effective story, create a session in an in-memory store whose event writes
// are redirected to the JSONL sink, and record the effective story so the
// trace is self-contained. The only axis they differ on is the harness wired
// into orchestrator.New: turn passes a noRunHarness (the --intent path never
// routes), drive passes a real routing harness (live | replay | VCR). This
// helper factors out everything else.
package main

import (
	"context"
	"os"
	"path/filepath"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// traceSession bundles the wired orchestrator, its JSONL trace sink, and the
// bootstrapped session for a trace-backed surface. Call Close when done — it
// tears down the harness, agent registry, in-memory store, JSONL sink, and
// any story-reconstruction temp files in the right order.
type traceSession struct {
	Def  *app.AppDef
	Orch *orchestrator.Orchestrator
	Sink *store.JSONLSink
	SID  app.SessionID

	closers []func()
}

// Close runs the registered teardown functions in reverse order (LIFO),
// mirroring the defer order the inline setup used.
func (ts *traceSession) Close() {
	for i := len(ts.closers) - 1; i >= 0; i-- {
		ts.closers[i]()
	}
}

// setupTraceSession opens (or creates) the JSONL trace at tracePath, resolves
// the effective story (from appPath on disk, or reconstructed from the trace
// itself when appPath is ""), builds a JSONL-backed orchestrator wired to the
// supplied routing harness, creates a session, and records the effective story.
//
// The harness h is owned by the caller's intent but Closed by traceSession.Close
// (the agent registry built here wraps it for external transports; the
// orchestrator routes through it). Pass the real routing harness for drive, or
// a noRunHarness for the direct-intent turn path.
//
// Errors are wrapped with infraError so a driver sees exit code 3 for any
// setup failure (distinct from a semantic turn rejection).
func setupTraceSession(ctx context.Context, appPath, tracePath string, h harness.Harness, opts ...orchestrator.Option) (*traceSession, error) {
	if tracePath == "" {
		return nil, infraError("--trace is required")
	}

	ts := &traceSession{}

	// Take ownership of the harness immediately: register its Close first so
	// EVERY error path below (and the success path) tears it down via ts.Close.
	// The caller must NOT close h itself — on a returned error h is already
	// closed; on success ts.Close handles it.
	if h != nil {
		ts.closers = append(ts.closers, func() { _ = h.Close() })
	}

	// Ensure the trace directory exists.
	if dir := filepath.Dir(tracePath); dir != "" && dir != "." {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return nil, infraError("create trace dir: %v", mkErr)
		}
	}

	// Open (or create) the JSONL trace.
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		return nil, infraError("open trace %q: %v", tracePath, err)
	}
	ts.Sink = sink
	ts.closers = append(ts.closers, func() { _ = sink.Close() })

	// Resolve the story. With --app, load from disk. Without --app, reconstruct
	// the effective story FROM the trace itself — the trace is self-contained,
	// so a continued turn no longer depends on the story files still being on
	// disk or unchanged.
	var def *app.AppDef
	if appPath != "" {
		def, err = loadAppWithEnv(appPath)
		if err != nil {
			ts.Close()
			return nil, infraError("%v", err)
		}
	} else {
		history := sink.History()
		latestTurn := app.TurnNumber(0)
		for _, ev := range history {
			if ev.Turn > latestTurn {
				latestTurn = ev.Turn
			}
		}
		files, entry, atErr := store.StoryAtTurn(history, latestTurn)
		if atErr != nil {
			ts.Close()
			return nil, infraError("reconstruct story from trace: %v (provide --app if the trace predates story snapshots)", atErr)
		}
		var cleanup func()
		def, cleanup, err = app.LoadFromFiles(files, entry)
		if err != nil {
			ts.Close()
			return nil, infraError("load story from trace: %v", err)
		}
		ts.closers = append(ts.closers, cleanup)
	}
	ts.Def = def

	m, err := machine.New(def)
	if err != nil {
		ts.Close()
		return nil, infraError("build machine: %v", err)
	}

	// In-memory store for session/snapshot metadata. Event writes are
	// redirected to the JSONLSink via WithEventSink; the in-memory store handles
	// the session lifecycle and is never written to for events.
	s, err := store.OpenMemory()
	if err != nil {
		ts.Close()
		return nil, infraError("open in-memory store: %v", err)
	}
	ts.closers = append(ts.closers, func() { _ = s.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
		ts.Close()
		return nil, infraError("validate hosts: %v", err)
	}

	// Build the agent registry for external transports declared in
	// agent_plugins:. The routing harness h backs the agent.claude builtin.
	agentReg, agentRegErr := agent.BuildRegistryFromDef(def, h)
	if agentRegErr != nil {
		ts.Close()
		return nil, infraError("build agent registry: %v", agentRegErr)
	}
	ts.closers = append(ts.closers, func() { _ = agentReg.Close() })

	orchOpts := []orchestrator.Option{
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
		orchestrator.WithAgentRegistry(agentReg),
	}
	orchOpts = append(orchOpts, opts...)
	orch := orchestrator.New(def, m, s, h, orchOpts...)
	ts.Orch = orch

	// Create a session in the in-memory store (needed for orchestrator plumbing).
	sid, err := orch.NewSession(ctx)
	if err != nil {
		ts.Close()
		return nil, infraError("new session: %v", err)
	}
	ts.SID = sid

	// Record the effective story (base snapshot on a fresh trace; diff if the
	// on-disk story drifted from what the trace already carries).
	if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
		ts.Close()
		return nil, infraError("record effective story: %v", err)
	}

	return ts, nil
}

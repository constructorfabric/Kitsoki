package studio

// session_runtime.go — the trace-backed driving runtime behind a SessionHandle.
//
// A driving handle owns a live orchestrator wired to a JSONL trace plus a
// headless TUI model used purely as the slice-1 Frame composer — the same two
// pieces `kitsoki drive` assembles inline (cmd/kitsoki/drive.go,
// cmd/kitsoki/trace_session.go), reproduced here because those live in package
// main and the studio package cannot import them. The runtime is the studio's
// equivalent of setupTraceSession + newDriveModel: load the story, build a
// JSONL-backed orchestrator over the handle's harness, create a session, run
// the initial on_enter, and seed a RootModel so every drive/submit/continue
// folds its TurnOutcome through the one canonical ApplyTurnOutcome path before
// ComposeFrame paints the still.
//
// The interpretive seam is the harness the handle carries (replay by default,
// per shared decision 3). The runtime never builds a live harness itself — it
// drives whatever the HarnessBuilder produced — so the no-LLM default is owned
// upstream in handles.go and proven by the no-live-fallthrough test.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
	rsserver "kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/tui"
)

// registerExtraHostCaps is a test-only injection seam: when non-nil it is called
// with the freshly-built host registry for each driving runtime so a no-LLM test
// can register an extra host capability (the operator-ask probe) that forwards to
// the in-context OperatorPrompter. Production leaves it nil — a studio runtime
// uses only host.RegisterBuiltins.
var registerExtraHostCaps func(reg *host.Registry)

// mustSeedJSON marshals an initial_world seed payload (a {"set":{k:v}} map of
// JSON-serialisable values). A marshal failure is a programmer error (the values
// came from a decoded JSON tool arg), so it panics rather than silently dropping
// a seed.
func mustSeedJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("studio: seed initial world: marshal: " + err.Error())
	}
	return b
}

// noRouteHarness is a placeholder harness for a runtime that never routes free
// text — the spec-render path (render.tui/png/web on a {story_path, state}
// spec), which only teleports + re-renders. The agent registry constructor
// requires a non-nil harness even when no agent will ever fire, so this
// satisfies the contract while failing loudly if anything DID try to route.
// Mirrors cmd/kitsoki noRunHarness (package main, not importable here).
type noRouteHarness struct{}

func (noRouteHarness) RunTurn(context.Context, harness.TurnInput) (mcpsdk.CallToolParams, error) {
	return mcpsdk.CallToolParams{}, fmt.Errorf("studio: noRouteHarness.RunTurn called (a spec render must never route free text)")
}
func (noRouteHarness) Close() error { return nil }

// sessionRuntime is the live driving substrate for one SessionHandle: the wired
// orchestrator, its JSONL trace sink, the bound session id, and the headless
// frame-composer model. It is stored on the handle (SessionHandle.Runtime) and
// torn down by CloseSession. Not safe for concurrent turns on one handle — the
// MCP server processes one tool call at a time per connection, and a handle is
// single-writer by construction.
type sessionRuntime struct {
	def  *app.AppDef
	orch *orchestrator.Orchestrator
	sink *store.JSONLSink
	// tap records the latest trace event so status/inspect can report live
	// in-flight progress while an async drive runs (never-silent).
	tap   *activityTap
	sid   app.SessionID
	model tui.RootModel
	mu    sync.Mutex
	// modelTurn is the highest persisted turn folded into model. It prevents
	// read-only render refreshes from appending duplicate transcript entries.
	modelTurn app.TurnNumber

	// driver binds the orchestrator + sid to the runstatus Driver API so the
	// session tools call the exact same Turn/SubmitDirect/ContinueTurn seam the
	// web surface drives (internal/runstatus/server/driver.go). This is the
	// "OrchestratorDriver directly" path the proposal names.
	driver rsserver.Driver

	jobStore  *jobs.JobStore
	scheduler jobs.Scheduler
	chatStore *chats.Store

	// lastTurnErr is the orchestrator error from the most recent
	// drive/submit/continue (nil on success). turnResponse surfaces it as
	// outcome.error / mode="error" alongside the frame — a replay miss is a
	// turn-level failure the agent should see, not a transport error.
	lastTurnErr error
	// lastOperationDrive is populated when a normal drive/submit/continue
	// automatically follows a run_in_background operation.
	lastOperationDrive *orchestrator.OperationDriveOutcome

	// inFlight is the suspend broker for a drive that has not settled yet: either
	// parked on operator-ask (session.answer fallback) or still running after
	// session.drive returned a bounded-wait response. Single in-flight turn per
	// handle.
	inFlight *suspendBroker

	closers []func()
}

type runningDrive struct {
	input     string
	startedAt time.Time
}

type studioBackgroundObserver struct {
	rt  *sessionRuntime
	sid app.SessionID
}

func (o *studioBackgroundObserver) OnBackgroundTurn(sid app.SessionID, outcome *orchestrator.TurnOutcome) {
	if o == nil || o.rt == nil || sid != o.sid || outcome == nil {
		return
	}
	o.rt.mu.Lock()
	defer o.rt.mu.Unlock()
	o.rt.model = o.rt.model.ApplyTurnOutcome(outcome, "(background)", nil)
	o.rt.modelTurn = outcome.TurnNumber
}

// Close tears down the runtime in reverse construction order (LIFO), mirroring
// the defer order setupTraceSession uses. Idempotent: a second Close is a no-op.
func (rt *sessionRuntime) Close() {
	for i := len(rt.closers) - 1; i >= 0; i-- {
		rt.closers[i]()
	}
	rt.closers = nil
}

// newSessionRuntime builds the driving runtime for storyPath against the handle's
// harness h, writing the durable trace to tracePath. It is the studio twin of
// cmd/kitsoki setupTraceSession + newDriveModel:
//
//  1. open (or create) the JSONL trace;
//  2. load the story from disk;
//  3. build a JSONL-backed orchestrator over h;
//  4. create a session and record the effective story;
//  5. run the initial on_enter so the first frame matches a fresh TUI session;
//  6. seed the headless RootModel used as the Frame composer.
//
// h is owned by the runtime on success (Close tears it down) and on every error
// path (h is closed before returning), so the caller must NOT close it.
// profiles/selectedProfile seed orchestrator.WithHarnessProfiles so an
// MCP-driven session can route its agent dispatch through an operator-declared
// backend (synthetic, codex, …) instead of the static default — the same
// remap `kitsoki turn --profile` applies. An empty map leaves the session on the
// legacy default-backend path (selectedProfile is then ignored).
func newSessionRuntime(ctx context.Context, storyPath, tracePath string, h harness.Harness, profiles map[string]orchestrator.HarnessProfile, selectedProfile string, initialWorld map[string]any, hostCassette string, failClosedAgentReplay bool, resolver app.ImportResolver, chatStore *chats.Store, configureHosts HostRegistryConfigurer, agentLaunchPolicy host.AgentLaunchPolicy) (*sessionRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if storyPath == "" {
		if h != nil {
			_ = h.Close()
		}
		return nil, &openError{Code: ErrBadRequest, Msg: "session: story_path is required"}
	}
	if tracePath == "" {
		if h != nil {
			_ = h.Close()
		}
		return nil, &openError{Code: ErrBadRequest, Msg: "session: trace path is required"}
	}

	rt := &sessionRuntime{}

	// A nil harness means "this runtime never routes free text" (the spec-render
	// path). The agent registry still requires a non-nil harness, so substitute
	// a no-route placeholder that fails loudly if anything tries to route.
	if h == nil {
		h = noRouteHarness{}
	}

	// Own the harness immediately so EVERY error path below tears it down.
	rt.closers = append(rt.closers, func() { _ = h.Close() })

	// Ensure the trace directory exists, then open the JSONL trace.
	if dir := filepath.Dir(tracePath); dir != "" && dir != "." {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			rt.Close()
			return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: create trace dir: %v", mkErr)}
		}
	}
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: open trace %q: %v", tracePath, err)}
	}
	rt.sink = sink
	rt.tap = &activityTap{}
	rt.closers = append(rt.closers, func() { _ = sink.Close() })

	def, err := app.LoadWithResolver(storyPath, nil, resolver)
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: load story %q: %v", storyPath, err)}
	}
	rt.def = def

	// Publish KITSOKI_APP_DIR so host.starlark.run's env-only prompt-path
	// resolver joins story-relative `script:` paths against the story
	// directory instead of the studio server's cwd. Mirrors publishAppDir in
	// cmd/kitsoki/session.go and the loadfiles.go loader sequence. Set only
	// after a successful load so a failed load does not mutate the global.
	if absDir, aerr := filepath.Abs(filepath.Dir(storyPath)); aerr == nil {
		_ = os.Setenv(host.AppDirEnv, absDir)
	}

	m, err := machine.New(def)
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: build machine: %v", err)}
	}

	// In-memory store for session/snapshot metadata; event writes redirect to
	// the JSONL sink via WithEventSink (the in-memory store is never the event
	// authority). This mirrors setupTraceSession exactly.
	s, err := store.OpenMemory()
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: open in-memory store: %v", err)}
	}
	rt.closers = append(rt.closers, func() { _ = s.Close() })

	jobStore, err := jobs.NewJobStore(s.DB())
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: open job store: %v", err)}
	}
	rt.jobStore = jobStore
	rt.scheduler = jobs.NewScheduler(jobStore)
	rt.chatStore = chatStore

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
	// Test-only injection seam: a flow/cassette test registers an extra host
	// capability (e.g. one that forwards to the in-context OperatorPrompter) so a
	// no-LLM drive can exercise the operator-ask branch end-to-end without
	// dispatching a real claude -p sub-agent. Nil in production.
	if registerExtraHostCaps != nil {
		registerExtraHostCaps(hostReg)
	}
	cassetteHandlers := map[string]bool{}
	if hostCassette != "" {
		seen, err := applyStudioHostCassette(hostReg, hostCassette, tapSink{EventSink: sink, tap: rt.tap}, failClosedAgentReplay)
		if err != nil {
			rt.Close()
			return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: apply host cassette: %v", err)}
		}
		cassetteHandlers = seen
	}
	if failClosedAgentReplay {
		installReplayAgentMissHandlers(hostReg, cassetteHandlers)
	}
	if configureHosts != nil {
		if err := configureHosts(hostReg); err != nil {
			rt.Close()
			return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: configure host registry: %v", err)}
		}
	}
	if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: validate hosts: %v", err)}
	}

	agentReg, agentRegErr := agent.BuildRegistryFromDef(def, h)
	if agentRegErr != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: build agent registry: %v", agentRegErr)}
	}
	rt.closers = append(rt.closers, func() { _ = agentReg.Close() })

	orchOpts := []orchestrator.Option{
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(tapSink{EventSink: sink, tap: rt.tap}),
		orchestrator.WithEventSinkAuthority(true),
		orchestrator.WithAgentRegistry(agentReg),
		orchestrator.WithScheduler(rt.scheduler),
		orchestrator.WithJobStore(rt.jobStore),
	}
	if chatStore != nil {
		orchOpts = append(orchOpts,
			orchestrator.WithChatStore(chathost.NewAdapter(chatStore)),
			orchestrator.WithChatsConcrete(chatStore),
		)
	}
	// Honor the global semantic-routing toggle. When the env var is absent,
	// studio sessions use the same simple default as the CLI: exact
	// deterministic first, then the selected harness/model.
	if opt, ok := semanticRoutingEnvOption(); ok {
		orchOpts = append(orchOpts, opt)
	}
	// A non-empty profile map routes agent dispatch (host.agent.*) through the
	// declared backend; selectedProfile becomes the session's initial selection.
	// Empty leaves the legacy default-backend path untouched (the no-op contract
	// of WithHarnessProfiles), so replay/flow sessions are unaffected.
	if len(profiles) > 0 {
		orchOpts = append(orchOpts, orchestrator.WithHarnessProfiles(profiles, selectedProfile))
	}
	if agentLaunchPolicy.Enabled {
		orchOpts = append(orchOpts, orchestrator.WithAgentLaunchPolicy(agentLaunchPolicy))
	}
	orch := orchestrator.New(def, m, s, h, orchOpts...)
	rt.orch = orch

	sid, err := orch.NewSession(ctx)
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: new session: %v", err)}
	}
	rt.sid = sid
	rt.driver = rsserver.OrchestratorDriver{Orch: orch, SID: sid, Jobs: rt.jobStore, Chats: rt.chatStore, TraceHistory: rt.history}
	obs := &studioBackgroundObserver{rt: rt, sid: sid}
	orch.RegisterObserver(obs)
	rt.closers = append(rt.closers, func() { orch.UnregisterObserver(obs) })

	if err := orch.RecordEffectiveStory(ctx, sid); err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: record effective story: %v", err)}
	}

	// Seed initial_world (the studio twin of a flow fixture's initial_world:)
	// BEFORE the initial on_enter, so the first state observes the seeded vars —
	// e.g. a ticket seeded into the bugfix pipeline for a headless drive. The
	// JSONL sink is the event authority here (WithEventSinkAuthority), so the seed
	// EffectApplied events MUST go to the sink, not the in-memory store; only then
	// does LoadJourney replay them into the world. Undeclared keys are dropped by
	// the schema-bound world (the same contract as a flow's initial_world).
	if len(initialWorld) > 0 {
		var seedEvents []store.Event
		for k, v := range initialWorld {
			seedEvents = append(seedEvents, store.Event{
				Kind:    store.EffectApplied,
				Turn:    0,
				Payload: mustSeedJSON(map[string]any{"set": map[string]any{k: v}}),
			})
		}
		for _, ev := range seedEvents {
			if seedErr := sink.Append(ev); seedErr != nil {
				rt.Close()
				return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: seed initial world: %v", seedErr)}
			}
		}
	}

	// Run the initial state's on_enter chain so the first frame and the session
	// world match a fresh TUI session (drive does the same on a fresh trace).
	if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session: run initial on_enter: %v", err)}
	}

	model, err := newComposerModel(orch, sid, rt.jobStore, rt.chatStore, rt.history)
	if err != nil {
		rt.Close()
		return nil, &openError{Code: ErrBadRequest, Msg: err.Error()}
	}
	rt.model = model
	if j, jerr := orch.LoadJourney(sid); jerr == nil {
		rt.modelTurn = j.Turn
	}

	return rt, nil
}

func applyStudioHostCassette(hostReg *host.Registry, cassettePath string, sink store.EventSink, failClosedAgentReplay bool) (map[string]bool, error) {
	cas, err := testrunner.LoadCassette(cassettePath)
	if err != nil {
		return nil, fmt.Errorf("load host cassette: %w", err)
	}
	stateOf := func() string { return "" }
	seen := map[string]bool{}
	for _, ep := range cas.Episodes {
		hn, ok := ep.Match["handler"].(string)
		if !ok || hn == "" || seen[hn] {
			continue
		}
		seen[hn] = true
		fallback, _ := hostReg.Get(hn)
		if failClosedAgentReplay && strings.HasPrefix(hn, "host.agent.") {
			fallback = replayAgentMissHandler(hn)
		}
		hostReg.Replace(hn, testrunner.BuildCassetteDispatcherWithSink(cas, hn, stateOf, fallback, nil, clock.Real(), sink, nil))
	}
	return seen, nil
}

var replayAgentHostHandlers = []string{
	"host.agent.ask",
	"host.agent.extract",
	"host.agent.decide",
	"host.agent.task",
	"host.agent.converse",
	"host.agent.search",
}

func installReplayAgentMissHandlers(hostReg *host.Registry, cassetteHandlers map[string]bool) {
	for _, name := range replayAgentHostHandlers {
		if cassetteHandlers[name] {
			continue
		}
		hostReg.Replace(name, replayAgentMissHandler(name))
	}
}

func replayAgentMissHandler(name string) host.Handler {
	return func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{}, fmt.Errorf(
			"studio: harness:replay cannot dispatch %s without a matching host_cassette episode; replay misses fail closed instead of falling through to a live agent",
			name,
		)
	}
}

// newComposerModel seeds the headless TUI model used solely as the slice-1
// Frame composer (the studio twin of cmd/kitsoki newDriveModel). It loads the
// journey, renders the initial typed view, and constructs a RootModel with no
// app path (edit mode disabled) so a drive folds outcomes through the same
// ApplyTurnOutcome path the live TUI runs.
func newComposerModel(orch *orchestrator.Orchestrator, sid app.SessionID, jobStore *jobs.JobStore, chatStore *chats.Store, historyFn func() store.History) (tui.RootModel, error) {
	j, err := orch.LoadJourney(sid)
	if err != nil {
		return tui.RootModel{}, fmt.Errorf("session: load journey: %w", err)
	}
	initialView, typedView, env, rr, err := orch.InitialViewTyped(j.World)
	if err != nil {
		return tui.RootModel{}, fmt.Errorf("session: render initial view: %w", err)
	}
	var traceHistory func() (store.History, error)
	if historyFn != nil {
		traceHistory = func() (store.History, error) { return historyFn(), nil }
	}
	return tui.NewRootModel(orch, sid, "", initialView,
		tui.WithInitialTypedView(typedView, env, rr),
		tui.WithJobStore(jobStore),
		tui.WithChatStore(chatStore),
		tui.WithTraceHistory(traceHistory),
	), nil
}

// drive routes free text through the orchestrator turn loop (orch.Turn — the
// interpretive seam), folds the outcome into the composer model, and returns the
// outcome plus the recomposed Frame at the given geometry. The Frame is always
// returned, even on a turn error, so the caller can show the agent the screen
// alongside the structured failure.
func (rt *sessionRuntime) drive(ctx context.Context, input string, cols, rows int) (*orchestrator.TurnOutcome, tui.Frame) {
	out, err := rt.driver.Turn(ctx, input)
	drive, finalOut, finalErr := rt.driveBackgroundOperationAfterTurn(ctx, out, err)
	rt.lastTurnErr = finalErr
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.model = rt.model.ApplyTurnOutcome(out, input, err)
	if out != nil {
		rt.modelTurn = out.TurnNumber
	}
	if drive != nil && drive.Final != nil {
		rt.model = rt.model.ApplyTurnOutcome(drive.Final, "(background operation)", finalErr)
		rt.modelTurn = drive.Final.TurnNumber
	}
	rt.lastOperationDrive = drive
	if finalOut != nil {
		out = finalOut
	}
	return out, tui.ComposeFrame(&rt.model, cols, rows)
}

func (rt *sessionRuntime) driveBackgroundOperationAfterTurn(ctx context.Context, out *orchestrator.TurnOutcome, err error) (*orchestrator.OperationDriveOutcome, *orchestrator.TurnOutcome, error) {
	if err != nil || out == nil || out.Mode != orchestrator.ModeTransitioned {
		return nil, out, err
	}
	driver, ok := rt.driver.(rsserver.BackgroundOperationDriver)
	if !ok {
		return nil, out, nil
	}
	drive, err := driver.DriveBackgroundOperation(ctx)
	if err != nil {
		return nil, out, err
	}
	if drive == nil {
		return nil, out, nil
	}
	if drive.Final != nil {
		return drive, drive.Final, nil
	}
	current, err := rt.driver.View(ctx)
	if err != nil {
		return drive, out, err
	}
	return drive, current, nil
}

// slash routes a TUI slash command through the same RootModel dispatcher the
// live terminal uses, then returns the recomposed frame. Commands that produce a
// tea.Cmd are rejected because a headless MCP render cannot safely execute
// terminal side effects such as tmux attach.
func (rt *sessionRuntime) slash(command string, cols, rows int) (tui.Frame, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return tui.Frame{}, fmt.Errorf("session.command: command is required")
	}
	if !strings.HasPrefix(command, "/") {
		return tui.Frame{}, fmt.Errorf("session.command: command must start with /")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	next, cmd := rt.model.RunSlashCommand(command)
	if cmd != nil {
		return tui.Frame{}, fmt.Errorf("session.command: %q produced an async TUI command and cannot run headlessly", command)
	}
	rt.model = next
	return tui.ComposeFrame(&rt.model, cols, rows), nil
}

// driveElicit routes free text through the turn loop with an operator prompter
// installed that forwards forwarded sub-agent questions to the driving MCP
// client via MCP elicitation (the primary transport). The turn blocks
// synchronously — the elicitation is a nested server→client request mid-turn —
// so this is exactly drive() plus the WithOperatorPrompter / WithKitsokiSessionID
// context seam. sid is the studio session id used to tag the operator-ask trace
// events (operator.question.asked carries it).
func (rt *sessionRuntime) driveElicit(ctx context.Context, input string, cols, rows int, prompter host.OperatorPrompter) (*orchestrator.TurnOutcome, tui.Frame) {
	ctx = host.WithOperatorPrompter(ctx, prompter)
	ctx = host.WithKitsokiSessionID(ctx, string(rt.sid))
	return rt.drive(ctx, input, cols, rows)
}

// driveSuspendable runs a drive turn on a background goroutine with a suspend
// broker installed as the operator prompter (the session.answer fallback). It
// returns as soon as EITHER the turn completes (no operator-ask fired) or the
// turn parks on an operator-ask. On a park, rt.inFlight holds the live broker so
// a later resumeSuspendable (session.answer) can deliver the answer.
//
// turnDone reports whether the turn already finished; when false, pq is the
// parked question and the turn goroutine stays alive inside the prompter's Ask.
// The background ctx is the host-supplied ctx wrapped with the operator-ask
// timeout (inside the bridge), so a never-answered park falls through to the
// headless tool-error path on its own.
func (rt *sessionRuntime) driveSuspendable(ctx context.Context, input string, cols, rows int, wait time.Duration) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	return rt.turnSuspendable(ctx, input, cols, rows, wait, func(turnCtx context.Context) (*orchestrator.TurnOutcome, tui.Frame) {
		return rt.drive(turnCtx, input, cols, rows)
	})
}

func (rt *sessionRuntime) submitSuspendable(ctx context.Context, intent string, slots map[string]any, cols, rows int, wait time.Duration) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	label := "intent:" + intent
	return rt.turnSuspendable(ctx, label, cols, rows, wait, func(turnCtx context.Context) (*orchestrator.TurnOutcome, tui.Frame) {
		return rt.submit(turnCtx, intent, slots, cols, rows)
	})
}

func (rt *sessionRuntime) continueSuspendable(ctx context.Context, slots map[string]any, cols, rows int, wait time.Duration) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	return rt.turnSuspendable(ctx, "continue", cols, rows, wait, func(turnCtx context.Context) (*orchestrator.TurnOutcome, tui.Frame) {
		return rt.cont(turnCtx, slots, cols, rows)
	})
}

func (rt *sessionRuntime) turnSuspendable(ctx context.Context, input string, cols, rows int, wait time.Duration, run func(context.Context) (*orchestrator.TurnOutcome, tui.Frame)) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	return rt.turnSuspendableResult(ctx, input, cols, rows, wait, func(turnCtx context.Context) turnResult {
		out, frame := run(turnCtx)
		return turnResult{outcome: out, frame: frame, err: rt.lastTurnErr, operationDrive: rt.lastOperationDrive}
	})
}

func (rt *sessionRuntime) driveOperationSuspendable(ctx context.Context, cols, rows int, wait time.Duration) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	return rt.turnSuspendableResult(ctx, "operation", cols, rows, wait, func(turnCtx context.Context) turnResult {
		drive, out, frame := rt.driveOperation(turnCtx, cols, rows)
		return turnResult{outcome: out, frame: frame, err: rt.lastTurnErr, operationDrive: drive}
	})
}

func (rt *sessionRuntime) turnSuspendableResult(ctx context.Context, input string, cols, rows int, wait time.Duration, run func(context.Context) turnResult) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, err error) {
	rt.mu.Lock()
	if rt.inFlight != nil {
		rt.mu.Unlock()
		return turnResult{}, nil, false, nil, fmt.Errorf("a turn is already running or awaiting the operator; wait for it to finish or answer it with session.answer before driving again")
	}
	startedAt := time.Now()
	snap, snapErr := rt.driveSnapshot()
	if snapErr != nil {
		rt.mu.Unlock()
		return turnResult{}, nil, false, nil, snapErr
	}
	broker := newSuspendBroker(input, startedAt, snap)
	rt.inFlight = broker
	rt.mu.Unlock()
	prompter := newStudioOperatorPrompter(&suspendTransport{broker: broker})

	// The turn goroutine owns rt.model mutation; it runs to completion (possibly
	// across several park/answer cycles) before finish() unblocks a waiter, so no
	// handler touches rt.model while the goroutine is live. The turn is detached
	// from the MCP request only after it parks on an operator question; if the
	// request times out before any result/question, cancel the hidden turn rather
	// than leaving it to mutate the session later.
	turnBase, cancelTurn := context.WithCancel(context.WithoutCancel(ctx))
	turnCtx := host.WithOperatorPrompter(turnBase, prompter)
	turnCtx = host.WithKitsokiSessionID(turnCtx, string(rt.sid))
	go func() {
		broker.finish(run(turnCtx))
		rt.clearInFlightIf(broker)
	}()

	waitCtx := ctx
	var cancelWait context.CancelFunc
	if wait > 0 {
		waitCtx, cancelWait = context.WithTimeout(ctx, wait)
		defer cancelWait()
	}
	r, q, werr := broker.waitNext(waitCtx)
	if werr != nil {
		if wait > 0 && werr == context.DeadlineExceeded {
			return turnResult{}, nil, false, broker.snapshotRunning(), nil
		}
		cancelTurn()
		rt.clearInFlightIf(broker)
		// The drive ctx was cancelled before a result/question; cancel the turn
		// context too so a timed-out MCP call does not keep writing late trace/model
		// updates behind the client's back.
		return turnResult{}, nil, false, nil, werr
	}
	if q != nil {
		return turnResult{}, q, false, nil, nil
	}
	rt.clearInFlightIf(broker)
	return r, nil, true, nil, nil
}

func (rt *sessionRuntime) clearInFlightIf(broker *suspendBroker) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.inFlight == broker {
		rt.inFlight = nil
	}
}

func (rt *sessionRuntime) driveSnapshot() (driveSnapshot, error) {
	j, err := rt.orch.LoadJourney(rt.sid)
	if err != nil {
		return driveSnapshot{}, fmt.Errorf("session.drive: load pre-drive snapshot: %w", err)
	}
	allowed := rt.orch.AllowedIntents(j.State, j.World)
	allowedNames := visibleAllowedIntentNames(string(j.State), allowed)
	view, verr := rt.orch.RenderState(j.State, j.World)
	if verr != nil {
		view = fmt.Sprintf("<render error: %v>", verr)
	}
	return driveSnapshot{
		state:          string(j.State),
		world:          cloneAnyMap(j.World.Vars),
		allowedIntents: allowedNames,
		lastView:       view,
	}, nil
}

// resumeSuspendable delivers the operator's answer to a parked question and
// blocks until the turn completes or parks on the NEXT operator-ask. It is the
// runtime half of session.answer: deliver → waitNext → {outcome | awaiting}.
func (rt *sessionRuntime) resumeSuspendable(ctx context.Context, questionID string, answers map[string]any, wait time.Duration) (res turnResult, pq *pendingQuestion, turnDone bool, running *runningDrive, ok bool, err error) {
	rt.mu.Lock()
	broker := rt.inFlight
	rt.mu.Unlock()
	if broker == nil {
		return turnResult{}, nil, false, nil, false, nil
	}
	if !broker.answer(questionID, answers) {
		return turnResult{}, nil, false, nil, false, nil
	}
	waitCtx := ctx
	var cancelWait context.CancelFunc
	if wait > 0 {
		waitCtx, cancelWait = context.WithTimeout(ctx, wait)
		defer cancelWait()
	}
	r, q, werr := broker.waitNext(waitCtx)
	if werr != nil {
		if wait > 0 && werr == context.DeadlineExceeded {
			return turnResult{}, nil, false, broker.snapshotRunning(), true, nil
		}
		return turnResult{}, nil, false, nil, true, werr
	}
	if q != nil {
		return turnResult{}, q, false, nil, true, nil
	}
	rt.clearInFlightIf(broker)
	return r, nil, true, nil, true, nil
}

// submit applies a chosen intent + slots with no routing (SubmitDirect — the
// deterministic menu-pick path) and returns the outcome plus the recomposed
// Frame.
func (rt *sessionRuntime) submit(ctx context.Context, intent string, slots map[string]any, cols, rows int) (*orchestrator.TurnOutcome, tui.Frame) {
	out, err := rt.driver.SubmitDirect(ctx, intent, slots)
	drive, finalOut, finalErr := rt.driveBackgroundOperationAfterTurn(ctx, out, err)
	rt.lastTurnErr = finalErr
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.model = rt.model.ApplyTurnOutcome(out, "", err)
	if out != nil {
		rt.modelTurn = out.TurnNumber
	}
	if drive != nil && drive.Final != nil {
		rt.model = rt.model.ApplyTurnOutcome(drive.Final, "(background operation)", finalErr)
		rt.modelTurn = drive.Final.TurnNumber
	}
	rt.lastOperationDrive = drive
	if finalOut != nil {
		out = finalOut
	}
	return out, tui.ComposeFrame(&rt.model, cols, rows)
}

// driveOperation advances the active operation handle through the same
// OperationDriver seam used by the web/runstatus surface, then recomposes the
// current frame for MCP clients. When the driver finds no safe turn to submit,
// it returns a read-only view so callers still get the current screen.
func (rt *sessionRuntime) driveOperation(ctx context.Context, cols, rows int) (*orchestrator.OperationDriveOutcome, *orchestrator.TurnOutcome, tui.Frame) {
	var drive *orchestrator.OperationDriveOutcome
	var out *orchestrator.TurnOutcome
	var err error
	driver, ok := rt.driver.(rsserver.OperationDriver)
	if !ok {
		err = fmt.Errorf("session.drive_operation: operation driving unavailable")
	} else {
		drive, err = driver.DriveOperation(ctx)
		if err == nil {
			if drive != nil && drive.Final != nil {
				out = drive.Final
			} else {
				out, err = rt.driver.View(ctx)
			}
		}
	}
	rt.lastTurnErr = err
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if out != nil {
		rt.model = rt.model.ApplyTurnOutcome(out, "(operation drive)", err)
		rt.modelTurn = out.TurnNumber
	} else {
		rt.refreshFromJourneyLocked()
	}
	rt.lastOperationDrive = drive
	return drive, out, tui.ComposeFrame(&rt.model, cols, rows)
}

// cont supplies missing slots for a pending clarification (ContinueTurn) and
// returns the outcome plus the recomposed Frame.
func (rt *sessionRuntime) cont(ctx context.Context, slots map[string]any, cols, rows int) (*orchestrator.TurnOutcome, tui.Frame) {
	out, err := rt.driver.ContinueTurn(ctx, slots)
	drive, finalOut, finalErr := rt.driveBackgroundOperationAfterTurn(ctx, out, err)
	rt.lastTurnErr = finalErr
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.model = rt.model.ApplyTurnOutcome(out, "", err)
	if out != nil {
		rt.modelTurn = out.TurnNumber
	}
	if drive != nil && drive.Final != nil {
		rt.model = rt.model.ApplyTurnOutcome(drive.Final, "(background operation)", finalErr)
		rt.modelTurn = drive.Final.TurnNumber
	}
	rt.lastOperationDrive = drive
	if finalOut != nil {
		out = finalOut
	}
	return out, tui.ComposeFrame(&rt.model, cols, rows)
}

// teleport resolves a stored inbox notification to its target and jumps the
// session there, mirroring the TUI's action-required banner and /jump path.
func (rt *sessionRuntime) teleport(ctx context.Context, notificationID string, cols, rows int) (*orchestrator.TurnOutcome, tui.Frame) {
	var out *orchestrator.TurnOutcome
	var err error
	if rt.jobStore == nil {
		err = fmt.Errorf("session.teleport: no job store configured")
	} else {
		n, nerr := rt.jobStore.GetNotification(ctx, notificationID)
		if nerr != nil {
			err = fmt.Errorf("session.teleport: resolve notification %q: %w", notificationID, nerr)
		} else if n == nil {
			err = fmt.Errorf("session.teleport: unknown notification %q", notificationID)
		} else {
			target := inbox.FromNotification(*n)
			if target.State == "" {
				err = fmt.Errorf("session.teleport: notification %q has no teleport target", notificationID)
			} else {
				out, err = rt.orch.Teleport(ctx, rt.sid, target)
				if err == nil {
					if markErr := rt.jobStore.MarkNotificationRead(ctx, notificationID); markErr != nil {
						err = fmt.Errorf("session.teleport: mark notification %q read: %w", notificationID, markErr)
					}
				}
			}
		}
	}
	rt.lastTurnErr = err
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.model = rt.model.ApplyTurnOutcome(out, "(teleport)", err)
	if out != nil {
		rt.modelTurn = out.TurnNumber
	}
	return out, tui.ComposeFrame(&rt.model, cols, rows)
}

// frame recomposes the current still WITHOUT advancing the machine — the
// read-only re-render render.tui/render.tui_png use on a handle. It reads the
// composer model as-is (the last settled paint), so "look at this" can never
// mutate state, world, or the trace (principle of least surprise).
func (rt *sessionRuntime) frame(cols, rows int) tui.Frame {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.refreshFromJourneyLocked()
	return tui.ComposeFrame(&rt.model, cols, rows)
}

// worldVars returns the handle's current flat world map (the same j.World.Vars
// session.inspect exposes), for the targeted session.world read. It loads the
// journey fresh so it reflects the last settled turn; it never mutates anything.
func (rt *sessionRuntime) worldVars() (map[string]any, error) {
	if rt.orch == nil {
		return nil, fmt.Errorf("session.world: runtime has no orchestrator")
	}
	rt.mu.Lock()
	broker := rt.inFlight
	rt.mu.Unlock()
	if broker != nil {
		if snap, ok := broker.snapshotState(); ok {
			return snap.world, nil
		}
	}
	j, err := rt.orch.LoadJourney(rt.sid)
	if err != nil {
		return nil, fmt.Errorf("session.world: load journey: %w", err)
	}
	return j.World.Vars, nil
}

func (rt *sessionRuntime) refreshFromJourneyLocked() {
	if rt.orch == nil {
		return
	}
	j, err := rt.orch.LoadJourney(rt.sid)
	if err != nil {
		return
	}
	if j.Turn <= rt.modelTurn {
		return
	}
	allowed := rt.orch.AllowedIntents(j.State, j.World)
	allowedNames := visibleAllowedIntentNames(string(j.State), allowed)
	view, err := rt.orch.RenderState(j.State, j.World)
	if err != nil {
		return
	}
	rt.model = rt.model.ApplyTurnOutcome(&orchestrator.TurnOutcome{
		Mode:           orchestrator.ModeTransitioned,
		View:           view,
		NewState:       j.State,
		AllowedIntents: allowedNames,
		TurnNumber:     j.Turn,
	}, "(refresh)", nil)
	rt.modelTurn = j.Turn
}

// history returns the JSONL trace events recorded so far, in append order, for
// session.trace. It reads the live sink (the same events `kitsoki turn --trace`
// writes); it never mutates anything.
func (rt *sessionRuntime) history() store.History {
	if rt.sink == nil {
		return nil
	}
	return rt.sink.History()
}

// releaseWorktreeOwners clears any .kitsoki-owner sentinel that still names
// this session. A session may have created a worktree through host.git_worktree
// during its lifetime; session.close must release that ownership marker so a
// later session can re-use the ticket-local checkout after the owner exits.
// This is best-effort and deliberately narrow: only sentinels that match the
// closing session id are removed.
func (rt *sessionRuntime) releaseWorktreeOwners() {
	if rt == nil || rt.orch == nil {
		return
	}
	vars, err := rt.worldVars()
	if err != nil {
		vars = nil
	}
	hist := rt.history()
	for i := len(hist) - 1; i >= 0; i-- {
		ev := hist[i]
		if ev.Kind != store.HostReturned || len(ev.Payload) == 0 {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		if fmt.Sprint(payload["namespace"]) != "host.git_worktree" {
			continue
		}
		path := extractWorktreePath(payload)
		if path == "" {
			continue
		}
		if clearWorktreeOwner(path, worktreeOwnerIDs(vars, string(rt.sid))...) {
			return
		}
	}
	if vars == nil {
		return
	}

	var candidates []string
	if repo, _ := vars["repo"].(string); strings.TrimSpace(repo) != "" {
		if workspaceID, _ := vars["workspace_id"].(string); strings.TrimSpace(workspaceID) != "" {
			candidates = append(candidates, filepath.Join(repo, ".worktrees", workspaceID))
		}
	}
	if worktreePath, _ := vars["worktree_path"].(string); strings.TrimSpace(worktreePath) != "" {
		candidates = append(candidates, worktreePath)
	}

	for _, candidate := range candidates {
		if clearWorktreeOwner(candidate, worktreeOwnerIDs(vars, string(rt.sid))...) {
			return
		}
	}
}

func extractWorktreePath(payload map[string]any) string {
	if data, ok := payload["data"].(map[string]any); ok {
		if path, _ := data["path"].(string); strings.TrimSpace(path) != "" {
			return path
		}
		if path, _ := data["worktree_path"].(string); strings.TrimSpace(path) != "" {
			return path
		}
	}
	if path, _ := payload["path"].(string); strings.TrimSpace(path) != "" {
		return path
	}
	if path, _ := payload["worktree_path"].(string); strings.TrimSpace(path) != "" {
		return path
	}
	return ""
}

func worktreeOwnerIDs(vars map[string]any, fallback string) []string {
	seen := map[string]bool{}
	var ids []string
	if vars != nil {
		if sid, _ := vars["session_id"].(string); strings.TrimSpace(sid) != "" {
			ids = append(ids, strings.TrimSpace(sid))
			seen[strings.TrimSpace(sid)] = true
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" && !seen[fallback] {
		ids = append(ids, fallback)
	}
	return ids
}

func clearWorktreeOwner(path string, ownerIDs ...string) bool {
	path = strings.TrimSpace(path)
	if path == "" || len(ownerIDs) == 0 {
		return false
	}
	ownerPath := filepath.Join(path, ".kitsoki-owner")
	b, err := os.ReadFile(ownerPath)
	if err != nil {
		return false
	}
	owner := strings.TrimSpace(string(b))
	for _, sid := range ownerIDs {
		if strings.TrimSpace(sid) != "" && owner == strings.TrimSpace(sid) {
			if err := os.Remove(ownerPath); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

// AppDef implements runstatus/server.Source for the browser surface used by
// render.web.
func (rt *sessionRuntime) AppDef() *app.AppDef { return rt.def }

// Events implements runstatus/server.Source without rendering the diagram.
func (rt *sessionRuntime) Events() ([]runstatus.TraceEvent, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sink == nil {
		return nil, nil
	}
	h := rt.sink.History()
	evs := make([]runstatus.TraceEvent, len(h))
	for i := range h {
		evs[i] = runstatus.ToTraceEvent(h[i])
	}
	runstatus.AggregateTaskDetails(evs)
	return evs, nil
}

// Snapshot implements runstatus/server.Source for the live browser view. It
// reads the same JSONL sink the studio runtime writes, under rt.mu because the
// sink is not concurrency-safe.
func (rt *sessionRuntime) Snapshot() (runstatus.Snapshot, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sink == nil {
		return runstatus.Snapshot{}, nil
	}
	snap, err := runstatus.FromSink(rt.sink, rt.def, string(rt.sid))
	if err != nil {
		return snap, err
	}
	if snap.Session.CurrentState == "" && rt.def != nil {
		snap.Session.CurrentState = string(app.Compile(rt.def).InitialState())
	}
	return snap, nil
}

var _ rsserver.Source = (*sessionRuntime)(nil)

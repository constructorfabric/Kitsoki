package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/agent"
	agentserver "kitsoki/internal/agent/server"
	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	embedstore "kitsoki/internal/embed"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/ide"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/mining"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// sessionRuntime bundles the orchestrator and the resources it owns for one
// session-bearing subcommand (`kitsoki run`, `kitsoki web`). It is the
// CONSTRUCTION result only: opening the session, wiring an event sink, running
// on_enter, and driving the TUI / HTTP surface stay with each caller, which
// have divergent lifecycles (resume picker + tea.Program for run; live HTTP
// server for web).
//
// Close releases everything the runtime opened, in reverse order. Callers defer
// it after a successful build.
type sessionRuntime struct {
	Def         *app.AppDef
	Orch        *orchestrator.Orchestrator
	Store       store.Store
	Journal     journal.Writer
	JournalRead journal.Reader
	JobStore    *jobs.JobStore
	Scheduler   jobs.Scheduler
	ChatStore   *chats.Store
	Machine     machine.Machine
	Harness     harness.Harness
	AgentReg    *agent.Registry
	Logger      *slog.Logger

	// DeferredAgentSink is non-nil when the runtime was built with a host
	// cassette (--host-cassette). Callers must call SetSink on it after wiring
	// the session's event sink so cassette agent events flow to the trace.
	DeferredAgentSink *store.DeferredSink

	closers []func()
}

// Close releases all resources the runtime opened, in reverse order of opening.
func (rt *sessionRuntime) Close() {
	for i := len(rt.closers) - 1; i >= 0; i-- {
		rt.closers[i]()
	}
}

// applyHostCassette loads a host cassette and replaces each handler it mentions
// (one episode group per `match.handler`) with a cassette-backed dispatcher in
// hostReg. It is shared by both postures: the nil-harness flow posture
// (relativeTo = the flow file's dir) and the live-harness posture (relativeTo =
// "" → resolve against cwd). The cassette's agent: blocks flow to the session
// trace via rt.DeferredAgentSink, which the caller forwards once the live sink
// is wired (registry.go calls SetSink right after orch.SetEventSink).
func applyHostCassette(rt *sessionRuntime, hostReg *host.Registry, cassettePath, relativeTo string) error {
	if !filepath.IsAbs(cassettePath) && relativeTo != "" {
		cassettePath = filepath.Join(filepath.Dir(relativeTo), cassettePath)
	}
	cas, err := testrunner.LoadCassette(cassettePath)
	if err != nil {
		return fmt.Errorf("load host cassette: %w", err)
	}
	// stateOf is unused for replay-only dispatch (no record sink); the cassette
	// matcher keys on handler + args only.
	stateOf := func() string { return "" }
	deferredSink := store.NewDeferredSink()
	rt.DeferredAgentSink = deferredSink
	seen := map[string]bool{}
	for _, ep := range cas.Episodes {
		hn, ok := ep.Match["handler"].(string)
		if !ok || hn == "" || seen[hn] {
			continue
		}
		seen[hn] = true
		fallback, _ := hostReg.Get(hn)
		disp := testrunner.BuildCassetteDispatcherWithSink(cas, hn, stateOf, fallback, nil, clock.Real(), deferredSink, nil)
		hostReg.ReplacePreservingCapabilities(hn, disp)
	}
	return nil
}

// runtimeConfig selects which construction posture buildSessionRuntime takes.
// The three postures are mutually exclusive in their agent/host wiring:
//
//   - Flow != nil: deterministic flow-driven posture. Host stubs from the flow
//     fixture (and/or a host cassette) back the session; the harness is nil
//     (intents are submitted explicitly, no LLM); no agent plugin registry.
//   - Flow == nil: live posture. A real harness is built (buildHarness), the
//     agent plugin registry is built from the app def, and host builtins are
//     registered + allow-list validated.
type runtimeConfig struct {
	// AppPath is the resolved path to the app.yaml. Required.
	AppPath string
	// Def is the pre-loaded app definition. Required (callers load it first so
	// they can inspect it / report errors before construction).
	Def *app.AppDef
	// DBPath is the SQLite session store path. Required (caller resolves the
	// default).
	DBPath string
	// ExecMode is the resolved execution mode.
	ExecMode orchestrator.ExecutionMode

	// Live-posture fields (ignored when Flow != nil).
	HarnessType   string
	ClaudeModel   string
	RecordingPath string
	RecordPath    string
	PromptOverlay string
	// AgentBackend selects the coding-agent CLI backend ("" / "claude" |
	// "copilot") for host.agent.* calls. Empty keeps the default (claude).
	AgentBackend string

	// HarnessProfiles / DefaultProfile carry the operator-declared harness
	// profiles (from .kitsoki.yaml) into the orchestrator so a live session can
	// switch backend/model/env via /provider /model or the web picker. Empty
	// leaves the static AgentBackend path untouched.
	HarnessProfiles map[string]orchestrator.HarnessProfile
	DefaultProfile  string

	// HarnessLadder carries the operator-declared `.kitsoki.yaml`
	// `harness_ladder:` block (automatic multi-provider fallback +
	// effort/model escalation for every host.agent.decide / host.agent.task
	// dispatch — see internal/host/ladder.go and
	// internal/webconfig.HarnessLadder). Zero value means the live runtime uses
	// host.DefaultLadderConfig; flow/cassette postures stay single-attempt
	// unless they explicitly install a ladder.
	HarnessLadder host.LadderConfig

	// AgentLaunchPolicy is the machine-local preflight guard for external
	// host.agent.* launches. Zero value means disabled.
	AgentLaunchPolicy host.AgentLaunchPolicy

	// WantRoomEnterSink allocates a TUI room-enter sink and wires it into the
	// orchestrator. `kitsoki run` sets this; `kitsoki web` does not.
	RoomEnterSink orchestrator.RoomEnterSink

	// Reloader, when non-nil, is injected as the orchestrator's reload closure
	// (WithReloader) so /reload re-fetches the def from this closure instead of
	// re-reading AppPath. `kitsoki run` with no path arg (a config-synthesized
	// implicit root) sets this to re-synthesize from .kitsoki.yaml; a
	// file-backed run leaves it nil and reload re-reads AppPath as before.
	Reloader func() (*app.AppDef, error)

	// Mining carries the resolved .kitsoki.yaml `mining:` block. When
	// Mining.Enabled is true (and a real harness backs the one agent pass), the
	// runtime builds an ambient session miner and injects it via
	// orchestrator.SetMiner. Default-zero (no block / enabled:false) ⇒ no miner is
	// built — the path every flow/test fixture takes, so no fixture spends LLM.
	Mining webconfig.MiningConfig
	// MiningRepoPath is the repo path the miner resolves transcripts for. Empty ⇒
	// the process working dir at Start time.
	MiningRepoPath string

	// ConnectIDEFromEnv enables auto-connecting an IDE link from
	// CLAUDE_CODE_SSE_PORT during construction. `kitsoki web` sets this (the
	// embedding VS Code extension advertises its MCP server that way); the TUI
	// `run` path leaves it false and manages its link explicitly via /ide.
	ConnectIDEFromEnv bool

	// Flow-posture fields.
	Flow         *testrunner.FlowFixture
	FlowFilePath string

	// HostCassette layers a host cassette over the live-harness posture (see
	// runtimeBase.HostCassette). Distinct from Flow.HostCassette, which only
	// applies in the nil-harness flow posture.
	HostCassette string
}

// runtimeBase carries the session-INVARIANT construction posture that
// `kitsoki web` resolves once at startup and every session the SessionRegistry
// spins up then inherits. It is the subset of runtimeConfig that does not
// depend on which story a session runs: the store path, execution mode, harness
// selection / recording knobs, and — critically — the deterministic NO-LLM
// posture (Flow / FlowFilePath, whose fixture may carry a HostCassette).
//
// The per-story fields (AppPath, Def) are filled in per session by config, so a
// single base produces one nil-harness, cassette/stub-backed sessionRuntime per
// session exactly as web.go's single-session path did before the registry
// existed.
type runtimeBase struct {
	DBPath   string
	ExecMode orchestrator.ExecutionMode

	HarnessType   string
	ClaudeModel   string
	RecordingPath string
	RecordPath    string
	AgentBackend  string

	// HarnessProfiles / DefaultProfile are resolved once at web startup from
	// .kitsoki.yaml and inherited by every session the registry spins up.
	HarnessProfiles map[string]orchestrator.HarnessProfile
	DefaultProfile  string
	// HarnessLadder mirrors runtimeConfig.HarnessLadder, inherited by every
	// session the registry spins up.
	HarnessLadder     host.LadderConfig
	AgentLaunchPolicy host.AgentLaunchPolicy

	// Mining is the resolved .kitsoki.yaml `mining:` block, inherited by every
	// session. Default-zero (no block / enabled:false) ⇒ no miner — every flow
	// posture leaves it zero, so no flow session ever spends LLM.
	Mining webconfig.MiningConfig

	// Flow / FlowFilePath select the deterministic flow-driven posture for the
	// whole server: when Flow != nil every session is built with a nil harness
	// and host stubs / cassette episodes (Flow.HostCassette) back its host.*
	// calls. A Playwright demo drives the live web UI with no LLM through this.
	Flow         *testrunner.FlowFixture
	FlowFilePath string

	// SeedFixture supplies ONLY initial_state / initial_world to seed a freshly
	// created session, WITHOUT forcing the nil-harness flow posture. It exists
	// for the live-harness replay posture (--harness replay --recording, where
	// free-text routing stays live but specific host.* calls and the start state
	// must be deterministic): the recording routes free text, a --host-cassette
	// backs host.* calls, and this fixture teleports the session onto the
	// mid-graph starting state (e.g. core.prd_published) the tour begins at.
	// Its host_handlers are ignored — the host cassette owns host.* responses.
	// Mutually exclusive with Flow in practice (Flow already seeds itself).
	SeedFixture *testrunner.FlowFixture

	// HostCassette layers a host cassette over the LIVE (harness) posture: when
	// a real harness is built (e.g. --harness replay drives free-text routing
	// deterministically) but specific host.* calls must still be stubbed (the
	// off-ramp's host.agent.converse), this path's episodes replace those
	// handlers in the real host registry. It is the replay-harness analogue of
	// Flow.HostCassette (which only applies in the nil-harness flow posture).
	// Empty = no cassette layered. Resolved relative to cwd.
	HostCassette string

	// DefaultActor is the operator identity the web server records as
	// slots.author on a browser-driven turn when no header / actor param
	// supplies one (see server.WithDefaultActor). Empty = none configured;
	// the registry fails a session start fast if a story enforces an author
	// ACL guard but no identity is configured.
	DefaultActor string

	// ConnectIDEFromEnv is threaded into each session's runtimeConfig so the
	// web posture auto-connects an IDE link from CLAUDE_CODE_SSE_PORT (the
	// embedding VS Code extension). The TUI run path leaves it false.
	ConnectIDEFromEnv bool
}

// config materialises a per-session runtimeConfig for the story at storyPath
// with the loaded def, inheriting every session-invariant field from the base.
// The deterministic posture (Flow / FlowFilePath) is threaded through so the
// produced sessionRuntime is nil-harness, cassette/stub-backed when the base
// carries a fixture — the same construction web.go performs today.
func (b runtimeBase) config(storyPath string, def *app.AppDef) runtimeConfig {
	return runtimeConfig{
		AppPath:           storyPath,
		Def:               def,
		DBPath:            b.DBPath,
		ExecMode:          b.ExecMode,
		HarnessType:       b.HarnessType,
		ClaudeModel:       b.ClaudeModel,
		RecordingPath:     b.RecordingPath,
		RecordPath:        b.RecordPath,
		AgentBackend:      b.AgentBackend,
		HarnessProfiles:   b.HarnessProfiles,
		DefaultProfile:    b.DefaultProfile,
		HarnessLadder:     b.HarnessLadder,
		AgentLaunchPolicy: b.AgentLaunchPolicy,
		Flow:              b.Flow,
		FlowFilePath:      b.FlowFilePath,
		HostCassette:      b.HostCassette,
		Mining:            b.Mining,
		MiningRepoPath:    filepath.Dir(storyPath),
		ConnectIDEFromEnv: b.ConnectIDEFromEnv,
	}
}

// buildSessionRuntime performs the orchestrator CONSTRUCTION shared by
// `kitsoki run` and `kitsoki web`: open the store, build journal reader/writer,
// job store + scheduler, chat store, machine, host registry (per posture),
// agent registry (live posture only), and the orchestrator itself — then run
// ValidatePromptExtensions. It does NOT create a session, set an event sink,
// run on_enter, or start any UI; those belong to the caller.
func buildSessionRuntime(cfg runtimeConfig) (*sessionRuntime, error) {
	if cfg.Def == nil {
		return nil, fmt.Errorf("buildSessionRuntime: nil app def")
	}
	def := cfg.Def
	logger := slog.Default()

	rt := &sessionRuntime{Def: def, Logger: logger}
	// On any error after a resource opens, release what we opened so far.
	ok := false
	defer func() {
		if !ok {
			rt.Close()
		}
	}()

	s, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	rt.Store = s
	rt.closers = append(rt.closers, func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	if err != nil {
		return nil, fmt.Errorf("open journal writer: %w", err)
	}
	rt.Journal = jw

	jr, err := journal.NewSQLiteReader(s.DB())
	if err != nil {
		return nil, fmt.Errorf("open journal reader: %w", err)
	}
	rt.JournalRead = jr

	jobStore, err := jobs.NewJobStore(s.DB(), jobs.WithJobJournalWriter(jw))
	if err != nil {
		return nil, fmt.Errorf("open job store: %w", err)
	}
	rt.JobStore = jobStore
	rt.Scheduler = jobs.NewScheduler(jobStore)

	rawChatStore, err := chats.NewStore(s.DB(), chats.WithJournalWriter(jw))
	if err != nil {
		return nil, fmt.Errorf("open chat store: %w", err)
	}
	rt.ChatStore = rawChatStore
	chatStoreAdapter := chathost.NewAdapter(rawChatStore)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}
	rt.Machine = m

	// ── Host registry + harness + agent, per posture ──────────────────────
	var (
		h              harness.Harness
		agentPluginReg *agent.Registry
		hostReg        *host.Registry
	)

	if cfg.Flow != nil {
		// Deterministic flow posture: stub host handlers, no harness, no
		// agent plugins. Mirrors testrunner.buildOrchestratorRig's stub
		// wiring (host.RegisterBuiltins + RegisterHostStubs), simplified — the
		// web surface never records, so no record-mode cassette wiring.
		hostReg = host.NewRegistry()
		host.RegisterBuiltins(hostReg)
		host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
		testrunner.RegisterHostStubs(hostReg, cfg.Flow.HostHandlers)

		if cfg.Flow.HostCassette != "" {
			if err := applyHostCassette(rt, hostReg, cfg.Flow.HostCassette, cfg.FlowFilePath); err != nil {
				return nil, err
			}
		}

		// Starlark HTTP cassette: unlike HostCassette (which replaces a whole
		// handler with a canned Result), this lets the REAL host.starlark.run
		// handler run — reading the script + sidecar from disk — and only
		// replays the ctx.http.* calls the script makes from the cassette. We
		// wrap the registered handler so each invocation runs against an
		// injected replay client; no socket is ever opened. Mirrors
		// testrunner.buildOrchestratorRig (replay-only — the web surface never
		// records).
		if cfg.Flow.StarlarkHTTPCassette != "" {
			cassettePath := cfg.Flow.StarlarkHTTPCassette
			if !filepath.IsAbs(cassettePath) && cfg.FlowFilePath != "" {
				cassettePath = filepath.Join(filepath.Dir(cfg.FlowFilePath), cassettePath)
			}
			raw, rerr := os.ReadFile(cassettePath)
			if rerr != nil {
				return nil, fmt.Errorf("read starlark http cassette: %w", rerr)
			}
			var cas starlarkhost.HTTPCassette
			if uerr := goyaml.Unmarshal(raw, &cas); uerr != nil {
				return nil, fmt.Errorf("parse starlark http cassette %q: %w", cassettePath, uerr)
			}
			httpClient := starlarkhost.NewReplayClient(&cas)
			if _, ok := hostReg.Get("host.starlark.run"); !ok {
				hostReg.Register("host.starlark.run", host.StarlarkRunHandler)
			}
			real, _ := hostReg.Get("host.starlark.run")
			hostReg.Replace("host.starlark.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
				return real(starlarkhost.WithHTTP(ctx, httpClient), args)
			})
		}

		// Starlark inspect cassette: the fs/probe sibling of the HTTP cassette
		// above. Unlike a host.starlark.run HostCassette stub (which replaces
		// the whole call with a canned Result), this lets the REAL handler run
		// a story's script for real — only its ctx.fs.*/ctx.probe I/O is served
		// from disk instead of touching the filesystem. Mirrors
		// testrunner.buildOrchestratorRig's identical wiring
		// (internal/testrunner/flows.go) so a flow fixture written for `test
		// flows` (e.g. stories/deliver's decompose_happy / slidey_decomposition
		// / dev-story's design_to_decompose_to_impl, which fake ctx.fs.read of a
		// decomposition manifest the stubbed decomposer agent never actually
		// wrote to disk) drives identically live under `kitsoki web --flow`.
		if cfg.Flow.StarlarkInspectCassette != "" {
			cassettePath := cfg.Flow.StarlarkInspectCassette
			if !filepath.IsAbs(cassettePath) && cfg.FlowFilePath != "" {
				cassettePath = filepath.Join(filepath.Dir(cfg.FlowFilePath), cassettePath)
			}
			raw, rerr := os.ReadFile(cassettePath)
			if rerr != nil {
				return nil, fmt.Errorf("read starlark inspect cassette: %w", rerr)
			}
			var cas starlarkhost.InspectCassette
			if uerr := goyaml.Unmarshal(raw, &cas); uerr != nil {
				return nil, fmt.Errorf("parse starlark inspect cassette %q: %w", cassettePath, uerr)
			}
			inspector := starlarkhost.NewReplayInspector(&cas)
			if _, ok := hostReg.Get("host.starlark.run"); !ok {
				hostReg.Register("host.starlark.run", host.StarlarkRunHandler)
			}
			real2, _ := hostReg.Get("host.starlark.run")
			hostReg.Replace("host.starlark.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
				return real2(starlarkhost.WithInspector(ctx, inspector), args)
			})
		}
		// Harness stays nil: flow intents are submitted explicitly via the
		// write RPCs; the orchestrator's RunIntent path never calls the
		// harness.
	} else {
		hostReg = host.NewRegistry()
		host.RegisterBuiltins(hostReg)
		host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
		// Layer a host cassette over the live-harness posture when requested
		// (e.g. --harness replay for free-text routing + --host-cassette for the
		// off-ramp's host.agent.converse). The cassette's episodes replace the
		// matching builtin handlers so those host.* calls are deterministic while
		// the real harness still drives intent routing. Applied BEFORE the
		// allow-list check so a stubbed handler is still a registered host.
		if cfg.HostCassette != "" {
			if err := applyHostCassette(rt, hostReg, cfg.HostCassette, ""); err != nil {
				return nil, err
			}
		}
		if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
			return nil, fmt.Errorf("validate hosts: %w", err)
		}

		if len(cfg.HarnessProfiles) > 0 {
			h = newSelectableRoutingHarness(cfg.HarnessType, cfg.ClaudeModel, cfg.AgentBackend, cfg.RecordingPath, cfg.RecordPath, def, cfg.HarnessProfiles, cfg.DefaultProfile, nil)
		} else {
			h, err = buildHarness(cfg.HarnessType, cfg.ClaudeModel, cfg.AgentBackend, cfg.RecordingPath, cfg.RecordPath, def)
			if err != nil {
				return nil, fmt.Errorf("build harness: %w", err)
			}
		}
		rt.Harness = h
		setHarnessLogger(h, logger)
		rt.closers = append(rt.closers, func() { _ = h.Close() })

		agentPluginReg, err = agent.BuildRegistryFromDef(def, h)
		if err != nil {
			return nil, fmt.Errorf("build agent registry: %w", err)
		}
		rt.AgentReg = agentPluginReg
		rt.closers = append(rt.closers, func() { _ = agentPluginReg.Close() })

		// Wire host.agent.search when app.routing.embedding is configured.
		// Supports endpoint mode (Endpoint set) and managed mode (Model set,
		// no Endpoint). In managed mode the sidecar is started with
		// --embeddings --pooling mean so it serves /v1/embeddings, not chat.
		if ec := routingEmbedConfig(def); ec != nil && (ec.Endpoint != "" || ec.Model != "") {
			embedder, embedClose, embedErr := buildEmbedder(ec)
			if embedErr != nil {
				return nil, fmt.Errorf("build embedder: %w", embedErr)
			}
			rt.closers = append(rt.closers, embedClose)
			cacheDir := ec.CacheDir
			if cacheDir == "" {
				cacheDir = ".kitsoki-embed-cache"
			}
			store := embedstore.NewStore(cacheDir)
			hostReg.Replace("host.agent.search",
				host.NewAgentSearchHandler(embedModel(ec), filepath.Dir(cfg.AppPath), embedder, store))
			slog.Info("host.agent.search: wired", "endpoint", ec.Endpoint, "model", embedModel(ec))
		}
	}

	// Agents registry (builtins + AppDef overrides), installed process-wide so
	// handlers honouring `agent:` resolve names. Same in both postures.
	metaAgentReg, err := agents.BuildRegistry(def.AgentSpecs())
	if err != nil {
		return nil, fmt.Errorf("build agents registry: %w", err)
	}
	host.SetAgentRegistry(metaAgentReg)

	// ── Orchestrator options ────────────────────────────────────────────────
	runOpts := []orchestrator.Option{
		orchestrator.WithLogger(logger),
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithScheduler(rt.Scheduler),
		orchestrator.WithJobStore(jobStore),
		orchestrator.WithChatStore(chatStoreAdapter),
		orchestrator.WithChatsConcrete(rawChatStore),
		orchestrator.WithJournalWriter(jw),
		orchestrator.WithJournalReader(jr),
		orchestrator.WithExecutionMode(cfg.ExecMode),
	}
	runOpts = append(runOpts, semanticRoutingOptions()...)
	if agentPluginReg != nil {
		runOpts = append(runOpts, orchestrator.WithAgentRegistry(agentPluginReg))
	}
	if cfg.RoomEnterSink != nil {
		runOpts = append(runOpts, orchestrator.WithRoomEnterSink(cfg.RoomEnterSink))
	}
	if cfg.Reloader != nil {
		runOpts = append(runOpts, orchestrator.WithReloader(cfg.Reloader))
	}
	if cfg.PromptOverlay != "" {
		runOpts = append(runOpts, orchestrator.WithPromptOverlay(cfg.PromptOverlay))
	}
	if cfg.AgentBackend != "" {
		runOpts = append(runOpts, orchestrator.WithAgentBackendName(cfg.AgentBackend))
	}
	if len(cfg.HarnessProfiles) > 0 {
		runOpts = append(runOpts, orchestrator.WithHarnessProfiles(cfg.HarnessProfiles, cfg.DefaultProfile))
	}
	harnessLadder := cfg.HarnessLadder
	if !harnessLadder.Enabled() && cfg.Flow == nil {
		harnessLadder = host.DefaultLadderConfig()
	}
	if harnessLadder.Enabled() {
		runOpts = append(runOpts, orchestrator.WithHarnessLadderConfig(harnessLadder))
	}
	if cfg.AgentLaunchPolicy.Enabled {
		runOpts = append(runOpts, orchestrator.WithAgentLaunchPolicy(cfg.AgentLaunchPolicy))
	}
	if d := def.Decider; d != nil {
		runOpts = append(runOpts, orchestrator.WithDecider(orchestrator.DeciderConfig{
			Agent: d.Agent, Schema: d.Schema, Prompt: d.Prompt, Threshold: d.Threshold,
		}))
	}

	// A nil harness (flow posture) is acceptable to orchestrator.New for the
	// RunIntent-only path; the LLM router is never reached for explicit-intent
	// submissions.
	var harnessArg harness.Harness = h
	orch := orchestrator.New(def, m, s, harnessArg, runOpts...)
	if selectable, ok := h.(*selectableRoutingHarness); ok {
		selectable.SetSelectionResolver(orch.Selection)
	}
	if perr := orchestrator.PromptValidationError(orch.ValidatePromptExtensions()); perr != nil {
		return nil, perr
	}
	rt.Orch = orch

	// ── Ambient session miner ────────────────────────────────────────────────
	// Built only in live posture (a real harness backs the one agent pass) and
	// only when mining.enabled. The orchestrator is the miner's EventSink, so the
	// miner is built AFTER the orchestrator and installed via SetMiner. Flow /
	// nil-harness postures never build it → no flow fixture ever spends LLM.
	if cfg.Mining.Enabled && cfg.Flow == nil && rt.Scheduler != nil {
		cadence, cadErr := cfg.Mining.CadenceOrDefault()
		if cadErr != nil {
			return nil, fmt.Errorf("mining.cadence: %w", cadErr)
		}
		repoPath := cfg.MiningRepoPath
		if repoPath == "" {
			repoPath = filepath.Dir(cfg.AppPath)
		}
		toolsDir, tdErr := filepath.Abs(filepath.Join(repoPath, "tools", "session-mining"))
		if tdErr != nil {
			toolsDir = filepath.Join(repoPath, "tools", "session-mining")
		}
		miner := &mining.Miner{
			Resolver: mining.TranscriptResolver{},
			Sched:    rt.Scheduler,
			Pipeline: &mining.ExecPipelineRunner{ToolsDir: toolsDir},
			Marks:    mining.NewMapWatermarkStore(cfg.Mining.MinedThrough),
			Sink:     &mining.SessionSink{Sink: orch},
			Cfg: mining.Config{
				Enabled:         true,
				Cadence:         cadence,
				FirstPassSample: cfg.Mining.FirstPassSampleOrDefault(),
			},
		}
		// RecipeHandler (the recipes → proposer hand-off) is wired by the
		// proposal-loop slice's runtime construction (it builds the Mapper/Drafter
		// personas). Until then the miner advances the watermark and emits
		// MiningPassRan; the recipes are dropped at the seam, not lost — the next
		// pass re-emits from the same transcripts only if the watermark did not
		// advance. The SessionSink's SID is filled per session in NewSession's
		// Start path via the orchestrator (the sink keys events by the session
		// the miner was started for).
		orch.SetMiner(&sessionBoundMiner{m: miner}, repoPath)
	}

	// IDE link (web posture only): when the embedding VS Code extension advertises
	// its MCP server via CLAUDE_CODE_SSE_PORT, connect so host.ide.* verbs drive
	// that window — opening the brief/PRD and showing refine diffs. SetIDELink
	// also engages the inner-agent env scrub (a live author can't hijack the
	// link). Best-effort: a missing/declined editor leaves the link nil (the
	// connected:false path), never failing construction.
	if cfg.ConnectIDEFromEnv && os.Getenv("CLAUDE_CODE_SSE_PORT") != "" {
		cwd, _ := os.Getwd()
		link := ide.NewLink(cwd, nil)
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		info, lerr := link.Connect(cctx)
		cancel()
		if lerr != nil {
			logger.Warn("ide link: connect skipped", "err", lerr)
		} else {
			orch.SetIDELink(link)
			rt.closers = append(rt.closers, func() { _ = link.Close() })
			logger.Info("ide link: connected", "ide", info.IDEName, "port", info.Port)
		}
	}
	ok = true
	return rt, nil
}

// sessionBoundMiner adapts *mining.Miner to orchestrator.SessionMiner, stamping
// the session id onto the miner's EventSink binding at Start time.
type sessionBoundMiner struct{ m *mining.Miner }

func (s *sessionBoundMiner) Start(ctx context.Context, sid app.SessionID, repoPath string) error {
	if s.m.Sink != nil {
		s.m.Sink.SID = sid
	}
	return s.m.Start(ctx, sid, repoPath)
}

func (s *sessionBoundMiner) Notify(ctx context.Context) { s.m.Notify(ctx) }

// routingEmbedConfig returns the EmbedConfig from def.App.Routing.Embedding,
// or nil when no embedding block is declared.
func routingEmbedConfig(def *app.AppDef) *app.EmbedConfig {
	if def.Routing == nil {
		return nil
	}
	return def.Routing.Embedding
}

// embedModel returns the effective model name from ec, defaulting to
// nomic-embed-text-v1.5 when Model is empty.
func embedModel(ec *app.EmbedConfig) string {
	if ec.Model != "" {
		return ec.Model
	}
	return "nomic-embed-text-v1.5"
}

// buildEmbedder constructs a LocalEmbedder for endpoint or managed mode.
// In endpoint mode (ec.Endpoint set) the sidecar attaches to a running server.
// In managed mode (ec.Model set, no Endpoint) the sidecar fetches the GGUF and
// spawns llama-server with --embeddings --pooling mean on first use.
func buildEmbedder(ec *app.EmbedConfig) (*agent.LocalEmbedder, func(), error) {
	model := embedModel(ec)
	opts := []agentserver.Option{
		// Embedding sidecars must run with --embeddings --pooling mean so the
		// server serves /v1/embeddings rather than /v1/chat/completions.
		agentserver.WithExtraArgs("--embeddings", "--pooling", "mean"),
	}
	sc := agentserver.NewSidecar(model, "", ec.Endpoint, 0, opts...)
	emb := agent.NewLocalEmbedder(model, sc)
	return emb, func() { _ = emb.Close() }, nil
}

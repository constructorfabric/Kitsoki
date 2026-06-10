package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/clock"
	embedstore "kitsoki/internal/embed"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/oracle"
	oracleserver "kitsoki/internal/oracle/server"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
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
	OracleReg   *oracle.Registry
	Logger      *slog.Logger

	// DeferredOracleSink is non-nil when the runtime was built with a host
	// cassette (--host-cassette). Callers must call SetSink on it after wiring
	// the session's event sink so cassette oracle events flow to the trace.
	DeferredOracleSink *store.DeferredSink

	closers []func()
}

// Close releases all resources the runtime opened, in reverse order of opening.
func (rt *sessionRuntime) Close() {
	for i := len(rt.closers) - 1; i >= 0; i-- {
		rt.closers[i]()
	}
}

// runtimeConfig selects which construction posture buildSessionRuntime takes.
// The three postures are mutually exclusive in their oracle/host wiring:
//
//   - Flow != nil: deterministic flow-driven posture. Host stubs from the flow
//     fixture (and/or a host cassette) back the session; the harness is nil
//     (intents are submitted explicitly, no LLM); no oracle plugin registry.
//   - Flow == nil: live posture. A real harness is built (buildHarness), the
//     oracle plugin registry is built from the app def, and host builtins are
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
	// OracleBackend selects the coding-agent CLI backend ("" / "claude" |
	// "copilot") for host.oracle.* calls. Empty keeps the default (claude).
	OracleBackend string

	// WantRoomEnterSink allocates a TUI room-enter sink and wires it into the
	// orchestrator. `kitsoki run` sets this; `kitsoki web` does not.
	RoomEnterSink orchestrator.RoomEnterSink

	// Flow-posture fields.
	Flow         *testrunner.FlowFixture
	FlowFilePath string
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
	OracleBackend string

	// Flow / FlowFilePath select the deterministic flow-driven posture for the
	// whole server: when Flow != nil every session is built with a nil harness
	// and host stubs / cassette episodes (Flow.HostCassette) back its host.*
	// calls. A Playwright demo drives the live web UI with no LLM through this.
	Flow         *testrunner.FlowFixture
	FlowFilePath string

	// DefaultActor is the operator identity the web server records as
	// slots.author on a browser-driven turn when no header / actor param
	// supplies one (see server.WithDefaultActor). Empty = none configured;
	// the registry fails a session start fast if a story enforces an author
	// ACL guard but no identity is configured.
	DefaultActor string
}

// config materialises a per-session runtimeConfig for the story at storyPath
// with the loaded def, inheriting every session-invariant field from the base.
// The deterministic posture (Flow / FlowFilePath) is threaded through so the
// produced sessionRuntime is nil-harness, cassette/stub-backed when the base
// carries a fixture — the same construction web.go performs today.
func (b runtimeBase) config(storyPath string, def *app.AppDef) runtimeConfig {
	return runtimeConfig{
		AppPath:       storyPath,
		Def:           def,
		DBPath:        b.DBPath,
		ExecMode:      b.ExecMode,
		HarnessType:   b.HarnessType,
		ClaudeModel:   b.ClaudeModel,
		RecordingPath: b.RecordingPath,
		RecordPath:    b.RecordPath,
		OracleBackend: b.OracleBackend,
		Flow:          b.Flow,
		FlowFilePath:  b.FlowFilePath,
	}
}

// buildSessionRuntime performs the orchestrator CONSTRUCTION shared by
// `kitsoki run` and `kitsoki web`: open the store, build journal reader/writer,
// job store + scheduler, chat store, machine, host registry (per posture),
// oracle registry (live posture only), and the orchestrator itself — then run
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

	// ── Host registry + harness + oracle, per posture ──────────────────────
	var (
		h         harness.Harness
		oracleReg *oracle.Registry
		hostReg   *host.Registry
	)

	if cfg.Flow != nil {
		// Deterministic flow posture: stub host handlers, no harness, no
		// oracle plugins. Mirrors testrunner.buildOrchestratorRig's stub
		// wiring (host.RegisterBuiltins + RegisterHostStubs), simplified — the
		// web surface never records, so no record-mode cassette wiring.
		hostReg = host.NewRegistry()
		host.RegisterBuiltins(hostReg)
		testrunner.RegisterHostStubs(hostReg, cfg.Flow.HostHandlers)

		if cfg.Flow.HostCassette != "" {
			cassettePath := cfg.Flow.HostCassette
			if !filepath.IsAbs(cassettePath) && cfg.FlowFilePath != "" {
				cassettePath = filepath.Join(filepath.Dir(cfg.FlowFilePath), cassettePath)
			}
			cas, casErr := testrunner.LoadCassette(cassettePath)
			if casErr != nil {
				return nil, fmt.Errorf("load host cassette: %w", casErr)
			}
			// stateOf is unused for replay-only dispatch (no record sink);
			// cassette matcher keys on handler + args only.
			stateOf := func() string { return "" }
			// DeferredSink: oracle events from cassette episodes that carry an
			// oracle: block are written here immediately but forwarded to the
			// real session sink only after the caller calls SetSink on it
			// (registry.go does this right after orch.SetEventSink(live)).
			deferredSink := store.NewDeferredSink()
			rt.DeferredOracleSink = deferredSink
			seen := map[string]bool{}
			for _, ep := range cas.Episodes {
				hn, okk := ep.Match["handler"].(string)
				if !okk || hn == "" || seen[hn] {
					continue
				}
				seen[hn] = true
				fallback, _ := hostReg.Get(hn)
				disp := testrunner.BuildCassetteDispatcherWithSink(cas, hn, stateOf, fallback, nil, clock.Real(), deferredSink, nil)
				hostReg.Replace(hn, disp)
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
		// Harness stays nil: flow intents are submitted explicitly via the
		// write RPCs; the orchestrator's RunIntent path never calls the
		// harness.
	} else {
		hostReg = host.NewRegistry()
		host.RegisterBuiltins(hostReg)
		if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
			return nil, fmt.Errorf("validate hosts: %w", err)
		}

		h, err = buildHarness(cfg.HarnessType, cfg.ClaudeModel, cfg.OracleBackend, cfg.RecordingPath, cfg.RecordPath, def)
		if err != nil {
			return nil, fmt.Errorf("build harness: %w", err)
		}
		rt.Harness = h
		setHarnessLogger(h, logger)
		rt.closers = append(rt.closers, func() { _ = h.Close() })

		oracleReg, err = oracle.BuildRegistryFromDef(def, h)
		if err != nil {
			return nil, fmt.Errorf("build oracle registry: %w", err)
		}
		rt.OracleReg = oracleReg
		rt.closers = append(rt.closers, func() { _ = oracleReg.Close() })

		// Wire host.oracle.search when app.routing.embedding is configured.
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
			hostReg.Replace("host.oracle.search",
				host.NewOracleSearchHandler(embedModel(ec), filepath.Dir(cfg.AppPath), embedder, store))
			slog.Info("host.oracle.search: wired", "endpoint", ec.Endpoint, "model", embedModel(ec))
		}
	}

	// Agents registry (builtins + AppDef overrides), installed process-wide so
	// handlers honouring `agent:` resolve names. Same in both postures.
	agentReg, err := agents.BuildRegistry(def.AgentSpecs())
	if err != nil {
		return nil, fmt.Errorf("build agents registry: %w", err)
	}
	host.SetAgentRegistry(agentReg)

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
	if oracleReg != nil {
		runOpts = append(runOpts, orchestrator.WithOracleRegistry(oracleReg))
	}
	if cfg.RoomEnterSink != nil {
		runOpts = append(runOpts, orchestrator.WithRoomEnterSink(cfg.RoomEnterSink))
	}
	if cfg.PromptOverlay != "" {
		runOpts = append(runOpts, orchestrator.WithPromptOverlay(cfg.PromptOverlay))
	}
	if cfg.OracleBackend != "" {
		runOpts = append(runOpts, orchestrator.WithOracleBackendName(cfg.OracleBackend))
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
	if perr := orchestrator.PromptValidationError(orch.ValidatePromptExtensions()); perr != nil {
		return nil, perr
	}
	rt.Orch = orch

	ok = true
	return rt, nil
}

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
func buildEmbedder(ec *app.EmbedConfig) (*oracle.LocalEmbedder, func(), error) {
	model := embedModel(ec)
	opts := []oracleserver.Option{
		// Embedding sidecars must run with --embeddings --pooling mean so the
		// server serves /v1/embeddings rather than /v1/chat/completions.
		oracleserver.WithExtraArgs("--embeddings", "--pooling", "mean"),
	}
	sc := oracleserver.NewSidecar(model, "", ec.Endpoint, 0, opts...)
	emb := oracle.NewLocalEmbedder(model, sc)
	return emb, func() { _ = emb.Close() }, nil
}

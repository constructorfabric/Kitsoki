// Package testrunner implements the Mode 2 (deterministic flow tests) and
// Mode 1 (input→intent pass-rate tests) runners. See docs/stories/state-machine.md.
package testrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/expr"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/intent"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/render"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// publishAppDirForTestrunner sets host.AppDirEnv to the absolute
// directory containing appPath. Mirrors cmd/kitsoki/session.go's
// publishAppDir but lives here so the testrunner package doesn't
// pull cmd/kitsoki/* into its import graph. Both RunFlows and
// RunIntents call this BEFORE app.Load so the loader's env-var
// validator can resolve `${KITSOKI_APP_DIR}` references in fields
// like meta_modes[*].cwd at validate time.
//
// Best-effort: a filepath.Abs failure (rare; only when the OS can't
// get the cwd) simply skips the setenv — the loader will then
// surface a clean "references unset env var KITSOKI_APP_DIR" error
// rather than crash, which matches the bug-2 negative-path test.
func publishAppDirForTestrunner(appPath string) {
	if abs, err := filepath.Abs(appPath); err == nil {
		_ = os.Setenv(host.AppDirEnv, filepath.Dir(abs))
	}
}

// ─── Flow fixture YAML format ────────────────────────────────────────────────

// FlowFixture is the top-level flow fixture document.
type FlowFixture struct {
	TestKind     string         `yaml:"test_kind"`
	App          string         `yaml:"app"`
	Recording    string         `yaml:"recording,omitempty"`
	InitialState string         `yaml:"initial_state"`
	InitialWorld map[string]any `yaml:"initial_world,omitempty"`
	Turns        []FlowTurn     `yaml:"turns"`

	// Mode selects the orchestrator's execution mode for this fixture
	// (execution-modes proposal): "" / "one-shot" (default — synthetic
	// emit chains auto-advance through gates) or "staged" (a multi-way
	// decision gate ends the turn). Only meaningful on the
	// orchestrator-backed runner.
	Mode string `yaml:"mode,omitempty"`

	// HostHandlers declares stub host.* handlers used by this flow.
	// Keys are the handler name (e.g. "host.run", "host.workspace_manager.get").
	// Each value declares the canned response. Presence of any host_handlers
	// entry implicitly opts the fixture into the orchestrator-backed runner.
	// Mutually exclusive with HostCassette.
	HostHandlers map[string]HostStub `yaml:"host_handlers,omitempty"`

	// HostCassette is the path to a host cassette file (kind: host_cassette).
	// When set the testrunner installs a cassette dispatcher for every handler
	// name referenced by the cassette's episodes. Mutually exclusive with
	// HostHandlers; compatible with HostBindings.
	HostCassette string `yaml:"host_cassette,omitempty"`

	// StrictCassetteCoverage, when true, fails the fixture if any cassette
	// episode was never matched during the run. Only meaningful when
	// host_cassette: is set. Default false preserves backward compatibility —
	// fixtures that reuse a shared cassette for a subset of turns won't fail.
	StrictCassetteCoverage bool `yaml:"strict_cassette_coverage,omitempty"`

	// StarlarkHTTPCassette is the path to an HTTP cassette (kind: http_cassette)
	// that serves ctx.http.* calls made by host.starlark.run scripts. Unlike
	// HostCassette (which replaces a whole handler with a canned Result), this
	// lets the REAL host.starlark.run handler run — reading the script + sidecar
	// from disk and validating inputs/outputs — and only replays its HTTP from
	// disk. The testrunner injects a starlark.ReplayClient via WithHTTP; the
	// adapter leaves it in place because HasHTTPClient(ctx) is then true.
	// Relative paths resolve against the fixture file's directory. Setting this
	// field opts the fixture into the orchestrator-backed runner.
	StarlarkHTTPCassette string `yaml:"starlark_http_cassette,omitempty"`

	// StarlarkInspectCassette is the path to an inspect cassette (kind:
	// inspect_cassette) that serves ctx.fs.* and ctx.probe calls made by
	// host.starlark.run scripts. It is the filesystem/probe sibling of
	// StarlarkHTTPCassette: the REAL host.starlark.run handler runs — reading the
	// script + sidecar from disk and validating inputs/outputs — and only its
	// fs/probe I/O is served from disk. The testrunner injects a
	// starlark.ReplayInspector via WithInspector; the adapter leaves it in place
	// because HasInspector(ctx) is then true, so no file is read and no process is
	// run. Relative paths resolve against the fixture file's directory. Setting
	// this field opts the fixture into the orchestrator-backed runner.
	StarlarkInspectCassette string `yaml:"starlark_inspect_cassette,omitempty"`

	// HostBindings rebinds named ifaces to alternative handlers for
	// this fixture only. Mirrors the production `imports.<alias>.
	// host_bindings:` block but applies at flow-test scope so a fixture
	// can swap a transport / vcs / ci provider without forking the
	// production app.yaml. Keys are top-level iface names declared on
	// the app (e.g. "transport"); values are the concrete host handler
	// to bind in place of the iface's declared default.
	//
	// When this field is set, the testrunner reloads the app via
	// app.LoadWithOverrides for this fixture only — so each fixture in
	// a multi-doc file can declare its own bindings without affecting
	// peers.
	HostBindings map[string]string `yaml:"host_bindings,omitempty"`

	// UseOrchestrator opts into the orchestrator-backed runner instead of the
	// legacy machine-only path. Defaults to false for backwards compatibility;
	// flows that declare host_handlers: or any advance_clock:/expect_inbox: turn
	// fields implicitly opt in regardless of this flag.
	UseOrchestrator *bool `yaml:"use_orchestrator,omitempty"`

	// Session-level assertions.
	ExpectTerminal           *bool          `yaml:"expect_terminal,omitempty"`
	ExpectEventsCountAtLeast *int           `yaml:"expect_events_count_atleast,omitempty"`
	ExpectEventsCountAtMost  *int           `yaml:"expect_events_count_atmost,omitempty"`
	ExpectNoErrors           *bool          `yaml:"expect_no_errors,omitempty"`
	ExpectWorldFinal         map[string]any `yaml:"expect_world_final,omitempty"`

	// ExpectFiles asserts that, after the flow completes, named files
	// exist (or don't) and their contents match a regex. The path itself
	// is a literal path (relative paths are resolved against the fixture
	// file's directory); the content_matches field is a Go regex. Use
	// expect_files for transport-stub variants like host.artifacts_dir
	// that land artefacts on disk — the fixture asserts the side effect
	// without inspecting transport-internal state.
	ExpectFiles []ExpectFile `yaml:"expect_files,omitempty"`

	// ExpectNoHostCalls fails the fixture if any of the named host
	// handlers fire across the whole run. Mirrors the per-turn variant
	// on FlowTurn but applied to every collected event. Useful for
	// asserting that a fixture's walk never touched a transport / VCS
	// op that belongs to a different pipeline.
	ExpectNoHostCalls []string `yaml:"expect_no_host_calls,omitempty"`
}

// ExpectFile is one entry in a fixture-level expect_files assertion.
// All three fields are optional but at least Path must be set; an
// entry with no content_matches and no MustNotExist simply asserts
// the file's existence.
type ExpectFile struct {
	// Path is the literal filesystem path to check. Relative paths are
	// resolved against the fixture file's directory at assertion time.
	Path string `yaml:"path"`
	// ContentMatches, when non-empty, is a Go regex run against the
	// file contents. The assertion fails if the regex does not match.
	ContentMatches string `yaml:"content_matches,omitempty"`
	// MustNotExist inverts the assertion — the file must NOT exist on
	// disk. Useful for asserting that a code-path was never reached.
	MustNotExist *bool `yaml:"must_not_exist,omitempty"`
}

// HostStub declares the canned response for a stub host handler used in a flow
// fixture. Mutually exclusive fields: at most one of Error, InfraError, or Data
// should be set. Delay (virtual time) and RequestClarification are independent.
type HostStub struct {
	// Data is returned in host.Result.Data on a successful invocation.
	Data map[string]any `yaml:"data,omitempty"`
	// Error sets host.Result.Error (a domain-level error). Empty = no error.
	// Mutually exclusive with InfraError.
	Error string `yaml:"error,omitempty"`
	// InfraError, when non-empty, makes the handler return (Result{}, error).
	// This simulates a handler that fails at the infrastructure level
	// (distinct from a domain-level Error). Mutually exclusive with Error.
	InfraError string `yaml:"infra_error,omitempty"`
	// Delay (e.g. "30s") simulates a slow handler using the Fake clock's
	// Sleep. Combined with FlowTurn.AdvanceClock, this lets a test verify
	// long-running background-job behaviour without real wall time.
	Delay string `yaml:"delay,omitempty"`
	// RequestClarification, when non-empty, causes the stub to call
	// host.RequestClarification mid-execution and block until the user answers
	// via answer_clarification. The value is the question string surfaced to the
	// user in the action_required notification. After the answer is received the
	// stub returns Data (or Error/InfraError) as normal.
	// Requires background: true on the invoking effect.
	RequestClarification string `yaml:"request_clarification,omitempty"`
	// ByOp lets one stub serve multiple ops under a prefix-fallback handler
	// (host.local_files.ticket, host.git, host.cypilot_artifacts, etc.) with
	// distinct envelopes per op. Keys are the `op:` arg value the stub
	// receives; values are the per-op envelope (Data + Error + InfraError).
	// When ByOp matches, the top-level Data/Error/InfraError fields are
	// ignored — the per-op envelope wins. When no key matches, the stub
	// falls through to the top-level envelope. Delay and RequestClarification
	// stay at the top level.
	ByOp map[string]HostStubEnvelope `yaml:"by_op,omitempty"`
	// ByCall lets one stub serve multiple call sites that share a handler name
	// (the common case: two `host.agent.decide` invokes in one room) with
	// distinct envelopes per call. Keys are the author-assigned `id:` on the
	// invoke effect, threaded into args under the reserved `call` key. Values
	// are the per-call envelope (Data + Error + InfraError). Resolution order:
	// ByCall is tried before ByOp; when neither matches, the stub falls through
	// to the top-level envelope. This is what makes agent calls addressable
	// without distorting the story (picking a different verb) or splitting the
	// phase. Delay and RequestClarification stay at the top level.
	ByCall map[string]HostStubEnvelope `yaml:"by_call,omitempty"`
}

// HostStubEnvelope is one per-op canned response under HostStub.ByOp.
// Mirrors the data/error/infra_error subset of HostStub; delay and
// clarification stay on the parent so a single stub can declare both
// once and dispatch only the result shape per op.
type HostStubEnvelope struct {
	Data       map[string]any `yaml:"data,omitempty"`
	Error      string         `yaml:"error,omitempty"`
	InfraError string         `yaml:"infra_error,omitempty"`
}

// FlowTurn is one turn in the fixture.
type FlowTurn struct {
	// Exactly one of Intent or Input must be set.
	Intent *FlowIntent `yaml:"intent,omitempty"`
	Input  string      `yaml:"input,omitempty"`

	// DisplayInput, when set on an intent: turn, overrides the synthetic
	// "[intent] <name>" string the RunIntent path stamps onto the emitted
	// turn.input / turn.start events with the operator's actual free-text
	// utterance. It does NOT affect routing — intent + slots still drive the
	// transition deterministically; only the recorded input STRING changes. Set
	// by the trace→flow converter (fromtrace.go) so a reconstructed trace's user
	// bubbles read the operator's real words. Empty preserves the synthetic
	// default, leaving existing fixtures and tests unaffected.
	DisplayInput string `yaml:"display_input,omitempty"`

	// WorldOverride mutates world before guard evaluation on this turn.
	// Lets fixtures probe arcs that would otherwise require a long preceding
	// flow (e.g. the L2 cycle-budget feedback arcs).
	WorldOverride map[string]any `yaml:"world_override,omitempty"`

	// AdvanceClock (e.g. "30s") moves the FakeClock forward by the given
	// duration after the turn completes, then waits for the scheduler and
	// the session listener to drain. Requires the orchestrator path (implicit
	// when set). Use after a turn that submits a background job to assert on
	// post-on_complete world state without real wall time.
	AdvanceClock string `yaml:"advance_clock,omitempty"`

	// ExpectInbox asserts notification counts after this turn (post
	// advance_clock if set). All keys are optional; only specified assertions
	// run. Requires the orchestrator path.
	ExpectInbox *FlowInboxExpectation `yaml:"expect_inbox,omitempty"`

	// ExpectJobs asserts on jobs that newly reached a terminal status during
	// this turn (post advance_clock if set). Matching is order-sensitive: the
	// i-th ExpectJob entry matches the i-th newly-terminal job (in
	// creation-time order) whose namespace matches. Surplus newly-terminal
	// jobs not asserted are silently allowed, so fixtures don't need to
	// enumerate every side-effect job. Requires the orchestrator path.
	//
	// Catches a bug class that expect_inbox can't see: when host.run is
	// invoked with cmd: in list-form, the string type assertion in the
	// handler fails and the job lands with status=failed, yet on_complete
	// still runs and the game continues — the misleading inbox entries
	// ("submitted" + a failure ledger) are the only signal. expect_jobs:
	// pins per-job terminal status so the next regression of this shape
	// fails the suite.
	ExpectJobs []ExpectJob `yaml:"expect_jobs,omitempty"`

	// Assertions.
	ExpectState          string         `yaml:"expect_state,omitempty"`
	ExpectNotState       string         `yaml:"expect_not_state,omitempty"`
	ExpectStateIn        []string       `yaml:"expect_state_in,omitempty"`
	ExpectSlots          map[string]any `yaml:"expect_slots,omitempty"`
	ExpectWorld          map[string]any `yaml:"expect_world,omitempty"`
	ExpectWorldFull      map[string]any `yaml:"expect_world_full,omitempty"`
	ExpectWorldUnchanged *bool          `yaml:"expect_world_unchanged,omitempty"`
	ExpectEvents         []FlowEvent    `yaml:"expect_events,omitempty"`
	ExpectEventsExact    []FlowEvent    `yaml:"expect_events_exact,omitempty"`
	ExpectError          *FlowError     `yaml:"expect_error,omitempty"`
	ExpectViewMatches    string         `yaml:"expect_view_matches,omitempty"`
	ExpectNoView         *bool          `yaml:"expect_no_view,omitempty"`

	// ExpectHostCalls is a turn-level shorthand for asserting that one
	// or more host handlers fired during the turn. Each entry expands
	// into one HostDispatched event match (subsequence semantics — extra
	// events between are tolerated). Args are matched as a partial map
	// against the dispatched effect's args payload.
	ExpectHostCalls []ExpectHostCall `yaml:"expect_host_calls,omitempty"`

	// ExpectNoHostCalls lists handler names that must NOT fire during
	// this turn. Any HostDispatched event whose effect.handler matches
	// fails the assertion.
	ExpectNoHostCalls []string `yaml:"expect_no_host_calls,omitempty"`
}

// ExpectHostCall is one entry in a per-turn expect_host_calls block —
// shorthand for an `expect_events: [{kind: HostDispatched, effect: {…}}]`
// match. The runner expands each entry into a FlowEvent before running
// the standard subsequence check.
type ExpectHostCall struct {
	// Handler is the dispatched handler name (e.g. "iface.vcs.branch"
	// or "host.agent.ask_with_mcp"). Required.
	Handler string `yaml:"handler"`
	// Args is an optional partial-match against the dispatched effect's
	// args payload. Missing keys are tolerated; mismatched keys fail.
	Args map[string]any `yaml:"args,omitempty"`
	// Times pins the exact number of matching HostDispatched events
	// emitted during this turn. When zero (default) the runner does a
	// subsequence-style "at least one match" check (matching the
	// existing expect_events: semantics).
	Times *int `yaml:"times,omitempty"`
}

// FlowInboxExpectation asserts notification counts after a turn. All fields
// are optional; only the ones explicitly set are checked.
type FlowInboxExpectation struct {
	// Unread asserts the total count of unread notifications.
	Unread *int `yaml:"unread,omitempty"`
	// NeedsAttention asserts the count of action_required notifications.
	NeedsAttention *int `yaml:"needs_attention,omitempty"`
	// Severities asserts that the sorted list of severity strings for all
	// unread notifications matches exactly (e.g. ["info", "success"]).
	Severities []string `yaml:"severities,omitempty"`
}

// ExpectJob is one entry in a per-turn expect_jobs assertion. It pins the
// terminal status of a job that landed during the turn, identified by its
// host handler namespace (the job's Kind).
//
// Matching contract:
//   - After advance_clock drains, the runner enumerates jobs whose ID was
//     unknown OR not-yet-terminal at the start of the turn, but is now in a
//     terminal state (done | failed | cancelled | awaiting_input).
//     awaiting_input is intentionally included so a clarification pause can
//     be asserted explicitly.
//   - The newly-terminal jobs are listed in creation-time order (jobs.created_at).
//   - Each ExpectJob[i] consumes the next unmatched newly-terminal job whose
//     Kind == Namespace. If the status doesn't match, the fixture fails with
//     a clear diff.
//   - Surplus newly-terminal jobs that don't match any remaining ExpectJob
//     entry are tolerated silently — fixtures can assert on a single
//     interesting job without enumerating every side-effect dispatch.
//   - If expect_jobs has N entries but fewer than N newly-terminal jobs were
//     found, the fixture fails with "expected N jobs to terminate, got M".
type ExpectJob struct {
	// Namespace is the handler name to match against the job's Kind
	// (e.g. "host.run", "host.transport.post"). Required.
	Namespace string `yaml:"namespace"`
	// Status is the expected terminal status. One of:
	//   done | failed | cancelled | awaiting_input
	// Required.
	Status string `yaml:"status"`
}

// FlowIntent is the structured intent in a fixture turn.
type FlowIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots,omitempty"`
}

// FlowEvent is one event in an expect_events assertion.
type FlowEvent struct {
	Kind   string         `yaml:"kind"`
	From   string         `yaml:"from,omitempty"`
	To     string         `yaml:"to,omitempty"`
	Effect map[string]any `yaml:"effect,omitempty"`
}

// FlowError is the expect_error assertion.
type FlowError struct {
	Code            string   `yaml:"code"`
	AllowedContains []string `yaml:"allowed_contains,omitempty"`
	GuardHint       string   `yaml:"guard_hint,omitempty"`
	MissingSlots    []string `yaml:"missing_slots,omitempty"`
}

// ─── Flow runner result types ─────────────────────────────────────────────────

// TurnResult holds the result for a single turn in a flow run.
type TurnResult struct {
	TurnIndex int
	Passed    bool
	Failures  []string
	NewState  app.StatePath
	View      string
	Events    []store.Event
}

// FlowResult holds the result for a complete flow file.
type FlowResult struct {
	File    string
	Passed  bool
	Turns   []TurnResult
	Skipped bool // when recording miss and allow-missing is set
}

// FlowReport is the aggregate result of running multiple flows.
type FlowReport struct {
	Results []FlowResult
	Passed  int
	Failed  int
}

// ─── Flow runner options ──────────────────────────────────────────────────────

// FlowOptions configure the flow runner.
type FlowOptions struct {
	// RecordingOverride overrides the recording path declared in each fixture file.
	RecordingOverride string
	// AllowMissingRecording downgrades recording-miss failures to skips.
	AllowMissingRecording bool
	// FailFast stops at first failure.
	FailFast bool
	// Verbose enables per-turn verbose output.
	Verbose bool
	// JSONOut is the optional path to write a JSON report.
	JSONOut string
	// OnRigClose, if non-nil, is invoked at the end of each orchestrator-backed
	// flow run — after assertions have completed but before the in-memory store
	// is closed. Intended for fixture-export tools that need the raw event log
	// the flow produced (the store goes away once the function returns). The
	// store and session id passed in are still live.
	//
	// sink is the authoritative JSONL trace for the run. Exporters should read
	// from it (runstatus.FromSink) rather than from st.LoadHistory: the SQLite
	// events table is lossy (no state_path / call_id / parent_turn columns) and
	// omits cassette agent events, whereas the JSONL sink is faithful.
	OnRigClose func(filePath string, st store.Store, sid app.SessionID, sink *store.JSONLSink) error

	// TracePath, when non-empty, fixes the path of the run's authoritative JSONL
	// event sink (and, by extension, the sibling agent-prompts/ directory where
	// large agent prompts are stored). Fixture exporters set this to a path in
	// the output directory so the prompt/response side-files end up next to the
	// generated snapshot, where the runstatus SPA fetches them. When empty (the
	// default), the rig uses a temp file that cleanup removes.
	TracePath string

	// ImportResolver is the injected ImportResolver (DI) through which an
	// `@kitsoki/<name>` import in the app under test resolves against the
	// `--kitsoki-repo` override or the embedded story library — letting
	// `kitsoki test flows` run a vendored instance in a foreign repo with no
	// on-disk kitsoki checkout. nil keeps the legacy error-on-missing behaviour.
	ImportResolver app.ImportResolver
}

// ─── orchRig holds all resources for an orchestrator-backed flow run ─────────

// orchRig bundles the orchestrator and associated resources for one flow run.
// Call cleanup() when done to close the store.
type orchRig struct {
	orch     *orchestrator.Orchestrator
	sched    jobs.Scheduler
	jobStore *jobs.JobStore
	st       store.Store
	sid      app.SessionID
	clk      *clock.Fake
	cleanup  func() error

	// currentStatePath is updated by the turn loop before each RunIntent call
	// so that cassette dispatchers can read the orchestrator's current state
	// without a synchronous LoadJourney on every handler invocation.
	currentStatePath app.StatePath

	// journalWriter is the in-memory journal writer wired by buildOrchestratorRig
	// when a host_cassette is configured. It is exposed here so the cassette
	// dispatcher can write KindAgentCall entries on replay (Phase 2) and so
	// downstream callers (e.g. fromflow) can pass the journal to runstatus.FromHistory.
	journalWriter journal.Writer

	// deferredAgentSink is the deferred sink used by cassette dispatchers to write
	// agent events. It's created before session creation and updated after NewSession
	// when the real sink is available.
	deferredAgentSink *store.DeferredSink

	// eventSink is the authoritative JSONL trace for this flow run. The
	// orchestrator dual-writes every turn event here (in addition to SQLite),
	// and cassette agent events are routed here too. It is the faithful trace
	// — unlike the SQLite events table, JSONL records state_path / call_id /
	// parent_turn — so fixture exporters (fromflow) read from it rather than
	// from store.LoadHistory. Backed by a temp file removed by cleanup.
	eventSink *store.JSONLSink

	// cassette holds the loaded host cassette when host_cassette: is set and
	// strict_cassette_coverage: true is declared on the fixture. nil otherwise.
	// Retained so runOneFlowOrchestrator can check for unmatched (orphan)
	// episodes after all turns complete.
	cassette *Cassette

	// httpCassetteFlush persists newly recorded Starlark HTTP exchanges back to
	// the starlark_http_cassette: file after all turns complete. nil unless the
	// effective HTTP record mode is non-none. Called from runOneFlowOrchestrator
	// before cleanup so a record run leaves the cassette on disk.
	httpCassetteFlush func() error
}

// buildOrchestratorRig constructs a fully wired orchestrator rig for one flow
// run, using an in-memory SQLite store and a fake clock at epoch zero.
//
// Host stubs from fixture.HostHandlers are registered in a host.Registry;
// each stub closure respects Delay (using the fake clock's Sleep), InfraError,
// Error, and Data fields in that priority order.
//
// filePath is the fixture file's absolute path; it is used to resolve
// host_cassette: relative paths.
func buildOrchestratorRig(ctx context.Context, def *app.AppDef, m machine.Machine, fixture *FlowFixture, filePath string, tracePath string) (*orchRig, error) {
	// Deterministic epoch.
	clk := clock.NewFake(time.Unix(0, 0))

	// In-memory SQLite store (no file I/O).
	st, err := store.OpenMemory()
	if err != nil {
		return nil, fmt.Errorf("buildOrchestratorRig: open store: %w", err)
	}

	// Job store (schema migration applied once on open).
	js, err := jobs.NewJobStore(st.DB())
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: job store: %w", err)
	}

	// Scheduler backed by the fake clock so Delay stubs block on fake time.
	sched := jobs.NewScheduler(js, jobs.WithClock(clk))

	// Build host registry. host_bindings: fixtures want at least some
	// production handlers live (the whole point is to swap a binding
	// and exercise the REAL handler under it). For backwards compat
	// with the larger pool of stub-only fixtures, builtins are
	// pre-registered ONLY when host_bindings: is set; the rest of the
	// suite keeps its "only what you stub is registered" behaviour so
	// an unstubbed host.run / host.git can't accidentally shell out.
	//
	// Stubs declared in host_handlers: are registered below and take
	// precedence over any built-in with the same name (host.Register
	// overwrites).
	reg := host.NewRegistry()
	if len(fixture.HostBindings) > 0 {
		// RegisterBuiltins covers host.jobs.answer_clarification too,
		// so the bare-registration branch below is skipped.
		host.RegisterBuiltins(reg)
	} else {
		// host.jobs.answer_clarification must be registered so that
		// the answer_clarification intent can store the user's answer
		// in the DB and let a waiting background job resume. Only the
		// non-host_bindings branch needs the explicit registration —
		// the builtin pre-register above covers it otherwise.
		if _, ok := fixture.HostHandlers["host.jobs.answer_clarification"]; !ok {
			reg.Register("host.jobs.answer_clarification", host.AnswerClarificationHandler)
		}
	}
	// Register host_handlers: stubs via the shared registration path so the
	// flow-test runner and `kitsoki web --flow` resolve a stub identically.
	RegisterHostStubs(reg, fixture.HostHandlers)

	// Allocate the rig pointer early so the cassette dispatcher's stateOf closure
	// can hold a reference to &rig.currentStatePath that the turn loop updates
	// before each RunIntent call. This avoids a synchronous LoadJourney per dispatch.
	var rig orchRig

	// Wire host_cassette: when set.
	if fixture.HostCassette != "" {
		cassettePath := fixture.HostCassette
		if !filepath.IsAbs(cassettePath) {
			cassettePath = filepath.Join(filepath.Dir(filePath), cassettePath)
		}
		cas, casErr := LoadCassette(cassettePath)
		if casErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: load cassette: %w", casErr)
		}

		// Reject unsupported env-var record modes (file-level was validated in LoadCassette).
		mode := CassetteRecordMode(cas)
		if vErr := ValidateRecordMode(mode); vErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: %w", vErr)
		}

		// KITSOKI_CASSETTE_STRICT: hard error when record mode is non-none.
		if CassetteStrictRecording() && mode != "none" && mode != "" {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: KITSOKI_CASSETTE_STRICT=1 but cassette record mode is %q", mode)
		}

		// Create an in-memory journal writer backed by the same SQLite DB so
		// cassette replay can write KindAgentCall entries (Phase 2) and record
		// mode can read them back (Phase 3).
		jw, jwErr := journal.NewSQLiteWriter(st.DB())
		if jwErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: create journal writer: %w", jwErr)
		}
		rig.journalWriter = jw

		// Build a journal lookup function for Phase 3 (record mode): given the
		// agent context and verb, read back the most recently written
		// KindAgentCall entry for that session and verb. The agent handlers do
		// not return call_id in result.Data, so we identify the entry by
		// session ID + verb + max rowid (latest write).
		journalLookup := func(ctx context.Context, verb string) (*host.AgentCallBody, bool) {
			oc := host.AgentCallCtxFrom(ctx)
			return lookupAgentCallByVerb(st.DB(), oc.SessionID, verb)
		}

		// stateOf reads the shared currentStatePath pointer updated by the turn loop.
		stateOf := func() string {
			return string(rig.currentStatePath)
		}

		// recordSink appends to the cassette file when recording is active.
		var recordSink func(*CassetteEpisode)
		if mode != "none" && mode != "" {
			recordSink = func(ep *CassetteEpisode) {
				_ = AppendEpisodeToFile(cas, ep)
			}
		}

		// Create a deferred agent event sink for cassette dispatchers.
		// The real sink will be set after session creation when the session ID is known.
		deferredAgentSink := store.NewDeferredSink()
		rig.deferredAgentSink = deferredAgentSink

		// Collect unique handler names from the cassette's episodes.
		seen := map[string]bool{}
		for _, ep := range cas.Episodes {
			h, ok := ep.Match["handler"].(string)
			if !ok || h == "" {
				continue
			}
			if seen[h] {
				continue
			}
			seen[h] = true
			handlerName := h
			// Capture any existing (real) handler as fallback. When host_bindings:
			// is set, builtins are pre-registered and act as the fallback for
			// cassette misses; without host_bindings: there are no pre-registered
			// builtins, so fallback is nil (miss is a hard error). See
			// docs/architecture/hosts.md (host_bindings) for the binding model.
			var fallback host.Handler
			if len(fixture.HostBindings) > 0 {
				fallback, _ = reg.Get(handlerName)
			}
			casDispatcher := BuildCassetteDispatcherWithJournalAndSink(cas, handlerName, stateOf, fallback, recordSink, clk, jw, journalLookup, deferredAgentSink)
			reg.Replace(handlerName, casDispatcher)
		}

		// When host_bindings: is set (builtins registered) and a record sink is
		// active, also install cassette dispatchers for every host.agent.*
		// builtin handler that isn't already wrapped by an episode above.
		// This lets the cassette record agent calls even when the cassette file
		// has no agent episodes yet — the first recording pass adds them.
		if len(fixture.HostBindings) > 0 && recordSink != nil {
			agentBuiltins := []string{
				"host.agent.ask",
				"host.agent.decide",
				"host.agent.extract",
				"host.agent.task",
				"host.agent.converse",
			}
			for _, handlerName := range agentBuiltins {
				if seen[handlerName] {
					continue // already wrapped via an episode above
				}
				fallback, hasFallback := reg.Get(handlerName)
				if !hasFallback {
					continue // not a builtin in this rig — skip
				}
				casDispatcher := BuildCassetteDispatcherWithJournalAndSink(cas, handlerName, stateOf, fallback, recordSink, clk, jw, journalLookup, deferredAgentSink)
				reg.Replace(handlerName, casDispatcher)
				seen[handlerName] = true
			}
		}

		// Retain the cassette for post-run orphan detection when
		// strict_cassette_coverage: true is declared on the fixture.
		if fixture.StrictCassetteCoverage {
			rig.cassette = cas
		}
	}

	// Wire starlark_http_cassette: when set. This is the Starlark HTTP replay
	// seam (distinct from host_cassette: which replaces a whole handler). We
	// load the cassette, build a ReplayClient, and WRAP the real
	// host.starlark.run handler so it runs against the injected replay client.
	// The adapter checks starlarkhost.HasHTTPClient(ctx) and skips installing a
	// production RecordingClient when a client is already present, so the
	// ReplayClient is the one used — no socket is ever opened.
	if fixture.StarlarkHTTPCassette != "" {
		cassettePath := fixture.StarlarkHTTPCassette
		if !filepath.IsAbs(cassettePath) {
			cassettePath = filepath.Join(filepath.Dir(filePath), cassettePath)
		}
		raw, rerr := os.ReadFile(cassettePath)
		if rerr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: read starlark http cassette: %w", rerr)
		}
		var cas starlarkhost.HTTPCassette
		if uerr := yaml.Unmarshal(raw, &cas); uerr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: parse starlark http cassette %q: %w", cassettePath, uerr)
		}

		// Effective record mode: KITSOKI_HTTP_CASSETTE_RECORD wins over the
		// cassette's record_mode: field; empty means replay-only ("none").
		mode := cas.RecordMode
		if env := os.Getenv("KITSOKI_HTTP_CASSETTE_RECORD"); env != "" {
			mode = env
		}
		if vErr := starlarkhost.ValidateRecordMode(mode); vErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: %w", vErr)
		}
		// KITSOKI_CASSETTE_STRICT forbids recording (CI guard), mirroring the
		// agent host_cassette strict check.
		if CassetteStrictRecording() && mode != "" && mode != starlarkhost.RecordModeNone {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: KITSOKI_CASSETTE_STRICT=1 but starlark http record_mode is %q", mode)
		}

		// Replay-only uses the lightweight ReplayClient; any record mode uses a
		// RecordReplayClient backed by a real transport, flushed after the run.
		var httpClient starlarkhost.HTTPClient
		if mode == "" || mode == starlarkhost.RecordModeNone {
			httpClient = starlarkhost.NewReplayClient(&cas)
		} else {
			rrc := starlarkhost.NewRecordReplayClient(&cas, mode, starlarkhost.NewRecordingClient())
			httpClient = rrc
			rig.httpCassetteFlush = func() error { return rrc.Flush(cassettePath, "") }
		}

		// The real host.starlark.run handler must be registered to wrap it. When
		// host_bindings: is set, builtins are already registered above; otherwise
		// register it here so reg.Get finds it.
		if _, ok := reg.Get("host.starlark.run"); !ok {
			reg.Register("host.starlark.run", host.StarlarkRunHandler)
		}
		real, _ := reg.Get("host.starlark.run")
		reg.Replace("host.starlark.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
			return real(starlarkhost.WithHTTP(ctx, httpClient), args)
		})
	}

	// Wire starlark_inspect_cassette: when set. This is the Starlark fs/probe
	// replay seam, the exact sibling of starlark_http_cassette: above. We load
	// the cassette, build a ReplayInspector, and WRAP the real host.starlark.run
	// handler so it runs against the injected replay inspector. The adapter checks
	// starlarkhost.HasInspector(ctx) and skips installing a production inspector
	// when one is already present, so the ReplayInspector is the one used — no
	// file is read and no process is run.
	if fixture.StarlarkInspectCassette != "" {
		cassettePath := fixture.StarlarkInspectCassette
		if !filepath.IsAbs(cassettePath) {
			cassettePath = filepath.Join(filepath.Dir(filePath), cassettePath)
		}
		raw, rerr := os.ReadFile(cassettePath)
		if rerr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: read starlark inspect cassette: %w", rerr)
		}
		var cas starlarkhost.InspectCassette
		if uerr := yaml.Unmarshal(raw, &cas); uerr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: parse starlark inspect cassette %q: %w", cassettePath, uerr)
		}
		inspector := starlarkhost.NewReplayInspector(&cas)

		// The real host.starlark.run handler must be registered to wrap it. When
		// host_bindings: is set, builtins are already registered above; otherwise
		// register it here so reg.Get finds it.
		if _, ok := reg.Get("host.starlark.run"); !ok {
			reg.Register("host.starlark.run", host.StarlarkRunHandler)
		}
		real, _ := reg.Get("host.starlark.run")
		reg.Replace("host.starlark.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
			return real(starlarkhost.WithInspector(ctx, inspector), args)
		})
	}

	// Use a no-op harness; the orchestrator path calls RunIntent directly
	// for intent: turns and never falls through to the harness in normal use.
	h := &noopHarness{}

	// Authoritative JSONL trace for this run. The orchestrator dual-writes
	// every turn event here AND to SQLite (loadJourney still reads SQLite).
	// Unlike the SQLite events table, JSONL records state_path / call_id /
	// parent_turn, so fixture exporters get a faithful trace from it.
	//
	// When tracePath is empty the trace is a temp file that cleanup removes;
	// when the caller supplies one (fixture export), the file and its sibling
	// agent-prompts/ directory are caller-owned and left in place by cleanup.
	traceOwned := tracePath == ""
	if traceOwned {
		traceFile, traceErr := os.CreateTemp("", "kitsoki-flow-*.jsonl")
		if traceErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: create trace temp file: %w", traceErr)
		}
		tracePath = traceFile.Name()
		_ = traceFile.Close()
		_ = os.Remove(tracePath) // OpenJSONL creates the file fresh
	} else {
		if mkErr := os.MkdirAll(filepath.Dir(tracePath), 0o755); mkErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("buildOrchestratorRig: mkdir trace dir: %w", mkErr)
		}
		_ = os.Remove(tracePath) // start from a fresh trace on re-runs
	}
	eventSink, sinkErr := store.OpenJSONL(tracePath)
	if sinkErr != nil {
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: open JSONL sink: %w", sinkErr)
	}
	rig.eventSink = eventSink

	orchOpts := []orchestrator.Option{
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(js),
		// Inject the same fake clock the scheduler uses so Timeout: firings
		// run on virtual time alongside background-job delays.
		orchestrator.WithClock(clk),
		// Dual-write every turn event to the JSONL trace (authority stays with
		// SQLite for loadJourney; see WithEventSink doc).
		orchestrator.WithEventSink(eventSink),
	}
	// Wire the journal writer into the orchestrator so agent handlers
	// can write KindAgentCall entries during record-mode live calls.
	if rig.journalWriter != nil {
		orchOpts = append(orchOpts, orchestrator.WithJournalWriter(rig.journalWriter))
	}

	// Execution mode (execution-modes proposal). Default one-shot preserves
	// every existing fixture; a fixture opts into staged with `mode: staged`.
	switch fixture.Mode {
	case "", "one-shot", "oneshot":
		// one-shot is the zero value; no option needed.
	case "staged":
		orchOpts = append(orchOpts, orchestrator.WithExecutionMode(orchestrator.ExecStaged))
	default:
		_ = eventSink.Close()
		_ = os.Remove(tracePath)
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: invalid mode %q (want \"staged\" or \"one-shot\")", fixture.Mode)
	}

	orch := orchestrator.New(def, m, st, h, orchOpts...)

	sid, err := orch.NewSession(ctx)
	if err != nil {
		_ = eventSink.Close()
		_ = os.Remove(tracePath)
		_ = st.Close()
		return nil, fmt.Errorf("buildOrchestratorRig: new session: %w", err)
	}

	// Update the deferred sink used by cassette dispatchers with a real sink.
	// Cassette dispatchers were created before NewSession (before we had the session ID),
	// so they captured a deferred sink that is now updated with the real sink.
	// Agent events from cassette replay are written to the JSONL trace (the
	// authoritative trace) so they carry state_path / call_id and survive — the
	// StoreSinkAdapter.Append path only buffered them and never flushed.
	if rig.deferredAgentSink != nil {
		rig.deferredAgentSink.SetSink(eventSink)
	}

	rig.orch = orch
	rig.sched = sched
	rig.jobStore = js
	rig.st = st
	rig.sid = sid
	rig.clk = clk
	rig.cleanup = func() error {
		_ = eventSink.Close()
		if traceOwned {
			_ = os.Remove(tracePath)
		}
		return st.Close()
	}
	return &rig, nil
}

// lookupAgentCallByVerb queries the journal for the most recently written
// KindAgentCall entry for the given session and verb and returns the parsed
// body. Returns (nil, false) on any error or when no entry is found. Used by
// the cassette dispatcher in record mode — agent handlers do not return
// call_id in result.Data so we identify the entry by session + verb + rowid
// ordering (latest write wins).
func lookupAgentCallByVerb(db *sql.DB, sessionID app.SessionID, verb string) (*host.AgentCallBody, bool) {
	if db == nil || verb == "" {
		return nil, false
	}
	row := db.QueryRow(
		`SELECT body_json FROM journal
		 WHERE kind = 'agent.call'
		   AND session_id = ?
		   AND json_extract(body_json, '$.verb') = ?
		 ORDER BY rowid DESC LIMIT 1`,
		string(sessionID), verb,
	)
	var bodyStr string
	if err := row.Scan(&bodyStr); err != nil {
		return nil, false
	}
	var body host.AgentCallBody
	if err := json.Unmarshal([]byte(bodyStr), &body); err != nil {
		return nil, false
	}
	return &body, true
}

// shouldUseOrchestrator reports whether the fixture should run through the
// orchestrator-backed path instead of the legacy machine-only path.
//
// The orchestrator path is required whenever:
//   - fixture.UseOrchestrator is explicitly true, OR
//   - any host_handlers are declared (stubs must be wired to a real registry), OR
//   - any turn declares advance_clock or expect_inbox (these require the
//     scheduler + listener goroutine infrastructure).
//
// If UseOrchestrator is explicitly false, the legacy path is always used
// regardless of any other fields.
func shouldUseOrchestrator(fixture *FlowFixture) bool {
	if fixture.UseOrchestrator != nil {
		return *fixture.UseOrchestrator
	}
	if len(fixture.HostHandlers) > 0 {
		return true
	}
	if fixture.HostCassette != "" {
		return true
	}
	if fixture.StarlarkHTTPCassette != "" {
		return true
	}
	for _, t := range fixture.Turns {
		if t.AdvanceClock != "" || t.ExpectInbox != nil || len(t.ExpectJobs) > 0 {
			return true
		}
	}
	return false
}

// ─── RunFlows runs all flow fixtures matching the glob ───────────────────────

// RunFlows loads the app, finds all flow fixtures matching the glob, and runs them.
// Returns a FlowReport and non-nil error only for fatal startup errors.
func RunFlows(ctx context.Context, appPath, glob string, opts FlowOptions) (*FlowReport, error) {
	// Publish KITSOKI_APP_DIR BEFORE loading so the app yaml's loader-
	// time env-var validator can resolve `${KITSOKI_APP_DIR}` in any
	// env-expanded field (e.g. meta_modes[*].cwd). Setting the env var
	// after Load was the bug-2 ordering issue — `hally test flows`
	// then rejected a perfectly valid yaml because the var wasn't set
	// yet at validation time.
	publishAppDirForTestrunner(appPath)

	// Load app.
	def, err := app.LoadWithResolver(appPath, nil, opts.ImportResolver)
	if err != nil {
		return nil, fmt.Errorf("load app %q: %w", appPath, err)
	}

	// Build machine.
	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}

	// Find fixture files.
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", glob, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no flow fixtures matched %q", glob)
	}

	report := &FlowReport{}
	for _, f := range files {
		results, err := runFlowFile(ctx, def, m, appPath, f, opts)
		if err != nil {
			return nil, fmt.Errorf("run flow %q: %w", f, err)
		}
		report.Results = append(report.Results, results...)
		for _, r := range results {
			if r.Skipped {
				continue
			}
			if r.Passed {
				report.Passed++
			} else {
				report.Failed++
			}
		}
		if opts.FailFast && report.Failed > 0 {
			break
		}
	}

	if opts.JSONOut != "" {
		b, _ := json.MarshalIndent(report, "", "  ")
		if werr := os.WriteFile(opts.JSONOut, b, 0644); werr != nil {
			return nil, fmt.Errorf("write JSON report: %w", werr)
		}
	}

	return report, nil
}

// runFlowFile parses and runs one flow fixture file (which may contain multiple
// YAML documents separated by ---). `appPath` is the root manifest's path —
// threaded so a fixture with host_bindings: can reload the app with overrides
// for that one flow run only.
func runFlowFile(ctx context.Context, def *app.AppDef, m machine.Machine, appPath, filePath string, opts FlowOptions) ([]FlowResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", filePath, err)
	}

	// Split on --- to handle multi-document files.
	docs := splitYAMLDocs(data)
	var results []FlowResult

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var fixture FlowFixture
		if err := yaml.Unmarshal([]byte(doc), &fixture); err != nil {
			return nil, fmt.Errorf("parse fixture in %q: %w", filePath, err)
		}
		if fixture.TestKind != "flow" {
			continue // skip non-flow docs
		}

		// Per-fixture host_bindings: rebuild def + machine for this
		// fixture only. The outer def/m stay untouched so peer fixtures
		// in the same file see the original bindings. The cost is one
		// extra app.Load per fixture that declares bindings — cheap
		// (single-digit ms in the kitsoki suite) and only paid by the
		// fixtures that opt in.
		fixDef, fixM := def, m
		if len(fixture.HostBindings) > 0 {
			overriddenDef, lerr := app.LoadWithResolver(appPath, fixture.HostBindings, opts.ImportResolver)
			if lerr != nil {
				return nil, fmt.Errorf("fixture in %q: load with host_bindings: %w", filePath, lerr)
			}
			overriddenM, mErr := machine.New(overriddenDef)
			if mErr != nil {
				return nil, fmt.Errorf("fixture in %q: build machine with host_bindings: %w", filePath, mErr)
			}
			fixDef, fixM = overriddenDef, overriddenM
		}

		// host_cassette: and host_handlers: are mutually exclusive — both set
		// is a load-time error to prevent ambiguous dispatch.
		if fixture.HostCassette != "" && len(fixture.HostHandlers) > 0 {
			return nil, fmt.Errorf("fixture in %q: host_cassette and host_handlers are mutually exclusive", filePath)
		}

		result, err := runOneFlow(ctx, fixDef, fixM, filePath, &fixture, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, *result)
	}

	return results, nil
}

// splitYAMLDocs splits a YAML file into individual documents on "---" boundaries.
func splitYAMLDocs(data []byte) []string {
	return strings.Split(string(data), "\n---")
}

// runOneFlow executes a single FlowFixture. It dispatches to the
// orchestrator-backed path when shouldUseOrchestrator returns true, otherwise
// falls back to the legacy machine-only path.
func runOneFlow(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	if shouldUseOrchestrator(fixture) {
		return runOneFlowOrchestrator(ctx, def, m, filePath, fixture, opts)
	}
	return runOneFlowLegacy(ctx, def, m, filePath, fixture, opts)
}

// runOneFlowLegacy executes a single FlowFixture against the machine directly
// (the original, non-orchestrator path). Preserved unchanged for backwards
// compatibility with existing fixtures that don't declare host_handlers or
// advance_clock.
func runOneFlowLegacy(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	result := &FlowResult{File: filePath}

	// Load recording if needed.
	var replayHarness *harness.ReplayHarness
	recordingPath := fixture.Recording
	if opts.RecordingOverride != "" {
		recordingPath = opts.RecordingOverride
	}

	// Resolve recording path relative to fixture file.
	if recordingPath != "" {
		if !filepath.IsAbs(recordingPath) {
			recordingPath = filepath.Join(filepath.Dir(filePath), recordingPath)
		}
		rh, err := harness.NewReplay(recordingPath)
		if err != nil {
			if opts.AllowMissingRecording {
				result.Skipped = true
				return result, nil
			}
			return nil, fmt.Errorf("load recording %q: %w", recordingPath, err)
		}
		replayHarness = rh
	}

	// Set up initial world.
	initialWorld := machine.WorldFromSchema(def.World)
	for k, v := range fixture.InitialWorld {
		initialWorld.Vars[k] = v
	}

	// Run turns.
	currentState := app.StatePath(fixture.InitialState)
	currentWorld := initialWorld
	allEvents := []store.Event{}
	hasError := false

	for i, turn := range fixture.Turns {
		var call intent.IntentCall

		if turn.Intent != nil {
			// Structured intent — bypass recording.
			call = intent.IntentCall{
				Intent: turn.Intent.Name,
				Slots:  world.Slots(turn.Intent.Slots),
			}
		} else if turn.Input != "" {
			// Recording lookup.
			if replayHarness == nil {
				return nil, fmt.Errorf("turn %d: input %q requires a recording but none was loaded", i+1, turn.Input)
			}
			params, err := replayHarness.RunTurn(ctx, harness.TurnInput{
				StatePath: currentState,
				UserText:  turn.Input,
			})
			if err != nil {
				if opts.AllowMissingRecording {
					result.Skipped = true
					return result, nil
				}
				return nil, fmt.Errorf("turn %d: recording miss for input %q in state %q: %w", i+1, turn.Input, currentState, err)
			}
			call, err = paramsToIntentCall(params)
			if err != nil {
				return nil, fmt.Errorf("turn %d: parse recording result: %w", i+1, err)
			}
		} else {
			return nil, fmt.Errorf("turn %d: neither intent nor input is set", i+1)
		}

		// Apply world_override before guard evaluation. Mutations are
		// applied in-place to the running world so the rest of the turn (guard
		// eval, effects, view render) sees them.
		for k, v := range turn.WorldOverride {
			currentWorld.Vars[k] = v
		}

		// Snapshot pre-turn world for expect_world_unchanged.
		preTurnWorld := cloneWorldVars(currentWorld)

		// Run the machine.
		machResult, err := m.Turn(ctx, currentState, currentWorld, call)
		if err != nil {
			return nil, fmt.Errorf("turn %d: machine.Turn: %w", i+1, err)
		}

		allEvents = append(allEvents, machResult.Events...)

		tr := TurnResult{TurnIndex: i}

		// Check expect_error.
		if turn.ExpectError != nil {
			if machResult.ValidationError == nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expected error %q but got none", turn.ExpectError.Code))
			} else {
				ve := machResult.ValidationError
				if string(ve.Code) != turn.ExpectError.Code {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expected error code %q but got %q", turn.ExpectError.Code, ve.Code))
				}
				// Check allowed_contains.
				for _, expected := range turn.ExpectError.AllowedContains {
					found := false
					for _, a := range ve.AllowedIntents {
						if a == expected {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.allowed_contains: %q not in allowed list %v", expected, ve.AllowedIntents))
					}
				}
				// Check missing_slots.
				for _, ms := range turn.ExpectError.MissingSlots {
					found := false
					for _, s := range ve.MissingSlots {
						if s == ms {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.missing_slots: %q not in %v", ms, ve.MissingSlots))
					}
				}
			}
			// State should be unchanged on error.
			tr.NewState = currentState
			tr.Events = machResult.Events
			tr.Passed = len(tr.Failures) == 0
			if machResult.ValidationError != nil {
				hasError = true
			}
			result.Turns = append(result.Turns, tr)
			continue
		}

		if machResult.ValidationError != nil {
			hasError = true
			tr.Failures = append(tr.Failures, fmt.Sprintf("unexpected validation error: %s", machResult.ValidationError.Error()))
		}

		// Update state/world for successful turn.
		currentState = machResult.NewState
		currentWorld = machResult.World
		tr.NewState = currentState
		// Re-render against the landed state/world so `expect_view_matches`
		// sees what `kitsoki session show` would render after the turn
		// settles — not the stale view machine.Turn captured before any
		// host-call cascade or on_complete chain advanced the state.
		if v, rErr := m.RenderState(currentState, currentWorld); rErr == nil {
			tr.View = v
		} else {
			tr.View = machResult.View
		}
		tr.Events = machResult.Events

		// Apply assertions.
		if turn.ExpectState != "" {
			if string(currentState) != turn.ExpectState {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state: got %q, want %q", currentState, turn.ExpectState))
			}
		}
		if turn.ExpectNotState != "" {
			if string(currentState) == turn.ExpectNotState {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_not_state: state must not be %q", turn.ExpectNotState))
			}
		}
		if len(turn.ExpectStateIn) > 0 {
			found := false
			for _, s := range turn.ExpectStateIn {
				if string(currentState) == s {
					found = true
					break
				}
			}
			if !found {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state_in: %q not in %v", currentState, turn.ExpectStateIn))
			}
		}

		// expect_world: subset match.
		for k, expected := range turn.ExpectWorld {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world[%q]: got %v (%T), want %v (%T)", k, got, got, expected, expected))
			}
		}

		// expect_world_full: full match.
		if len(turn.ExpectWorldFull) > 0 {
			for k, expected := range turn.ExpectWorldFull {
				got := currentWorld.Vars[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full[%q]: got %v, want %v", k, got, expected))
				}
			}
			for k := range currentWorld.Vars {
				if _, ok := turn.ExpectWorldFull[k]; !ok {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full: unexpected key %q in world", k))
				}
			}
		}

		// expect_world_unchanged.
		if turn.ExpectWorldUnchanged != nil && *turn.ExpectWorldUnchanged {
			for k, before := range preTurnWorld {
				after := currentWorld.Vars[k]
				if !deepEqualValues(before, after) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_unchanged: world[%q] changed from %v to %v", k, before, after))
				}
			}
		}

		// expect_events: ordered subsequence.
		if len(turn.ExpectEvents) > 0 {
			if errs := assertEventsSubsequence(tr.Events, turn.ExpectEvents); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_events_exact: full ordered match.
		if len(turn.ExpectEventsExact) > 0 {
			if errs := assertEventsExact(tr.Events, turn.ExpectEventsExact); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_host_calls / expect_no_host_calls.
		if len(turn.ExpectHostCalls) > 0 {
			if errs := assertHostCalls(tr.Events, turn.ExpectHostCalls); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		if len(turn.ExpectNoHostCalls) > 0 {
			if errs := assertNoHostCalls(tr.Events, turn.ExpectNoHostCalls); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_view_matches.
		if turn.ExpectViewMatches != "" {
			re, reErr := regexp.Compile(turn.ExpectViewMatches)
			if reErr != nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: invalid regex %q: %v", turn.ExpectViewMatches, reErr))
			} else if !re.MatchString(tr.View) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: view does not match %q\nview: %q", turn.ExpectViewMatches, tr.View))
			}
		}

		// expect_no_view.
		if turn.ExpectNoView != nil && *turn.ExpectNoView {
			if tr.View != "" {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_no_view: view is not empty: %q", tr.View))
			}
		}

		// expect_slots — assert on accepted slots (we don't have a clean path here
		// unless we compare the call slots; do that).
		if len(turn.ExpectSlots) > 0 {
			for k, expected := range turn.ExpectSlots {
				got := call.Slots[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_slots[%q]: got %v, want %v", k, got, expected))
				}
			}
		}

		tr.Passed = len(tr.Failures) == 0
		result.Turns = append(result.Turns, tr)

		if opts.FailFast && !tr.Passed {
			break
		}
	}

	// Session-level assertions.
	sessionFailures := []string{}

	if fixture.ExpectTerminal != nil && *fixture.ExpectTerminal {
		// Look up whether current state is terminal.
		stateIsTerminal := isTerminalState(def, currentState)
		if !stateIsTerminal {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_terminal: state %q is not terminal", currentState))
		}
	}

	if fixture.ExpectEventsCountAtLeast != nil {
		if len(allEvents) < *fixture.ExpectEventsCountAtLeast {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atleast: got %d events, want >= %d", len(allEvents), *fixture.ExpectEventsCountAtLeast))
		}
	}
	if fixture.ExpectEventsCountAtMost != nil {
		if len(allEvents) > *fixture.ExpectEventsCountAtMost {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atmost: got %d events, want <= %d", len(allEvents), *fixture.ExpectEventsCountAtMost))
		}
	}

	if fixture.ExpectNoErrors != nil {
		if *fixture.ExpectNoErrors && hasError {
			sessionFailures = append(sessionFailures, "expect_no_errors: there were validation errors")
		}
	}

	if len(fixture.ExpectWorldFinal) > 0 {
		for k, expected := range fixture.ExpectWorldFinal {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				sessionFailures = append(sessionFailures, fmt.Sprintf("expect_world_final[%q]: got %v, want %v", k, got, expected))
			}
		}
	}
	// Fixture-level expect_no_host_calls + expect_files. Same shape as
	// the orchestrator path above; lives here so legacy-path fixtures
	// can also assert filesystem side effects and no-spurious-call guards.
	if len(fixture.ExpectNoHostCalls) > 0 {
		sessionFailures = append(sessionFailures, assertNoHostCalls(allEvents, fixture.ExpectNoHostCalls)...)
	}
	if len(fixture.ExpectFiles) > 0 {
		sessionFailures = append(sessionFailures, assertExpectFiles(filepath.Dir(filePath), fixture.ExpectFiles)...)
	}

	// Compute overall pass.
	allTurnsPassed := true
	for _, tr := range result.Turns {
		if !tr.Passed {
			allTurnsPassed = false
			break
		}
	}
	result.Passed = allTurnsPassed && len(sessionFailures) == 0

	// Append session-level failures as a synthetic turn.
	if len(sessionFailures) > 0 {
		result.Turns = append(result.Turns, TurnResult{
			TurnIndex: -1,
			Passed:    false,
			Failures:  sessionFailures,
		})
	}

	return result, nil
}

// runOneFlowOrchestrator executes a single FlowFixture through the
// orchestrator-backed path. It:
//   - Builds an in-memory SQLite store + fake clock + job scheduler + host
//     registry (from fixture.HostHandlers).
//   - Seeds InitialWorld by applying world_override on the first turn (or
//     directly before the first RunIntent call).
//   - Runs each turn by calling orch.RunIntent for intent: turns.
//   - After each turn, if AdvanceClock is set: advances the fake clock then
//     waits for the scheduler and listener to drain.
//   - Applies all the same turn assertions as the legacy path, plus
//     ExpectInbox assertions (world-based via $inbox counts from jobStore).
//
// The orchestrator path is fully backwards-compatible with fixture fields that
// don't mention scheduling: a fixture that is auto-upgraded (e.g. because it
// sets host_handlers) but doesn't use advance_clock or expect_inbox will
// simply run through the orchestrator and apply the same assertions.
func runOneFlowOrchestrator(ctx context.Context, def *app.AppDef, m machine.Machine, filePath string, fixture *FlowFixture, opts FlowOptions) (*FlowResult, error) {
	result := &FlowResult{File: filePath}

	// Build the rig (store + scheduler + orchestrator).
	rig, err := buildOrchestratorRig(ctx, def, m, fixture, filePath, opts.TracePath)
	if err != nil {
		return nil, fmt.Errorf("runOneFlowOrchestrator: %w", err)
	}
	defer func() { _ = rig.cleanup() }()

	// Seed InitialWorld by teleporting the session into the right initial state
	// and world. We use orch.Teleport if available; otherwise we fall back to
	// injecting world via a world_override on the first turn combined with using
	// initial_state as the session's starting state.
	//
	// The simplest approach: persist a synthetic EffectApplied event for each
	// initial-world key and a StateTransitioned event for the initial state before
	// any turns run.  The orchestrator's loadJourney will replay these so the first
	// turn sees the right initial conditions.
	if err := seedInitialState(ctx, rig, def, fixture); err != nil {
		return nil, fmt.Errorf("runOneFlowOrchestrator: seed initial state: %w", err)
	}

	// Run turns.
	allEvents := []store.Event{}
	hasError := false

	for i, turn := range fixture.Turns {
		var (
			outcome *orchestrator.TurnOutcome
			turnErr error
			call    intent.IntentCall
		)

		// Pre-turn job snapshot for expect_jobs diffing. We capture the set
		// of every job ID currently in the store along with its status; a
		// "newly-terminal" job after the turn is one whose ID was either
		// not present, or was present in a non-terminal status, but now
		// holds a terminal status. Cheap relative to the DB calls already
		// happening per turn.
		preTurnJobStatus := map[jobs.JobID]jobs.JobStatus{}
		if len(turn.ExpectJobs) > 0 {
			snapshot, snapErr := rig.jobStore.ListBySession(ctx, rig.sid)
			if snapErr != nil {
				return nil, fmt.Errorf("turn %d: pre-turn job snapshot: %w", i+1, snapErr)
			}
			for _, j := range snapshot {
				preTurnJobStatus[j.ID] = j.Status
			}
		}

		if turn.Intent != nil {
			// Load the current world before building the intent call so that
			// slot values containing {{ world.* }} templates can be expanded.
			preJ, preJErr := rig.orch.LoadJourney(rig.sid)
			if preJErr != nil {
				return nil, fmt.Errorf("turn %d: load world for slot rendering: %w", i+1, preJErr)
			}

			// Update the shared state pointer so cassette dispatchers see the
			// current state during this turn's RunIntent call. Background-job
			// handlers dispatched during this turn read this value at invocation
			// time; the turn loop updates it again before the next turn.
			rig.currentStatePath = preJ.State

			resolvedSlots := renderSlots(turn.Intent.Slots, preJ.World)
			call = intent.IntentCall{
				Intent: turn.Intent.Name,
				Slots:  world.Slots(resolvedSlots),
			}

			// Apply world_override before the turn so the machine sees it.
			// In the orchestrator path we do this by injecting a synthetic
			// EffectApplied event before calling RunIntent.
			if len(turn.WorldOverride) > 0 {
				if woErr := injectWorldOverride(ctx, rig, turn.WorldOverride); woErr != nil {
					return nil, fmt.Errorf("turn %d: world_override: %w", i+1, woErr)
				}
			}

			outcome, turnErr = rig.orch.RunIntentWithInput(ctx, rig.sid, call.Intent, map[string]any(call.Slots), turn.DisplayInput)
		} else if turn.Input != "" {
			// input: turns in the orchestrator path require a recording to resolve the
			// intent. For now we return an error — the orchestrator path is only used
			// for intent: turns (host_handlers, advance_clock). If someone wants
			// recording-resolved turns with orchestrator features they should add a static
			// harness; that's a future enhancement.
			return nil, fmt.Errorf("turn %d: input: %q in orchestrator-path fixture; use intent: instead (recording routing not yet supported on orchestrator path)", i+1, turn.Input)
		} else if turn.AdvanceClock != "" {
			// Clock-only turn: no user input, just advance virtual time.  Used
			// by Timeout: fixtures that need to fire a synthetic
			// transition without first issuing a user intent.  Synthesise an
			// empty TurnOutcome reflecting the current state so the assertion
			// logic below can run.
			preJ, preJErr := rig.orch.LoadJourney(rig.sid)
			if preJErr != nil {
				return nil, fmt.Errorf("turn %d: load journey for clock-only turn: %w", i+1, preJErr)
			}
			outcome = &orchestrator.TurnOutcome{
				Mode:       orchestrator.ModeTransitioned,
				NewState:   preJ.State,
				TurnNumber: preJ.Turn,
			}
		} else {
			return nil, fmt.Errorf("turn %d: turn requires one of intent:, input:, or advance_clock", i+1)
		}

		if turnErr != nil {
			return nil, fmt.Errorf("turn %d: RunIntent: %w", i+1, turnErr)
		}

		allEvents = append(allEvents, outcome.Events...)

		// AdvanceClock: move the fake clock forward, then wait for scheduler + listener.
		if turn.AdvanceClock != "" {
			// app.ParseDuration accepts Go-std durations plus Nd (days), so
			// Timeout: fixtures can write "11d" for the canonical OT case.
			d, parseErr := app.ParseDuration(turn.AdvanceClock)
			if parseErr != nil {
				return nil, fmt.Errorf("turn %d: advance_clock %q: %w", i+1, turn.AdvanceClock, parseErr)
			}
			if d > 0 {
				waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if advErr := advanceAndWait(waitCtx, rig, d); advErr != nil {
					cancel()
					return nil, fmt.Errorf("turn %d: advance_clock: %w", i+1, advErr)
				}
				cancel()
			}
		}

		tr := TurnResult{TurnIndex: i}

		// Reload journey to get the post-completion world (after on_complete fires).
		journey, loadErr := rig.orch.LoadJourney(rig.sid)
		if loadErr != nil {
			return nil, fmt.Errorf("turn %d: load journey: %w", i+1, loadErr)
		}
		currentState := journey.State
		currentWorld := journey.World

		// Resolve the pre-turn world snapshot for expect_world_unchanged.
		// Since we apply world_override before RunIntent, we need the world
		// before world_override injection. We use the state before injection.
		// For simplicity: the pre-turn world is the world immediately after
		// the previous turn's journey reload (we already track it above).
		// We'll capture it here by reloading before the turn's assertions run.

		// check expect_error: in the orchestrator path ValidationError manifests
		// as ModeRejected in the outcome.
		if turn.ExpectError != nil {
			if outcome.Mode != orchestrator.ModeRejected {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expected error %q but got none (mode=%s)", turn.ExpectError.Code, outcome.Mode))
			} else {
				if string(outcome.ErrorCode) != turn.ExpectError.Code {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error: code got %q, want %q", outcome.ErrorCode, turn.ExpectError.Code))
				}
				for _, expected := range turn.ExpectError.AllowedContains {
					found := false
					for _, a := range outcome.AllowedIntents {
						if a == expected {
							found = true
							break
						}
					}
					if !found {
						tr.Failures = append(tr.Failures, fmt.Sprintf("expect_error.allowed_contains: %q not in %v", expected, outcome.AllowedIntents))
					}
				}
			}
			tr.NewState = currentState
			tr.Events = outcome.Events
			tr.Passed = len(tr.Failures) == 0
			if outcome.Mode == orchestrator.ModeRejected {
				hasError = true
			}
			result.Turns = append(result.Turns, tr)
			continue
		}

		if outcome.Mode == orchestrator.ModeRejected {
			hasError = true
			tr.Failures = append(tr.Failures, fmt.Sprintf("unexpected rejection: code=%s hint=%s", outcome.ErrorCode, outcome.GuardHint))
		}

		tr.NewState = currentState
		// Use the outcome view when available — it reflects runtime-injected
		// content (e.g. the on_error redirect error banner) that a bare
		// template re-render cannot reproduce. Fall back to re-rendering
		// against the post-completion state/world when the outcome has no
		// view (e.g. clock-only turns or turns where the view was not yet
		// captured at return time).
		if outcome.View != "" {
			tr.View = outcome.View
		} else if v, rErr := rig.orch.RenderState(currentState, currentWorld); rErr == nil {
			tr.View = v
		}
		tr.Events = outcome.Events

		// expect_state.
		if turn.ExpectState != "" && string(currentState) != turn.ExpectState {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state: got %q, want %q", currentState, turn.ExpectState))
		}
		// expect_not_state.
		if turn.ExpectNotState != "" && string(currentState) == turn.ExpectNotState {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_not_state: state must not be %q", turn.ExpectNotState))
		}
		// expect_state_in.
		if len(turn.ExpectStateIn) > 0 {
			found := false
			for _, s := range turn.ExpectStateIn {
				if string(currentState) == s {
					found = true
					break
				}
			}
			if !found {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_state_in: %q not in %v", currentState, turn.ExpectStateIn))
			}
		}
		// expect_world: subset match.
		for k, expected := range turn.ExpectWorld {
			got := currentWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world[%q]: got %v (%T), want %v (%T)", k, got, got, expected, expected))
			}
		}
		// expect_world_full.
		if len(turn.ExpectWorldFull) > 0 {
			for k, expected := range turn.ExpectWorldFull {
				got := currentWorld.Vars[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full[%q]: got %v, want %v", k, got, expected))
				}
			}
			for k := range currentWorld.Vars {
				if _, ok := turn.ExpectWorldFull[k]; !ok {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_world_full: unexpected key %q in world", k))
				}
			}
		}
		// expect_events / expect_events_exact.
		if len(turn.ExpectEvents) > 0 {
			if errs := assertEventsSubsequence(tr.Events, turn.ExpectEvents); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		if len(turn.ExpectEventsExact) > 0 {
			if errs := assertEventsExact(tr.Events, turn.ExpectEventsExact); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		// expect_host_calls / expect_no_host_calls — sugar over
		// HostDispatched event matching.
		if len(turn.ExpectHostCalls) > 0 {
			if errs := assertHostCalls(tr.Events, turn.ExpectHostCalls); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		if len(turn.ExpectNoHostCalls) > 0 {
			if errs := assertNoHostCalls(tr.Events, turn.ExpectNoHostCalls); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}
		// expect_view_matches.
		if turn.ExpectViewMatches != "" {
			re, reErr := regexp.Compile(turn.ExpectViewMatches)
			if reErr != nil {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: invalid regex %q: %v", turn.ExpectViewMatches, reErr))
			} else if !re.MatchString(tr.View) {
				tr.Failures = append(tr.Failures, fmt.Sprintf("expect_view_matches: view does not match %q\nview: %q", turn.ExpectViewMatches, tr.View))
			}
		}
		// expect_no_view.
		if turn.ExpectNoView != nil && *turn.ExpectNoView && tr.View != "" {
			tr.Failures = append(tr.Failures, fmt.Sprintf("expect_no_view: view is not empty: %q", tr.View))
		}
		// expect_slots.
		if len(turn.ExpectSlots) > 0 {
			for k, expected := range turn.ExpectSlots {
				got := call.Slots[k]
				if !deepEqualValues(got, expected) {
					tr.Failures = append(tr.Failures, fmt.Sprintf("expect_slots[%q]: got %v, want %v", k, got, expected))
				}
			}
		}

		// expect_inbox: query unread counts from the job store.
		if turn.ExpectInbox != nil {
			if errs := assertInbox(ctx, rig.jobStore, rig.sid, turn.ExpectInbox); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		// expect_jobs: diff against the pre-turn snapshot and match each
		// expected entry against a newly-terminal job by namespace.
		if len(turn.ExpectJobs) > 0 {
			if errs := assertJobs(ctx, rig.jobStore, rig.sid, preTurnJobStatus, turn.ExpectJobs); len(errs) > 0 {
				tr.Failures = append(tr.Failures, errs...)
			}
		}

		tr.Passed = len(tr.Failures) == 0
		result.Turns = append(result.Turns, tr)

		if opts.FailFast && !tr.Passed {
			break
		}
	}

	// Session-level assertions.
	sessionFailures := []string{}

	// Re-load final journey state.
	finalJourney, loadErr := rig.orch.LoadJourney(rig.sid)
	if loadErr != nil {
		return nil, fmt.Errorf("load final journey: %w", loadErr)
	}
	finalState := finalJourney.State
	finalWorld := finalJourney.World

	if fixture.ExpectTerminal != nil && *fixture.ExpectTerminal {
		if !isTerminalState(def, finalState) {
			sessionFailures = append(sessionFailures, fmt.Sprintf("expect_terminal: state %q is not terminal", finalState))
		}
	}
	if fixture.ExpectEventsCountAtLeast != nil && len(allEvents) < *fixture.ExpectEventsCountAtLeast {
		sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atleast: got %d events, want >= %d", len(allEvents), *fixture.ExpectEventsCountAtLeast))
	}
	if fixture.ExpectEventsCountAtMost != nil && len(allEvents) > *fixture.ExpectEventsCountAtMost {
		sessionFailures = append(sessionFailures, fmt.Sprintf("expect_events_count_atmost: got %d events, want <= %d", len(allEvents), *fixture.ExpectEventsCountAtMost))
	}
	if fixture.ExpectNoErrors != nil && *fixture.ExpectNoErrors && hasError {
		sessionFailures = append(sessionFailures, "expect_no_errors: there were validation errors")
	}
	if len(fixture.ExpectWorldFinal) > 0 {
		for k, expected := range fixture.ExpectWorldFinal {
			got := finalWorld.Vars[k]
			if !deepEqualValues(got, expected) {
				sessionFailures = append(sessionFailures, fmt.Sprintf("expect_world_final[%q]: got %v, want %v", k, got, expected))
			}
		}
	}
	// Fixture-level expect_no_host_calls: scan every event collected
	// across the run, not just the final turn's slice. This catches a
	// spurious call that fires anywhere in the flow.
	if len(fixture.ExpectNoHostCalls) > 0 {
		sessionFailures = append(sessionFailures, assertNoHostCalls(allEvents, fixture.ExpectNoHostCalls)...)
	}
	// Fixture-level expect_files: filesystem assertions resolved against
	// the fixture's own directory so authors write paths relative to
	// where the fixture lives.
	if len(fixture.ExpectFiles) > 0 {
		sessionFailures = append(sessionFailures, assertExpectFiles(filepath.Dir(filePath), fixture.ExpectFiles)...)
	}

	// Persist any newly recorded Starlark HTTP exchanges back to the cassette
	// file (no-op for replay-only runs). Done before the orphan check / cleanup
	// so a record run leaves the regenerated cassette on disk.
	if rig.httpCassetteFlush != nil {
		if ferr := rig.httpCassetteFlush(); ferr != nil {
			sessionFailures = append(sessionFailures, fmt.Sprintf("flush starlark http cassette: %v", ferr))
		}
	}

	// Post-run cassette orphan check: when strict_cassette_coverage: true is
	// declared, any episode that was never matched at least once is a phantom
	// episode the test never exercised.
	if rig.cassette != nil {
		if unmatched := rig.cassette.UnmatchedEpisodes(); len(unmatched) > 0 {
			sessionFailures = append(sessionFailures,
				fmt.Sprintf("unexpected remaining episodes: %v", unmatched))
		}
	}

	allTurnsPassed := true
	for _, tr := range result.Turns {
		if !tr.Passed {
			allTurnsPassed = false
			break
		}
	}
	result.Passed = allTurnsPassed && len(sessionFailures) == 0

	if len(sessionFailures) > 0 {
		result.Turns = append(result.Turns, TurnResult{
			TurnIndex: -1,
			Passed:    false,
			Failures:  sessionFailures,
		})
	}

	if opts.OnRigClose != nil {
		if err := opts.OnRigClose(filePath, rig.st, rig.sid, rig.eventSink); err != nil {
			return nil, fmt.Errorf("OnRigClose: %w", err)
		}
	}

	return result, nil
}

// ─── Orchestrator path helpers ────────────────────────────────────────────────

// seedInitialState writes synthetic events that set the session's starting
// state and world to match fixture.InitialState and fixture.InitialWorld.
// The orchestrator's loadJourney replays the event log, so persisting these
// events before any turns is sufficient to bootstrap the session.
func seedInitialState(ctx context.Context, rig *orchRig, def *app.AppDef, fixture *FlowFixture) error {
	if fixture.InitialState == "" && len(fixture.InitialWorld) == 0 {
		return nil
	}

	// Build a set of synthetic seed events at turn 0.
	var events []store.Event

	// TransitionApplied moves the journey to the desired initial state.
	// BuildJourney interprets {"to": "..."} to update js.State.
	if fixture.InitialState != "" {
		events = append(events, store.Event{
			Kind: store.TransitionApplied,
			Turn: 0,
			Payload: mustJSON(map[string]any{
				"from":   "",
				"to":     fixture.InitialState,
				"intent": "__seed__",
			}),
		})
	}

	// Apply each initial-world key as an EffectApplied event.
	for k, v := range fixture.InitialWorld {
		events = append(events, store.Event{
			Kind: store.EffectApplied,
			Turn: 0,
			Payload: mustJSON(map[string]any{
				"set": map[string]any{k: v},
			}),
		})
	}

	// Stamp state_path on the synthetic seed events so the turn-0 group in the
	// runstatus trace UI resolves to the initial state's phase rather than the
	// "—" fallback. The seed transition lands the session in InitialState, so
	// every seed event is attributed to that state.
	if fixture.InitialState != "" {
		for i := range events {
			if events[i].StatePath == "" {
				events[i].StatePath = app.StatePath(fixture.InitialState)
			}
		}
	}

	// Persist seed events via StoreSinkAdapter (wave-2a seam).
	sink := store.NewStoreSinkAdapter(rig.st, rig.sid)
	if err := sink.AppendBatch(events); err != nil {
		return err
	}

	// Arm any Timeout: declared on the seeded initial state.  Without this,
	// fixtures whose initial_state has a Timeout would not see it fire on
	// advance_clock, because seed events bypass the orchestrator's normal
	// transition path that calls armTimeoutForState.
	if fixture.InitialState != "" {
		rig.orch.ArmTimeoutForInitialState(rig.sid, app.StatePath(fixture.InitialState))
	}
	return nil
}

// injectWorldOverride writes a set of EffectApplied events for each
// world_override key so the orchestrator's next journey load reflects the
// override.
//
// Turn allocation: the override events are stamped with a fresh side-channel
// turn = journey.Turn + 1 (i.e. one past the highest turn in the event log).
// This mirrors the off-path appender in internal/orchestrator/offpath.go,
// which faces the same constraint: the events table's PRIMARY KEY is
// (session_id, turn, seq) and appendEventsTx overwrites Seq with a monotonic
// 0..N-1 starting from 0 per call, so two AppendEvents calls that share a
// turn number with any already-persisted events collide on the PK.
//
// Semantically these events sit *between* turn N-1 and the upcoming turn N:
// they belong to neither, but live as a "side-channel patch" between them.
// The orchestrator's next RunIntent recomputes turnNum = journey.Turn + 1
// from a fresh loadJourney, so it will pick up override.Turn + 1 naturally —
// the override and the next foreground turn cannot collide.
//
// The pre-existing bug was Turn: 0 hard-coded: the FIRST call worked because
// seedInitialState wrote turn-0 events with seq 0..N-1 and the override then
// appended at turn 0 with seq RESET to 0..M-1 (PK collision); SQLite raised
// the constraint error on every override after the first. The earliest
// known passing call (oregon-trail's winning_deterministic.yaml has 6
// world_override blocks) didn't hit the bug because that fixture has no
// host_handlers — it runs through the static-harness path (line ~573 in
// this file) which mutates currentWorld.Vars in-place without touching the
// event log at all.
func injectWorldOverride(ctx context.Context, rig *orchRig, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	// Allocate a fresh turn = max(history)+1, identical to the off-path
	// appender's strategy. loadJourney already walks the event log and
	// records the highest turn it saw, so we don't pay an extra DB hit.
	preJ, err := rig.orch.LoadJourney(rig.sid)
	if err != nil {
		return fmt.Errorf("injectWorldOverride: load journey: %w", err)
	}
	overrideTurn := preJ.Turn + 1
	events := make([]store.Event, 0, len(overrides))
	for k, v := range overrides {
		events = append(events, store.Event{
			Kind: store.EffectApplied,
			Turn: overrideTurn,
			Payload: mustJSON(map[string]any{
				"set": map[string]any{k: v},
			}),
		})
	}
	// Persist world-override events via StoreSinkAdapter (wave-2a seam).
	sink := store.NewStoreSinkAdapter(rig.st, rig.sid)
	return sink.AppendBatch(events)
}

// waitForJobsParked blocks until every currently-running job goroutine has
// either registered a clock waiter (the canonical handler shape: the first
// thing a host stub does is clock.Sleep(delay)) or has terminated on its own
// without touching the clock (e.g. infra_error stubs with no delay).  It is
// the race-free pre-condition for a safe rig.clk.Advance(d) call.
//
// Implementation: race two waits — clock.BlockUntilContext(rc) returns when
// every running job is parked; scheduler.WaitIdle returns when every running
// job has terminated.  Whichever fires first is sufficient: in both cases
// Advance is safe (it either fires the parked waiters or is a no-op).  If the
// running count snapshot drops mid-wait (a non-clock job finishes while a
// clock job has yet to park), the WaitIdle goroutine eventually catches up;
// loop on the running count until either both barriers agree or ctx fires.
func waitForJobsParked(ctx context.Context, rig *orchRig) error {
	for {
		rc := rig.sched.RunningCount()
		if rc == 0 {
			return nil
		}
		// Already parked? Skip the goroutine setup.
		if rig.clk.WaitCount() >= rc {
			return nil
		}

		// Race the two barriers.  parkCtx is cancelled when either fires (or
		// when ctx fires) so the loser's goroutine returns promptly.
		parkCtx, cancel := context.WithCancel(ctx)
		parked := make(chan struct{})
		idle := make(chan struct{})

		go func() {
			_ = rig.clk.BlockUntilContext(parkCtx, rc)
			close(parked)
		}()
		go func() {
			_ = rig.sched.WaitIdle(parkCtx)
			close(idle)
		}()

		select {
		case <-parked:
		case <-idle:
		case <-ctx.Done():
			cancel()
			<-parked
			<-idle
			return ctx.Err()
		}
		cancel()
		<-parked
		<-idle

		// Re-check: if a job terminated without ever parking, runningCount may
		// have dropped while we waited.  Loop and re-evaluate.
		if rig.sched.RunningCount() == 0 || rig.clk.WaitCount() >= rig.sched.RunningCount() {
			return nil
		}
		// Otherwise, loop with the new (lower) running-count snapshot.
	}
}

// advanceAndWait advances the fake clock by d, then drains the scheduler and
// session listener with the given context deadline.
//
// The drain algorithm is entirely event-driven — no real-time sleep is used,
// which eliminates the 50 ms flake window that appeared on slow CI:
//
//  1. Subscribe to the session's job-event channel before advancing, so we
//     never miss events that arrive between the Advance call and our first
//     channel read.
//  2. Park barrier: wait until every still-running job goroutine has registered
//     itself as a clock waiter (or has terminated on its own).  Without this
//     barrier there is a race where Submit's goroutine has incremented
//     runningCount but has not yet called clock.Sleep — Advance fires no
//     waiter, time moves forward, the goroutine then registers a waiter whose
//     deadline is in the past relative to wall-clock-of-test but in the future
//     relative to (already-bumped) fake time, and WaitIdle hangs until ctx
//     deadline.  See clock.Fake.BlockUntilContext for the canonical fix.
//  3. Advance the fake clock.  This unblocks any timers whose deadline ≤
//     now+d, including RequestClarification's poll timer for a resumed job.
//  4. Wait for the scheduler to be idle (WaitIdle): all job goroutines are
//     either terminal or awaiting_input.
//  5. Wait for the orchestrator listener to process all queued terminal events
//     (WaitListenerIdle).  on_complete effects run inside this call.
//  6. Drain any remaining session-channel events (on_complete may have
//     dispatched new jobs whose terminal events arrive here).  For each
//     event, repeat steps 4–5 so cascading on_complete chains fully settle.
//  7. After WaitIdle + WaitListenerIdle both return with the channel empty,
//     the system is quiescent; return nil.
//
// The outer context (typically 5 s) is the hard deadline; ctx.Err() is
// returned if it fires before the drain completes.
// clarificationPollTick nudges the fake clock past host.RequestClarification's
// 200ms poll interval so an answered-but-not-yet-resumed job's poll loop fires.
const clarificationPollTick = 250 * time.Millisecond

// resumeInFlight reports whether a clarification answer has been stored (the DB
// row is back to `running`) but the scheduler is still idle — i.e. the handler's
// poll loop hasn't observed the answer and called Resumed yet. Such a job WILL
// resume and reach terminal, so the drain must not declare the turn settled.
func resumeInFlight(ctx context.Context, rig *orchRig) bool {
	if rig.jobStore == nil {
		return false
	}
	running, err := rig.jobStore.ListJobsByStatus(ctx, rig.sid, jobs.JobRunning)
	if err != nil {
		return false
	}
	return len(running) > 0
}

func advanceAndWait(ctx context.Context, rig *orchRig, d time.Duration) error {
	if err := waitForJobsParked(ctx, rig); err != nil {
		return fmt.Errorf("park barrier: %w", err)
	}
	rig.clk.Advance(d)

	// Drain loop: WaitIdle + WaitListenerIdle each cover one barrier; together
	// they guarantee "no jobs running" + "all events fanned out have been
	// processed by the listener".  Cascading on_complete chains can dispatch
	// new jobs during the wait, so we loop until IsIdle() reports a stable
	// no-running state after both barriers cleared.
	const maxIter = 32
	for i := 0; i < maxIter; i++ {
		if err := rig.sched.WaitIdle(ctx); err != nil {
			return fmt.Errorf("scheduler WaitIdle: %w", err)
		}
		if err := rig.orch.WaitListenerIdle(ctx, rig.sid); err != nil {
			return fmt.Errorf("listener WaitListenerIdle: %w", err)
		}
		// Drain any due Timeout: firings.  The timeout dispatcher
		// runs its synthetic turns on independent goroutines, so neither
		// scheduler.WaitIdle nor orch.WaitListenerIdle covers them.
		if err := rig.orch.WaitTimeoutsDrained(ctx, rig.sid); err != nil {
			return fmt.Errorf("timeout dispatcher WaitTimeoutsDrained: %w", err)
		}
		// If on_complete didn't dispatch any new background work during the
		// listener's processing, runningCount is still zero and we are done.
		// Otherwise loop: the next WaitIdle blocks until the cascading job
		// finishes; the next WaitListenerIdle drains its event.
		if rig.sched.IsIdle() {
			// Clarification-resume race: host.jobs.answer_clarification flips the
			// DB row to `running`, but the handler's poll loop only re-registers
			// the job as running (Resumed → runningCount++) on its next 200ms
			// clock tick. Between answer-storage and that tick the scheduler
			// briefly reports idle while a resume is in flight, so WaitIdle can
			// return before the resumed handler completes — the terminal event
			// then lands after this drain returns and `expect_jobs: status done`
			// races (the documented clarification race, worse under load). Detect
			// it via the DB (status=running while the scheduler is idle) and nudge
			// the fake clock so the poll loop wakes, resumes, and completes before
			// we declare the turn drained.
			if resumeInFlight(ctx, rig) {
				rig.clk.Advance(clarificationPollTick)
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("advanceAndWait: drain did not stabilize after %d iterations", maxIter)
}

// assertInbox checks inbox notification counts against the expectation.
func assertInbox(ctx context.Context, js *jobs.JobStore, sid app.SessionID, exp *FlowInboxExpectation) []string {
	var failures []string
	if exp == nil || js == nil {
		return failures
	}

	counts, err := js.UnreadCount(ctx, sid)
	if err != nil {
		return []string{fmt.Sprintf("expect_inbox: UnreadCount: %v", err)}
	}

	total := 0
	for _, cnt := range counts {
		total += cnt
	}

	if exp.Unread != nil && total != *exp.Unread {
		failures = append(failures, fmt.Sprintf("expect_inbox.unread: got %d, want %d", total, *exp.Unread))
	}

	if exp.NeedsAttention != nil {
		attn := counts[jobs.SeverityActionRequired]
		if attn != *exp.NeedsAttention {
			failures = append(failures, fmt.Sprintf("expect_inbox.needs_attention: got %d, want %d", attn, *exp.NeedsAttention))
		}
	}

	if len(exp.Severities) > 0 {
		var got []string
		for sev, cnt := range counts {
			for j := 0; j < cnt; j++ {
				got = append(got, string(sev))
			}
		}
		sort.Strings(got)
		want := make([]string, len(exp.Severities))
		copy(want, exp.Severities)
		sort.Strings(want)

		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			failures = append(failures, fmt.Sprintf("expect_inbox.severities: got %v, want %v", got, exp.Severities))
		}
	}

	return failures
}

// terminalJobStatuses is the set of job statuses considered terminal by
// expect_jobs. JobAwaitingInput is included so a fixture can explicitly
// assert that a job paused for clarification (without continuing past it).
var terminalJobStatuses = map[jobs.JobStatus]bool{
	jobs.JobDone:          true,
	jobs.JobFailed:        true,
	jobs.JobCancelled:     true,
	jobs.JobAwaitingInput: true,
}

// finalJobStatuses is the subset of terminal statuses that represent a job's
// permanently final state. Used in the pre-turn snapshot diff: a job
// transitioning from awaiting_input → done is "newly terminal" this turn, so
// awaiting_input must NOT count as "already terminal" for diffing purposes
// (otherwise the resume-and-complete turn would see zero newly-terminal jobs).
var finalJobStatuses = map[jobs.JobStatus]bool{
	jobs.JobDone:      true,
	jobs.JobFailed:    true,
	jobs.JobCancelled: true,
}

// validExpectJobStatuses is the set of strings accepted in expect_jobs.status,
// matched against jobs.JobStatus values. Kept in sync with terminalJobStatuses.
var validExpectJobStatuses = map[string]bool{
	string(jobs.JobDone):          true,
	string(jobs.JobFailed):        true,
	string(jobs.JobCancelled):     true,
	string(jobs.JobAwaitingInput): true,
}

// assertJobs implements the expect_jobs assertion. It re-reads the job store,
// computes the set of jobs that newly reached a terminal status during this
// turn (relative to preTurnStatus), and matches each expected entry to the
// next unmatched newly-terminal job whose Kind == Namespace in creation-time
// order. See the ExpectJob doc-comment for the full matching contract.
func assertJobs(ctx context.Context, js *jobs.JobStore, sid app.SessionID, preTurnStatus map[jobs.JobID]jobs.JobStatus, expected []ExpectJob) []string {
	if len(expected) == 0 || js == nil {
		return nil
	}

	// Validate expected status strings up front so a typo in the fixture
	// produces an obvious error rather than a silent no-match.
	var failures []string
	for i, exp := range expected {
		if exp.Namespace == "" {
			failures = append(failures, fmt.Sprintf("expect_jobs[%d]: namespace is required", i))
		}
		if !validExpectJobStatuses[exp.Status] {
			failures = append(failures, fmt.Sprintf("expect_jobs[%d]: status %q is not one of done|failed|cancelled|awaiting_input", i, exp.Status))
		}
	}
	if len(failures) > 0 {
		return failures
	}

	all, err := js.ListBySession(ctx, sid)
	if err != nil {
		return []string{fmt.Sprintf("expect_jobs: ListBySession: %v", err)}
	}

	// Newly-terminal: a job whose current status is in terminalJobStatuses
	// (done | failed | cancelled | awaiting_input) AND whose pre-turn status
	// was either absent (new job dispatched this turn) or not yet in
	// finalJobStatuses (running, or awaiting_input that has now landed
	// permanently). The asymmetry lets the assertion fire on
	// awaiting_input → done resumes — see finalJobStatuses doc.
	// Preserve creation-time order (ListBySession returns ASC).
	type termJob struct {
		j      jobs.Job
		taken  bool
		status jobs.JobStatus
	}
	var newlyTerminal []*termJob
	for _, j := range all {
		if !terminalJobStatuses[j.Status] {
			continue
		}
		prev, hadPrev := preTurnStatus[j.ID]
		// Skip jobs that were already in a permanently-final status — only
		// transitions into a (new) terminal status this turn count.
		if hadPrev && finalJobStatuses[prev] {
			continue
		}
		// Skip jobs that were already awaiting_input and remain awaiting_input
		// — no transition occurred this turn.
		if hadPrev && prev == jobs.JobAwaitingInput && j.Status == jobs.JobAwaitingInput {
			continue
		}
		jj := j
		newlyTerminal = append(newlyTerminal, &termJob{j: jj, status: jj.Status})
	}

	if len(newlyTerminal) < len(expected) {
		return []string{fmt.Sprintf("expect_jobs: expected %d jobs to terminate this turn, got %d", len(expected), len(newlyTerminal))}
	}

	for i, exp := range expected {
		// Find the next unmatched newly-terminal job with matching namespace.
		var match *termJob
		for _, tj := range newlyTerminal {
			if tj.taken {
				continue
			}
			if tj.j.Kind == exp.Namespace {
				match = tj
				break
			}
		}
		if match == nil {
			// Build a diagnostic of what is available.
			available := make([]string, 0, len(newlyTerminal))
			for _, tj := range newlyTerminal {
				if tj.taken {
					continue
				}
				available = append(available, fmt.Sprintf("%s/%s", tj.j.Kind, tj.status))
			}
			failures = append(failures, fmt.Sprintf("expect_jobs[%d]: no newly-terminal job found with namespace=%q (remaining: %v)", i, exp.Namespace, available))
			continue
		}
		match.taken = true
		if string(match.status) != exp.Status {
			failures = append(failures, fmt.Sprintf("expect_jobs[%d]: namespace=%q got status=%q, want %q (job_id=%s, error=%q)",
				i, exp.Namespace, match.status, exp.Status, match.j.ID, match.j.Error))
		}
	}

	return failures
}

// renderSlots expands `{{ world.* }}` template expressions in slot values
// against the current world snapshot. Only string values that contain `{{`
// are processed; all other values are passed through unchanged.
//
// This enables fixture turns to reference runtime-generated values such as
// last_job_id without hard-coding them:
//
//	slots:
//	  job_id: "{{ world.last_job_id }}"
//	  answer: "prod"
//
// A rendering error causes the slot value to be replaced with the raw
// template string plus an error annotation, which will typically cause the
// subsequent intent call to fail with a recognisable message rather than
// silently passing a wrong value.
func renderSlots(slots map[string]any, w world.World) map[string]any {
	if len(slots) == 0 {
		return slots
	}
	env := expr.Env{World: map[string]any(w.Vars)}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, "{{") {
			out[k] = v
			continue
		}
		rendered, err := render.Pongo(s, env)
		if err != nil {
			// Keep a diagnostic string so the turn fails clearly.
			out[k] = fmt.Sprintf("(renderSlots error for %q: %v)", s, err)
			continue
		}
		out[k] = rendered
	}
	return out
}

// mustJSON marshals v to JSON and panics on error (only for trusted values).
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return b
}

// noopHarness is a stub harness for the orchestrator path. It should never
// be called in normal operation since RunIntent bypasses the harness. If it is
// called (e.g. due to an input: turn being accidentally routed here), it returns
// an error that clearly explains what happened.
type noopHarness struct{}

func (h *noopHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("noopHarness: RunTurn called in orchestrator-path fixture (state=%q input=%q); use intent: turns only", in.StatePath, in.UserText)
}

func (h *noopHarness) Close() error { return nil }

var _ harness.Harness = (*noopHarness)(nil)

// ─── Assertion helpers ────────────────────────────────────────────────────────

// countHostDispatchedMatching returns the number of HostDispatched
// events whose namespace equals handler AND whose args partially-match
// the args map (missing keys tolerated, set keys must equal). The
// HostDispatched event payload uses `namespace:` for the handler name
// (matching the store schema in internal/store/events.go); the testrunner
// surface stays in author-friendly "handler" terms.
func countHostDispatchedMatching(actual []store.Event, handler string, args map[string]any) int {
	count := 0
	for _, ev := range actual {
		if ev.Kind != store.HostDispatched || ev.Payload == nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		gotHandler, _ := payload["namespace"].(string)
		if gotHandler != handler {
			continue
		}
		if len(args) > 0 {
			gotArgs, _ := payload["args"].(map[string]any)
			match := true
			for k, want := range args {
				if !deepEqualValues(gotArgs[k], want) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		count++
	}
	return count
}

// assertHostCalls runs expect_host_calls against the turn's events. An
// entry with Times pins the exact count; an entry without Times runs as
// "at least one match". Both shapes expand from the same data type so
// authors flip between modes by adding/removing the field.
func assertHostCalls(actual []store.Event, calls []ExpectHostCall) []string {
	var failures []string
	for _, c := range calls {
		got := countHostDispatchedMatching(actual, c.Handler, c.Args)
		switch {
		case c.Times != nil:
			if got != *c.Times {
				failures = append(failures, fmt.Sprintf("expect_host_calls: %s expected %d invocations, got %d", c.Handler, *c.Times, got))
			}
		default:
			if got == 0 {
				failures = append(failures, fmt.Sprintf("expect_host_calls: %s never invoked (args=%v)", c.Handler, c.Args))
			}
		}
	}
	return failures
}

// assertNoHostCalls fails when any of the named handlers fired during
// the turn (or, when called fixture-scope, across the full run). The
// dispatched-event scan ignores args — listing a handler in
// expect_no_host_calls is an absolute "must not fire" guard. The
// namespace field on the HostDispatched payload carries the handler
// name (see countHostDispatchedMatching for the same convention).
func assertNoHostCalls(actual []store.Event, handlers []string) []string {
	if len(handlers) == 0 {
		return nil
	}
	banned := make(map[string]struct{}, len(handlers))
	for _, h := range handlers {
		banned[h] = struct{}{}
	}
	var failures []string
	for _, ev := range actual {
		if ev.Kind != store.HostDispatched || ev.Payload == nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		got, _ := payload["namespace"].(string)
		if _, ok := banned[got]; ok {
			failures = append(failures, fmt.Sprintf("expect_no_host_calls: %s was invoked but listed as forbidden", got))
		}
	}
	return failures
}

// assertExpectFiles checks every ExpectFile against the filesystem.
// Paths are resolved against fixtureDir when relative. content_matches
// uses Go regexp; must_not_exist inverts the assertion. A failure is
// returned per offending entry so the report shows all mismatches.
func assertExpectFiles(fixtureDir string, files []ExpectFile) []string {
	var failures []string
	for _, ef := range files {
		path := ef.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(fixtureDir, path)
		}
		info, statErr := os.Stat(path)
		exists := statErr == nil && !info.IsDir()
		if ef.MustNotExist != nil && *ef.MustNotExist {
			if exists {
				failures = append(failures, fmt.Sprintf("expect_files[%s]: must_not_exist but file is present", ef.Path))
			}
			continue
		}
		if !exists {
			failures = append(failures, fmt.Sprintf("expect_files[%s]: file does not exist (resolved: %s)", ef.Path, path))
			continue
		}
		if ef.ContentMatches != "" {
			re, reErr := regexp.Compile(ef.ContentMatches)
			if reErr != nil {
				failures = append(failures, fmt.Sprintf("expect_files[%s]: invalid regex %q: %v", ef.Path, ef.ContentMatches, reErr))
				continue
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				failures = append(failures, fmt.Sprintf("expect_files[%s]: read failed: %v", ef.Path, readErr))
				continue
			}
			if !re.Match(data) {
				failures = append(failures, fmt.Sprintf("expect_files[%s]: content does not match %q", ef.Path, ef.ContentMatches))
			}
		}
	}
	return failures
}

// assertEventsSubsequence checks that each expected event appears in order
// in the actual events list (extra events between them are allowed).
func assertEventsSubsequence(actual []store.Event, expected []FlowEvent) []string {
	var failures []string
	ai := 0
	for _, exp := range expected {
		found := false
		for ai < len(actual) {
			ev := actual[ai]
			ai++
			if matchEvent(ev, exp) {
				found = true
				break
			}
		}
		if !found {
			failures = append(failures, fmt.Sprintf("expect_events: event %+v not found in subsequence", exp))
		}
	}
	return failures
}

// assertEventsExact checks that the events list exactly matches expected.
func assertEventsExact(actual []store.Event, expected []FlowEvent) []string {
	var failures []string
	if len(actual) != len(expected) {
		failures = append(failures, fmt.Sprintf("expect_events_exact: got %d events, want %d", len(actual), len(expected)))
		return failures
	}
	for i, exp := range expected {
		if !matchEvent(actual[i], exp) {
			failures = append(failures, fmt.Sprintf("expect_events_exact[%d]: event mismatch. got kind=%q, want kind=%q", i, actual[i].Kind, exp.Kind))
		}
	}
	return failures
}

// matchEvent checks if an actual store.Event matches an expected FlowEvent.
func matchEvent(ev store.Event, exp FlowEvent) bool {
	if string(ev.Kind) != exp.Kind {
		return false
	}
	if ev.Payload == nil {
		return exp.From == "" && exp.To == "" && len(exp.Effect) == 0
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return false
	}
	if exp.From != "" {
		if from, _ := payload["from"].(string); from != exp.From {
			return false
		}
	}
	if exp.To != "" {
		if to, _ := payload["to"].(string); to != exp.To {
			return false
		}
	}
	if len(exp.Effect) > 0 {
		// Check that effect fields match.
		for k, v := range exp.Effect {
			payloadVal := payload[k]
			if !deepEqualValues(payloadVal, v) {
				return false
			}
		}
	}
	return true
}

// deepEqualValues compares two values accounting for JSON number types.
func deepEqualValues(a, b any) bool {
	// Normalize both to JSON and back to handle int/float64 differences.
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	return string(ja) == string(jb)
}

// isTerminalState returns true if the given state path is declared terminal.
func isTerminalState(def *app.AppDef, statePath app.StatePath) bool {
	s := lookupStateDef(def.States, string(statePath))
	return s != nil && s.Terminal
}

// lookupStateDef finds a state in the nested state map by dot-separated path.
func lookupStateDef(states map[string]*app.State, path string) *app.State {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 0 {
		return nil
	}
	s, ok := states[parts[0]]
	if !ok || s == nil {
		return nil
	}
	if len(parts) == 1 {
		return s
	}
	return lookupStateDef(s.States, parts[1])
}

// cloneWorldVars clones world vars for pre-turn snapshot.
func cloneWorldVars(w world.World) map[string]any {
	out := make(map[string]any, len(w.Vars))
	for k, v := range w.Vars {
		out[k] = v
	}
	return out
}

// paramsToIntentCall converts mcp.CallToolParams from a replay harness to an IntentCall.
func paramsToIntentCall(params mcp.CallToolParams) (intent.IntentCall, error) {
	if params.Name != "transition" {
		return intent.IntentCall{}, fmt.Errorf("unexpected tool name %q (want transition)", params.Name)
	}
	argsMap, ok := params.Arguments.(map[string]any)
	if !ok {
		b, _ := json.Marshal(params.Arguments)
		if err := json.Unmarshal(b, &argsMap); err != nil {
			return intent.IntentCall{}, fmt.Errorf("parse arguments: %w", err)
		}
	}
	intentName, _ := argsMap["intent"].(string)
	if intentName == "" {
		return intent.IntentCall{}, fmt.Errorf("missing intent field")
	}
	var slots world.Slots
	if sv, ok := argsMap["slots"]; ok && sv != nil {
		if m, ok := sv.(map[string]any); ok {
			slots = world.Slots(m)
		}
	}
	return intent.IntentCall{Intent: intentName, Slots: slots}, nil
}

// ─── Reporter ─────────────────────────────────────────────────────────────────

// PrintFlowReport writes the human-readable report to stdout.
func PrintFlowReport(report *FlowReport) {
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, r := range report.Results {
		status := "PASS"
		if r.Skipped {
			status = "SKIP"
		} else if !r.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%-6s\t%s\n", status, filepath.Base(r.File))
		for _, tr := range r.Turns {
			turnStatus := "  ok  "
			if !tr.Passed {
				turnStatus = "  FAIL"
			}
			if tr.TurnIndex < 0 {
				fmt.Fprintf(w, "  %s\t[session]\n", turnStatus)
			} else {
				fmt.Fprintf(w, "  %s\tturn %d → %s\n", turnStatus, tr.TurnIndex+1, tr.NewState)
			}
			for _, f := range tr.Failures {
				fmt.Fprintf(w, "\t  ✗ %s\n", f)
			}
		}
	}
	_ = w.Flush()

	fmt.Printf("\nSummary: %d/%d flows pass\n", report.Passed, report.Passed+report.Failed)
	if report.Failed > 0 {
		fmt.Println("FAIL")
	} else {
		fmt.Println("PASS")
	}
}

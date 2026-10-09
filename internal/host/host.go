// Package host implements the host invocation registry.
// See docs/architecture/hosts.md.
//
// Handlers are registered at process start and invoked from the effect executor
// in internal/machine/ when an effect declares invoke: host.<name>.
//
// # Handler interface
//
// A Handler is a plain function: (ctx, args) -> (Result, error).
// The Result.Data map is bound into world slots per the effect's bind: spec.
// Result.Error is for expected, application-level errors (distinguished from
// infrastructure failures which return a non-nil Go error).
//
// # Auth / secrets
//
// Secrets are loaded from env and ~/.kitsoki/secrets.yaml at registry creation
// time and injected into every handler call via context.
//
// # Allow-list enforcement
//
// Apps declare required hosts in a top-level `hosts:` section. The loader
// validates that every invoke: host.* matches the allow-list, and the
// registry validates at startup that every declared host is registered.
package host

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/clock"
)

// Handler is the callable unit for a host invocation.
// args are the template-resolved values from the effect's with: block.
// Returns (Result, nil) on success or expected-error; returns (Result{}, err) on infra failure.
type Handler func(ctx context.Context, args map[string]any) (Result, error)

// Result is the structured return from a Handler.
type Result struct {
	// Data is bound into world/proposal per the effect's bind: spec.
	Data map[string]any
	// Error is non-empty when the handler encountered an expected, domain-level error.
	// Infra failures are returned as Go errors instead.
	Error string
	// FailureKind optionally classifies a non-empty Error for the harness
	// ladder's routing decision (see ladder.go): FailureInfra rotates to the
	// next provider/harness lane and backs the current lane off;
	// FailureCapability escalates effort (same model) before trying a stronger
	// model; FailureFatal (a config/argument error no rung can fix) stops the
	// ladder immediately.
	// Zero value (FailureNone) means "unclassified" — a handler that doesn't
	// populate it is unaffected; the ladder falls back to a best-effort text
	// heuristic over Error. Only host.agent.decide / host.agent.task populate
	// this today.
	FailureKind FailureKind
}

// Registry holds the set of registered Handler functions, keyed by name.
// Names should be dot-separated, e.g. "host.workspace_manager.get".
// The registry is safe for concurrent reads after initialization.
type Registry struct {
	mu              sync.RWMutex
	handlers        map[string]Handler
	ticketProviders map[string]struct{}
	secrets         map[string]string
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		handlers:        make(map[string]Handler),
		ticketProviders: make(map[string]struct{}),
		secrets:         LoadSecrets(),
	}
}

// Register adds a handler under the given name.
// Panics if a handler with that name is already registered (init-time contract).
func (r *Registry) Register(name string, h Handler) {
	r.register(name, h, false)
}

// RegisterTicketProvider registers h and marks name as safe for nested dispatch
// by host.ticket_federation. The marker is deliberately separate from naming:
// a mutable ticket-source configuration must not be able to turn an arbitrary
// registered host (host.run, host.git, …) into a provider merely by spelling
// its name in world data.
func (r *Registry) RegisterTicketProvider(name string, h Handler) {
	r.register(name, h, true)
}

func (r *Registry) register(name string, h Handler, ticketProvider bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists {
		panic(fmt.Sprintf("host: handler %q already registered", name))
	}
	r.handlers[name] = h
	if ticketProvider {
		r.ticketProviders[name] = struct{}{}
	}
}

// Replace registers a handler, overwriting any existing entry with the
// same name. Unlike Register (which panics on duplicate as an init-time
// contract against accidental shadowing in production code), Replace is
// the test-friendly variant: a fixture that wants to stub a production
// handler simply re-registers it on top of the builtin. Replacement clears
// explicit capability markers; wrappers/cassettes that preserve the original
// capability class use ReplacePreservingCapabilities instead. Returns true
// when an existing handler was overwritten so callers can audit/log.
func (r *Registry) Replace(name string, h Handler) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.handlers[name]
	r.handlers[name] = h
	delete(r.ticketProviders, name)
	return existed
}

// ReplacePreservingCapabilities replaces a handler while retaining its current
// capability markers. Cassette and flow harnesses use this when they wrap a
// real provider with deterministic replay. Ordinary Replace deliberately
// clears markers so a generic dynamically loaded handler cannot inherit ticket
// federation authority merely by reusing a provider's name.
func (r *Registry) ReplacePreservingCapabilities(name string, h Handler) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.handlers[name]
	r.handlers[name] = h
	return existed
}

// ReplaceTicketProvider is the idempotent counterpart to
// RegisterTicketProvider. It is used by shared registries whose Starlark
// bindings are discovered lazily: replacing the handler must also retain the
// explicit capability marker consumed by host.ticket_federation.
func (r *Registry) ReplaceTicketProvider(name string, h Handler) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.handlers[name]
	r.handlers[name] = h
	r.ticketProviders[name] = struct{}{}
	return existed
}

// IsTicketProvider reports whether name was explicitly registered as a ticket
// provider. Prefix fallback is intentionally not used: federation source
// declarations name the provider carrier (for example host.gh.ticket), while
// the federation appends and controls the operation suffix itself.
func (r *Registry) IsTicketProvider(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.ticketProviders[name]
	return ok
}

// Get returns the handler for the given name.
//
// Lookup tries an exact match first. If no handler is registered at
// the full name, Get falls back to the longest registered prefix
// (split on '.'). This is the dispatch surface for host_interface ops
// (docs/stories/imports.md, "host_interfaces"): a name like "host.diary.announce"
// resolves to a registered "host.diary.announce" handler when one
// exists, otherwise to a registered "host.diary" handler that takes
// the op via args. Author convention: register per-op handlers when
// each op has a meaningfully different surface; share one handler
// when ops dispatch on payload.
//
// Returns (nil, false) if neither the exact name nor any prefix
// resolves to a registered handler.
func (r *Registry) Get(name string) (Handler, bool) {
	h, _, ok := r.getWithName(name)
	return h, ok
}

// getWithName is Get plus the actual registered name that matched —
// either the exact `name` or the prefix it fell back to. Invoke uses
// the difference to inject the dropped suffix as args["op"].
func (r *Registry) getWithName(name string) (Handler, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if h, ok := r.handlers[name]; ok {
		return h, name, true
	}
	// Walk back from the end, stripping one dotted segment per iteration.
	for cur := name; ; {
		dot := lastDot(cur)
		if dot < 0 {
			return nil, "", false
		}
		cur = cur[:dot]
		if h, ok := r.handlers[cur]; ok {
			return h, cur, true
		}
	}
}

// Names returns the sorted set of verb names currently registered. Used by
// the effect-taxonomy builtin-classification coverage test (handlers_test.go)
// to assert every verb RegisterBuiltins registers has a default effect
// classification in internal/effect, so a new verb can't ship unclassified.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// lastDot returns the index of the last '.' in s, or -1 if absent.
// Inlined to avoid importing strings into this file.
func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// ValidateAllowList checks that every name in the allow-list is
// resolvable via Get (i.e., either registered exactly or via the
// prefix-fallback documented on Get). Returns one error listing all
// missing handlers.
func (r *Registry) ValidateAllowList(allowList []string) error {
	var missing []string
	for _, name := range allowList {
		if _, ok := r.Get(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("host: unregistered hosts declared in app manifest: %v", missing)
	}
	return nil
}

// Invoke calls the named handler with the provided args.
//
// When the lookup falls back to a registered prefix (e.g.
// `host.local_files.ticket.search` → `host.local_files.ticket`), the
// dropped suffix is injected into args as `op` so the shared handler
// can dispatch on it. This is what makes
// `iface.ticket.search` → `host.local_files.ticket` work without each
// op getting its own Register call: the handler reads `args["op"]`
// and switches. If the caller already supplied an `op` it wins (the
// suffix is only filled in when op is absent or empty).
//
// Returns ErrHostNotFound if no handler is registered under name.
func (r *Registry) Invoke(ctx context.Context, name string, args map[string]any) (Result, error) {
	h, registeredName, ok := r.getWithName(name)
	if !ok {
		return Result{}, fmt.Errorf("host: no handler registered for %q", name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(r.secrets) > 0 && len(SecretsFromContext(ctx)) == 0 {
		ctx = WithSecrets(ctx, r.secrets)
	}
	if registeredName != name {
		// Prefix-fallback hit: inject the trailing suffix as args["op"]
		// when the caller hasn't already supplied one.
		if op, _ := args["op"].(string); op == "" {
			suffix := name[len(registeredName):]
			suffix = strings.TrimPrefix(suffix, ".")
			if suffix != "" {
				if args == nil {
					args = map[string]any{}
				}
				args["op"] = suffix
			}
		}
	}
	return h(ctx, args)
}

// ErrHostNotFound is returned when the registry has no handler for a name.
var ErrHostNotFound = fmt.Errorf("host: handler not found")

// secretsKey is the context key for injected secrets.
type secretsKey struct{}

// WithSecrets injects a secrets map into a context for use by handlers.
func WithSecrets(ctx context.Context, secrets map[string]string) context.Context {
	return context.WithValue(ctx, secretsKey{}, secrets)
}

// SecretsFromContext retrieves the secrets map from a context.
// Returns nil if no secrets were injected.
func SecretsFromContext(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(secretsKey{}).(map[string]string); ok {
		return v
	}
	return nil
}

// ─── Job context ─────────────────────────────────────────────────────────────

// ClarificationRequester is the subset of *jobs.JobStore that a background
// handler can call mid-flight to pause execution and ask the user a question.
//
// Defining it here in the host package avoids an import cycle: jobs already
// imports host (for host.Handler), so host cannot import jobs directly.
// *jobs.JobStore satisfies this interface via structural typing — no explicit
// implementation required on the jobs side.
//
// The method names with the "Any" suffix accept schema/answer as any so that
// the interface can be declared without importing the jobs package types.
type ClarificationRequester interface {
	// RequestClarificationAny transitions the job to awaiting_input and stores
	// the given schema (a jobs.ClarificationSchema value, passed as any to
	// avoid an import cycle) so the inbox can render a form. Returns an error
	// when the job is already awaiting_input (clarification collision).
	RequestClarificationAny(ctx context.Context, id string, schema any) error
	// AnswerClarificationRaw returns the raw JSON-encoded clarification answer
	// once the user has submitted it, or ("", nil) when not yet available.
	// The caller is responsible for polling until a non-empty value is returned.
	AnswerClarificationRaw(ctx context.Context, id string) (string, error)
}

// jobsKey is the unexported context key for the injected JobContext.
type jobsKey struct{}

// JobContext is the active-job handle injected into a Handler's context when
// it is running as a background job. Synchronous (foreground) calls do not
// see a JobContext — check Store != nil before calling any method.
type JobContext struct {
	// Store provides mid-flight clarification support.
	// nil when the handler is running in a foreground (non-background) call.
	Store ClarificationRequester
	// JobID is the unique identifier for the running job.
	JobID string
	// awaiting signals the scheduler that the job has entered awaiting_input.
	// Called automatically by the host.RequestClarification helper.
	awaiting func(id string) error
	// resume signals the scheduler that the job has received its answer and is
	// running again (re-increments runningCount).  Called automatically by
	// host.RequestClarification after reading the answer.
	resume func(id string) error
}

// NewJobContext constructs a JobContext with the given store, job ID,
// awaiting signaller, and resume signaller.  The awaiting func is called by
// host.RequestClarification after the DB row has been flipped to
// awaiting_input so the scheduler can fan out a JobAwaitingInput event.
// The resume func is called after the answer arrives so the scheduler
// re-increments runningCount and WaitIdle correctly blocks while the resumed
// handler continues working.  Pass nil for either when no scheduler signal is
// needed (e.g. in tests that use a no-op scheduler).
func NewJobContext(store ClarificationRequester, jobID string, awaiting, resume func(id string) error) JobContext {
	return JobContext{
		Store:    store,
		JobID:    jobID,
		awaiting: awaiting,
		resume:   resume,
	}
}

// WithJobContext injects a JobContext into ctx so that a background handler
// can access its job identity and clarification facilities.
func WithJobContext(ctx context.Context, jc JobContext) context.Context {
	return context.WithValue(ctx, jobsKey{}, jc)
}

// JobContextFromContext retrieves the JobContext from ctx.
// Returns the zero value (Store == nil) when no JobContext was injected,
// which is the case for every synchronous (foreground) handler call.
func JobContextFromContext(ctx context.Context) JobContext {
	if v, ok := ctx.Value(jobsKey{}).(JobContext); ok {
		return v
	}
	return JobContext{}
}

// ─── Clock context ────────────────────────────────────────────────────────────

// clockKey is the unexported context key for an injected clock.Clock.
type clockKey struct{}

// WithClock injects a clock.Clock into ctx so that clarification poll loops
// and other time-dependent handler code can use a fake clock in tests.
func WithClock(ctx context.Context, c clock.Clock) context.Context {
	return context.WithValue(ctx, clockKey{}, c)
}

// ClockFromContext retrieves the clock.Clock from ctx.
// Returns clock.Real() when no clock has been injected.
func ClockFromContext(ctx context.Context) clock.Clock {
	if c, ok := ctx.Value(clockKey{}).(clock.Clock); ok && c != nil {
		return c
	}
	return clock.Real()
}

// ─── Actor context ───────────────────────────────────────────────────────────

// actorKey is the unexported context key for an injected operator identity
// (graph-mcp plan §3.4's stamps seam). Deliberately separate from
// internal/runstatus/server's actorCtxKey, which is scoped to unrelated
// story-author slots.author injection.
type actorKey struct{}

// WithActor injects an operator identity string into ctx so that
// host.graph.propose/authorize/apply handlers can stamp authored_by/
// authorized_by without threading an explicit param through every call
// site above the host.graph dispatch boundary.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFromContext retrieves the injected actor from ctx.
// Returns "" when no actor has been injected (stamps that key off actor
// non-empty are simply skipped).
func ActorFromContext(ctx context.Context) string {
	if a, ok := ctx.Value(actorKey{}).(string); ok {
		return a
	}
	return ""
}

// ─── Steward context ─────────────────────────────────────────────────────────

// stewardKey is the unexported context key for the trusted/steward flag
// (graph-mcp plan §3.4 red-team amendment #1: provenance strip). Only a
// caller explicitly marked steward-trusted may have graph.Propose honor
// caller-supplied Provenance for the D9 auto-authorize allowlist — an
// ordinary (agent/CLI/kit_call) caller's provenance is always stripped
// before it reaches internal/graph.Propose, regardless of what the wire
// args contained, so an untrusted proposal can never self-authorize via a
// forged provenance block.
type stewardKey struct{}

// WithSteward injects the trusted/steward flag into ctx. Server
// construction (or a CLI's own steward-mode entry point) sets this true
// only for callers it has independently established as trusted operators;
// every other caller path (kit_call, starlark, MCP agent tools) leaves it
// unset, i.e. untrusted by default.
func WithSteward(ctx context.Context, steward bool) context.Context {
	return context.WithValue(ctx, stewardKey{}, steward)
}

// StewardFromContext reports whether ctx was marked steward-trusted.
// Defaults to false (untrusted) when nothing was injected — fail closed.
func StewardFromContext(ctx context.Context) bool {
	if s, ok := ctx.Value(stewardKey{}).(bool); ok {
		return s
	}
	return false
}

// RequestClarification is the high-level helper that handler authors call to
// pause the job and ask the user a question. It:
//  1. Calls jc.Store.RequestClarification to write the schema to the DB and
//     flip the job row to awaiting_input.
//  2. Signals the scheduler via jc.awaiting so it can fan out a
//     JobAwaitingInput event to all subscribers (including the orchestrator's
//     session listener which will post the action_required notification).
//  3. Polls jc.Store.AnswerClarificationRaw every 200 ms until the user
//     submits an answer, then returns the raw JSON string.
//
// The schema argument should be a jobs.ClarificationSchema value; it is passed
// as any to avoid an import cycle.
//
// Returns an error when:
//   - the context is cancelled (ctx.Err() wrapped),
//   - Store == nil (not a background job — use in foreground handler),
//   - RequestClarification fails (e.g. collision),
//   - the scheduler signaller fails: the clarification request is in the
//     JobStore but the inbox notification will NOT be posted automatically;
//     surface this error to the caller as any other infrastructure failure.
func RequestClarification(ctx context.Context, schema any) (string, error) {
	jc := JobContextFromContext(ctx)
	if jc.Store == nil {
		return "", fmt.Errorf("host.RequestClarification: not running as a background job (no JobContext in ctx)")
	}
	if err := jc.Store.RequestClarificationAny(ctx, jc.JobID, schema); err != nil {
		return "", fmt.Errorf("host.RequestClarification: %w", err)
	}
	// Notify the scheduler that we are now awaiting input. If this fails, the
	// DB row is already flipped to awaiting_input but no JobAwaitingInput event
	// will be fanned out — the orchestrator session listener won't post the
	// action_required notification. Return the error so the caller can propagate
	// it rather than silently leaving the user without a notification.
	if jc.awaiting != nil {
		if err := jc.awaiting(jc.JobID); err != nil {
			return "", fmt.Errorf("host.RequestClarification: signal scheduler: %w", err)
		}
	}
	// Poll for the answer using the injectable clock so tests can drive time
	// deterministically without a real 200 ms wall-clock wait.
	const pollInterval = 200 * time.Millisecond
	clk := ClockFromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("host.RequestClarification: context cancelled while waiting for answer: %w", ctx.Err())
		case <-clk.After(pollInterval):
		}
		raw, err := jc.Store.AnswerClarificationRaw(ctx, jc.JobID)
		if err != nil {
			return "", fmt.Errorf("host.RequestClarification: poll answer: %w", err)
		}
		if raw != "" {
			// Signal the scheduler that the job is running again so WaitIdle
			// correctly blocks while the resumed handler continues its work.
			if jc.resume != nil {
				if resumeErr := jc.resume(jc.JobID); resumeErr != nil {
					// Non-fatal: the handler will continue regardless; log only.
					_ = resumeErr
				}
			}
			return raw, nil
		}
	}
}

// Package host — the write-mode gate for write_mode: read_only agent rooms.
//
// A dispatched agent in a write_mode: read_only room (internal/app State.WriteMode)
// boots read-only: read tools (Read/Grep/Glob, read-only Bash) flow unconstrained,
// but every MUTATING tool call — Write/Edit/MultiEdit/NotebookEdit, a Bash command
// that fails the read-only profile, or an effect ≥ write host call — is gated. This
// file is the gate's decision spine; bash_mcp.go's MCP-wrapper pattern carries the
// per-Bash-command interception (see WriteModeGateBashProfile), and agent_task.go
// wires the read-only floor at dispatch time.
//
// The boundary is sharp (agent-write-mode-opt-in.md "The model"):
//
//   - DETERMINISTIC (engine, replayable): the mutating-step check (classifyToolCall),
//     the active-scope short-circuit (a turn|session grant already covers it), and
//     the headless DENY when no operator is attached. None of this is an LLM call.
//   - INTERPRETIVE (recorded): exactly one thing — the operator's opt-in verdict and
//     its scope, surfaced via the operator-ask bridge and recorded as a
//     WriteModeGranted event. There is NO LLM in this gate: write mode is granted by
//     a human or, headless, denied — never by a model.
//
// The gate is intentionally self-contained and DI-friendly: NewWriteModeGate takes
// the room posture and the active scope; Resolve drives the decision against an
// injected operator-ask surface (a closure, defaulting to the in-context bridge).
// This is the stateless probe the no-LLM tests exercise directly
// (write_mode_gate_test.go), mirroring bash_profile_test.go's table style.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// MutatingEffect is the side-effect class a gated tool call carries. It mirrors
// the effect-taxonomy write/external split the gate keys on; "write" is a
// reversible local mutation (Edit/Write/side-effecting Bash), "external" is an
// irreversible outside-world call (a push, a transport post).
type MutatingEffect string

const (
	// EffectWrite is a reversible local mutation (Write/Edit/MultiEdit/
	// NotebookEdit, a side-effecting Bash command).
	EffectWrite MutatingEffect = "write"
	// EffectExternal is an irreversible external call (git push, a PR, a
	// host.transport.post). A turn/session grant covers write but NOT external —
	// external always re-asks per action (agent-write-mode-opt-in.md open Q2).
	EffectExternal MutatingEffect = "external"
)

// GrantScope is the breadth of an operator's write-mode opt-in.
type GrantScope string

const (
	// ScopeNone is the absence of a grant (the headless / pre-grant default).
	ScopeNone GrantScope = ""
	// ScopeAction grants exactly the one gated call; no world key is written.
	ScopeAction GrantScope = "action"
	// ScopeTurn grants the rest of the foreground turn; the engine clears the
	// write_mode_scope world key at turn end.
	ScopeTurn GrantScope = "turn"
	// ScopeSession grants the rest of the session; cleared at session end.
	ScopeSession GrantScope = "session"
)

// mutatingTools is the set of built-in tool names that always mutate (mirrors
// readOnlyDeniedTools minus Bash, which is classified per-command). A call to any
// of these in a read_only room is a mutating step that needs a grant.
var mutatingTools = map[string]MutatingEffect{
	"Write":        EffectWrite,
	"Edit":         EffectWrite,
	"MultiEdit":    EffectWrite,
	"NotebookEdit": EffectWrite,
}

// ToolCall is the minimal description of a tool invocation the gate classifies.
// Name is the tool (Write/Edit/Bash/…); Bash carries the command; Effect, when
// set, is an explicit host-call class (effect-taxonomy) that overrides the
// name-based lookup — so a host.transport.post can declare itself external.
type ToolCall struct {
	Name    string
	Command string         // for Bash: the raw command string
	Effect  MutatingEffect // optional explicit class for host calls
}

// classifyToolCall is the DETERMINISTIC mutating-step check. It returns
// (effect, isMutating): isMutating false means the call is a read (Read/Grep/Glob,
// a read-only-profile Bash command) and passes through the gate with no decision
// point. A non-empty explicit Effect always wins (host-call taxonomy). For Bash,
// the read-only bash profile verdict decides: a command the read-only allowlist
// accepts is a read; anything it rejects is a write-class mutation.
func classifyToolCall(tc ToolCall) (MutatingEffect, bool) {
	if tc.Effect != "" {
		return tc.Effect, true
	}
	if eff, ok := mutatingTools[tc.Name]; ok {
		return eff, true
	}
	if tc.Name == "Bash" {
		// A read-only-profile-conforming command is a read; a rejected one mutates.
		if msg := ApplyBashProfile(&BashProfile{Kind: BashProfileReadOnly}, tc.Command); msg != "" {
			return EffectWrite, true
		}
		return "", false
	}
	// Read/Grep/Glob/WebFetch/WebSearch and anything else: not a mutating step.
	return "", false
}

// scopeCovers reports whether an active grant scope covers a mutating call of the
// given effect WITHOUT a re-ask. A turn/session grant covers EffectWrite; an
// EffectExternal call ALWAYS re-asks regardless of an active grant ("stop asking
// me about edits" must never silently authorize a push). ScopeAction never
// pre-covers a later call (it is one-shot, consumed at grant time).
func scopeCovers(active GrantScope, eff MutatingEffect) bool {
	if eff == EffectExternal {
		return false
	}
	return active == ScopeTurn || active == ScopeSession
}

// WriteModeDecision is the recorded outcome of one gate resolution.
type WriteModeDecision struct {
	// Granted is true when the call may proceed (an active scope covered it, or
	// the operator opted in). False means the call is denied (headless, or the
	// operator declined) and the agent stays read-only.
	Granted bool
	// Scope is the breadth the operator chose (or the covering active scope).
	Scope GrantScope
	// By is "operator" when a human resolved it, "headless_denied" when no
	// operator was attached, "active_scope" when a prior grant short-circuited it.
	By string
	// NewActiveScope is the scope the engine should now persist in the
	// write_mode_scope world key (turn/session). Empty leaves it unchanged.
	NewActiveScope GrantScope
}

// WriteModeAsker forwards an action proposal to the operator and returns the
// chosen scope (or ScopeNone for a deny). It is the DI seam the gate resolves
// through: production wires operatorAskGrant (the operator-ask bridge); tests
// inject a stub. A nil asker means "no operator" → headless deny.
type WriteModeAsker func(ctx context.Context, action string, eff MutatingEffect) (GrantScope, error)

// WriteModeGate holds the room posture and the active grant scope for one
// dispatched task. It is constructed per dispatch from the room's State.WriteMode
// and the seed scope read from the write_mode_scope world key.
type WriteModeGate struct {
	// ReadOnly is true when the room runs write_mode: read_only. False (open /
	// absent) makes Resolve a pass-through — today's behavior verbatim.
	ReadOnly bool
	// active is the grant breadth carried in from the world key, advanced on a
	// turn/session grant within this dispatch.
	active GrantScope
	// ask forwards an action proposal; nil ⇒ headless (deny mutating steps).
	ask WriteModeAsker
}

// NewWriteModeGate builds a gate. readOnly mirrors the room posture; active is
// the seed scope (from the write_mode_scope world key); ask is the operator
// surface (nil ⇒ headless deny).
func NewWriteModeGate(readOnly bool, active GrantScope, ask WriteModeAsker) *WriteModeGate {
	return &WriteModeGate{ReadOnly: readOnly, active: active, ask: ask}
}

// ActiveScope returns the gate's current grant breadth (for persisting back into
// the world key after a dispatch).
func (g *WriteModeGate) ActiveScope() GrantScope { return g.active }

// Resolve runs the gate for one tool call and records a WriteModeGranted event on
// the sink in ctx when an interpretive decision is made. A read (or an open room)
// returns granted with no event. A mutating call covered by an active scope
// returns granted with no NEW event (the original grant is authoritative). An
// ungranted mutating call forwards an action proposal: an operator grant records a
// granted event and (for turn/session) advances the active scope; headless or a
// decline records a denial. The returned decision tells the caller whether to let
// the tool call proceed.
func (g *WriteModeGate) Resolve(ctx context.Context, tc ToolCall) WriteModeDecision {
	if g == nil || !g.ReadOnly {
		return WriteModeDecision{Granted: true} // open room: pass-through, no gate
	}
	eff, mutating := classifyToolCall(tc)
	if !mutating {
		return WriteModeDecision{Granted: true} // read: unconstrained, no decision point
	}
	action := describeAction(tc, eff)

	// Active-scope short-circuit: a turn/session grant already covers a write.
	if scopeCovers(g.active, eff) {
		return WriteModeDecision{Granted: true, Scope: g.active, By: "active_scope"}
	}

	// No covering grant: forward an action proposal. No asker (headless) → deny.
	if g.ask == nil {
		g.record(ctx, action, eff, ScopeNone, "headless_denied", false)
		return WriteModeDecision{Granted: false, By: "headless_denied"}
	}
	scope, err := g.ask(ctx, action, eff)
	if err != nil || scope == ScopeNone {
		// Operator declined, timed out, or the surface detached: hold read-only.
		g.record(ctx, action, eff, ScopeNone, "operator", false)
		return WriteModeDecision{Granted: false, By: "operator"}
	}
	// Granted. A turn/session grant for a WRITE advances the active scope so a
	// later write short-circuits; an external grant is always one-shot (it never
	// arms a scope, mirroring scopeCovers).
	newActive := g.active
	if eff != EffectExternal && (scope == ScopeTurn || scope == ScopeSession) {
		newActive = scope
		g.active = scope
	}
	g.record(ctx, action, eff, scope, "operator", true)
	return WriteModeDecision{Granted: true, Scope: scope, By: "operator", NewActiveScope: newActive}
}

// record appends a WriteModeGranted event to the sink in ctx (best-effort; a nil
// sink — flow tests, headless — is a silent no-op). It pins the gated action, its
// effect class, the chosen scope, who decided, and whether it was granted: the
// agent's side-effect audit trail (agent-write-mode-opt-in.md "Decision recording").
func (g *WriteModeGate) record(ctx context.Context, action string, eff MutatingEffect, scope GrantScope, by string, granted bool) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	body := writeModeGrantedPayload{
		State:   string(oc.StatePath),
		Action:  action,
		Effect:  string(eff),
		Scope:   string(scope),
		By:      by,
		Granted: granted,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        time.Now(),
		Kind:      store.WriteModeGranted,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
	})
}

// writeModeGrantedPayload is the WriteModeGranted event body
// (agent-write-mode-opt-in.md "Decision recording").
type writeModeGrantedPayload struct {
	State   string `json:"state"`
	Action  string `json:"action"`
	Effect  string `json:"effect"`
	Scope   string `json:"scope,omitempty"`
	By      string `json:"by"`
	Granted bool   `json:"granted"`
}

// describeAction renders the human-facing action label surfaced to the operator
// and recorded on the event ("Edit ./x.go" | "Bash: git push" | "host.transport.post").
func describeAction(tc ToolCall, eff MutatingEffect) string {
	switch {
	case tc.Name == "Bash":
		cmd := strings.TrimSpace(tc.Command)
		if cmd == "" {
			return "Bash"
		}
		return "Bash: " + cmd
	case tc.Name != "":
		return tc.Name
	default:
		return string(eff)
	}
}

// ── Context plumbing + operator-ask bridge ────────────────────────────────────

// writeModeGateKey carries a *WriteModeGate down to the per-call interception
// point (the bash MCP wrapper) within a dispatched task.
type writeModeGateKey struct{}

// WithWriteModeGate installs gate on ctx. A nil gate is a no-op so callers needn't
// guard the open-room case.
func WithWriteModeGate(ctx context.Context, gate *WriteModeGate) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, writeModeGateKey{}, gate)
}

// WriteModeGateFromContext returns the gate installed with WithWriteModeGate, or
// nil when none is installed (open rooms, every legacy path).
func WriteModeGateFromContext(ctx context.Context) *WriteModeGate {
	g, _ := ctx.Value(writeModeGateKey{}).(*WriteModeGate)
	return g
}

// writeModeActionProposal builds the operator-ask question for a gated mutating
// step: a single question whose options are the offered scopes plus deny. The
// surfaced default is "turn" (agent-write-mode-opt-in.md open Q1: one operator
// instruction → one grant); an EffectExternal call omits turn/session (a push is
// one-shot) and offers only action + deny.
func writeModeActionProposal(action string, eff MutatingEffect) OperatorQuestion {
	q := OperatorQuestion{
		Header:   "write mode",
		Question: "The agent wants to " + action + ". Allow this " + string(eff) + " action?",
	}
	if eff == EffectExternal {
		q.Options = []OperatorOption{
			{Label: string(ScopeAction), Description: "Allow just this one external action"},
			{Label: "deny", Description: "Deny — keep the agent read-only"},
		}
		return q
	}
	q.Options = []OperatorOption{
		{Label: string(ScopeTurn), Description: "Allow edits for the rest of this turn (recommended)"},
		{Label: string(ScopeAction), Description: "Allow just this one edit"},
		{Label: string(ScopeSession), Description: "Allow edits for the rest of this session"},
		{Label: "deny", Description: "Deny — keep the agent read-only"},
	}
	return q
}

// operatorAskGrant is the production WriteModeAsker: it forwards an action proposal
// through the operator-ask bridge (the in-context OperatorPrompter) and maps the
// operator's chosen label to a GrantScope. A non-scope label (or any error)
// resolves to ScopeNone (deny). Returns a nil asker effectively when no prompter
// is attached — callers detect that via OperatorInteractive and pass nil to the
// gate so it takes the headless-deny path with a recorded denial.
func operatorAskGrant(ctx context.Context, action string, eff MutatingEffect) (GrantScope, error) {
	prompter, ok := OperatorPrompterFrom(ctx)
	if !ok {
		return ScopeNone, nil
	}
	q := writeModeActionProposal(action, eff)
	answers, err := prompter.Ask(ctx, kitsokiSessionIDFromCtx(ctx), []OperatorQuestion{q})
	if err != nil {
		return ScopeNone, err
	}
	label := firstAnswerLabel(answers, q.Question)
	switch GrantScope(label) {
	case ScopeAction:
		return ScopeAction, nil
	case ScopeTurn:
		// An external action never honours a turn grant; collapse to action.
		if eff == EffectExternal {
			return ScopeAction, nil
		}
		return ScopeTurn, nil
	case ScopeSession:
		if eff == EffectExternal {
			return ScopeAction, nil
		}
		return ScopeSession, nil
	default:
		return ScopeNone, nil // "deny" or anything unrecognised
	}
}

// firstAnswerLabel extracts the operator's chosen label from the prompter answer
// map (keyed by question text; value is a string or []string for multi-select).
func firstAnswerLabel(answers map[string]any, question string) string {
	v, ok := answers[question]
	if !ok {
		for _, vv := range answers { // tolerate a differently-keyed single answer
			v = vv
			break
		}
	}
	switch t := v.(type) {
	case string:
		return t
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// gateAskerFor returns the WriteModeAsker the gate should use for this dispatch:
// the operator-ask bridge when a live surface is attached, else nil (headless →
// deny). The split is the single interactivity predicate (OperatorInteractive),
// matching the operator-ask bridge's own no-op-without-a-surface posture.
func gateAskerFor(ctx context.Context) WriteModeAsker {
	if OperatorInteractive(ctx) {
		return operatorAskGrant
	}
	return nil
}

// seedScopeFromWorld reads the active write_mode_scope grant breadth from a world
// snapshot (the engine-reserved app.WriteModeScopeWorldKey). Returns ScopeNone
// when unset or not a recognised scope. Used to build a gate that honours a
// turn/session grant established earlier in the same turn/session.
func seedScopeFromWorld(vars map[string]any) GrantScope {
	if vars == nil {
		return ScopeNone
	}
	s, _ := vars[app.WriteModeScopeWorldKey].(string)
	switch GrantScope(s) {
	case ScopeTurn:
		return ScopeTurn
	case ScopeSession:
		return ScopeSession
	default:
		return ScopeNone
	}
}

// IsReadOnlyWriteMode reports whether a room posture string is the read_only
// gate posture. Centralises the app-vocabulary comparison so callers outside the
// app package (the orchestrator wiring) don't duplicate the literal.
func IsReadOnlyWriteMode(writeMode string) bool {
	return writeMode == app.WriteModeReadOnly
}

// applyReadOnlyFloorCLIArgs rewrites a base CLI arg slice for a write_mode:
// read_only dispatch: it downgrades --permission-mode bypassPermissions to
// "default" (so the --allowedTools allowlist actually binds — under
// bypassPermissions the CLI approves every tool, making the allowlist advisory,
// see converseToolPolicy) and appends readOnlyDeniedTools to --disallowedTools as
// the hard backstop (honoured under every permission mode). It mirrors the
// read-only converse posture exactly, so a read_only task boots like a
// bash_profile: read-only / external_side_effect: false agent does today. The
// existing --disallowedTools AskUserQuestion entry (added by buildBaseCLIArgs) is
// merged so a tool is never emitted twice.
func applyReadOnlyFloorCLIArgs(cliArgs []string) []string {
	out := make([]string, 0, len(cliArgs)+4)
	existingDeny := ""
	for i := 0; i < len(cliArgs); i++ {
		switch cliArgs[i] {
		case "--permission-mode":
			out = append(out, "--permission-mode", "default")
			i++ // skip the bypassPermissions value
		case "--disallowedTools":
			if i+1 < len(cliArgs) {
				existingDeny = cliArgs[i+1]
				i++
			}
		default:
			out = append(out, cliArgs[i])
		}
	}
	// Merge the read-only deny set with whatever was already denied (AskUserQuestion),
	// de-duplicated.
	deny := withAlwaysDenied(readOnlyDeniedTools)
	if existingDeny != "" {
		for _, t := range strings.Split(existingDeny, ",") {
			deny = mergeDedup(deny, t)
		}
	}
	return appendDisallowedToolsFlag(out, deny)
}

// mergeDedup appends t to list only when absent, returning the (possibly grown)
// slice. Small-N linear scan; the deny set is a handful of entries.
func mergeDedup(list []string, t string) []string {
	for _, x := range list {
		if x == t {
			return list
		}
	}
	return append(list, t)
}

// describeGateErrorForLLM renders the tool-error text the agent sees when a
// mutating step is denied — a clear, actionable message so the model proceeds
// read-only rather than retrying blindly.
func describeGateErrorForLLM(action string, by string) string {
	if by == "headless_denied" {
		return fmt.Sprintf("write-mode gate: %q was denied — no operator is attached to grant write mode, "+
			"so this room is read-only. Continue with read-only work (Read/Grep/Glob, read-only Bash) "+
			"and report what you would change instead of attempting the mutation.", action)
	}
	return fmt.Sprintf("write-mode gate: the operator declined %q — this room stays read-only. "+
		"Continue with read-only work and report what you would change instead.", action)
}

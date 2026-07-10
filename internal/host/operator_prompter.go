// Package host — operator-question forwarding seam.
//
// When a dispatched `claude -p` agent agent needs to ask the operator a
// clarifying question, the built-in AskUserQuestion tool is the wrong channel:
// headless `-p` has no TTY, so the CLI auto-resolves AskUserQuestion with EMPTY
// answers (~37ms; anthropics/claude-code#50728) and the model proceeds on a
// guess. AskUserQuestion is therefore hard-denied on every agent subprocess
// (see alwaysDeniedTools).
//
// The supported replacement forwards the question INTO kitsoki for generic
// handling: a replacement MCP tool (mcp__operator__ask, wired in a later phase)
// calls back through this seam, the live surface (web/TUI) renders the question
// natively, the operator answers, and the answer is returned to the model as the
// tool result. OperatorPrompter is the dependency-injected boundary between the
// surface-agnostic host handler and whichever surface owns the operator session.
//
// Interactivity is detected by the PRESENCE of a prompter in context: the TUI /
// web run loop installs one via WithOperatorPrompter; non-interactive callers
// (`kitsoki turn`, flow-fixture tests, cassette replay, the agent-serve headless
// path) install none. When no prompter is attached the dispatch layer keeps the
// "tool-denied" posture — the replacement tool is not offered and AskUserQuestion
// stays denied — so the model decides on its own rather than blocking forever on
// a question nobody can answer.
//
// Mirrors the WithStreamSink / WithKitsokiSessionID seams: a typed context key,
// a nil-safe With… constructor, and a presence-returning accessor.
package host

import "context"

// OperatorOption is one selectable answer for an OperatorQuestion. It mirrors a
// single AskUserQuestion option so the replacement tool is a drop-in: Label is
// the choice the operator picks (and the value returned to the model); Description
// explains what the choice means.
type OperatorOption struct {
	Label       string
	Description string
}

// OperatorQuestion is one question forwarded to the operator. The fields mirror
// the built-in AskUserQuestion schema verbatim so the replacement MCP tool can
// accept the identical payload and our typed-view choice rendering can be reused:
//
//   - Question:    the full question text shown to the operator.
//   - Header:      a short (≤12 char) chip/label categorising the question.
//   - Options:     2–4 mutually-exclusive (or, with MultiSelect, combinable) choices.
//   - MultiSelect: when true the operator may pick more than one Option.
type OperatorQuestion struct {
	Question    string
	Header      string
	Options     []OperatorOption
	MultiSelect bool
}

// OperatorPrompter forwards agent questions to the live operator surface and
// blocks until the operator answers (or the wait is cancelled / times out).
//
// The returned map mirrors AskUserQuestion's answer shape: it is keyed by each
// question's Question text, and each value is the chosen Option.Label or a
// custom single-select answer (a string), or, for a MultiSelect question, the
// chosen labels ([]string). A returned error means no usable answer was obtained
// (operator cancelled, timed out, ctx done, or the surface detached) — the
// dispatch layer surfaces that to the model as a tool error so the agent
// proceeds without the input rather than hanging.
//
// Concurrency contract: Ask is called from the host handler's per-call socket
// listener goroutine and MUST honour ctx cancellation (the agent call, and thus
// the whole turn, blocks inside it). Implementations may serialise questions per
// session; callers must not assume concurrency.
type OperatorPrompter interface {
	Ask(ctx context.Context, sessionID string, questions []OperatorQuestion) (answers map[string]any, err error)
}

// operatorPrompterKey is the context key for a per-session OperatorPrompter.
type operatorPrompterKey struct{}

// WithOperatorPrompter returns a child context carrying prompter. The TUI / web
// run loop installs one so agent dispatch knows a live operator can answer
// forwarded questions. A nil prompter is a no-op — returns ctx unchanged so the
// "tool-denied" headless posture is the default for callers that never set one.
func WithOperatorPrompter(ctx context.Context, prompter OperatorPrompter) context.Context {
	if prompter == nil {
		return ctx
	}
	return context.WithValue(ctx, operatorPrompterKey{}, prompter)
}

// OperatorPrompterFrom returns the prompter installed in ctx and whether one was
// present. The boolean is the interactivity signal the dispatch layer gates on:
// present ⇒ attach the replacement ask tool; absent ⇒ keep AskUserQuestion denied
// with no replacement (the model decides on its own).
func OperatorPrompterFrom(ctx context.Context) (OperatorPrompter, bool) {
	p, ok := ctx.Value(operatorPrompterKey{}).(OperatorPrompter)
	return p, ok && p != nil
}

// OperatorInteractive reports whether a live operator surface is attached to
// this call — i.e. whether forwarded questions can actually be answered. It is
// the single predicate the dispatch layer uses to choose between the
// forward-the-question posture and the tool-denied headless posture.
func OperatorInteractive(ctx context.Context) bool {
	_, ok := OperatorPrompterFrom(ctx)
	return ok
}

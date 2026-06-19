# Operator-ask forwarding — answering an agent's questions

A dispatched oracle agent (`ask`/`decide`/`task`/`converse`, each a
`claude -p` subprocess) sometimes needs to ask the operator a clarifying
question. The built-in `AskUserQuestion` tool is the obvious vehicle —
and it is a landmine. **Operator-ask forwarding** replaces it: the
agent's question is carried *into kitsoki*, rendered natively on the live
surface (web + TUI), answered by the operator, and the answer is fed back
to the agent as the tool result so it continues.

## The problem: headless `AskUserQuestion` auto-resolves empty

Under headless `claude -p` there is no interactive terminal, so the
built-in `AskUserQuestion` tool resolves immediately with **empty
answers** (anthropics/claude-code#50728) — a silent failure: the agent
"asked", got blank input, and proceeds on garbage with no signal to the
operator. So `AskUserQuestion` is **hard-denied everywhere** — it is in
`alwaysDeniedTools` in `internal/host/agents.go` on every subprocess.
Denying it is correct but leaves the agent unable to ask at all; the
forwarding bridge restores that ability through a path kitsoki controls.

## Design: synchronous block via a per-call socket + a DI prompter

Oracle verbs run **in-process, synchronously, mid-turn**, blocking the
orchestrator turn goroutine inside the host verb while `claude -p` runs
as a direct child. A blocking round-trip from that subprocess back out to
the operator is therefore consistent with how oracle calls already
behave — the turn is *already* parked. The bridge is one synchronous
block layered on top of that park:

```
claude -p (agent)
  └─ calls mcp__operator__ask({questions:[…]})        # AskUserQuestion stays DENIED
       └─ kitsoki mcp-operator-ask  (MCP stdio grandchild)
            └─ dials $KITSOKI_OPERATOR_ASK_SOCK (per-call unix socket), sends questions, BLOCKS
                 └─ host operatorAskListener (internal/host/operator_ask_bridge.go) bridges socket ⇄ prompter
                      └─ ctx OperatorPrompter.Ask(sessionID, questions)   # DI seam
                           ├─ WEB: register pending Q → SSE push → answer RPC resolves
                           └─ TUI: bubbletea msg → choice modal → submit resolves
                      ← answers written back over the socket
            ← tool_result = answers
  └─ agent continues; oracle verb returns; turn completes & renders
```

### Why an MCP tool, not a claude-code hook

A `PreToolUse` hook can only **allow/deny** a call — it cannot *supply a
tool_result*. Answering a question means returning a real value to the
model mid-run, and the only mechanism that does that is an **MCP tool**
the agent calls. So the "replacement" is literal: deny `AskUserQuestion`,
add the MCP tool `mcp__operator__ask` whose result *is* the operator's
answer.

### Interactivity gating — the tool is attached only when answerable

The decisive question at dispatch is **is a live operator surface
attached to this session?** That capability is carried in context as an
`OperatorPrompter` (DI seam, `internal/host`), set by the TUI/web run
loop and **absent** for `kitsoki turn`, flow-fixture tests, cassette
replay, and the headless `oracle-serve` path. The seam mirrors
`WithKitsokiSessionID` / `WithStreamSink`:

```go
type OperatorQuestion struct{ Question, Header string; Options []OperatorOption; MultiSelect bool }
type OperatorPrompter interface {
    Ask(ctx context.Context, sessionID string, qs []OperatorQuestion) (answers map[string]any, err error)
}
func WithOperatorPrompter(ctx context.Context, p OperatorPrompter) context.Context
```

- **Prompter present** → host dispatch creates a per-call unix socket +
  listener goroutine, injects `KITSOKI_OPERATOR_ASK_SOCK` into the
  subprocess env, attaches the `operator-ask` MCP server, adds
  `mcp__operator__ask` to `--allowedTools`, and adds a system-prompt
  clause telling the agent to use it for clarifying questions.
  `AskUserQuestion` stays denied — exactly one path to ask.
- **Prompter absent (headless)** → none of that is attached;
  `AskUserQuestion` stays denied and the agent is instructed to proceed
  on its own.

This is why automated tests (no prompter) never block and never need an
operator: the tool simply isn't there.

### Rejected alternative — suspend/resume across turns

Capturing the question, killing the subprocess, transitioning to a
"question room", answering next turn, and `claude --resume`-ing was
rejected: MCP tool calls are synchronous, so killing the process leaves
an **unanswered `tool_use`** at history's end that `--resume` cannot
cleanly satisfy by injecting a later user message. The synchronous block
matches existing oracle-call-blocks-turn behavior and is far simpler.

## The three surfaces

1. **Host bridge** — `internal/host/operator_ask_bridge.go` listens on
   the per-call socket and calls `OperatorPrompter.Ask`; the
   `kitsoki mcp-operator-ask` subcommand (`cmd/kitsoki`) is the MCP stdio
   grandchild that dials the socket. Reuses the oracle-serve framing
   (newline-delimited JSON) and `resolveKitsokiBin` /
   `writeMCPConfigTempfile` helpers.
2. **Web** — `internal/runstatus/server/operator_questions.go`: a
   per-session pending-question registry keyed by `question_id`, an SSE
   feed (`/rpc/questions`), and the answer RPC
   `runstatus.session.answer_question {session_id, question_id, answers}`
   that resolves the waiting prompter. The frontend renders
   `OperatorQuestionModal.vue` reusing the typed-view choice widget.
3. **TUI** — `internal/tui/operator_prompter.go` +
   `operator_question.go`: an `OperatorPrompter` that pushes a bubbletea
   message into the running program and renders an inline
   `ModeOperatorQuestion` choice widget, resolving on submit.

## Wire / answer schema = `AskUserQuestion`'s, verbatim

Mirroring the built-in shape makes this a drop-in replacement and lets it
reuse the existing typed-view choice rendering:

- **Request**: `{questions:[{question, header, options:[{label, description}], multiSelect}]}`
- **Answer**: `{answers: {"<question text>": "<label>" | ["<label>",…]}}`

The wait is bounded (default ~5 min, matching the task timeout). On
timeout, operator-cancel, or context cancellation the bridge returns a
tool **error** ("operator did not answer; proceed without this input") so
the agent continues gracefully rather than hanging, and the socket +
registry entry are always torn down on return.

## Observability

Three greppable slog events land in the session trace (and the
agent-action transcript, so [`kitsoki-debugging`](../../.agents/skills/kitsoki-debugging/SKILL.md)
can see them), each carrying `question_id`, `headers`, `duration_ms`, and
`outcome`:

| Event | Meaning |
|---|---|
| `operator.question.asked` | a forwarded question reached the operator surface |
| `operator.question.answered` | the operator answered; the answer is en route to the agent |
| `operator.question.unanswered` | timeout / cancel — the agent got a tool error and proceeded |

If a dispatched agent seems stuck, a modal never appeared, or the agent
got blank answers, these three events are the first thing to grep.

## Nested sub-agents inherit the bridge

An agent that spawns its own sub-agents via the `Task` tool **can** forward
operator questions from inside those sub-agents: the `--mcp-config` (and the
allowed `mcp__operator__ask` tool) is inherited by `Task`-spawned sub-agents,
so a sub-agent's call reaches the per-call socket exactly as the top-level
agent's would. Verified `2026-06-12` with a real `claude -p` run (claude
2.1.173) against a socket harness mirroring `attachOperatorAsk`: a top-level
agent instructed to call the tool *only from a spawned sub-agent* produced a
single socket hit tagged with the sub-agent's question, and the sub-agent
received the operator's answer back through the tool result. No special
handling is required for the nested case.

## Second consumer: the write-mode gate's action proposals

The bridge is surface-agnostic, so a second caller reuses it without new wire:
the **write-mode gate** for `write_mode: read_only` agent rooms (see
[hosts.md](hosts.md#write-mode-gate) and
[state-machine.md](../stories/state-machine.md#write_mode)). When the dispatched
agent attempts a mutating step (an `Edit`/`Write`, a Bash command the read-only
profile rejects, or an `effect ≥ write` host call), the gate forwards an **action
proposal** through the same `OperatorPrompter.Ask` seam — a single question
("The agent wants to *edit X* / *run Y*. Allow this *write* action?") whose
options are the grant scopes plus deny:

| Option | Meaning |
|---|---|
| `turn` (surfaced default) | allow edits for the rest of this turn |
| `action` | allow just this one call |
| `session` | allow edits for the rest of the session |
| `deny` | keep the agent read-only |

The interactivity gate is identical to forwarded questions: with no operator
attached the gate takes the headless path and **denies** the mutating step (the
agent gets a tool-error and stays read-only), mirroring the
no-replacement-tool posture above. An `effect: external` action (a push, a PR)
omits `turn`/`session` — it always re-asks per action, so "stop asking me about
edits" never silently authorizes an irreversible call. The operator's verdict is
recorded as a `machine.write_mode_granted` trace event (the gate's audit trail);
unlike a forwarded question, the write-mode grant is a *recorded interpretive
decision*, not just a passthrough. See `internal/host/write_mode_gate.go`
(`operatorAskGrant`, `writeModeActionProposal`).

## Open items (deferred pending LLM budget)

- **Live end-to-end test**: the path is covered by stub/cassette tests
  (no real LLM); a gated, real-`claude -p` end-to-end run is deferred and
  must only be done on explicit request (it incurs LLM cost).

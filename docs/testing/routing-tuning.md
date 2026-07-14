# Routing Testing and Tuning

Use this loop when a story should route free-text reliably before it spends an
agent turn. The goal is not to make every phrase deterministic; it is to catch
high-value operator phrases, preserve good off-ramps, and leave a recording that
future no-LLM runs can replay.

## Mode 0 vs Mode 1: which runner asserts what

Two runners read the SAME `test_kind: intents` fixture files but assert
different things — pick the one that matches what you're actually testing:

- **`kitsoki test routing`** (Mode 0, `internal/testrunner/routing.go`) calls
  `orchestrator.Classify` directly — the deterministic display/example match →
  semantic synonym/template → optional embedding tier. **No harness is ever
  constructed; there is no LLM in this path, full stop.** A phrase with no
  matching recording entry is a genuine FAIL here, not a silent skip. Use this
  to prove a phrasing resolves (or correctly defers) WITHOUT spending a live
  model call, and to gate CI on the no-LLM tiers specifically.
- **`kitsoki test intents`** (Mode 1, `internal/testrunner/intents.go`) runs the
  full harness stack (static recording lookup, or a live claude/codex/copilot
  profile) — this is the tool for benchmarking against a live model and for
  tuning phrasing that genuinely needs the LLM. Its static mode SKIPS (counts
  as a pass) any input with no recording entry — it cannot prove a deterministic
  tier resolved something.

A fixture can declare `defers_to_interpreter: true` instead of an `intent:`
block to assert the OPPOSITE under `test routing` — that Classify returns
ok=false for that phrase (content-bearing free text these tiers correctly
decline to guess at). See the field's doc comment in
`internal/testrunner/routing.go` for the precise scope (it only speaks for
Classify's three tiers, not a state's `default_intent` sink or the LLM).

```sh
go run ./cmd/kitsoki test routing stories/dev-story/app.yaml \
  --intents stories/dev-story/intents/workflow_core5_routing.yaml
```

## Routing feedback (WS-C C4)

Every surface that renders a routed turn can offer a thumbs-up/down
affordance so operators flag misroutes as they happen, without waiting for a
tuning pass. The TUI's is `/route up|down` (see
`internal/tui/commands_route.go`), which journals
`Orchestrator.RecordRoutingFeedback` — a standalone `routing.feedback` journal
entry (`internal/journal/types.go` `KindRoutingFeedback`) keyed to
`(phrase, state, intent, tier, verdict)`, mirrored as the `turn.routing_feedback`
trace event. It never touches world or machine state. Web/VS Code/gh-agent
affordances are not yet built — see
`.context/dev-workflows-surface-matrix-plan.md` WS-C C4.

The tuning loop this feeds is **minimize negative feedback over time**, not a
static misroute-rate floor: mine `routing.feedback` entries with `verdict:
down` the same way you mine transcripts below, turning each into a new
near-miss fixture.

## Loop

1. Write 10-15 intent cases under `stories/<story>/intents/`.
   - Include the happy path.
   - Include near misses that must route elsewhere.
   - Keep expected slots explicit when the target intent captures text.
2. Run a cheap plan first:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --dry-run
   ```

3. Run one live tuning pass against the target profile:

   ```sh
   go run ./cmd/kitsoki test intents stories/dev-story/app.yaml \
     --intents stories/dev-story/intents/landing_proposal_routing.yaml \
     --harness claude \
     --profile codex-native \
     --runs 1 \
     --max-cost 5 \
     --json .artifacts/dynamic-workflows/landing-proposal-routing-gpt55-tuned.json \
     --emit-recording .artifacts/dynamic-workflows/landing-proposal-routing-tuned.recording.yaml
   ```

4. Summarize failures:

   ```sh
   python3 tools/routing-tuning/intent_report_summary.py \
     .artifacts/dynamic-workflows/landing-proposal-routing-gpt55-tuned.json
   ```

5. Tune the story, not the test harness.
   - Add or sharpen intent examples.
   - Clarify intent descriptions and boundaries.
   - Raise/lower priority only when the boundary is genuinely wrong.
   - Update wrong expectations when the model found a better existing route.
6. Re-run with `--runs 1` until clean.
7. Run a confidence pass with `--runs 3` or higher only after the one-pass set is
   clean.

## Session Mining Link

Session mining should feed this loop. Mine real transcripts for:

- phrases users typed at a hub or ambiguous room;
- corrections like "no, I meant proposal/design";
- turns that landed in a broad `work` sink but then produced a proposal;
- turns that entered a specialized room accidentally.

Convert those mined phrases into intent fixtures before tuning. Keep the mined
source path or trace id in fixture comments when it is useful evidence.

Useful starting points:

- `stories/dev-story/intents/landing_proposal_routing.yaml`
- `docs/testing/examples/landing-proposal-routing-report.md`
- `docs/architecture/ambient-mining.md`
- `.agents/skills/story-coverage-mining/SKILL.md`
- `.agents/skills/session-idea-mining/SKILL.md`

## Evidence

Store live reports and emitted recordings under `.artifacts/<topic>/`. Do not
commit those generated reports unless a proposal explicitly needs them as a
review artifact. Commit the fixture, story tuning, docs, and any small reusable
tooling.

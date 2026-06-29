# Session Mining Backend Generalization

**Status:** Draft v1, partially implemented. The initial canonical corpus
contract, JSONL source adapters, and kitsoki trace-pattern substrate are in
`internal/mining/corpus.go` and `internal/mining/kitsokipattern/`; pipeline
driver integration, ambient miner source registry wiring, and CLI/report
surfaces remain. Focused package tests for `internal/mining/...` pass in the
current checkout.
**Kind:** Epic
**Date:** 2026-06-22

## Why

Kitsoki already has a useful session-mining concept, but the current substrate is
Claude Code shaped at the ingestion layer. The downstream goals are broader:
efficiently answer questions over prior work, find examples, create test
scenarios, surface reusable workflow patterns, and identify candidates for
progressive determinism across Claude Code, Codex, kitsoki itself, and future
agent backends.

The existing architecture has the right instincts: local-first artifacts,
distilled traces, one strictly bounded agent pass, deterministic grounding,
watermarks, no-LLM tests, and traceable outputs. The main problem is that those
strengths sit behind source assumptions such as `~/.claude/projects/<slug>`,
Claude Code JSONL fields, `jq` extraction, and `entrypoint=cli|sdk` semantics.
Those should become one backend adapter, not the pipeline contract.

## Existing State

Reusable pieces that should remain:

- `tools/session-mining/README.md` defines three useful modes: pattern mining,
  focused idea mining, and intent mining. Intent mining is already close to the
  desired model: preserve instances, recover verbatim user text, draft cited
  recipes, ground them deterministically, score determinism, and emit linked
  `intents.json` / `analysis.json`.
- `tools/session-mining/prep.py` provides the operational spine: filter source
  sessions, distill to compact traces, bin-pack batches, emit a manifest, and
  capture real cost telemetry when available.
- `tools/session-mining/intents.workflow.js` keeps the LLM in one schema-checked
  pass and requires citations on each proposed action.
- `ground.py`, `tag_score.py`, `emit.py`, `outcomes.py`, `verify_link.py`, and
  `validate_reports.py` make the useful claims evidence-backed instead of
  narrative-only.
- `docs/proposals/session-pattern-mining/README.md` maps mining output to
  kitsoki stories and the progressive-determinism ladder.
- `docs/stories/story-coverage-mining.md` shows how mined intents become story
  coverage, fixture gaps, divergence checks, and new scenarios.
- `internal/mining` already wraps mining as an injected runtime service with a
  resolver, watermark store, pipeline runner, recipe handler, proposer, and
  no-LLM seams.
- `docs/architecture/ambient-mining.md` documents the propose/apply loop,
  provenance events, and the key invariant that tests and flow fixtures must not
  spend LLM.
- Kitsoki traces already contain higher-signal pattern evidence than raw CLI
  transcripts: routed intents, room/state transitions, exits, host calls,
  `machine.gate_decided`, `mining.pass_ran`, proposal events, world changes, and
  `transcript_ref` pointers into agent-action sidecars.
- The routing stack already records provenance (`turn.start` routing attrs,
  `turn.*_routed`, `turn.context_route_decided`, contextual route receipts, and
  overrides). A user re-routing because the first route was bad is direct
  supervision data, not just UX noise.

Current limitations:

- `internal/mining.TranscriptResolver` is Claude Code specific:
  repo path -> `~/.claude/projects/<slug>`.
- `prep.py` assumes each source directory is a flat directory of Claude Code
  `.jsonl` transcripts.
- `distill.jq` assumes Claude Code's user/assistant message layout and tool-use
  block schema.
- Seed/live filtering relies on Claude Code's `entrypoint` / `promptSource`
  fields to distinguish interactive human sessions from dispatched agents.
- Cost and outcome extraction are Claude Code oriented, with no normalized
  fallback contract for sources that expose usage, tool results, or turns
  differently.
- Pattern mining is still framed mostly around developer CLI workflows. It does
  not yet mine kitsoki-native behavior as first-class evidence: repeated story
  paths, recurring gates, operator refinements, failed exits, fixture gaps,
  abandoned flows, or high-value transitions that could become more
  deterministic.
- Routing feedback is not yet normalized as a mining input. When a user rewinds,
  switches route, restarts from a checkpoint, or immediately rephrases after a
  bad route, that should become a trace-level correction signal that can be mined
  without LLM judgment.
- The ambient miner can accept extra `transcript_dirs`, but not a typed set of
  backends with discovery, source identity, or backend-specific scan rules.
- The pipeline outputs are useful, but not yet centered around a generic
  evidence model that every consumer can reuse for questions, examples, tests,
  coverage, pattern ranking, and determinism training.

## What Changes

Introduce a backend-normalized session mining substrate:

```
source adapters
  kitsoki-trace, claude-code, codex, imported-jsonl
        |
        v
canonical session envelope
  session metadata, ordered turns, tool calls, tool results, costs, files, source refs
        |
        v
distilled trace + evidence index
  human-readable trace lines plus machine-readable anchors into raw source
        |
        v
analysis drivers
  intent mining, kitsoki pattern mining, idea mining, example search, scenario
  authoring, story coverage, progressive determinism candidate discovery
```

The key move is to make source adapters responsible for backend quirks, and make
every later stage consume the same canonical envelope and evidence index.

## Slices

| Slice | Kind | Scope | Depends on | Status |
|---|---|---|---|---|
| Backend-normal corpus | runtime | `SessionSource` adapters for Claude Code, Codex, kitsoki traces, and imported JSONL; canonical session envelope; evidence index. | — | Draft |
| Kitsoki trace pattern matching | tracing | Deterministic mining over kitsoki trace events: typed tokens, bounded path windows, SCC loop signatures, route-feedback aggregation, and structural verification. | Backend-normal corpus | [Partial substrate shipped](kitsoki-trace-pattern-matching.md) |
| Mining drivers | runtime+tooling | Thin drivers for intent mining, example search, scenario authoring, story coverage, and progressive-determinism candidates over the canonical corpus. | Backend-normal corpus | Draft |
| Ambient miner integration | runtime | Wire typed source configuration, watermarks, no-LLM test seams, and proposal artifacts into `internal/mining`. | Backend-normal corpus, Mining drivers | Draft |

## Canonical Contracts

### Source Adapter

Each backend implements:

```go
type SessionSource interface {
    ID() SourceID
    Discover(ctx context.Context, scope SourceScope) ([]SessionRef, error)
    Load(ctx context.Context, ref SessionRef) (CanonicalSession, error)
}
```

`Discover` is cheap and incremental. `Load` is exact and source preserving. The
source implementation owns backend-specific details such as:

- Claude Code: path slugging, `entrypoint` filtering, tool-use block parsing.
- Codex: session location, turn shape, tool-call/tool-result structure, usage
  telemetry if present.
- Kitsoki trace: `agent.call.*`, `machine.gate_decided`, `transcript_ref`, and
  runstatus sidecars. This is not only an agent-transcript adapter; it is the
  canonical source for kitsoki-native pattern mining because it preserves the
  state machine behavior that CLI logs can only imply. It also owns routing
  feedback extraction: route decisions, route overrides, correction turns, and
  the final accepted route.
- Imported JSONL: a stable exchange format for experiments or other tools.

### Canonical Session

The normalized session should be expressive enough for every mining consumer
without erasing provenance:

```jsonc
{
  "schema_version": "session-corpus.v1",
  "source": { "backend": "kitsoki-trace|claude-code|codex|imported-jsonl",
              "ref": "...", "path": "...", "mtime": 0 },
  "session": { "id": "...", "repo": "...", "cwd": "...",
               "started_at": "...", "entrypoint": "human|agent|unknown" },
  "turns": [
    { "id": "...", "role": "user|assistant|system|tool",
      "text": "...", "source_ref": { "line": 123 } }
  ],
  "tool_calls": [
    { "id": "...", "turn_id": "...", "tool": "Bash",
      "input": { "command": "go test ./..." },
      "result": { "is_error": false, "stdout_head": "...", "stderr_head": "" },
      "source_ref": { "line": 145 } }
  ],
  "usage": { "exact": true, "input_tokens": 0, "output_tokens": 0,
             "total_cost_usd": 0 },
  "kitsoki": {
    "story": "...",
    "rooms": ["idle", "test", "review"],
    "intents": ["start", "accept"],
    "routes": [
      { "decision_id": "...", "input": "...", "routed_by": "semantic",
        "selected": { "intent": "quit", "slots": {} },
        "final": { "intent": "refine", "slots": { "feedback": "..." } },
        "feedback": "bad_route|route_override|rephrase|checkpoint_restart",
        "source_ref": { "event": 42 } }
    ],
    "gates": [{ "id": "...", "decision": "...", "verdict": "..." }],
    "exits": ["done"],
    "world_changes": [{ "key": "flows_green", "value": true }]
  }
}
```

This is not the public report. It is the private, local evidence substrate under
`.artifacts/session-mining/<job>/canonical/`, with source refs back to the raw
session data. The `kitsoki` block is optional for CLI sources and required for
`kitsoki-trace` sources where those events exist.

### Distilled Trace Plus Evidence Index

Keep the current trace readability:

```
USER: fix the failing tests
AI: I will inspect the failures.
  > Bash: go test ./internal/mining/...
  > Read: internal/mining/pipeline.go
```

Add a sidecar `evidence.json` mapping each trace line to canonical IDs:

```jsonc
{
  "trace": "traces/<session>.txt",
  "lines": {
    "3": { "kind": "tool_call", "tool_call_id": "...",
           "source_ref": { "backend": "codex", "path": "...", "line": 88 } }
  }
}
```

`ground.py` should validate against this evidence index instead of reparsing a
Claude-shaped trace line. The human-readable trace remains stable; grounding
gets stronger and backend-independent.

## Analysis Drivers

The same canonical corpus should support several drivers:

| Driver | Question | Output | LLM use |
|---|---|---|---|
| Intent mining | What did the user ask, and what grounded actions reproduce it? | `intents.json`, `analysis.json` | One schema-checked pass, then deterministic grounding |
| CLI pattern mining | Which recurring developer workflow types are worth automation? | redacted shareable `report.json`, `BRIEF.md` | Extractor over redacted traces |
| Kitsoki pattern mining | Which story paths, gates, exits, refinements, failures, and transitions recur across runs? | `patterns.json`, ladder candidates, scenario seeds | Mostly deterministic over trace events; optional summarizer |
| Focused idea mining | What have I said about topic X? | local themed brief | Reader fan-out over scoped batches |
| Example search | Show evidence-backed examples of workflow/pattern X | `examples.md` plus cited trace refs | Optional reranker only |
| Scenario authoring | Turn examples into flow/test candidates | scenario catalog + fixtures draft | Agent draft gated by deterministic fixture validation |
| Story coverage mining | Does a story conform to real usage? | coverage worksheet and tickets | Human/LLM map gate, evidence-backed |
| Progressive determinism mining | Which gates can move down the ladder? | gate candidates, labels, validation plan | Optional classifier, deterministic scoring |

The drivers should be thin. They should select a corpus, declare an output
schema, call common batching/grounding utilities, and write reviewable artifacts.

### Kitsoki Pattern Mining

Kitsoki is a special source because it already emits structured behavior. For
pattern discovery, prefer deterministic extraction from trace events before
asking an LLM to summarize:

- room paths: repeated `state A -> state B -> exit` paths, loops, restarts, and
  abandoned runs;
- intent paths: free-text routing outcomes, direct intent submissions, unknown
  or corrected routes, and repeated synonyms;
- gate paths: `machine.gate_decided` decisions, verdict distribution,
  confidence, decider type, and follow-up correction/refinement;
- host/agent paths: host call sequences, `agent.call.*` outcomes,
  `transcript_ref` availability, tool-call counts, and failure classes;
- route-feedback paths: route decisions followed by override, rewind,
  checkpoint restart, immediate contradictory intent, or accepted alternative;
- proposal paths: `mining.pass_ran`, `mining.proposal_raised`,
  `mining.proposal_decided`, accept/refine/reject ratios, flow-gate failures;
- test paths: flow names, fixture outcomes, world assertions, and branches that
  are never exercised;
- world paths: stable world mutations that indicate a mechanical transition or
  a repeated decision label.

The output should not be the same as CLI pattern mining's shareable
`report.json`. Kitsoki-native patterns need a richer local report:

```jsonc
{
  "schema_version": "kitsoki-patterns.v1",
  "patterns": [
    {
      "id": "verify-modality-gate",
      "kind": "gate|route|route-feedback|room-path|host-sequence|proposal|fixture-gap",
      "story": "implementation",
      "evidence": [
        { "trace": "runs/abc.jsonl", "events": [42, 43, 44],
          "analysis_ref": "analysis.json#abc#3" }
      ],
      "frequency": 12,
      "outcomes": { "accepted": 8, "refined": 3, "abandoned": 1 },
      "determinism_candidate": {
        "current_ladder": "L2-agent-gated",
        "target_ladder": "L3-default-decider",
        "validator": "flow fixture covers trace/live/manual branches"
      }
    }
  ]
}
```

This report becomes the bridge from observed kitsoki behavior to concrete work:
new flow fixtures, story enrichments, routing synonyms, gate defaults, proposal
tickets, and candidate decider training labels.

Algorithm guidance for this slice lives in
[`kitsoki-trace-pattern-matching.md`](kitsoki-trace-pattern-matching.md). The proposed strategy is
deterministic-first: normalize trace events into typed tokens, mine bounded path
windows and directly-follows graphs, collapse repeated SCC loops into canonical
path signatures, aggregate route-feedback corrections, and reserve exact graph
matching or frequent subgraph mining for small promoted candidates. LLMs may
summarize mined reports, but should not be the matcher or support counter.

### Route Feedback

Bad routing is one of the highest-value deterministic mining signals because the
user often supplies the label by correcting it. Kitsoki should record this as a
trace fact, not leave it to transcript interpretation.

Add or normalize a routing feedback event that links:

- the original route decision: decision ID, input text, state, allowed intents,
  selected intent/class, slots, tier (`routed_by`), match type, confidence, and
  reason;
- the feedback action: switch route, rewind route, checkpoint restart, direct
  intent submit, correction/rephrase, or explicit "wrong route" UI action;
- the accepted correction: final intent/class/slots or lane;
- optional operator note when the user supplies one;
- provenance: turn/event IDs for both the bad route and the correction.

Shape:

```jsonc
{
  "kind": "turn.route_feedback",
  "attrs": {
    "decision_id": "ctxroute-...",
    "feedback": "bad_route",
    "original": {
      "input": "this needs another pass",
      "state": "review",
      "routed_by": "semantic",
      "intent": "accept",
      "slots": {},
      "confidence": 0.72
    },
    "correction": {
      "mode": "switch_route|rewind|direct_intent|rephrase|checkpoint_restart",
      "intent": "refine",
      "slots": { "feedback": "this needs another pass" }
    }
  }
}
```

The miner can then compute route quality deterministically:

- false-positive routes: original intent differs from corrected final intent;
- missing synonyms: rephrase/direct intent repeatedly corrects to the same
  target;
- bad slot extraction: same intent, changed slots;
- bad class choice: contextual route lane corrected to an advancing intent, or
  vice versa;
- dangerous defaults: default intent captures inputs that users later re-route;
- determinism candidates: repeated corrections with the same final label become
  synonym/default-decider candidates.

This should feed both `patterns.json` and a small route-feedback report:

```jsonc
{
  "route_feedback": [
    {
      "route_key": "review:semantic:accept",
      "corrections": 7,
      "top_final": "refine",
      "examples": [
        { "trace": "runs/abc.jsonl", "event": 51,
          "decision_id": "ctxroute-...", "input_hash": "..." }
      ],
      "suggestion": "add synonym or lower semantic confidence for accept"
    }
  ]
}
```

Because this is linked to explicit route decisions and corrections, it does not
need an LLM to find. An LLM may summarize examples after the deterministic report
exists, but it should not decide whether the correction happened.

## Progressive Determinism

Use the canonical evidence model to make determinism candidates explicit:

1. Detect repeated intent clusters with stable tool signatures and low outcome
   variance.
2. Detect correction/satisfaction signals from follow-up turns and tool outcomes.
3. Identify judgment gates by repeated forks: code-vs-test fix, verification
   modality, accept/refine/restart, conflict strategy, commit-scope selection.
4. For kitsoki traces, treat existing gate decisions and operator refinements as
   label evidence. A recurring gate with stable verdicts and green validation is
   a stronger ladder candidate than a CLI-only pattern with inferred decisions.
5. Treat route feedback as supervised routing data. A corrected route is a
   negative label for the original route and a positive label for the final
   route. Repeated corrections are candidates for synonyms, confidence threshold
   changes, contextual-router examples, or default-decider promotion.
6. Score each candidate with measured signals:
   frequency, grounded action completeness, retry/edit-rerun cycles, correction
   rate, outcome variance, existing story coverage, gate-verdict entropy, and
   fixture coverage.
7. Emit a candidate record:

```jsonc
{
  "gate_id": "verify-modality",
  "evidence": ["analysis.json#sess#3", "analysis.json#sess#9"],
  "current_ladder": "L2-agent-gated",
  "target_ladder": "L3-default-decider",
  "validator": "flow fixture covers trace/live/manual branches",
  "risk": "medium"
}
```

This connects mining directly to the kitsoki moat: interpretive choices become
named gates, recorded outcomes become labels, and stable labels become default
deciders.

## Config Surface

Extend the `mining:` block from Claude-specific transcript dirs to typed
sources:

```yaml
mining:
  enabled: true
  cadence: 30s
  first_pass_sample: 12
  priority_threshold: 0.6
  sources:
    - backend: kitsoki-trace
      dir: .artifacts/runs
      include: both
      patterns: true
    - backend: claude-code
      repo: .
      include: human
    - backend: codex
      repo: .
      include: human
  mined_through:
    kitsoki-trace:.artifacts/runs: 1770000000
    claude-code:/Users/brad/code/Kitsoki: 1770000000
```

Compatibility lean:

- Keep existing `transcript_dirs` as a migration input only, or translate it at
  config load into `sources: [{backend: claude-code, dir: ...}]`.
- Runtime code should use `sources`, not carry a second compatibility path.
- Watermarks key by source identity, not only Claude slug.

## Implementation Slices

### 1. Corpus Adapter Substrate

Kind: runtime/tooling.

- Add `tools/session-mining/corpus/` or `internal/mining/corpus/` for canonical
  source types and schemas.
- Implement `claude-code` adapter first by moving existing `prep.py`,
  `distill.jq`, `cost_extract.py`, `outcomes.py`, and resolver assumptions
  behind the adapter.
- Add fixtures that cover human sessions, dispatched agent sessions, tool calls,
  tool results, missing result IDs, and usage telemetry.
- Tests are pure fixture tests; no LLM.

### 2. Kitsoki Trace Adapter And Pattern Extractor

Kind: tracing/tooling.

- Implement `kitsoki-trace` as a first-class source before or alongside the
  first CLI adapter, not as a later import format.
- Normalize trace events into canonical turns/tool calls where applicable, and
  preserve kitsoki-native fields: app/story, session ID, state/room path,
  intents, exits, gate decisions, host calls, proposal events, world mutations,
  flow/test outcomes, and `transcript_ref` sidecars.
- Add a deterministic `kitsoki-patterns` driver that reads canonical kitsoki
  fields and emits repeated room paths, gate patterns, route corrections,
  proposal outcomes, host sequences, fixture gaps, and ladder candidates.
- Add `turn.route_feedback` normalization. It may be produced directly by new
  route UI actions, or derived when existing trace events show a route override,
  rewind, checkpoint restart, direct-intent correction, or immediate rephrase.
- Add fixtures from small committed trace snippets and runstatus sidecars. Do
  not require live web, live TUI, or live LLM.
- Ensure pattern evidence can cite event indices and, when an agent transcript is
  involved, the sidecar event range.

### 3. Codex Adapter

Kind: runtime/tooling.

- Discover the local Codex session store format and implement source discovery.
- Normalize Codex user turns, assistant turns, tool calls, tool results, usage,
  cwd/repo metadata, and session identity.
- Add fixtures from sanitized local/canned Codex transcripts.
- If Codex does not expose some field, set `unknown` / `exact:false`; do not
  fake parity with Claude.

### 4. Evidence Index And Grounding Upgrade

Kind: tracing/tooling.

- Emit `canonical/*.json` plus `traces/*.txt` plus `evidence/*.json`.
- Update `ground.py` to validate tool and parameters through evidence IDs rather
  than Claude-shaped trace parsing.
- Keep the trace text format stable for human review and existing prompts.
- Update schemas so `analysis.json.actions[].cite` can point to a trace line and
  evidence ID.

### 5. Multi-Source Prep And Watermarks

Kind: runtime.

- Replace `TranscriptResolver` with a source registry and source-scoped
  watermark keys.
- Update `ExecPipelineRunner` to accept a prepared corpus/job directory instead
  of one Claude project dir.
- Preserve seed/live policy, but express it as `include: human|agent|both`
  instead of `entrypoint=cli|sdk`.
- Keep flow posture gated out: no live LLM in tests or cassettes.

### 6. Driver API

Kind: tooling/story.

- Factor shared driver utilities: scope selection, bin packing, schema
  validation, evidence linking, report emission, and redaction gates.
- Make each mining use case a small driver command:
  `intent`, `pattern`, `idea`, `examples`, `scenario`, `coverage`, `gates`.
- Outputs stay under `.artifacts/session-mining/<job>/` unless explicitly
  producing a scrubbed shareable pattern report.

### 7. Kitsoki Story And Ambient Mining Integration

Kind: story/runtime.

- Update `stories/dev-story-mining` to ask for a corpus source/profile instead
  of "Claude Code transcripts".
- Let ambient mining consume the generic source registry.
- Add a kitsoki-native pattern view that can start from trace evidence and
  choose: author a fixture, enrich a story gate, add a route synonym, ticket a
  divergence, or nominate a default decider.
- Add flow fixtures for source selection and for no-source/no-history behavior.
- Keep the author/map/decide gates cassette-backed and fixture-tested.

### 8. Documentation Migration

Kind: docs.

- Update `tools/session-mining/README.md` around the canonical corpus contract.
- Update `docs/architecture/ambient-mining.md` for source registry, evidence
  index, watermarks, and traceability chain.
- Update `docs/stories/story-coverage-mining.md` to say "sessions" and "sources"
  instead of Claude Code where the backend does not matter.
- Migrate implemented sections into narrative docs as each slice ships.
- Trim/delete the promoted proposal when implementation lands.

## Traceability Requirements

Every generated artifact must answer:

- Which raw source sessions were included?
- Which backend adapter loaded each session?
- Which trace line supports each action, outcome, cost, and user text?
- Which schema version and prompt version produced each report?
- Which deterministic checks passed?
- Which LLM pass, if any, produced the hypothesis?
- Which routing decisions were corrected, how, and what final route superseded
  them?
- Which records were quarantined, dropped, or redacted, and why?

Minimum job layout:

```
.artifacts/session-mining/<job>/
  manifest.json
  sources.json
  canonical/<session>.json
  traces/<session>.txt
  evidence/<session>.json
  batches/batch-01.txt
  agent/agent-batch-01.json
  grounded.json
  scored.json
  intents.json
  analysis.json
  validation.json
```

## Efficiency Requirements

- Discovery reads metadata only when possible.
- Loading is incremental by source watermark.
- Distillation and evidence emission are per-session cacheable by source ref and
  mtime/content hash.
- Batch packing remains byte-budgeted to control reader-agent cost.
- Scoping uses cheap prefilters before any LLM pass: repo, backend, mtime,
  grep, tags, changed files, story profile.
- Pattern sharing stays redacted and aggregated; raw/canonical artifacts stay
  local and gitignored.
- Tests use fixtures and cassettes only.
- Route-feedback mining is event-based and should run before any summarizer or
  agent pass.

## Non-Goals

- A universal standard for every AI tool's private transcript format.
- Real-time interception of all backends. Mining can start from offline local
  session logs and kitsoki traces.
- Perfect semantic conformance without a judgment gate. Story coverage still
  needs a human/LLM map step when shell behavior and story world diverge.
- Automatic application of mined changes. Ambient proposals still require the
  existing proposal/apply gate.
- Runtime compatibility shims that keep old naming forever. Translate old
  config at migration boundaries; keep the canonical runtime surface clean.

## Open Questions

1. Where should canonical corpus code live: `tools/session-mining` Python first,
   `internal/mining/corpus` Go first, or a shared JSON schema plus one reference
   implementation? Lean: schema plus Python first, because the existing pipeline
   is Python and fixture-heavy.
2. Should kitsoki traces become a first-class source immediately? Lean: yes.
   They are the best pattern-mining source because they contain state-machine
   facts directly. CLI adapters prove breadth; kitsoki traces prove product
   utility.
3. How much Codex transcript history is available locally and how stable is its
   format? Lean: implement as best-effort with explicit `exact:false` fields and
   fixture-lock the format we rely on.
4. Should `distill.jq` be replaced outright? Lean: keep trace text output, but
   move backend parsing out of jq. Use jq only inside the Claude adapter if it
   remains useful.
5. Does ambient mining need cross-backend dedup? Lean: yes, but only after
   source-normalized instance IDs exist; initially dedup by normalized action
   signature plus user text hash within one job.

## Acceptance Criteria

- A single job can include Claude Code and Codex sessions and emit one linked
  `intents.json` / `analysis.json` pair.
- A single job can include kitsoki traces and emit `patterns.json` with cited
  room paths, gate decisions, proposal outcomes, and ladder candidates.
- Route feedback from bad routing is captured or normalized into trace evidence
  and mined deterministically into route-quality, synonym, and default-decider
  candidates.
- Kitsoki pattern extraction has a deterministic fixture suite over committed
  trace snippets and sidecars.
- Each report row has source refs back to raw backend data through
  `evidence.json`.
- Existing Claude Code intent-mining fixtures still pass.
- New Codex adapter fixtures pass without requiring network or live LLM.
- `ground.py` validates actions via evidence IDs, not Claude-specific parsing.
- Ambient mining can resolve configured sources and watermarks without assuming
  `~/.claude/projects/<slug>`.
- Story coverage mining can consume the generic reports without knowing which
  backend produced the source session.
- No automated test path spends LLM.

# Kitsoki Trace Pattern Matching

**Status:** Draft v1, partially implemented. `internal/mining/kitsokipattern`
now provides deterministic event-token lenses, bounded-window patterns,
route-feedback aggregation, SCC-based cycle path signatures, and fixture tests.
Artifact persistence, external export hooks, richer structural verification, and
driver/CLI integration remain.
**Kind:** Tracing
**Epic:** [Session Mining Backend Generalization](session-mining-backend-generalization.md)
**Date:** 2026-06-22

## Why

Kitsoki trace pattern mining should not start with an LLM or with general
subgraph mining over the whole story graph. The useful substrate is more
specific:

1. A kitsoki run is an ordered event log over a directed cyclic state graph.
2. The trace already names the interesting control points: route decisions,
   route corrections, accepted intents, guard evaluations, transitions, world
   updates, host calls, agent calls, proposal events, and flow/test outcomes.
3. Most valuable patterns are repeated bounded paths through that log, repeated
   local process fragments, or repeated corrections against a route/guard/gate,
   not arbitrary graph motifs.

Recommended shape:

```
kitsoki JSONL traces
  -> canonical typed event stream
  -> bounded path windows + directly-follows graph + correction labels
  -> deterministic miners:
       sequence / episode mining
       local process-model mining
       cycle-aware path signatures
       route-feedback aggregation
       conformance / fixture-gap checks
  -> optional exact structural matcher for promoted candidates
  -> reviewable pattern artifacts with source refs
```

LLMs can still summarize a finished report, but should not decide whether a
pattern exists. The first-class output should be deterministic evidence:
support counts, sequence IDs, event refs, matched spans, route/correction
labels, and reproducible fixture candidates.

## What Changes

Add a deterministic kitsoki trace-pattern substrate under the broader
[`session-mining-backend-generalization.md`](session-mining-backend-generalization.md)
epic. The substrate normalizes trace events into typed tokens, builds bounded
path windows and trace-derived graphs, collapses cyclic paths into comparable
signatures, mines route-feedback and workflow fragments with deterministic
algorithms, and emits cited pattern artifacts that can become fixtures,
synonyms, default routes, gate/decider candidates, or examples.

## Impact

The slice turns kitsoki traces into a reusable pattern corpus without adding
paid model calls to mining or tests. It should make recurring loops,
corrections, fixture gaps, and progressive-determinism candidates visible from
evidence instead of prose interpretation. It also creates a clean boundary for
optional offline experiments with sequence, process, or graph-mining tools
without making those tools part of the runtime path.

## Local Facts This Builds On

- `docs/tracing/trace-format.md` defines the JSONL trace as the session and
  records each event with `turn`, `seq`, `kind`, `state_path`, and `payload`.
- `turn.start` already carries routing provenance: `routed_by`, `match_type`,
  `confidence`, and direct-submit markers.
- The event vocabulary includes `machine.transition`, guard rejection,
  validation failure, state enter/exit, world update, host/harness events,
  agent-call events, and mining proposal events.
- `docs/architecture/semantic-routing.md` defines a deterministic-first routing
  stack and already documents contextual route decisions, route application, and
  route override events.
- The parent
  [`session-mining-backend-generalization.md`](session-mining-backend-generalization.md)
  epic treats `kitsoki-trace` as a first-class backend and proposes
  route-feedback normalization.

## Representation

Use three linked representations, all derived from the same canonical session:

### 1. Event Tokens

Each trace event becomes one normalized token:

```jsonc
{
  "token_id": "traceA:turn=12:seq=4",
  "case_id": "traceA",
  "turn": 12,
  "seq": 4,
  "kind": "machine.transition",
  "state": "review",
  "label": "transition:review:accept->testing",
  "attrs": {
    "intent": "accept",
    "from": "review",
    "to": "testing"
  }
}
```

The label is configurable by lens:

- `control`: room/state + intent + transition only;
- `route`: input signature + routed_by + selected intent + correction;
- `guard`: guard expression/verdict + selected arm;
- `host`: host/agent call names and outcome class;
- `world`: selected world keys, redacted and allow-listed;
- `test`: flow name, assertion branch, fixture gap.

### 2. Bounded Path Windows

For each trace, generate windows over the event stream:

- contiguous windows: `k=2..12` event n-grams;
- skip windows: allow up to `g` uninterpreted events between salient tokens;
- cycle-collapsed windows: repeated SCC loops represented as
  `loop(roomA -> roomB, count=3)` instead of unbounded repeated tokens;
- turn windows: all salient events inside a turn as one itemset.

This gives sequence miners a finite alphabet and prevents cycles from exploding
the search space.

### 3. Trace-Derived Graphs

Build small derived graphs instead of mining the raw story graph directly:

- **Directly-follows graph:** nodes are normalized event labels; edges count
  observed immediate succession.
- **Room path graph:** nodes are state paths; edges are observed transitions,
  annotated with intent, route tier, guard, host calls, and outcomes.
- **Correction graph:** original route decision -> correction action -> accepted
  final route, annotated with event refs.
- **Candidate pattern graph:** only for promoted windows, a small typed graph
  with nodes like `route`, `transition`, `guard`, `host`, `world_update`.

The story graph is the schema/model; the trace-derived graph is the observed
behavior.

## Algorithm Families

### 1. Sequence Pattern Mining

Use for repeated room paths, intent paths, host-call paths, and route-correction
paths.

Good fits:

- PrefixSpan-style projected mining for frequent ordered subsequences.
- SPADE/SPAM-style vertical-id-list mining when support lookup and incremental
  updates matter.
- Closed/maximal variants to suppress noisy subpatterns.
- Top-k variants to avoid hard global support thresholds on small corpora.

Why it fits kitsoki:

- Trace events are already ordered.
- We can map each run or each turn group to a sequence database case.
- Support can be exact and citeable: pattern `P` occurs in traces
  `traceA@turns[4..7]`, `traceB@turns[2..6]`, etc.
- It works before any LLM pass.

Where to use it:

- "Users route to design, refine twice, then publish."
- "Bad route to `accept` is followed by rewind and `refine`."
- "Host search fails, user clarifies repo, search succeeds."
- "Decision gate rejects, same world key changes, gate passes."

Constraints we should expose:

- minimum support or top-k;
- max pattern length;
- max gap;
- required/forbidden token kinds;
- case boundary: per trace, per story, per room family, per issue/work item.

Relevant references:

- SPMF lists PrefixSpan, SPADE, SPAM, closed/maximal/top-k sequential pattern
  miners, and sequence-ID output useful for evidence refs:
  <https://www.philippe-fournier-viger.com/spmf/index.php?link=algorithms.php>
- SPMF's PrefixSpan docs define support as the fraction of sequences containing
  a subsequence and support max pattern length plus sequence IDs:
  <https://www.philippe-fournier-viger.com/spmf/PrefixSpan.php>
- SPMF's SPADE docs describe the same sequence database model with optional
  sequence IDs and maximum pattern length:
  <https://www.philippe-fournier-viger.com/spmf/SPADE.php>

### 2. Frequent Episode Mining

Use when a pattern is local to one long stream and may include partial order or
bounded gaps.

Why it fits kitsoki:

- A single long dogfood trace may contain repeated local episodes even before
  we have enough separate sessions for sequence-database support.
- Some patterns are "A and B happen within the same turn before C", not a strict
  contiguous sequence.
- Episode mining with non-overlap support is a good way to count repeated
  correction/failure loops without double-counting overlapping windows.

Where to use it:

- route feedback episodes;
- guard-failure then clarification then success;
- host error then on-error transition then retry;
- repeated `world.update` + `emit_intent` chains.

Relevant reference:

- SPMF lists frequent episode and episode-rule algorithms including MINEPI and
  partially ordered episode rules:
  <https://www.philippe-fournier-viger.com/spmf/index.php?link=algorithms.php>

### 3. Process Mining / Local Process Models

Use for loops, choice, and recurring workflow fragments that are bigger than a
simple sequence but smaller than the whole story.

Good fits:

- Directly-follows graph construction from the event log.
- Local process model mining for recurring sequential, choice, concurrency, and
  loop fragments.
- Inductive-miner style cuts when we want a compact process-tree explanation.
- Conformance checking against an existing story/flow when the question is
  "what real paths diverge from the intended fixture/model?"

Why it fits kitsoki:

- Kitsoki traces are event logs with case IDs, ordered activities, and rich
  attributes.
- We care about local reusable fragments, not necessarily one global process
  model; global process discovery over a flexible story could produce a
  low-value "anything can happen" model.
- Loops and choices are explicit in process-mining notation, which maps well to
  cyclic story graphs.

Where to use it:

- recurring design/refine/publish loops;
- proposal review branches;
- fixture-gap detection;
- "happy path vs actual path" conformance;
- route-correction loops as a local process model.

Relevant references:

- Tax et al., "Mining Local Process Models", positions local process-model
  mining between process discovery and sequence/episode mining, and explicitly
  handles sequence, choice, concurrency, and loops:
  <https://arxiv.org/abs/1606.06066>
- Process mining literature treats event logs as case/activity/timestamp data
  and distinguishes process discovery, conformance checking, and enhancement:
  <https://en.wikipedia.org/wiki/Process_mining>

### 4. Cycle-Aware Path Signatures

Use as Kitsoki's cheap native matcher before heavier algorithms.

Algorithm:

1. Build the observed room path graph for a story/run corpus.
2. Compute strongly connected components over observed room transitions.
3. Assign each SCC a stable ID from sorted state paths and edge labels.
4. Convert each trace path into a canonical signature:
   - states outside SCCs stay literal;
   - repeated internal SCC walks collapse into `SCC(id, entry, exit, edge_multiset,
     count_bucket)`;
   - host/guard/route labels are retained as optional dimensions.
5. Hash signatures with versioned label normalizers.

Why it fits kitsoki:

- The story graph is directed and cyclic, and many analogous user journeys will
  spin the same loop a different number of times.
- Collapsing loops turns "refine once" and "refine four times" into comparable
  patterns while preserving count buckets.
- This is deterministic, cheap, and explainable.

Outputs:

```jsonc
{
  "kind": "cycle_path_signature",
  "signature": "idle -> design -> SCC(refine_loop, count=2-4) -> publish",
  "support": 17,
  "examples": [
    {"trace": "a.jsonl", "turns": [3, 9], "events": [12, 44]},
    {"trace": "b.jsonl", "turns": [5, 12], "events": [21, 70]}
  ]
}
```

This should be implemented before general subgraph mining because it captures
the core "same cycles and flows" requirement directly.

### 5. Weisfeiler-Lehman Hashes For Near-Duplicate Grouping

Use to bucket small candidate pattern graphs before exact matching.

Why it fits kitsoki:

- Candidate pattern graphs have labels and directions.
- WL/color-refinement hashes are fast and useful as a prefilter.
- Identical hashes are not proof of semantic equality, but they are useful to
  group likely-equivalent candidates before exact checking or human review.

Where to use it:

- group promoted local process fragments;
- group fixture candidates with same structure but different concrete slots;
- group host/agent failure-recovery motifs.

Important caveat:

- Treat WL hash as a candidate key, not as proof. Keep source refs and run exact
  label-aware comparison before emitting "same pattern" claims.

Relevant reference:

- NetworkX's Weisfeiler-Lehman graph hash docs describe iterative neighborhood
  aggregation, directed graph behavior, and the warning that hash similarity
  does not imply graph similarity:
  <https://networkx.org/documentation/stable/reference/algorithms/generated/networkx.algorithms.graph_hashing.weisfeiler_lehman_graph_hash.html>

### 6. Exact Graph / Subgraph Isomorphism For Promoted Candidates

Use for "does this candidate structure occur here?" after deterministic miners
have narrowed the search space.

Good fits:

- VF2-style directed graph matching with node and edge attribute predicates.
- Monomorphism when the candidate omits unimportant edges.
- Bounded candidate size, e.g. 3-12 nodes.

Why not use it as the first pass:

- Subgraph matching is expensive in the general case.
- Full traces contain too many incidental events unless first normalized and
  windowed.
- Exact matching needs a declared abstraction level; otherwise small label
  differences hide useful analogies.

Where to use it:

- verify candidate local process pattern occurrences;
- match a known route-feedback anti-pattern across traces;
- find examples for a story authoring recipe;
- prove that a fixture candidate is structurally covered or missing.

Relevant reference:

- NetworkX's VF2 docs expose directed graph matchers, subgraph isomorphism,
  semantic feasibility predicates, and categorical/numerical attribute matchers:
  <https://networkx.org/documentation/stable/reference/algorithms/isomorphism.vf2.html>

### 7. Frequent Subgraph Mining

Use carefully, offline, and only on small derived candidate graphs.

Good fits:

- gSpan/cgSpan-style discovery over a database of small labeled graphs.
- Closed frequent subgraphs to reduce redundancy.
- Top-k frequent subgraphs when support thresholds are hard to set.

Why it is second-phase, not hot-path:

- Full trace-derived graphs will be noisy and high-cardinality.
- The most useful Kitsoki patterns usually have temporal semantics; flattening
  them into arbitrary graph motifs can lose ordering unless encoded carefully.
- Frequent subgraph mining can produce many structurally true but operationally
  useless motifs.

Where to use it:

- discover repeated `route -> guard -> host -> transition` structures after
  event normalization;
- discover common story motifs across many stories, not just many runs;
- group analogous patterns that differ in state names but share typed roles.

Relevant references:

- SPMF lists graph pattern mining algorithms including gSpan and cgSpan:
  <https://www.philippe-fournier-viger.com/spmf/index.php?link=algorithms.php>
- cgSpan describes closed graph-based substructure mining as a gSpan extension:
  <https://arxiv.org/abs/2112.09573>

### 8. Bounded Simple Cycle Enumeration

Use for story and observed-path diagnostics, not for all pattern mining.

Why it fits kitsoki:

- Story graphs are cyclic; enumerating short simple cycles identifies the loops
  that traces can exercise.
- Comparing declared cycles to observed cycles tells us which loops are real,
  noisy, untested, or candidates for fixture generation.

Where to use it:

- list observed loops by story and support;
- detect high-friction loops with many route corrections;
- find untested declared loops;
- cap cycle length to keep the search practical.

Relevant reference:

- Gupta and Suzumura give an algorithm for bounded-length simple cycles in
  directed graphs and emphasize sparse directed graphs and length constraints:
  <https://arxiv.org/abs/2105.10094>

### 9. Automata / Aho-Corasick / Rule Network Matching

Use for matching known pattern libraries against normalized event-token streams.

Good fits:

- Aho-Corasick over token IDs for many exact sequence patterns at once.
- Small deterministic finite automata for patterns with wildcards/gaps.
- Rete-like partial-match indexes if we eventually have many stateful rules over
  facts such as route decision + later correction + final accepted route.

Why it fits kitsoki:

- Once pattern candidates are promoted into a library, repeated mining should be
  incremental and cheap.
- Known anti-pattern detection can run during trace ingestion.

Where to use it:

- route-feedback anti-patterns;
- known host retry loops;
- known progressive-determinism candidate shapes;
- "this exact event path exists" example search.

Relevant references:

- Aho-Corasick matches many string patterns simultaneously with linear scan
  behavior plus output matches:
  <https://en.wikipedia.org/wiki/Aho%E2%80%93Corasick_algorithm>
- Rete is designed for many-rule/many-fact matching with shared partial matches:
  <https://en.wikipedia.org/wiki/Rete_algorithm>

## Recommended Kitsoki Mining Pipeline

### Phase 0: Normalization

Implement a `kitsoki.TracePatternCorpus` builder:

- read JSONL trace;
- preserve raw `source_ref` for every event;
- normalize labels through versioned lenses;
- group events by `case_id`, `story_hash`, `app_id`, `state_path`, and turn;
- derive route feedback records from explicit `turn.route_feedback` when
  present, and from contextual route override/rewind/rephrase/checkpoint
  patterns while that event is being added.

Artifact:

```
.artifacts/session-mining/<job>/kitsoki/events.jsonl
.artifacts/session-mining/<job>/kitsoki/event-index.json
```

### Phase 1: Cheap Deterministic Indexes

Build:

- per-lens token dictionaries;
- directly-follows counts;
- transition matrix by story/state/intent;
- route decision table;
- route correction table;
- host/agent outcome table;
- bounded windows with source refs;
- cycle-collapsed path signatures.

This phase should answer many questions without a mining algorithm:

- "What are the top corrected routes?"
- "Which guards are most often followed by refinement?"
- "Which loops occur often but have no fixture?"
- "Which route tier creates the most operator rewinds?"

### Phase 2: Pattern Discovery

Run deterministic miners in order:

1. top-k frequent contiguous windows;
2. top-k gap-bounded subsequences;
3. frequent episodes for long traces;
4. local process model discovery for high-support windows;
5. route-feedback aggregation;
6. conformance/fixture-gap checks against declared flows.

Only promote candidates that have:

- minimum support or high surprise;
- at least two source refs unless explicitly looking for one-off failures;
- stable label abstraction;
- a proposed utility class: fixture seed, synonym/default-intent candidate,
  story pattern, guard/gate promotion, host reliability fix, or docs/example.

### Phase 3: Structural Verification

For promoted candidates:

- build small candidate pattern graphs;
- bucket by WL hash;
- run exact directed graph matching with label predicates;
- emit source refs for every occurrence;
- calculate pattern confidence from deterministic evidence, not prose.

### Phase 4: Artifact Emission

Emit reviewable artifacts:

```jsonc
{
  "schema_version": "kitsoki-patterns.v1",
  "job_id": "2026-06-22T...",
  "pattern": {
    "id": "route-feedback.accept-to-refine",
    "kind": "route-feedback",
    "lens": "route",
    "support": 9,
    "score": 0.86,
    "signature": "route(accept) -> feedback(bad_route) -> final(refine)",
    "utility": ["synonym_candidate", "turncache_negative_label"],
    "evidence": [
      {"trace": "run-a.jsonl", "events": [12, 13, 18], "turns": [4, 5]},
      {"trace": "run-b.jsonl", "events": [40, 41, 45], "turns": [12, 13]}
    ],
    "suggestion": {
      "type": "routing_training_case",
      "negative": {"intent": "accept"},
      "positive": {"intent": "refine"}
    }
  }
}
```

Also emit:

- `patterns.md`: human-readable ranked report;
- `examples.md`: cited examples by pattern;
- `fixtures.todo.yaml`: flow/test candidates, not auto-applied;
- `route-feedback.json`: route-specific deterministic report;
- `coverage-gaps.json`: story/flow conformance deltas.

## Scoring

A useful score should be deterministic and decomposed:

```text
score =
  support_weight
+ recurrence_weight
+ correction_weight
+ fixture_gap_weight
+ determinism_promotion_weight
+ cost_or_latency_weight
- entropy_penalty
- label_instability_penalty
- one_trace_penalty
```

Where:

- `support_weight`: number of distinct traces/runs/cases;
- `recurrence_weight`: pattern recurs across days or story hashes;
- `correction_weight`: user explicitly corrected the route/gate/outcome;
- `fixture_gap_weight`: path has no matching flow fixture;
- `determinism_promotion_weight`: same LLM route/gate repeatedly resolves to
  same intent/verdict;
- `cost_or_latency_weight`: repeated paid tier or slow host path;
- `entropy_penalty`: many different finals after same prefix;
- `label_instability_penalty`: pattern disappears under small label changes;
- `one_trace_penalty`: one long trace generated all support.

The score should always expose its terms. No opaque "interestingness" score.

## Why LLMs Are The Wrong Primary Matcher

LLMs are weak as the first-pass matcher here because:

- the trace structure is already machine-readable;
- source refs and support counts must be exact;
- cycles and repeated loops are easy for prose summarizers to over- or
  under-count;
- similar text can hide different state/guard semantics;
- different text can represent the same typed route/transition pattern;
- repeatable mining needs stable output under CI and no-cost flow tests.

Good LLM roles:

- summarize already-mined deterministic patterns;
- draft a fixture from a cited pattern;
- propose a human label for a cluster;
- explain why a pattern may be useful.

Bad LLM roles:

- decide whether two traces match;
- count occurrences;
- infer support;
- silently collapse cycles;
- decide conformance without event-level evidence.

## Implementation Recommendation

Start with a pure-Go deterministic core:

1. `internal/mining/kitsokipattern`: canonical event-token builder and indexes.
2. Bounded window miner and top-k support counter.
3. Cycle-aware path-signature builder over observed room paths.
4. Route-feedback aggregator.
5. Pattern artifact schema and validator.
6. Fixture-candidate emitter.

Then add optional adapters:

- SPMF-compatible export for sequence/episode mining experiments;
- graph export for VF2/WL/gSpan experiments;
- local process model export if we want to compare against process-mining
  tooling without pulling a Python runtime into kitsoki.

Keep the runtime dependency surface small. The production path can be custom
Go because the first two phases are straightforward and evidence-critical.
Use external tools behind explicit offline commands only.

## Tasks

- [x] Define the in-memory `kitsoki-patterns.v1` report shape: event tokens,
   route feedback, bounded-window patterns, cycle paths, scores, and evidence
   refs.
- [x] Implement the kitsoki trace event normalizer with versioned lenses for
   `route`, `control`, `guard`, `host`, `world`, and `fixture`.
- [x] Build deterministic initial indexes: token dictionaries via emitted
   tokens, directly-follows counts, bounded windows, route decision/correction
   records, and cycle-collapsed path signatures.
- [x] Add route-feedback aggregation from explicit `turn.route_feedback` events
   and lower-confidence derived feedback from contextual route overrides.
- [x] Add deterministic fixtures over synthetic trace snippets; no real LLM
   calls.
- [ ] Persist `kitsoki-patterns.v1` artifacts: event-token JSONL, event index,
   `patterns.json`, `route-feedback.json`, `coverage-gaps.json`, and cited
   examples.
- [ ] Add top-k gap-bounded subsequence miners with source refs.
- [ ] Add richer structural verification for promoted candidates using small typed pattern
   graphs, WL-hash bucketing, and exact directed matching.
- [ ] Emit fixture-candidate and example artifacts without auto-applying changes.
- [ ] Document the shipped schema and runtime behavior, then trim/delete this
   proposal.

## Non-Goals

- Whole-story arbitrary subgraph mining as the primary matcher.
- LLM-based occurrence counting, support calculation, or structural matching.
- Automatic application of mined fixtures, synonyms, routes, or gate defaults.
- Adding SPMF, PM4Py, NetworkX, or another external mining runtime to kitsoki's
  default execution path.
- Mining arbitrary world keys without an allow-list/redaction policy.

## Acceptance Criteria

- A fixture-backed job can read kitsoki JSONL trace snippets and emit
  `kitsoki-patterns.v1` artifacts with event-level source refs.
- Repeated cycles through the same story loop are grouped by a stable
  cycle-aware path signature with count buckets.
- Route corrections produce deterministic `route-feedback.json` records that
  identify the original route, the correction, and the accepted final route.
- Promoted pattern candidates can be structurally verified against source traces
  without invoking an LLM.
- Fixture candidates and example reports cite exact trace events and remain
  review-only.
- All automated tests use static trace fixtures/cassettes and spend no LLM.

## Open Design Questions

- What are the first three lenses to ship? I would choose `route`, `control`,
  and `fixture`.
- Should support be per trace, per turn, per work item, or configurable? It
  should be configurable but default to per trace plus a separate occurrence
  count.
- Which world keys are allowed in pattern labels? This needs an allow-list to
  avoid leaking arbitrary user data into artifacts.
- How should route-feedback derivation behave before `turn.route_feedback`
  exists? It should emit a lower-confidence derived label with the exact
  derivation rule.
- Do we want online mining during ingestion, or batch-only? Batch-only first;
  online matching of promoted patterns can come later with automata/rule indexes.

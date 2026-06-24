# Semantic routing

Kitsoki resolves every user turn through a four-tier routing stack
before the LLM gets to look at the input. This doc covers the tiers,
how to grow the synonym library, and how the turncache works.

This page is the user-facing reference for what shipped; the
implementation lives in [`internal/semroute/`](../../internal/semroute)
(matcher, template compiler, Aho-Corasick wiring) and
[`internal/slotparse/`](../../internal/slotparse) (typed slot parsers).

## 1. The four tiers

Every foreground turn runs the tiers in order and stops at the first
that resolves:

| Tier                 | Entry point                       | Confidence    | Trace event                       | Chip |
|----------------------|-----------------------------------|---------------|-----------------------------------|------|
| Deterministic        | `TryDeterministic`                | 1.00          | `turn.deterministic_hit`          | `▣`  |
| Synonym (bare)       | `TrySemantic`                     | 0.90          | `turn.semantic_hit`               | `⌁`  |
| Synonym template     | `TrySemantic`                     | 0.80 / 0.65   | `turn.semantic_hit`               | `◐`  |
| Turn cache           | `tryTurnCache`                    | varies        | `turn.turncache_hit`              | `⟲`  |
| Default intent       | `routeViaDefaultIntent`           | 1.00          | `turn.default_routed`             | `⤳`  |
| Embedding (opt-in)   | `routeViaEmbeddingTier`           | varies        | `turn.embedding_hit`              | `◉`  |
| Contextual (opt-in)  | `routeViaContextualRouter`        | varies        | `turn.context_route_decided`      | `⊕`  |
| LLM                  | `harness.RunTurn`                 | varies        | `turn.llm_routed`                 | `✦`  |

The default-intent tier only runs when the current state declares one;
otherwise the stack falls straight through to the LLM.

The TUI route badge next to the user's input names the tier that
resolved the turn. `ctrl+r` toggles the full route trace overlay
which shows every tier that ran and what each said.

> Note: the deterministic tier (exact menu-display or example-string
> match) is currently only applied by the TUI before submission. CLI
> invocations (`kitsoki run --raw …`, programmatic callers) enter
> `Orchestrator.Turn` directly and start from the semantic tier. The
> match outcome is identical for inputs that any tier resolves — but
> a CLI invocation with an exact menu string will pay the lex+matcher
> cost the TUI avoids. Future versions may move the deterministic
> prepass into `Turn` itself.

### 1.1 Deterministic

Exact match against:

- A menu entry's display string (case-folded, trimmed); or
- A unique intent **example** for one of the allowed intents.

Cost: a map lookup. Confidence: always 1.00. This is the cheapest
tier and the only one with `Display` -wins semantics — input that
matches what's literally rendered to the user always routes to the
right place.

### 1.2 Synonym (bare)

A declared synonym is a plain phrase listed under `synonyms:` on an
intent. The matcher runs UAX#29 word segmentation + Porter2 stemming
+ stopword stripping over both the input and the declared synonyms.
A hit fires when the synonym's stem-bag is a subset of the input's
stem-bag.

```yaml
intents:
  ford:
    title: "Ford the river"
    examples: ["ford", "ford the river"]
    synonyms:
      - wade
      - "walk it"
      - "drive through the water"
```

"`let's wade across`" → input stems `{across, wade}` (the "let's" is
a stopword) → contains `wade` → routes to `ford` at confidence 0.90.

Multi-pattern matching uses Cloudflare's pure-Go Aho-Corasick
implementation; cost is ~3 µs per turn over a thousand-pattern app.

If two intents share a synonym, the matcher returns
`Confidence=0.50` with a `Candidates` list — the orchestrator
surfaces the existing `AMBIGUOUS_INTENT` disambiguation card.

**Leading-verb tie-break.** Subset matching is order-blind, so an
imperative whose *object* contains another intent's verb stem ties
spuriously: `"commit the staged fix"` matches both `commit` (via
"commit") and `stage` (via "staged"→stage), yet a human reads "commit"
as the command and "staged fix" as its object. The matcher recovers
that from the one cue a typed command reliably carries — the verb leads.
When the input's first content stem belongs to **exactly one** tied
candidate's matched entry, that candidate wins at 0.90 (`MatchReason:
leading-verb:<stem>`). When the leading stem is in zero or ≥2
candidates' entries the cue is absent or itself ambiguous, so the tie
stands and the disambiguation card fires — the "ties signal authored
ambiguity" contract is preserved for genuinely ambiguous input.

### 1.3 Synonym templates

A `{slot_name}` inside a synonym string captures a contiguous run of
tokens for the named slot. The captured run is fed to the typed
parser in [`internal/slotparse`](../../internal/slotparse) keyed off
`slot.type`.

```yaml
intents:
  propose_purchase:
    examples: ["buy 2 oxen and 200 lbs of food"]
    synonyms:
      - "buy {items} for {total_cost}"
      - "purchase {items}"
      - "spend {total_cost} on {items}"
    slots:
      total_cost: { type: int }
      items:      { type: string }
```

Template syntax is intentionally tiny: literal tokens (preserving
stopwords as positional anchors), `{slot_name}` captures, and
nothing else. No alternation, no regex, no optionals. Authors who
want choice write multiple templates.

#### Slot parser types

| `slot.type`         | Parser                                                       |
|---------------------|--------------------------------------------------------------|
| `int`               | digit form ("6") or spelled cardinal ("six")                 |
| `money` / `$int`    | `$120`, `120 dollars`, `120 bucks`                           |
| `enum`              | direct, synonym (word-bag), Damerau-Levenshtein-1 fuzzy      |
| `bool`              | yes/no/y/n/true/false/sure/nope                              |
| `list[T]`           | comma- or `and`-separated run of `T`s; prefix-wins on junk   |
| `date`              | "today", "tomorrow", "next monday", "march 3" (year-rollover), `2026-03-15`, `3/15/2026` |
| `string`            | catch-all — only fills via explicit template capture         |

The list parser dispatches on the `<inner>` suffix:
`type: "list[int]"`, `type: "list[enum]"`, etc. The inner slot
inherits Values + Synonyms so list-of-enum works without extra
config.

Confidence bands:

- **0.80** — all captured slots parsed; submit immediately.
- **0.65** — at least one slot named but unparseable; the
  orchestrator runs `ComputeClarification` so the user can fill the
  missing slot directly.

> Case folding caveat: captured `string` slot values are NFKC-
> normalised and lowercased by `lex.Tokenize` before the matcher
> sees them, so the value handed to downstream views is always
> lowercase (`"star wars"`, not `"Star Wars"`). Typed slots (`enum`,
> `int`, `date`, …) don't surface this — their parsers map back to
> the canonical value declared in YAML — but a free-form `string`
> slot rendered through a pongo2 template will show the lowercased
> form. If authors need original-case preservation, that's a future
> feature.

### 1.4 Turn cache

After all three semantic-routing tiers miss, the orchestrator
consults the SQLite-backed turncache. The cache key is
`(app_id, app_hash, state_path, lex.Signature(input))`:

- `app_id` namespaces rows so one cache file can serve many apps.
- `app_hash` is a SHA-1 over the intents + slots + synonyms surface;
  it invalidates the cache the moment the routing-relevant YAML
  changes.
- `state_path` keeps "let's hunt" at the trail from leaking into
  "let's hunt" at a fort, where the verb may mean something else.
- `lex.Signature(input)` is the sorted stem-bag with stopwords
  stripped — a 64-bit prefix is plenty.

On a hit the orchestrator re-runs `Machine.Validate` against the
live world before applying. A re-validate failure increments a
strike count; three strikes evicts the row.

#### When does the cache help?

The first time anyone types a free-form phrase, the LLM resolves it
and writes the result to the cache. Every subsequent identical
phrase (same lexical signature, same state) skips the LLM — typical
latency drops from 2-5s to ~80 µs.

#### When does the cache *not* help?

- First occurrence of a phrasing — by definition there's no row yet.
- Different state path — the same words can mean different things.
- After a synonym/intent edit — the app_hash changes and the sweep
  on next session start wipes the rows.

#### Flushing the cache

The cache lives in SQLite (by default at
`$XDG_DATA_HOME/kitsoki/turncache.sqlite`). Delete the file to
reset; the orchestrator will recreate it lazily.

A more surgical approach: bump the routing-relevant YAML and let the
§7.1 `app_hash` sweep handle the invalidation automatically.

### 1.5 Default intent (free-text sink)

A `mode: conversational` / discovery room is mostly free prose: the
operator types a description, a question, a half-formed idea. None of
that matches a command intent deterministically, and handing it to the
LLM router risks a near-miss — a real dogfood bug had "this doc" routed
to `look` instead of the room's `discuss` arc, so the message never
reached the conversation.

A state can name a **default intent** — the deterministic sink for
anything the match-based tiers don't claim:

```yaml
states:
  proposal:
    mode: conversational
    default_intent: discuss          # the free-text sink
    on:
      discuss:                       # one required string slot
        - target: .
          effects:
            - invoke: host.agent.converse
              with: { question: "{{ slots.message }}" }
      begin_proposal: [ ... ]        # named commands still win earlier
      quit: [ ... ]
```

When deterministic + synonym + turn-cache all miss, the orchestrator
routes the **whole utterance** to `default_intent`, filling its single
required string slot (`slots.message` here) — no LLM classification.
Commands the operator does name ("ready", "quit", "see docs/…") still
resolve in the earlier tiers, so only genuinely unmatched prose reaches
the sink.

Contract (enforced at load time): the named intent must have an `on:`
arc in the state and declare **exactly one required string slot**. The
tier is opt-in — a state without `default_intent` falls straight through
to the LLM as before. The intent name is import-rewritten like any other
arc, so a bare `discuss` keeps working when the room is folded under an
alias (`core__discuss`). Trace event: `turn.default_routed`
(`routed_by: "default"`).

## 2. Per-app routing config

```yaml
app:
  id: oregon-trail
  routing:
    enabled: true               # default true; set false to keep today's behaviour
    semantic_high_bar: 0.80     # submit at or above (default 0.80)
    semantic_mid_bar:  0.65     # clarify at or above (default 0.65)
    cache_enabled:    true
    cache_max_age:    "30d"     # rotate out very-old verdicts
    cache_cap:        10000     # rows per app before LRU trim
    cache_trim_fraction: 0.10   # drop the coldest 10% on overflow
    revalidate_strikes: 3       # cache-row eviction threshold
    confidence_decay: false     # halve max_age for low-confidence rows
    stopwords_extra:  ["yall", "y'all", "wagon"]
    extract_llm_on_no_match: false  # default false; see §2.1
```

A nil `routing:` block means "use defaults" (see
`app.DefaultRoutingConfig`). Set `enabled: false` to disable the
routing tiers entirely and fall straight back to the
deterministic-or-LLM behaviour kitsoki shipped before §10 Phase 7.

### 2.1 LLM-tier backend and `extract_llm_on_no_match`

The LLM tier (the last tier in §1) resolves through the agent
dispatch path, so it can be backed by **any** declared `agent_plugins:`
entry — not only the default `agent.claude`. The natural choice for
routing is the cheap, offline, schema-bounded `builtin.local_llm`
backend (`agent: agent.local`): see
[agent-plugin.md §9 "Local model backend"](agent-plugin.md#9-local-model-backend).
Backing the routing LLM tier with a local model keeps routing working
on a plane / in an air-gapped CI box and avoids consuming a Claude
session for a decision that fires on nearly every turn.

`extract_llm_on_no_match` (default **false**) is the opt-in that lets
the *deterministic* router (`TrySemantic`) reach for that LLM tier on a
**no_match** before falling through to the main-turn LLM. The
deterministic tiers (synonyms, slot_template) always run first; this
only changes what happens *after* a deterministic miss, and it is
strictly opt-in because the extra extract-LLM call adds a model
round-trip on every otherwise-unrouted turn.

> **Status.** The config field is honoured and recorded today (an
> opted-in no_match is traced distinctly), but the free-form-verdict →
> `semroute.Verdict` confidence-band mapping is uncalibrated, so an
> opted-in app still falls through to the main-turn LLM for now. Wiring
> the actual local-model routing call lands with that calibration
> (proposal Open Question 4). Backing the LLM tier of the *extract verb*
> with `agent.local` (via the effect's `agent:` alias) needs no flag
> and works today.

## 3. Growing the synonym library

The cache makes a wrong routing decision **cheap**: the second time
someone types the same phrase, the cache short-circuits the LLM.
But the *right* fix is usually a new author-declared synonym so
the resolution is explicit, auditable, and works on the *first*
occurrence.

Two CLI surfaces drive this workflow:

### 3.1 `kitsoki replay-routing`

Re-routes a recorded session through the full deterministic →
semroute → turncache stack with the LLM stubbed to return what the
recording captured. Reports a per-tier breakdown and a per-turn
audit trail.

```
$ kitsoki replay-routing stories/oregon-trail/app.yaml --target 0.40
Routing summary for oregon-trail (64 turns):
  deterministic:    23 ( 35.9%)
  synonym (bare):    8 ( 12.5%)
  synonym tmpl:      8 ( 12.5%)
  cache:             1 (  1.6%)
  LLM:              24 ( 37.5%)   <-- target ≤40%
  mismatched:       12 ( 18.8%)
```

**Honesty note on the published number.** An earlier round (proposal
§10 Phase 7) reported 21.9% on this same recording. That measurement
was taken before the replay-routing pass applied the production
`RequiresUnfilledSlot` guard — a verdict that named the verb but
couldn't fill a required slot was counted as a semroute hit even
though the live orchestrator drops those turns to the LLM. With the
guard in place (issue H2), the honest measured rate is 37.5%; the
prior number was a calibration artefact, not a real production cost.
The calibration test (`internal/semroute/calibration_test.go`) now
enforces ≤40% so future work has headroom to push it back below 30%
without the test constantly red.

`--csv <file>` writes a per-turn audit trail (turn id, state, input,
expected vs actual intent, tier that resolved). Grep the rows where
`actual_tier=llm` to find phrasings worth adding as synonyms.

`--target 0.40` makes the CLI exit non-zero when the LLM fallthrough
rate exceeds 40%. The Oregon Trail end-to-end test
(`internal/semroute/calibration_test.go`) enforces the same gate so
future commits can't regress the matcher silently. The target is
deliberately above the proposal's 30% aspiration to leave room for
the next round of synonym authoring without keeping the test red.

### 3.2 `kitsoki inspect --synonym-suggestions`

Reads the turncache, groups rows by `(state, intent)`, and surfaces
the LLM-resolved phrasings that the synonym layer didn't catch.
Output is a copy-pasteable YAML block with hit-count comments so
the author can judge each candidate:

```yaml
# 14 LLM-resolved hits, 8 sessions; first seen 2026-04-22
intents:
  ford:
    synonyms:
      - "wade through"
      - "drive the wagon across"
      - "let's just ford it"
```

**Auto-promotion is deliberately not implemented**:
every declared synonym should be one a human has eyeballed. The
synonym table is part of the app's contract with its users —
auto-growing it would erode that contract. The inspect surface is
read-only.

### 3.3 `kitsoki inspect --unused-synonyms`

The other half of the loop: list every declared synonym whose
hit-count is zero. Candidates for pruning, since they bloat the
prompt and the index without paying any routing dividend.

### 3.4 `kitsoki inspect --routing-stats`

Per-intent hit rates across all tiers, plus the top-N hottest
cached signatures. Use during phase-7-style calibration to see
which intents are getting the most traffic and where the cache is
earning its keep.

## 4. The lexical signature

Two phrasings collide in the cache when their `lex.Signature` is the
same. The signature is:

```
content  = [t.Norm for t in lex.Tokenize(input) if not t.IsStop]
content  = sorted(unique(content))
return sha1(strings.Join(content, " "))[:16]
```

So:

| Input                              | Signature notes              |
|------------------------------------|------------------------------|
| `buy 6 oxen and 200 lbs of food`   | `{200, 6, buy, food, lb, oxen}` → A |
| `let's buy six oxen, 200 lbs food` | `{200, 6, buy, food, lb, oxen}` → A ✓ |
| `purchase 6 oxen + 200lbs food`    | `{200, 6, food, lb, oxen, purchas}` → B ✗ |
| `buy 6 oxen`                       | `{6, buy, oxen}` → C            |

`purchase` doesn't normalise to `buy` under pure stemming — the
synonym layer maps them, the cache only sees exact lexical match.
This is intentional: signature collision rates stay low, and the
synonym → cache flow is the documented promotion path.

## 5. Transport routing seam (agent-split D13)

The semantic routing tiers (§1.2 and §1.3) run through the same
`host.agent.extract` handler that an app author invokes explicitly via
effects. `TrySemantic` calls `host.RunExtractForRouting`, which injects
the already-compiled `semroute.Matcher` into the context rather than
re-loading YAML from disk.

This makes transport-level routing one consumer of the extract handler,
not a parallel code path. Concretely:

- All `extract.resolver.matched` trace events fire regardless of whether
  the resolution came from a live session turn or a programmatic call.
- The `resolved_by` field on journal entries (`synonyms` / `slot_template`
  / `no_match`) is available for replay tools and dashboards.
- A future `host.agent.extract` invocation in an effect can reuse the
  same in-process matcher the router already built — no double compile.

From the app author's perspective there is no visible difference.
Transport tests continue to pass unchanged; the seam is below the
`Turn()` surface.

A fourth consumer of these same no-LLM tiers is the **pre-LLM intercept
gate** ([`prompt-intercept.md`](prompt-intercept.md)):
`Orchestrator.Classify` runs the deterministic / synonym / embedding
tiers with **zero effects** so an external caller (`kitsoki intercept`,
the Claude Code `UserPromptSubmit` hook) can cheaply ask "is this a known
command?" before the model runs, then execute only when a conservative
gate reads the verdict's confidence band (§1) as a confident, fully-slotted
match — everything else passes straight through to the LLM.

## 6. Embedding routing tier

An optional embedding tier sits between the lexical template tier (§1.3) and
the LLM, catching paraphrase the lexical layer misses. It is off by default
and enabled per app:

```yaml
app:
  routing:
    embedding:
      enabled: true
```

When enabled, the tier embeds allowed intent names as `Document` vectors once
at startup (lazy, cached by intent name), embeds the utterance as `Query` at
turn time, and applies the `ConfidentBar`/`Margin` gate:

- top-1 cosine ≥ `confident_bar` **and** (top1 − top2) ≥ `margin` → confident
  hit at `semroute.ConfidenceEmbedding`.
- top-1 ≥ bar but margin too narrow → tie → `AMBIGUOUS_INTENT` disambiguation.
- otherwise → zero verdict → falls through to the LLM as before.

`semroute.ConfidenceEmbedding` is a new band on the existing five-band scale
that fits between the lexical template tier (0.65–0.80) and the LLM.

**`routing.embedding` config fields** (see `EmbedTierConfig` in
`internal/orchestrator/embed_tier.go`):

| Field           | Default                  | Meaning |
|-----------------|--------------------------|---------|
| `enabled`       | `false`                  | Opt-in gate |
| `model`         | `nomic-embed-text-v1.5`  | Embedding model |
| `dim`           | `256`                    | Matryoshka truncation for nomic |
| `endpoint`      | `""`                     | Attach to already-running embedding server |
| `confident_bar` | `0.82`                   | top-1 cosine ≥ this → confident match |
| `margin`        | `0.08`                   | top1 − top2 must exceed this; else tie |

**Calibration note.** `confident_bar: 0.82` and `margin: 0.08` are
placeholder values. The Oregon Trail bake-off (`stories/oregon-trail/`) with
`KITSOKI_EMBED_E2E=1` will tune them and record the LLM-fallthrough delta
relative to the current 37.5% baseline.

The tier is **additive and opt-in**: stories that do not set
`routing.embedding.enabled: true` are byte-identical to today; no cassettes
change. The embedding sidecar lifecycle (fetch, spawn, teardown) follows the
same pattern as `agent.local`; see
[`agent-plugin.md §9`](agent-plugin.md#9-local-model-backend) and
[`docs/architecture/embeddings.md`](embeddings.md) for the full substrate
reference.

## 7. Contextual routing tier

The **contextual routing tier** fires after all deterministic, synonym, turn-cache, and embedding tiers miss, on any state that has opted in with `contextual_routing: {enabled: true}`. Where earlier tiers resolve *which declared intent* an utterance matches, the contextual router distinguishes four richer outcome classes before falling back to the raw LLM:

| Class | Meaning | Outcome |
|---|---|---|
| `intent` | The utterance maps to a declared on-path intent (with optional slots). | State machine advances identically to a synonym hit. |
| `help` | A question about how to use the story, current room, or kitsoki. | Appended to the room's help lane. State/world unchanged. |
| `room_request` | Free-form work belonging in the room's operating context. | Appended to the room's work lane. State/world unchanged. |
| `meta_edit` | A request to edit story, room, prompts, routing, or kitsoki behaviour. | Appended to the room's meta lane. State/world unchanged. |

Only `class=intent` advances the FSM. Lane classes (`help`, `room_request`, `meta_edit`) route the utterance into a persistent room chat lane without changing state or world. The tier is strictly opt-in — states without `contextual_routing: {enabled: true}` are unaffected.

### 7.1 Enabling the tier

```yaml
states:
  landing:
    contextual_routing:
      enabled: true
      help_chat:  story.ask           # class=help → story.ask meta mode
      room_chat:  work                # class=room_request → work lane
      meta_chat:  story.edit          # class=meta_edit → story.edit meta mode
      pending_plan_path: landing_note.plan  # enables plan-continuation guard (§7.3)
      plan_accept_intent: accept_plan # default "accept_plan"
      plan_refine_intent: work        # default "work"
```

All three lane fields are optional. The contextual router only offers classes for declared lanes; load-time validation rejects a class whose backing surface is not declared.

### 7.2 Room chat lanes

The three non-intent classes dispatch to **room chat lanes** backed by the same chat store that persists meta-mode conversations (`internal/chats/`). The key tuple is:

```
(app_id, "room:<kind>", state_path)
```

mirroring meta-mode's `(app_id, "meta:<modeName>", state_path)`. See [`internal/roomchat/lane.go`](../../internal/roomchat/lane.go). The three lane kinds are `LaneHelp`, `LaneWork`, and `LaneMeta`. `VerbForLane` selects the agent verb:

- `LaneHelp` / `LaneMeta` → always `host.agent.ask` (read-only).
- `LaneWork` with `write_mode: readonly` → `host.agent.ask`.
- `LaneWork` otherwise → `host.agent.task`.

The active lane is the most-recent non-archived row for `(app_id, room, state_path)`, created lazily on first use. A `/meta new`-style reset is available via `roomchat.Resolver.StartNew`.

### 7.3 Plan-continuation guard

When `pending_plan_path` is set and the world contains a non-empty map at that path, the contextual router **short-circuits the LLM** with a deterministic classification:

- **Affirmation** (`ok`, `yes`, `go`, `apply it`, …) → routes to `plan_accept_intent` at confidence 1.0. Trace: `turn.context_route_decided` with `reason: plan_affirmation`.
- **Any other content** → routes to `plan_refine_intent`, filling its single required string slot at confidence 1.0. Trace: `turn.context_route_decided` with `reason: plan_refine`.

This keeps plan acceptance and refinement no-LLM and replayable, and prevents a `default_intent` from consuming an affirmation intended for the pending plan.

### 7.4 Route receipt

Every contextual routing decision emits `turn.context_route_decided` and — for lane dispatches — `turn.context_route_applied`, and produces a `ContextRouteReceipt` on `TurnOutcome.ContextRoute`:

```go
// internal/orchestrator/context_route.go
type ContextRouteReceipt struct {
    Class        string            // intent | help | room_request | meta_edit
    Intent       string            // populated for class=intent
    Reason       string            // model's stated rationale
    Confidence   float64           // [0, 1]
    TargetChatID string            // populated for lane classes
    TargetLane   string            // "help" | "work" | "meta"
    Alternatives []ContextRouteAlt // next-best classes considered
    DecisionID   string            // "<session_id>:<turn_number>" — rewind target
}
```

The `DecisionID` is the stable handle for rewind; TUI/web reads the receipt to render the route badge and alternative actions.

### 7.5 Rewind and override

`Orchestrator.RewindRoute(sid, decisionID, newClass, reason)` reverses one routing decision ([`internal/orchestrator/rewind.go`](../../internal/orchestrator/rewind.go)):

1. Parse `decisionID` → `turnN`.
2. Recover the original utterance and old class from the `TurnStarted` event at `turnN`.
3. Replay event history up to (but not including) `turnN` via `store.BuildJourneyUntil` to get the pre-turn state/world.
4. Re-snapshot at `turnN` with the pre-turn world so `LoadHistory` skips the overridden events without deleting them.
5. Re-dispatch the utterance under `newClass`: lane classes append to the chosen lane; `class=intent` rewind requires `IntentAccepted` event recovery and is not yet implemented.
6. Append a `turn.context_route_overridden` event at `turnN+1`.

Rewind is **one-decision deep and foreground-only** in v1 — it does not attempt to rewrite concurrent background lanes or chain multiple decisions.

### 7.6 No-LLM replay

When a trace contains a `turn.context_route_decided` event, replay re-uses the recorded verdict instead of invoking the contextual router LLM — the same approach the turn cache uses for ordinary LLM routing. The flow tests in `internal/orchestrator/context_route_*_test.go` cover all four verdict classes plus rewind deterministically via cassettes.

**Code:** [`internal/orchestrator/context_route.go`](../../internal/orchestrator/context_route.go) (types + parser), [`internal/orchestrator/semantic.go`](../../internal/orchestrator/semantic.go) `routeViaContextualRouter`, [`internal/roomchat/`](../../internal/roomchat/) (lane substrate), [`internal/orchestrator/rewind.go`](../../internal/orchestrator/rewind.go) (one-decision rewind).

## 8. See also

- [`architecture.md`](overview.md) §3 — where the routing tiers
  sit in the broader turn pipeline.
- [`authoring.md`](../stories/authoring.md) §6.1 — the YAML reference for
  `synonyms:`.
- [`hosts.md`](hosts.md#hostagentextract) — `host.agent.extract`
  reference (agent-split Phase 5 handler).
- [`prompt-intercept.md`](prompt-intercept.md) — the pre-LLM intercept
  gate, another consumer of these no-LLM tiers via `Orchestrator.Classify`.
- [`meta-mode.md`](../stories/meta-mode.md) §8 — meta-mode chat
  persistence; room chat lanes (§7.2) use the same store with `room:<kind>`
  keys.
- `internal/slotparse/` godoc — every typed parser's exact contract.
- `internal/semroute/` godoc — the matcher, template compiler, and
  Aho-Corasick wiring.
- `internal/roomchat/` — room chat lane substrate (§7.2).

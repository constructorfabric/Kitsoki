# Semantic routing

Kitsoki resolves every user turn through a four-tier routing stack
before the LLM gets to look at the input. This doc covers the tiers,
how to grow the synonym library, and how the turncache works.

This page is the user-facing reference for what shipped.

## 1. The four tiers

Every foreground turn runs the tiers in order and stops at the first
that resolves:

| Tier            | Entry point          | Confidence | Trace event              | Chip |
|-----------------|----------------------|------------|--------------------------|------|
| Deterministic   | `TryDeterministic`   | 1.00       | `turn.deterministic_hit` | `▣`  |
| Synonym (bare)  | `TrySemantic`        | 0.90       | `turn.semantic_hit`      | `⌁`  |
| Synonym template| `TrySemantic`        | 0.80 / 0.65| `turn.semantic_hit`      | `◐`  |
| Turn cache      | `tryTurnCache`       | varies     | `turn.turncache_hit`     | `⟲`  |
| LLM             | `harness.RunTurn`    | varies     | `turn.llm_routed`        | `✦`  |

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
stem-bag (semantic-routing proposal §4.1).

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

### 1.3 Synonym templates

A `{slot_name}` inside a synonym string captures a contiguous run of
tokens for the named slot. The captured run is fed to the typed
parser in [`internal/slotparse`](../internal/slotparse) keyed off
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
  changes (semantic-routing proposal §7.1).
- `state_path` keeps "let's hunt" at the trail from leaking into
  "let's hunt" at a fort, where the verb may mean something else.
- `lex.Signature(input)` is the sorted stem-bag with stopwords
  stripped — a 64-bit prefix is plenty.

On a hit the orchestrator re-runs `Machine.Validate` against the
live world before applying. A re-validate failure increments a
strike count; three strikes evicts the row (proposal §7.2).

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
```

A nil `routing:` block means "use defaults" (see
`app.DefaultRoutingConfig`). Set `enabled: false` to disable the
routing tiers entirely and fall straight back to the
deterministic-or-LLM behaviour kitsoki shipped before §10 Phase 7.

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

**Auto-promotion is deliberately not implemented** (proposal §7.7):
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

## 5. Transport routing seam (oracle-split D13)

The semantic routing tiers (§1.2 and §1.3) run through the same
`host.oracle.extract` handler that an app author invokes explicitly via
effects. `TrySemantic` calls `host.RunExtractForRouting`, which injects
the already-compiled `semroute.Matcher` into the context rather than
re-loading YAML from disk.

This makes transport-level routing one consumer of the extract handler,
not a parallel code path. Concretely:

- All `extract.resolver.matched` trace events fire regardless of whether
  the resolution came from a live session turn or a programmatic call.
- The `resolved_by` field on journal entries (`synonyms` / `slot_template`
  / `no_match`) is available for replay tools and dashboards.
- A future `host.oracle.extract` invocation in an effect can reuse the
  same in-process matcher the router already built — no double compile.

From the app author's perspective there is no visible difference.
Transport tests continue to pass unchanged; the seam is below the
`Turn()` surface.

## 6. See also

- [`architecture.md`](architecture.md) §3 — where the routing tiers
  sit in the broader turn pipeline.
- [`authoring.md`](authoring.md) §6.1 — the YAML reference for
  `synonyms:`.
- [`hosts.md`](hosts.md#hostoracleextract) — `host.oracle.extract`
  reference (oracle-split Phase 5 handler).
- `internal/slotparse/` godoc — every typed parser's exact contract.
- `internal/semroute/` godoc — the matcher, template compiler, and
  Aho-Corasick wiring.

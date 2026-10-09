# Proposal — Semantic routing: synonyms, slot parsers, and a turn-result cache

**Status:** v1 shipped. All seven phases plus the TUI route badges
landed on branch `semantic-routing-proposal`. The shipped reference
moved to **[`../semantic-routing.md`](../architecture/semantic-routing.md)**;
[`../architecture.md`](../architecture/overview.md) §3 covers where the
tiers sit in the broader turn pipeline; [`../authoring.md`](../stories/authoring.md)
§6.1 is the authoring cheat-sheet for `synonyms:`. This document
keeps only the **open questions** the design raised and the
**calibration history** for Oregon Trail. The single-decision-point
direction (collapsing the TUI's routing pre-pass and `Turn`/`OneShot`
into one shared stack) shipped as the never-silent runtime — see
[`semantic-routing.md`](../architecture/semantic-routing.md#13-synonym-templates)
and [`overview.md`](../architecture/overview.md#3-the-journey-of-one-turn)
(the proposal, `never-silent-runtime.md`, was deleted per lifecycle
guidance once its only open item — near-miss/workbench wiring — shipped).

The original design (rationale, alternatives, library survey) lives
in git history at commit
[`7331630`](../../) (`docs/proposals/semantic-routing-proposal.md`
before the v1-shipped trim). The library-evaluation appendix is
preserved at the bottom of this file because future authors
considering an embedding tier (§A) will want it.

---

## 1. Open questions

These came up during design + implementation and remain genuinely
open — none block shipping.

### 1.1 Per-state synonyms

The matcher today scopes by allowed-intent: a synonym only fires
when its intent is allowed at the current state. Per-state synonyms
would let "more shopping" mean `repeat_purchase` only on the
post-checkout substate. v1 says no — state-scoping comes from
`allowed_intents` already, and adding per-state synonyms duplicates
the menu surface. **Revisit if real apps hit collisions** (none
seen on Oregon Trail / cloak / dev-story / robbery).

### 1.2 Should the deterministic and semantic tiers be unified?

They share enough that a single `Matcher` interface with a
`Confidence float64` is tempting. But the deterministic tier has
a strong invariant — "a hit must match a *menu entry that was
rendered to the user*" — that the semantic tier deliberately
relaxes. Merging the codepaths loses an audit property. **Keep
them separate.**

### 1.3 Meta-mode interaction

Meta-mode runs its own harness (chat conversation). It should be
unaffected — semantic routing only fires when the orchestrator is
about to call `harness.RunTurn`, which meta-mode bypasses. **Verified
in `internal/metamode/controller_test.go`; no further work needed.**

### 1.4 Drive-mode reproducibility

`kitsoki drive --recording …` should pin a cache snapshot,
otherwise driving the same script twice can be deterministic-or-not
depending on cache state. The `kitsoki replay-routing` CLI uses
`--no-cache` to bypass the cache during calibration; drive-mode
should grow the same flag. **Open: not yet wired into `kitsoki
drive`.**

### 1.5 Auto-promotion of cache → synonyms (deliberately not built)

`kitsoki inspect --synonym-suggestions` reads cache rows and emits
copy-pasteable YAML. **It is strictly read-only.** Every declared
synonym should be one the author has eyeballed; auto-promotion
would erode the contract between the app and its users. Meta-mode's
`propose synonyms` action sits on top of this primitive: it stages
an edit for review, never lands it without confirmation. The CLI
surface is intentionally narrow.

### 1.6 The slot-aware list parser's "prefix-wins" choice

`internal/slotparse/ParseList` returns the prefix that parsed when
a mid-stream item fails (e.g. `"6, 12, blue, 3"` → `[6, 12]`). The
alternative — "all-or-nothing" — felt user-hostile, but neither is
obviously right for every domain. **Revisit if an app needs the
all-or-nothing variant.** A future entry point would be
`ParseListStrict(tokens, inner)`.

### 1.7 The lexical signature collision rate

The signature is a 64-bit SHA-1 prefix. Collisions are theoretically
possible but verified by `Machine.Validate` before applying. **No
real-world collision observed** across the Oregon Trail recording's
64 entries; benchmark this on larger corpora as they appear.

---

## 2. Calibration history

The §10 Phase 7 aspiration is **30% LLM fallthrough rate or lower**
on the Oregon Trail recording (64 turns). The current enforced
ceiling — see [calibration_test.go](../../internal/semroute/calibration_test.go) —
is 40%, set so the test isn't constantly red while the synonym
library is grown back under 30%.

| Stage                              | LLM-fallthrough rate | What changed                                                              |
|------------------------------------|----------------------|---------------------------------------------------------------------------|
| Phase 7 entry                      | **35.9%**            | All §10 work landed except calibration — measured starting point.         |
| + `propose_crossing` synonyms      | **28.1%**            | Added bare synonyms for the four method values plus "caulk and float", "hire a ferryman", "wait for the water to drop". |
| + `rest` "hold up" synonyms        | **25.0%**            | Added "hold up", "hold up here", "let's hold up", "stop here".            |
| + `refine_purchase` short forms    | **23.4%**            | Added "less food" / "more food" / "less bullets" / "more bullets" / "fewer oxen" / "more oxen". |
| + `restart_from {stage}` templates | **23.4%**            | Three templates capturing the landmark via the enum parser.                |
| + `generate_names` templates       | **21.9%**            | "use {theme} names", "generate {theme} names", "generate names from a {theme} theme", "{theme} themed names". |
| ⚠ corrected gate (issue H2)        | **45.3%**            | Re-measured with production's `RequiresUnfilledSlot` guard applied. The earlier numbers above were taken without it — they over-counted bare-synonym matches that production would route to the LLM. |
| + `propose_crossing {method}` templates + `slots.method.synonyms` | **37.5% (current)** | Replaced bare-method synonyms with `{method}` / `take the {method}` / `{method} the river` / `{method} the wagon` templates so the matcher fills `slots.method` and clears the `RequiresUnfilledSlot` guard. |

The Oregon Trail end-to-end calibration test
(`internal/semroute/calibration_test.go`) enforces the ≤40% gate so
future commits can't regress the matcher silently. The recording
itself lives at `stories/oregon-trail/recording.yaml`; the synonyms
above sit in `stories/oregon-trail/intents.yaml` with `# Phase-7
calibration` comments so a future author understands why each was
added.

**What's blocking the return to ≤30%.** Most of the remaining LLM
turns either (1) hit `Intent.Examples` first ("banker" → example
"I am a banker") so the bare-string path returns a slot-less verdict
before the matcher tries templates, or (2) are designed-LLM cases
(open-ended questions, generative naming, out-of-band enum values).
The §4.2 slot-level synonyms feature is wired for the enum parser
but not yet exercised by the bare-string match path; once a
"slot value triggers slot fill" pass lands in `internal/semroute`,
the bare match can complete the slot without an explicit `{slot}`
template per phrasing and the rate should fall back under 30%
without further YAML edits.

### What stayed LLM-only (and why)

Twelve turns in the recording resolve via the LLM by design:

- **Open-ended `ask_question` turns** — "what month should we
  leave", "how do we ford in spring snowmelt", "ask what to do at
  the kansas river crossing". The {question} slot is free-form
  user text; capturing it via a template would gain nothing.
- **`name_party after X`** — the LLM does the actual *name
  generation* work; the synonym layer could route the intent but
  not fill the names.
- **Out-of-band enum values** — "i want to be a doctor"
  (doctor ∉ {banker, carpenter, farmer}); the LLM is the right
  layer to detect and respond to an unsupported value.
- **Highly-compositional purchases** — "i want 10 oxen, 2000 lbs
  food, 30 boxes bullets, 10 clothing, 4 wheels, 2 axles, 2
  tongues". The author could write a per-item template, but a
  single LLM call is clearer than the YAML to express it.

These are the cases where the LLM earns its keep. They're also why
the target is 30% and not 0%.

---

## A. Why not embeddings (yet)

A small embedding model (e.g. `all-MiniLM-L6-v2`, 22M params, ~80
MB in fp16) would let us replace `lex.Signature` with a 384-dim
vector and the synonym match with cosine similarity over a
per-state intent index. Pros: handles paraphrase the lexical layer
misses ("walk the wagon over" → `ford`). Cons:

- The model is a binary asset → either embed in the Go binary
  (size bloat) or download on first run (network dependency the
  user explicitly wants to avoid).
- ONNX-runtime-go and gomlx both add cgo dependencies. The current
  build is pure Go + SQLite. Trading that for ~5% better routing
  on paraphrases is a poor swap for an in-process router.
- Even with embeddings, slot extraction still needs a parser layer
  — embedding similarity tells you "this looks like a purchase,"
  not "the user said $240."

If the lexical fallthrough rate sticks above the target after
calibration, the natural next step is to add an `internal/embed`
package behind the same `semroute.Matcher` interface, gated by a
build tag so it's opt-in. **Today the lexical layer reaches 37.5%
on Oregon Trail under the corrected `RequiresUnfilledSlot` gate
(issue H2); the synonym-authoring work to bring it back below 30%
is tracked in §2 above.**

---

## B. Library-evaluation history (kept for posterity)

The original proposal surveyed Go ecosystem libraries for every
piece of the pipeline. The shipped surface adopts:

- `github.com/clipperhouse/uax29/v2` — UAX#29 word boundaries
  (already in deps via charmbracelet).
- `golang.org/x/text/unicode/norm` — NFKC normalisation.
- `golang.org/x/text/cases` — language-aware lowercasing.
- `github.com/kljensen/snowball` — Porter2 stemmer (pure Go).
- `github.com/cloudflare/ahocorasick` — multi-pattern matching.
- `github.com/agnivade/levenshtein` — Damerau-Levenshtein-1 fuzzy.
- `github.com/araddon/dateparse` — ISO + slash + RFC date forms.
- `modernc.org/sqlite` — the turncache backend.

Rolled our own (each is <100 LoC of focused code):

- Stopword list (~60 English entries).
- Lexical signature (SHA-1 over sorted stem-bag).
- Synonym template NFA (literal + `{slot}` capture, nothing else).
- Spelled-out integer parser ("six", "two hundred fifty").
- Per-type slot parsers (bool, list[T], money, date heuristics).
- Cache table CRUD (~150 LoC SQLite shim).
- Confidence bands (five named constants).

Considered and rejected:

- `bleve` — too heavy; we'd use 3% of it.
- `prose/v2` — Penn Treebank POS-tagging is overkill for typed
  intent routing where the slot grammar is already known.
- `sugarme/tokenizer` (HuggingFace) — BPE/WordPiece for transformer
  models, not relevant to the lexical tier.
- `hugot` / `onnx-go` — cgo dependencies; belong in a future
  embedding-tier proposal, not the base layer.
- `participle` — parser generator for a four-rule grammar would
  invite the synonym DSL to grow uncontrollably.

Net dependency cost: **7 small pure-Go libraries, 0 cgo**. The
build stays pure Go + SQLite.

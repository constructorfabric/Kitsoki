# Runtime: a schema-verified artifact format (markdown-as-data, lossless round-trip)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone

## Why

kitsoki writes **markdown-with-YAML-frontmatter artifacts** but has **no schema**
for any of them — a malformed ticket is only discovered when a downstream reader
trips over it (`listAllBugs` already swallows unparseable files so one bad file
doesn't poison a search, `localfiles_ticket.go:313`). Two wants follow from that
gap, and a third, smaller one rides along for free:

**1. Schema-verifiable artifacts (primary).** An artifact's frontmatter should
validate against a pinned schema id *at write time*, so the producing host call
fails fast instead of emitting a subtly-wrong file that breaks a reader three
rooms later. There is no schema mechanism today.

**2. Markdown-as-data, the data-primary direction (primary).** We want to author
a doc *source* as a structured record — some fields holding markdown block
scalars — validate it, then render a polished doc through the templating layer we
already use for views. Today there is no supported way to carry a markdown blob
inside a schema'd field and render it back cleanly.

**3. Lossless mutate-and-write (secondary, opportunistic).** The current write
path (`writeBugFile`, `localfiles_ticket.go:267`) decodes frontmatter into a
`map[string]any` (`:134`) and re-serializes with goccy `yaml.Marshal`. A Go map
has no author-order, so a single-key mutation like `ticket.transition` (`:491`)
re-emits the whole block in the marshaller's key order, dropping any comments and
collapsing block scalars. **In practice this is currently latent, not active:**
the on-disk corpus has already been normalized to goccy's sorted-key order (so a
rewrite is order-*idempotent* and produces no diff), no ticket carries a
frontmatter comment, and none uses a block scalar or anchor. So today this costs
us nothing — but it forecloses want #2 (you cannot keep a markdown block scalar
in a field through a rewrite), and it is a correctness landmine the moment an
author hand-orders keys or adds a comment. A node-based write path removes the
landmine and is a prerequisite for #2 — it is *not*, on its own, the reason to
build this.

### Where the artifact shape lives today

It is **not** three independent implementations. There is **one** parser and
**one** serializer, shared:

- **`internal/host/localfiles_ticket.go`** — owns both: `splitFrontmatter` (read,
  `:165`) and `writeBugFile` (write, `:267`). This is the only Go code that
  *serializes* frontmatter.
- **`internal/host/append_file_transport.go`** — **reuses** that I/O directly:
  `readBugFile` (`:84`) and `writeBugFile` (`:119`). It does not hand-roll
  anything; the `target_kind:` preamble is just a `BugFile.Front` map handed to
  the shared writer.
- **`internal/host/cypilot_artifacts.go`** — **reuses** `splitFrontmatter`
  (`:255`) for reads and **shells out to `cpt generate`** for writes
  (`:302`). It never serializes frontmatter in Go.

So the fix centralizes naturally: replace `splitFrontmatter` + `writeBugFile`
with the new package and every caller inherits validation + fidelity. cypilot's
write path is out of scope (it's an external binary); only its read-call moves.

This is grounded in a completed deep-research pass + an empirical round-trip
test (see [The model](#the-model)): the convenient Go path (`goccy`
decode→`Marshal`) cannot round-trip losslessly, and `gopkg.in/yaml.v3`'s
`yaml.Node` API can. That decides the library — it is not evidence of present
data loss.

## What changes

Add one small package — `internal/artifact` — that owns the
markdown-with-frontmatter artifact: parse, **lossless** mutate-and-write,
schema validation against a pinned `schema:` id, and a typed accessor for
markdown-block-scalar fields. The three hand-rolled sites migrate onto it.

> An artifact is a markdown body plus YAML frontmatter carrying a pinned
> `schema:` id. The frontmatter is held as a `yaml.Node` tree so a
> write-back preserves comments, key order, and block scalars; the engine
> validates it against the schema at write time. Markdown can live *in* the
> body **or** as a block-scalar field — same container, two altitudes.

## Impact

- **Code seams:** new `internal/artifact/` (parse, write, validate, render).
  Migration is centralized: `internal/host/localfiles_ticket.go` (replace
  `splitFrontmatter` + `writeBugFile`) fixes the ticket and append-transport
  paths at once, since `append_file_transport.go` already reuses that I/O.
  `cypilot_artifacts.go` only swaps its `splitFrontmatter` *read* call — its
  writes go through the external `cpt` binary and are untouched.
- **Vocabulary:** no new author-facing effects required for v1 — the existing
  `ticket.*` / cypilot host calls keep their signatures and gain validation +
  fidelity underneath. A general `host.artifact.*` surface is an
  [open question](#open-questions), not v1.
- **Stories affected:** none change behavior. `stories/bugfix/`, `stories/prd/`,
  and any cypilot story keep working; their on-disk artifacts simply stop being
  reordered on rewrite and start being schema-checked.
- **Backward compat:** existing artifact files parse unchanged (no `schema:`
  key ⇒ validation skipped, see [migration](#backward-compatibility--migration)).
- **Dependencies:** **no new module downloads** — `gopkg.in/yaml.v3`,
  `github.com/santhosh-tekuri/jsonschema/v6`, and `github.com/yuin/goldmark` are
  all already in `go.mod`. Note this *promotes* two of them to **direct** deps
  the project now owns: `yaml.v3` is currently unused under `internal/`, and
  `goldmark` is `// indirect` (pulled in transitively via
  `charmbracelet/glamour`, the TUI's renderer). No new third-party surface, but
  the dependency-tracking surface does grow by two.
- **Docs on ship:** a new `docs/architecture/artifacts.md`; cross-links from
  `docs/stories/bugs.md` and the cypilot story docs.

## The model

An artifact is two parts with a `---` fence, the universal convention
(Astro/Hugo/Jekyll/Obsidian all use it):

```
---
schema: ticket/v1        # pinned schema id — selects the validation schema
id: BUG-0007
status: open             # ticket.transition rewrites ONLY this, losslessly
# operator note: keep this comment across rewrites
summary: |               # a markdown-block-scalar field (data-primary)
  The reload path re-runs `on_enter`.
  See `internal/machine` for the guard.
---
## Repro

1. …
```

Two altitudes, one container:

- **Frontmatter-primary** — the markdown lives in the **body**; frontmatter is
  metadata. (Tickets today.)
- **Data-primary** — markdown lives in **block-scalar fields** (`summary` above),
  schema-validated, then rendered to a polished doc via pongo2. The body may be
  empty. This is the OpenAPI pattern (CommonMark inside `description` fields) and
  is what lets a doc *source* be schema-verifiable.

Both are the same file shape; the schema decides which fields are markdown.

### What `Render` produces (and what goldmark is *for*)

Be precise about altitudes, because the two libraries do different jobs and it is
easy to conflate them:

- **pongo2** does the data-primary assembly: a structured record (its
  block-scalar fields holding raw markdown) → a **markdown document** via a
  template. This is the only step that *emits* a doc. goldmark plays **no part**
  in markdown-out — it does not render markdown→markdown.
- **goldmark** does the *input* side: parsing/validating that a block-scalar
  field (or the body) is well-formed markdown in a **pinned dialect**, so
  "is this a valid markdown field" is deterministic. This is also what makes the
  `contentMediaType: text/markdown` assertion meaningful downstream.

So **`Render`'s output target is a markdown document string** (pongo2), not HTML
and not a terminal view. The TUI view path is a *separate* renderer
(`charmbracelet/glamour`, which wraps goldmark to emit ANSI) and is explicitly
**not** unified here — the shared artifact is "pin the same goldmark *parse*
dialect," not "produce the same output as a view." Conflating the two (HTML vs
ANSI vs markdown) is a known trap; see [non-goals](#non-goals).

### Round-trip fidelity — the load-bearing decision

The hard requirement for want #2 is **lossless** parse→mutate→serialize. This was
tested empirically against both YAML libraries already in `go.mod`, mutating one
scalar and re-encoding (the `ticket.transition` shape). To make the comparison
fair, the goccy column uses its *best* order-preserving API (`MapSlice` decode →
`Marshal`), **not** the lossier `map[string]any` path the production code
actually uses — the point is that even goccy's strongest path still loses the
other fidelity dimensions:

| Failure mode | `yaml.v3` (`yaml.Node`) | `goccy` (`MapSlice` decode→`Marshal`) |
|---|---|---|
| Head / inline comments | preserved | **lost** |
| Key ordering | preserved | preserved (production `map[string]any` path: **lost**) |
| Markdown block scalar (`\|`) | preserved verbatim | **collapsed to `"...\n..."`** |
| Anchors / aliases (`&x` / `*x`) | preserved | **expanded / merged** |
| Zero-padded literal `0010` | preserved | **mangled to `8` (octal!)** |
| `yes` / `no` literal form | preserved | **requoted** |
| Surgical single-key mutation | yes — rest untouched | n/a |

**Decision: hold the frontmatter as a `yaml.Node` tree and mutate the tree in
place; never decode-into-`map`/struct-then-`Marshal` on a write path.** The
current `ticket.transition` key-reordering is the production-path corollary
(`map[string]any` + `goccy.Marshal`); the block-scalar collapse is the one that
*blocks want #2*, and no goccy API avoids it. Decode-into-struct stays fine for
**read-only** consumers (listing, rendering) that discard the file afterward —
`internal/artifact` will expose both a `Node`-backed mutable handle and a
convenience typed decode for read paths.

Reality check on severity: against today's corpus this table is a *synthetic
stressor*, not a description of live loss — existing tickets are already in
sorted-key order, comment-free, and use no block scalars or anchors. The table
selects the library; want #2 is what makes the fidelity actually load-bearing.

(goccy *does* have a `CommentMap`/AST that can preserve more, but the idiomatic
path the codebase actually uses is the lossy one, and `yaml.Node` reaches
fidelity with no extra plumbing. goccy stays where it's used for decode-only
work — `secrets.go`, `agent_extract_helpers.go`, etc.; this proposal does not
rip it out.)

> **Why a package, not a two-function patch?** The round-trip fix alone *is*
> roughly a two-function change (`splitFrontmatter` + `writeBugFile` → node-backed
> equivalents). If lossless rewrite were the only goal, that patch would be the
> right call and this proposal would be overkill. The package earns its keep on
> wants #1 and #2 — a schema registry, write-time validation, type normalization,
> and the data-primary render path — which need a home of their own and which a
> patch to two ticket helpers has nowhere to live. Read this proposal as
> "build the schema + data-primary substrate, and get lossless rewrite as a
> byproduct," not "build a package to fix `ticket.transition`."

### Schema validation

`schema:` names a versioned id (`ticket/v1`). The package resolves it to a JSON
Schema and validates the decoded frontmatter with
`santhosh-tekuri/jsonschema/v6` (already a dep) at **write time**, so a bad
artifact never lands on disk. A markdown-block-scalar field is typed
`{"type": "string", "contentMediaType": "text/markdown"}` — `contentMediaType`
is the JSON Schema idiom for "this string is markdown"; it is **advisory**
(the validator does not parse it), which is correct: goldmark does the real
markdown parsing, the schema only asserts the field is a string and present.

**Type normalization is a required step, not a free one.** `santhosh-tekuri`
validates the JSON value model (`string`, `float64`/`json.Number`, `bool`, `nil`,
`map[string]any`, `[]any`). A raw YAML decode does not produce that model: an
integer decodes to `int`, and — critically — YAML auto-coerces an *unquoted*
timestamp to `time.Time` and `yes`/`no` to `bool`. Feeding those straight to the
validator yields confusing rejections (e.g. a hand-authored unquoted `filed_at`
decodes to `time.Time` and fails a `{"type":"string"}` field; the corpus quotes
it today precisely to stay a string). So the package **must normalize the decoded
tree to the JSON value model before validating** — the simplest reliable route is
to encode the `yaml.Node` to JSON and decode back, or decode YAML directly into
JSON-compatible types. This is the one part of the "already a dep, mechanical"
story that is actual work; it gets its own task (1.3a).

### Prior art (don't reinvent)

- **Frontmatter-primary + schema** — Astro Content Collections (Zod-validated
  frontmatter) and `remark-lint-frontmatter-schema` (JSON-Schema-via-AJV, with
  an in-file `$schema` key). We copy the `schema:`-key → JSON-Schema approach.
- **Markdown-in-fields** — OpenAPI pins CommonMark *by version* in `description`
  fields. Lesson: pin the dialect (a fixed goldmark extension set), don't leave
  it ambiguous.
- **Typed-tag unification** — Markdoc (Stripe): markdown + a typed, validated
  tag schema, "docs as data." It is the north star for a *future* inline-typed
  layer, but it is **JS-only — no Go port** — so we borrow the design (a typed
  schema over the goldmark AST), not the code, and not in v1. (See
  [non-goals](#non-goals).)
- *Refuted during research, do not cite:* Backstage `description` is plain text,
  not markdown; MyST does **not** round-trip notebooks losslessly.

## Engine seams & invariants

`internal/artifact` is a library, not a new engine loop stage; it is called from
host handlers. Invariants it enforces:

1. **Write-time schema validation.** An artifact whose frontmatter names a
   `schema:` that fails validation is **not written**; the host call returns a
   `Result.Error` (the existing `ticket.*` error convention,
   `localfiles_ticket.go:475`). No `schema:` key ⇒ validation skipped (compat).
2. **Lossless rewrite.** Any mutate-and-write goes through the `yaml.Node`
   handle; decode-then-marshal on a write path is the bug being removed.
3. **Pinned markdown dialect.** One shared goldmark *parse* config (a fixed
   extension set) for body + block-scalar fields, so "is this valid markdown" is
   deterministic. This pins the dialect goldmark parses; it does **not** claim
   byte-identical output with the TUI view path, which renders through glamour to
   ANSI (a different output target — see [What `Render` produces](#what-render-produces-and-what-goldmark-is-for)).

No new *load-time* (story-load) invariant — artifacts are runtime data, not
story structure. Schema ids are resolved from a registry whose location is an
[open question](#open-questions).

## Backward compatibility / migration

- Existing artifact files parse unchanged. Files with **no** `schema:` key skip
  validation, so today's tickets/cypilot artifacts/transport files keep working
  untouched.
- Migration is centralized, not per-producer: swap the shared `splitFrontmatter`
  + `writeBugFile` for the `internal/artifact` handle and tickets *and* the
  append-transport inherit it at once (the transport reuses that I/O). cypilot
  only changes its read call. The behavioral change is *positive* —
  `ticket.transition` stops reordering frontmatter and comment-preservation
  becomes free.
- Adding `schema:` to a ticket kind is opt-in per kind; ship `ticket/v1` and add
  the key to the ticket template, leaving older files valid. **Author the schema
  from the corpus, not from memory:** it must accept every field shape existing
  tickets already carry — `external: {}`, `related: [...]`, empty-string
  `assignee`/`url`/`trace_ref`, and the quoted `filed_at` timestamp. The corpus
  regression (task 2.4) is the gate that proves this — it runs `ticket/v1`
  against every `issues/**/*.md` before the key is added to the template.

## Tasks

```
## 1. Package
- [ ] 1.1 internal/artifact: yaml.Node-backed Parse → handle{Front, Body}
- [ ] 1.2 Lossless SetField / Write (Node mutation; round-trip test is the gate)
- [ ] 1.3a Normalize decoded frontmatter to the JSON value model (Node→JSON→any,
          or YAML→JSON-typed decode) so int/time.Time/bool coercions don't break
          validation — this is real work, not a freebie (see Schema validation)
- [ ] 1.3b Validate(handle) via santhosh-tekuri/jsonschema/v6 against `schema:` id
- [ ] 1.4 Render(handle) → **markdown document string** via pongo2 (body +
          block-scalar fields); goldmark only parses/validates the dialect on the
          way in, it does NOT emit the doc and this is NOT the glamour/ANSI view path
- [ ] 1.5 Schema registry (embedded schemas/*.json; resolve `schema:` → schema)

## 2. Verification
- [ ] 2.1 Round-trip golden: parse → SetField("status") → write; assert comments,
          key order, block scalars, anchors, `0010` all survive (the table above)
- [ ] 2.2 Validation unit: a bad `ticket/v1` artifact is rejected, a good one passes;
          include a coercion case (unquoted date / `yes`) to prove 1.3a holds
- [ ] 2.3 Data-primary render: a record with a markdown `summary` field → expected doc
- [ ] 2.4 Corpus regression: `ticket/v1` validates EVERY existing issues/**/*.md;
          this is the real backward-compat gate, not just the synthetic golden

## 3. Adopt + document
- [ ] 3.1 Migrate localfiles_ticket.go (replace splitFrontmatter + writeBugFile)
          onto internal/artifact — this also fixes append_file_transport.go for
          free, since it reuses readBugFile/writeBugFile
- [ ] 3.2 Migrate cypilot_artifacts.go READ call (swap splitFrontmatter only); its
          writes shell out to `cpt` and are out of scope
- [ ] 3.3 Ship ticket/v1 schema (authored FROM the corpus, per 2.4); add `schema:`
          to the ticket template
- [ ] 3.4 Write docs/architecture/artifacts.md; cross-link bugs.md; trim/delete this proposal
```

## Verification

No LLM needed. The **round-trip golden** (2.1) drives the exact
`ticket.transition` mutation through the package and asserts byte-level fidelity
on the failure modes in the table — this asserts the *capability* (the old
`map[string]any`+goccy path fails it; the new path passes). Note this is a
synthetic input: today's corpus would not exercise the comment/block-scalar/
anchor rows, so 2.1 proves the library choice, not that we are repairing live
loss. The load-bearing backward-compat test is the **corpus regression** (2.4):
`ticket/v1` must validate every existing `issues/**/*.md`. Schema rejection (2.2,
including a coercion case for 1.3a) and data-primary render (2.3) are plain table
tests. The migrated `ticket.*` host calls are then exercised by the existing
bugfix flow fixtures unchanged.

## Open questions

1. **Schema registry location** — embed `internal/artifact/schemas/*.json` via
   `go:embed`, or let a story ship its own artifact schemas alongside its YAML?
   *Lean: embed the core kinds (`ticket/v1`); allow story-supplied schemas later.*
2. **A general `host.artifact.*` surface** — should authors get
   `host.artifact.read` / `.write` / `.validate` as first-class host calls, or
   does the package stay internal behind the existing `ticket.*` / cypilot
   calls? *Lean: internal for v1; promote to host calls only when a story needs
   to author an arbitrary artifact kind.*
3. **CUE instead of JSON Schema?** Research flagged CUE as "very relevant"
   (validate + define + round-trip in one Go-native tool) but produced no
   verified evidence on its comment/round-trip behavior, and we already depend on
   `santhosh-tekuri/jsonschema`. *Lean: JSON Schema for v1; revisit CUE only if
   schema *authoring* ergonomics become the pain.*

## Non-goals

- **No Markdoc-style inline typed tags in v1.** Validating structure *inside*
  the markdown body (typed tags over the goldmark AST) is the north star, but
  it's a separate, larger slice — v1 validates frontmatter + block-scalar fields
  only.
- **No new templating engine.** Rendering stays goldmark + the existing pongo2
  layer; this proposal does not introduce MDX/Markdoc execution.
- **Not ripping out goccy.** goccy stays on decode-only paths; this only changes
  the *write/round-trip* path to `yaml.Node`.
- **No story-load schema for artifacts.** Artifacts are runtime data; story
  structure validation is unchanged.

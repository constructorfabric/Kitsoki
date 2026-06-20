# Runtime: `reference` filter — line-number any embedded content, get attribution back

**Status:** Core implemented (filter + tests + author docs). Remaining: adopt in a
real story, optional structured trace field. See Tasks.
**Kind:**   runtime
**Epic:**   — standalone

> **Landed:** the `reference` filter (`internal/render/pongo.go` `filterReference`,
> registered globally in `init()`), unit + backend-agnostic tests
> (`internal/render/pongo_test.go`), and author docs folded into
> `docs/stories/prompts.md` § Embedding reference material and the
> `kitsoki-story-authoring` SKILL. What's left is adoption in a real story and the
> optional structured `references:` trace field — the rest of this doc is the
> spec for those follow-ons; delete it once a story adopts the filter.

<!--
  Companion to docs/stories/prompts.md (prompt extension). That doc covers how a
  story's prompts compose and specialize via the @story/@shared/@import search
  path. This proposal adds a primitive that is deliberately NOT tied to that
  machinery: a built-in template filter that line-numbers embedded content and
  emits a traceable source attribution — available in every render path,
  regardless of which renderer or agent backend is in play.

  v2 supersedes v1's `{% reference %}` tag, which resolved paths through the
  prompt-extension search-path loader (@shared/@story). That loader only exists
  when an AppRenderer is injected — so the tag silently did nothing on the inline
  render.Pongo path (CLI one-shots, meta/off-path agents, tests). The corrected
  requirement: built-in, backend-agnostic, no @ syntax.
-->

## Findings: how prompt construction works

Three facts about prompt construction frame the design. They came out of a review
of the Messages API + Claude Code injection paths, but apply to any prompt we
hand an LLM:

1. **There is no max single-message length.** A prompt is bounded only by the
   model's *context window* (Opus 4.x: 1M input tokens) and a transport-level
   request-size cap (~32 MB on the wire, far past any prose reference). One
   message holding a whole reference document is fine; splitting buys nothing the
   model can see — it reads the concatenation either way.

2. **Embed, don't instruct.** The reliable way to put a document in front of the
   model is to inline its bytes into the prompt — *not* to tell the model "read
   `foo.md`" and hope it spends a tool call. Inlining is deterministic: the
   content is present at render time, identical every run, no model-discretion
   step in between.

3. **Faithful attribution makes context traceable.** If a report cites
   "`spec.md` line 54," the prompt must carry that text *with its own line
   numbers* so the citation lands right — and must name the source so that,
   reading the recorded prompt later, you can recover what the model saw and find
   it in the repo. Verbatim text + line numbers + a source label + a content hash
   turn an embedded blob into a checkable citation.

**Never truncate to fit.** If a reference exceeds the window, slice it
deliberately — a silently truncated, line-numbered document throws off every
downstream citation.

## What the composition system already gives authors

Prompt extension (`docs/stories/prompts.md`) covers most of "embed context":

- **`{{ args.x }}`** — per-turn scalars from the effect's `with.context.args:`.
- **`{% extends %}` + `spec_` blocks** — a project overlay fills a
  `spec_project_context` hole with inline prose a human writes.
- **`{% include "@shared/frag.md" %}`** — pulls a shared fragment and **renders
  it** as a sub-template, merged into the parent.

What none does is take **arbitrary content and present it as line-numbered,
attributed evidence**. And the include/extends path is bound to the
`@`-namespaced search-path loader, which exists *only* when the full prompt-
extension `AppRenderer` is injected — not on the inline render path that CLI
one-shots, meta/off-path agents, and tests use. A reference primitive must not
inherit that coupling.

## What changes

Add one **built-in pongo2 filter** — `reference` — registered globally in
`internal/render/pongo.go` `init()` next to `col` / `reverse` / `wordwrap`. It
takes the embedded **content** as its input value and a **source label** as its
parameter, prefixes every line with its number, and wraps the result in an
attribution block carrying the label, line span, and a content hash:

```pongo
{{ args.spec | reference:"api-spec.md" }}
```

renders to:

```
<reference src="api-spec.md" lines="1-87" sha256="9f2a1c4b">
   1 | # API spec
   2 |
   3 | Every request carries a bearer token in `Authorization`.
 ...
  87 | // end
</reference>
```

One sentence: **`reference` is a pure formatter — content in, line-numbered
attributed block out — that works in any render context because it resolves
nothing.** It never reads a file, never touches the search path, never needs a
loader or an overlay. That is exactly what makes it backend-agnostic: it's a
function of its inputs, available wherever pongo2 runs.

Because it's content-driven, **where the content comes from is the author's
choice** and is orthogonal to this filter:

- a literal in the prompt, or a `{{ args.x }}` / `{{ world.x }}` value;
- the bytes of a **recorded host file-read** — the kitsoki-idiomatic, traceable
  way to pull a file into a deterministic pipeline (the read lands as a host-call
  event, so the source path + bytes are already in the trace), then the value
  flows in as an arg and the filter formats it.

The filter numbers from line 1 of the content it's given, so handing it a whole
file yields **absolute** line numbers that match a report's citations. (Offset
support for slices is an open question below.)

## Impact

- **Code seams:** `pongo2.RegisterFilter("reference", …)` in
  `internal/render/pongo.go` `init()` (the global filter registry — same site as
  `filterCol`/`filterReverse`). Because registration is global, the filter is
  live on **both** the inline `render.Pongo` path (`pongo.go:196`) and the
  `AppRenderer` paths (`prompt_renderer.go`) — i.e. every prompt render, under any
  agent backend.
- **Vocabulary:** one new built-in filter (table below). No new effect, world
  key, host call, tag, or `@` namespace.
- **Stories affected:** none — additive; no prompt uses it until an author does.
- **Backward compat:** a prompt that doesn't use `|reference` renders
  byte-identically.
- **Docs on ship:** fold into `docs/stories/prompts.md` (new "Embedding reference
  material" section) + the `kitsoki-story-authoring` SKILL; delete this proposal.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| filter | `reference` | `{{ CONTENT \| reference:"LABEL" }}` | CONTENT = any string value; LABEL = source name for attribution. Emits a line-numbered `<reference src=… lines=… sha256=…>…</reference>` block. Pure: resolves nothing, reads nothing. |

## The model

```
{{ args.spec | reference:"api-spec.md" }}
        │
   input value: the content string (from a literal, arg, world var,
                 or the bytes of a recorded host file-read — author's choice)
   param:        "api-spec.md"  (source label, free-form)
        │
        ├─ split content into lines
        ├─ prefix each with its 1-based number, right-aligned gutter:  "%*d | "
        ├─ sha256(content)  → citation pin
        └─ emit:  <reference src="api-spec.md" lines="1-N" sha256="…">
                    1 | …
                    …
                  N | …
                  </reference>
```

This is **deterministic formatting** — interpretive in the same sense the rest of
prompt rendering is (not at all). No decider, no gate, no LLM. It contrasts with
`{% include %}` on two axes: include *resolves a path* (search-path / `@`-bound)
and *renders the target as a sub-template*; `reference` does neither — it formats
the literal value it's handed. That's why it survives on the inline path where no
loader exists.

## Decision recording

No new interpretive decision — but the **provenance is a labeled datapoint**,
parallel to how prompt extension records `prompt_overlay` / `spec_*`
(`internal/host/prompt_render.go:80`, `docs/stories/prompts.md` §Trace provenance):

- The agent-call event already captures the **rendered prompt bytes**, which now
  contain the attribution header + line-numbered body verbatim — the trace is
  self-describing and the citation is walkable by a human with zero new code.
- When the content came from a **host file-read**, that read is *itself* a
  recorded host-call event — source path and bytes already in the trace,
  independent of the prompt. The `sha256` in the attribution lets you tie the
  embedded block back to that read.
- **Optional follow-up:** record a structured `references: [{src, sha256, lines}]`
  list on the agent-call event so "which sources fed this prompt, at what bytes"
  is queryable without parsing prompt text — the moat move (a tracing.md concern;
  link a tracing proposal if the schema change wants its own review).

## Engine seams & invariants

- **Global registration = backend-agnostic.** Registering in `pongo.go` `init()`
  (not per-`TemplateSet`) is the whole point: the filter is part of the engine,
  so it's available identically on the inline path and the AppRenderer path. No
  context key, no injected loader, no `@` resolution — nothing a particular
  backend or render entry point could fail to wire up.
- **Panic safety.** Both render paths already `recover()` around filter execution
  (`pongo.go:212`, `prompt_renderer.go:367`) because filters run arbitrary Go on
  author input — `reference` inherits that net. Keep its body allocation-bounded
  (don't number a multi-GB blob unguarded); a non-string input returns unchanged
  rather than erroring, matching `reverse`'s degrade-to-no-op convention.
- **No load-time file validation needed** — it resolves no path, so there's no
  "missing target" failure mode to guard (unlike `{% extends %}`). A bad value is
  just empty/odd output, visible in `kitsoki prompts render`.
- **Preview unchanged.** `kitsoki prompts render` prints the assembled prompt, so
  an author sees the fully line-numbered, attributed block before any LLM call.

## Backward compatibility / migration

Purely additive and opt-in. No prompt uses `|reference` until an author adds it;
every existing prompt and cassette renders byte-identically. No migration.

## Tasks

```
## 1. Engine
- [x] 1.1 pongo2.RegisterFilter("reference", filterReference) in pongo.go init()
- [x] 1.2 filterReference: split lines, right-aligned numbered gutter, sha256, attribution wrapper
- [x] 1.3 Nil / missing input → pass through unchanged (reverse-style no-op)
- [ ] 1.5 (optional) structured `references: [{src, sha256, lines}]` provenance on the agent-call event

## 2. Verification
- [x] 2.1 Unit: numbers lines 1..N with correct right-aligned gutter; lines= span + sha256 correct
- [x] 2.2 Unit: content containing `{{ }}` / `{% %}` is emitted verbatim (filter input is not re-parsed)
- [x] 2.3 Unit: filter renders identically on the inline render.Pongo path AND the AppRenderer path (backend-agnostic guarantee)
- [ ] 2.4 Flow fixture: a story prompt that references arg content renders deterministically (no LLM) — lands with adoption

## 3. Adopt + document
- [ ] 3.1 Adopt in one real story prompt (content via a recorded host read or an arg)
- [x] 3.2 Fold into docs/stories/prompts.md + kitsoki-story-authoring SKILL (delete this proposal once 3.1 lands)
```

## Verification

No LLM needed. Render a prompt that pipes a fixture string through `|reference`
on the **inline** path (plain `render.Pongo`, no AppRenderer) and on the
AppRenderer path — assert identical output, correct absolute line numbers, the
`sha256`, and that a `{{`-containing input came through verbatim. Stateless
throughout; `kitsoki prompts render` is the manual spot-check.

## Open questions

1. **Filter name.** `reference` reads well and matches the request's word, but
   collides with the codebase's existing "prompt reference" term (a search-path
   path). Alternatives: `cite`, `linecite`, `evidence`. *Lean: `reference` —
   disambiguate in prose; the collision is conceptual, not syntactic.*
2. **Absolute line offset for slices.** A pongo2 filter takes one param (the
   label). If an author embeds a *slice* and needs the gutter to start at the
   slice's real first line, there's no second param. Options: (a) whole-file only,
   numbering from 1 (simplest — finding #1 says length isn't the constraint);
   (b) pack it into the label (`"spec.md@51"`) and parse; (c) a sibling
   `lineoffset:N` filter chained before it. *Lean: (a) ship whole-file-from-1;
   add an offset only if a real story needs slicing.*
3. **Gutter / wrapper format.** `"%*d | "` gutter + XML-ish `<reference>` wrapper
   vs. a Markdown fenced block with a caption. *Lean: XML-ish wrapper + `N | `
   gutter — LLMs parse the delimiters unambiguously and it greps cleanly in the
   trace. ASCII only (no box-drawing) for clean tokenization.*
4. **Structured provenance now or later.** Ship verbatim-in-bytes first (zero
   schema change); add the `references:` event field as a fast follow? *Lean: yes
   — the bytes are already traceable; the queryable datapoint wants its own
   tracing review.*

## Non-goals

- **Resolving or reading files from the template.** The filter formats a value it
  is handed; getting a file's bytes is a separate, already-traced concern (a host
  read, an arg). This is what keeps it backend-agnostic and `@`-free.
- **Re-rendering referenced content.** `reference` is verbatim by definition; an
  author who wants a rendered sub-template uses `{% include %}`.
- **Auto-citation enforcement.** It gives the model citable, attributed context;
  it does not make the model cite, nor verify citations it produces — a
  story-prompt / rubric concern.
```

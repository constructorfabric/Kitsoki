# Runtime: Prompt extension as the primary story-customization seam

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone

## Why

A story's prompts are where its generic logic meets a specific project's
reality — coding standards, repo layout, domain vocabulary, review rubric,
house tone. Today there is no way for a *project* to inject that detail
without **forking the story**:

- Prompts are leaf template files rendered through the package-level
  `render.Pongo()` — four prompt-file sites (`internal/host/oracle_ask.go:117,136`,
  `oracle_ask_with_mcp.go:344`, `oracle_helpers.go:34`) plus one inline
  agent system-prompt string (`oracle_ask_with_mcp.go:424`). `Pongo` calls
  `pongo2.FromString` against a bare string with **no `TemplateSet`**
  (`internal/render/pongo.go:178`), so `{% include %}` and `{% extends %}`
  cannot resolve — there is no template root to look names up in.
- Views already have the mechanism prompts lack: `render.AppRenderer`
  (`internal/render/renderer.go:38`) owns a per-app `pongo2.TemplateSet`
  rooted at `<appDir>/views/`, so views compose via `{% extends %}` /
  `{% include %}` / `RenderExtended` blocks. Prompts are routed around it.
- The only cross-prompt composition that exists is
  `imports.overrides.prompts` (`internal/app/imports_overrides.go:87`), a
  **whole-file path swap** at fold time (`docs/stories/imports.md:175`).
  It lets a parent *replace* a child's prompt wholesale; it cannot
  *extend* one, share a fragment, or fill a hole the base author marked.

So a project adopting `bugfix` or `prd` to its own repo must copy the
story and edit prompt bodies in place — exactly the fork the imports moat
is meant to prevent. The generic story and the project's specifics fuse
into one artifact, and every upstream prompt improvement has to be
re-merged by hand.

This proposal makes **prompt extension** — a story ships base prompts with
named extension points; a project supplies a thin overlay that fills them
— the supported, documented, primary way to specialize a story.

## What changes

Prompts gain the same per-app template-set rendering views already have,
plus an **overlay search path** so a project's prompt files can
`{% extends %}` the story's base prompts and override named `{% block %}`s
— without touching the story. One sentence: *a prompt renders through a
per-app renderer whose loader resolves `extends`/`include` across an
ordered overlay → story → shared search path, so projects extend prompts
instead of forking stories.*

Three moving parts:

1. **Prompt renderer.** The oracle host sites render through a
   prompt-scoped `AppRenderer` (a `TemplateSet`) instead of bare
   `render.Pongo()`. The search path arrives via the host context, the
   same channel the view renderer already travels — not a process env var
   (see Engine seams).
2. **Bespoke search-path loader.** A story declares its prompt root and
   optional shared dirs; a project supplies an overlay dir at run time. A
   custom `pongo2.TemplateLoader` resolves names across the ordered path
   and interprets the `@`-namespaces below. Stock multi-loader stacking
   will *not* do — see Engine seams for why.
3. **Namespaced references.** `extends`/`include` targets are namespaced so
   an overlay can extend the very base it shadows: `@story/…` (the story's
   own prompt dir), `@shared/…` (shared fragments), bare name (search-path
   order, overlay first).

`imports.overrides.prompts` is generalized from *swap* to *overlay*: the
override file may now `{% extends "@story/…" %}` rather than only replace.

## Impact

- **Code seams:**
  - Five `render.Pongo()` sites (four prompt files + one inline system
    prompt, listed in Why) → the prompt renderer.
  - `internal/render/renderer.go` — generalize `AppRenderer`'s root (today
    hard-wired to `<appDir>/views/`) so a prompt renderer can root at the
    prompt search path; or a sibling `NewPromptRenderer`.
  - `internal/app/imports_overrides.go:87` — generalize the prompt override
    from swap to overlay-extend; resolve and carry the search path.
- **Vocabulary:** new app-config `prompts:` block; `@story`/`@shared`
  reference namespaces; the `spec_<name>` block convention. Table below.
- **Backward compat:** default-on but inert — a story with no `prompts:`
  block, no overlay, and no `{% block %}`/`{% include %}` resolves through a
  one-root path and renders byte-identically (verification §2.2). Cassettes
  unchanged.
- **Docs on ship:** `docs/stories/prompts.md` (new), `authoring.md`,
  `imports.md`, the `kitsoki-story-authoring` skill, embedded `doc.go`, and
  a recipes section — a first-class deliverable (Tasks §4); the mechanism
  is only as good as the authoring guidance.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| app config | `prompts:` | `{ root: ./prompts, shared: [./prompts/_shared], overlay: <runtime> }` | declares the prompt search roots; `overlay` is supplied at run, not committed |
| reference ns | `@story/<path>` | extends/include target | resolves against the story's own prompt root, bypassing the overlay — lets an overlay extend the base it shadows |
| reference ns | `@shared/<path>` | extends/include target | resolves against declared `shared:` dirs |
| reference ns | `<bare>` | extends/include target | search-path order (overlay first), so an overlay file shadows a base of the same name |
| block convention | `spec_<name>` | `{% block spec_… %}` | marks a **specialization surface**: default is provisional. Empty body ⇒ must fill; non-empty ⇒ may refine. Enumerable by scanning prompt files |
| CLI | `kitsoki prompts spec <story>` | lists `spec_*` blocks + defaults | the specialization surface authors, projects, and improvement agents read |
| import override | `overrides.prompts.<child>` | now may point at an overlay that `{% extends %}` | generalizes the §10 swap (`imports.md:175`) from replace → extend |

## The model

```
effect: invoke host.oracle.ask  with: { prompt: prompts/diagnose.md, args: {…} }
                       │
        resolvePromptPath + prompt renderer (search path from host context)
                       │
   ┌───────────────────┼────────────────────────────┐
   ▼                   ▼                              ▼
overlay/diagnose.md   story/prompts/diagnose.md    @shared/review_rubric.md
{% extends            {% block spec_project %}      (included fragment)
  "@story/diagnose.md" %}  {% endblock %}
{% block spec_project %}
  Acme repo: gofmt, no naked returns…   ← project specifics, no fork
{% endblock %}
```

A story author writes the base prompt with `{% block %}` holes and
`{% include "@shared/…" %}` fragments; a project drops an overlay that
`{% extends "@story/…" %}` and fills the blocks. The effect's unchanged
`prompt: prompts/diagnose.md` resolves overlay-first, so it picks up the
overlay automatically. Rendering is pure template expansion over files on a
fixed, loader-resolved path — replayable, same inputs → same bytes. This is
execution, not interpretation; it does not introduce a decision to record.

### Specialization convention: provisional default sections

Not all extension points are equal. A `{% block %}` is either a **hole**
(empty default — the story expects a project to fill it) or a **provisional
default** (a working body the story ships so it runs standalone, but one the
author knows is generic and likely to need specialization — a diagnosis
rubric, a review tone). Both are marked by one convention: a **`spec_`
prefix on the block name**, the single machine-readable signal that "this
section's default is provisional." Non-`spec_` blocks are structural and not
specialization targets.

A name prefix (not a comment) is chosen because it is a valid pongo2
identifier, survives `{% extends %}`/override unchanged, and lets a load-time
scan **enumerate the specialization surface** — the set of `spec_*` blocks,
each with its default body and whether it's a hole. Three consumers read that
surface:

1. **Authors / projects** — `kitsoki prompts spec <story>` lists which
   sections are provisional and where to specialize.
2. **Improvement / meta agents** — their system prompt is told that `spec_*`
   defaults are provisional and that the corrective action for a
   project-specific gap is to specialize the block *in an overlay*, never to
   edit the story's base prompt — keeping self-improvement on the seams the
   author marked rather than rewriting shared logic.
3. **The trace** — recording which `spec_*` blocks defaulted vs. were
   overridden makes "this provisional default was never specialized here" a
   labeled, queryable datapoint (the moat's decision-as-datapoint corollary),
   so the improvement loop can rank which defaults most often underperform.

The convention is advisory: a story with no `spec_*` blocks exposes an empty
surface and behaves exactly as today.

## Decision recording

No new interpretive decision is introduced — prompt assembly is
deterministic. The oracle-call trace event **already records the rendered
prompt bytes** (`appendOracleCalledEvent(…, rendered, …)`,
`oracle_ask.go:276`, stored inline or in a sidecar by
`oracle_event_sink.go:260`), so the post-extends/post-include text the LLM
saw is already captured. The only gap is provenance: record the
**contributing overlay path** and **which `spec_*` blocks defaulted vs. were
overridden**, so a run with an overlay in effect is reconstructable and the
spec_-datapoint above is queryable. That field addition is a `tracing.md`
follow-up.

## Engine seams & invariants

- **Render seam.** Replace the five `render.Pongo()` sites with a prompt
  renderer obtained from the host context. Inline content still renders via
  `FromString` (fast path preserved for non-templated prose,
  `renderer.go:118`), but `{% include %}`/`{% extends %}` now resolve.
  Re-use `RenderExtended`'s block-via-context trick (`renderer.go:180`) so
  block bodies are never re-parsed.
- **Bespoke loader required.** Stock pongo2 multi-loader stacking does *not*
  give overlay-first bare names for `extends`/`include` targets: pongo2's
  `resolveFilename` (used to resolve a target *inside* an already-loaded
  template) consults `loaders[0]` only and resolves names relative to the
  parent template, while only top-level `resolveTemplate` iterates loaders.
  The overlay/`@story`/`@shared` resolution must therefore live in one custom
  `TemplateLoader` that interprets the prefixes and precedence itself — it
  cannot ride on loader ordering. (Settles part of Open Q2: whichever
  renderer shape we pick, the loader is bespoke.)
- **Search-path transport.** The resolved overlay → story → shared path is
  threaded through the **host context**, the channel the view renderer
  already uses — *not* a process env var. `KITSOKI_APP_DIR` is a
  process-global `os.Setenv` (`cmd/kitsoki/session.go:1392`, read at
  `oracle_ask.go:396`); modeling a new var on it would inherit that
  process-global race under concurrent oracle calls. Carrying the path in
  context avoids it.
- **Load-time invariants (fail-fast at load, not oracle dispatch):**
  - every `{% extends %}`/`{% include %}` target in a story's prompts
    resolves on the story-only path (a story is valid with no overlay);
  - every `{% block %}` an overlay overrides exists in its base;
  - `@story`/`@shared` namespaces resolve to declared roots;
  - an overlay may only target prompts the story actually references — the
    typo guard `applyPromptOverrides` already applies
    (`imports_overrides.go:87`).

## Backward compatibility / migration

- Default-on, inert by construction (see Impact): no `prompts:` block → a
  one-root path → identical output; existing cassettes pass (§2.2).
- `imports.overrides.prompts` keeps working as a swap; the overlay-extend
  form is additive. No story must migrate.
- Becoming *project-extensible* is opt-in per story — it happens only once
  an author carves `{% block %}` points. Migrating `bugfix` and `prd` is the
  reference adoption (§3.1), not a forced sweep.

## Tasks

```
## 1. Render engine
- [ ] 1.1 Prompt renderer: TemplateSet with a bespoke search-path loader (generalize AppRenderer root or add NewPromptRenderer)
- [ ] 1.2 @story / @shared / bare resolution inside the custom loader
- [ ] 1.3 Swap the 5 render.Pongo() sites (oracle_ask ×2, oracle_ask_with_mcp file + inline system prompt, oracle_helpers) onto the prompt renderer
- [ ] 1.4 Resolve prompts: config → search path, threaded through the host context (no env var)
- [ ] 1.5 Regression guard: every existing story's prompts render byte-identically vs. today's render.Pongo() (locks backward compat — verification §2.2)

## 2. Composition + invariants
- [ ] 2.1 Generalize imports.overrides.prompts from swap → overlay-extend
- [ ] 2.2 Load-time invariants: story-only path resolves; overridden blocks exist; namespaces resolve; clear errors
- [ ] 2.3 Enumerate the spec_* surface at load (scan prompt files); expose via `kitsoki prompts spec <story>`
- [ ] 2.4 Improvement/meta system prompts advised that spec_* defaults are provisional + overlay is the edit target (not the base prompt)
- [ ] 2.5 Trace records the contributing overlay path + which spec_* blocks defaulted vs. overridden (bytes already captured — oracle_ask.go:276); tracing.md follow-up

## 3. Adopt
- [ ] 3.1 Carve spec_* extension points (holes + provisional defaults) into bugfix + prd prompts; ship one example overlay
- [ ] 3.2 Convert one existing imports.overrides.prompts swap to the extend form as a worked migration

## 4. Document (first-class, and the bulk of the effort — may ship as its own pass after §1–3 land)
- [ ] 4.1 New docs/stories/prompts.md: model, search path, namespaces, blocks, overlays, fail-fast errors, the spec_* convention
- [ ] 4.2 docs/stories/authoring.md: replace incidental prompt mentions (~L194, L260) with a real "extensible prompts" section
- [ ] 4.3 docs/stories/imports.md §10: rewrite swap → overlay-extend; keep swap as the degenerate case
- [ ] 4.4 kitsoki-story-authoring SKILL.md: "Prompt extension" section + checklist (mark spec_* blocks, holes vs. provisional defaults, name fragments, never fork)
- [ ] 4.5 Embedded doc.go for the prompt renderer (doc-standard rubric; go-module-docs skill)
- [ ] 4.6 Recipes: add project context; share a rubric fragment; override one block inherit the rest; swap a whole prompt (legacy)
- [ ] 4.7 Migrate shipped sections out of this proposal into the above; delete this file
```

## Verification

- **2.1 Stateless render unit:** a `prompts/` fixture with a base
  (`{% block x %}default{% endblock %}`), a shared include, and an overlay
  that extends + overrides `x`; assert the renderer's output for (no overlay)
  = default and (overlay) = overridden, byte-exact. Pure expansion — no LLM
  (memory: no-LLM-tests-by-default).
- **2.2 Regression:** every existing story's prompts render byte-identically
  through the new one-root path vs. today's `render.Pongo()` — the
  backward-compat guard (Task 1.5).
- **2.3 Load-time errors:** a malformed overlay (overrides a nonexistent
  block; targets an unreferenced prompt; unresolved `@shared`) fails at load
  with a located message; a story whose prompt `{% include %}`s a missing
  fragment fails on the story-only path.
- **2.4 Specialization surface:** a fixture story with two `spec_*` blocks
  (one hole, one provisional default) and one structural block; assert
  `kitsoki prompts spec` lists exactly the two `spec_*` blocks with their
  default bodies and hole/provisional flag, and omits the structural block.
  Pure enumeration — no LLM.

## Open questions

1. **Overlay supply mechanism** — committed `prompts.overlay:` field vs.
   run-time `kitsoki run --prompt-overlay <dir>` vs. both. *Lean: run-time
   flag + optional config default; the overlay is project-specific and
   shouldn't be baked into the shared story.*
2. **Generalize `AppRenderer` vs. add `NewPromptRenderer`** — one renderer
   with a configurable root, or a prompt-specific sibling. *Lean: generalize
   the root; views and prompts are the same machinery with different search
   paths, and one path keeps the `RenderExtended` trick shared. (The loader
   is bespoke regardless — Engine seams.)*
3. **Should views and prompts share a search path / namespace?** *Lean: no
   for v1 — keep the roots distinct; revisit if a real shared fragment
   appears.*
4. **Prompt render env** — today prompt rendering passes only
   `expr.Env{Args:…}` (`oracle_ask.go:117`); `{{ world.* }}` is unavailable
   unless threaded through `args`. Do extension blocks need `world`? *Lean:
   out of scope — keep Args-only so this change is purely about composition,
   not the context surface. This is the one deferred adjacent change; nothing
   here blocks adding `world`/`slots` later.*

## Non-goals

- A **cross-story** prompt library beyond `@shared` within one app's declared
  dirs — no global registry in v1.
- Migrating every story to carve extension points — only `bugfix`/`prd` as
  references; the rest opt in when a project needs them.
- Any change to view rendering — views already have this; this proposal
  brings prompts to parity, it doesn't touch the view path.

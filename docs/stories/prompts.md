# Prompt extension — specialize a story without forking it

A story's prompts (`prompts/*.md`, rendered through pongo2 when an agent
effect invokes them) are where its generic logic meets a specific project's
reality: coding standards, repo layout, domain vocabulary, review rubric,
house tone. **Prompt extension** is how a *project* injects that detail
without copying and editing the story — the supported, primary way to
specialize a generic story.

Prompts render through a per-app pongo2 `TemplateSet` with an ordered
**overlay → story** search path, exactly the machinery views already use. So
a prompt can `{% extends %}` / `{% include %}` other prompts, and a project
can drop an *overlay* that extends a story's base prompts and fills named
`{% block %}` extension points — the story stays untouched and every upstream
prompt improvement keeps flowing.

> A story with no overlay and no blocks renders byte-identically to the
> pre-extension path. Prompt extension is default-on but inert until you use
> it.

## The search path

A prompt reference is resolved across an ordered path:

| Reference | Resolves to | Use |
|---|---|---|
| `prompts/x.md` (bare) | overlay first, then story | the normal effect `prompt:` value; an overlay file of the same name shadows the base |
| `@story/prompts/x.md` | the story's own prompt root, **bypassing the overlay** | an overlay file extends the base it shadows |
| `@shared/frag.md` | the declared `shared:` dirs | a fragment reused across a story's own prompts |

Story prompt references keep their existing `prompts/…` form and resolve
relative to the story's base dir (the directory holding `app.yaml`). The
overlay mirrors that layout.

## Two kinds of extension point — the `spec_` convention

An author marks the sections a project is expected to specialize with a
`{% block %}` whose name carries a **`spec_` prefix**. That prefix is the one
machine-readable signal that *this section's default is provisional*:

- **Hole** — an empty default body; the story expects a project to fill it:

  ```pongo
  {% block spec_project_context %}{% endblock %}
  ```

- **Provisional default** — a working body the story ships so it runs
  standalone, but one the author flagged as generic and likely to need
  project-specific specialization:

  ```pongo
  {% block spec_diagnosis_rubric %}
  Default rubric: reproduce, isolate, fix, verify.
  {% endblock %}
  ```

Non-`spec_` blocks are **structural** — they organize the prompt and are not
specialization targets. List a story's specialization surface with:

```bash
kitsoki prompts spec stories/bugfix/app.yaml
```

```
Specialization surface for Bug-fix pipeline (2 block(s)):

prompts/reproducing_executing.md
  HOLE     spec_project_context         (empty — project must fill)
  DEFAULT  spec_repro_conventions       - Do not fabricate evidence. …
```

## Specializing a story (the overlay)

A project supplies an **overlay directory** that mirrors the story's prompt
layout. Each overlay prompt extends the base it shadows and overrides the
`spec_` blocks it cares about:

```pongo
{# overlay/prompts/reproducing_executing.md #}
{% extends "@story/prompts/reproducing_executing.md" %}

{% block spec_project_context %}
This is the Acme monorepo. Tests live in `*_test.go` beside the code; run a
single package with `go test ./path/...`. Never touch `vendor/`.
{% endblock %}
```

The base's structural text and any `spec_` block the overlay leaves alone are
inherited unchanged. The effect's `prompt:` value never changes — the bare
name resolves overlay-first, so the overlay is picked up automatically.

Point a run at an overlay:

```bash
kitsoki run stories/bugfix/app.yaml --prompt-overlay ./acme-prompts
```

or declare a default in the story's `app.yaml` (see below). A run-time
`--prompt-overlay` wins over a declared default.

**Preview before you run.** To see the exact text the LLM will receive —
post-`{% extends %}`, overlay applied — without running the story or making any
LLM call:

```bash
kitsoki prompts render stories/bugfix/app.yaml prompts/reproducing_executing.md \
  --prompt-overlay ./acme-prompts \
  --arg ticket_id=ACME-1 --arg ticket_title="login fails"
```

It prints the assembled prompt and warns if the overlay overrides a block the
base doesn't declare — the typo that would otherwise silently do nothing.

## `prompts:` app config

```yaml
prompts:
  shared:                       # dirs holding @shared/<path> fragments
    - prompts/_shared
  overlay: ../acme-prompts      # optional default overlay (usually supplied at run)
```

All paths are relative to the app's base dir unless absolute. A nil/absent
`prompts:` block means story-only — the inert, byte-identical default.

## Extending an imported story's prompts

When you `import:` another story, you can extend *its* prompts the same way —
without the wholesale `overrides.prompts` swap. A parent override prompt
references the imported child's base via the `@import/<alias>/<path>`
namespace:

```pongo
{# parent override, swapped in via imports.<alias>.overrides.prompts #}
{% extends "@import/frontier/prompts/scout_brief.md" %}
{% block spec_scout_report %}…trail-flavored scout report…{% endblock %}
```

`<alias>` is the import alias from the parent's `imports:` block. The
whole-file swap (`overrides.prompts`) still works and is the right tool when
you mean to replace a child prompt outright; `@import` extend is for
specializing one block while inheriting the rest. Worked example:
`stories/oregon-trail/prompts/scout_brief_trail.md`. (Today this covers a
parent's immediate imports.)

## Trace provenance

Prompt assembly is deterministic — no new interpretive decision is recorded.
The agent-call event already captures the rendered prompt bytes (post-extends,
post-include) the LLM saw. When an overlay is in effect, the `ask` / `decide` /
`task` events also record:

- `prompt_overlay` — the contributing overlay directory, so a run that used an
  overlay is reconstructable;
- `spec_overridden` / `spec_defaulted` — which of the base's `spec_` blocks the
  overlay overrode vs. left at their provisional default on that render. This
  turns "this provisional default was never specialized here" into a labeled,
  queryable datapoint the improvement loop can rank.

## Embedding reference material — the `reference` filter

`{% include %}` pulls in another *template* and renders it. To embed an external
*document* as evidence — a spec, a report, a coding standard the LLM should read
and cite — use the built-in **`reference` filter**. It takes the content as its
input and a source label as its parameter, line-numbers the content, and wraps it
in an attribution block:

```pongo
{{ args.spec | reference:"api-spec.md" }}
```

renders to:

```
<reference src="api-spec.md" lines="1-87" sha256="9f2a1c4b">
 1 | # API spec
 2 |
 3 | Every request carries a bearer token in `Authorization`.
 …
87 | // end
</reference>
```

Why a filter, not a tag with a path:

- **It resolves nothing.** The content is a value you already have — a literal, a
  `{{ args.x }}` / `{{ world.x }}`, or the bytes of a recorded host file-read.
  Because it reads no file and uses no `@story`/`@shared` search path, it works in
  *every* render context — the inline path (CLI one-shots, meta / off-path agents)
  as well as the overlay/`AppRenderer` path — under any agent backend.
- **The body is verbatim.** Unlike `{% include %}`, the filter never re-parses its
  input as a template, so a document containing `{{ … }}` / `{% … %}` is embedded
  untouched.
- **Line numbers are absolute when you pass a whole file.** They start at line 1
  of the content given, right-aligned to the width of the last line, so a report
  that cites "`api-spec.md` line 54" lands on the right text. Don't pass a slice
  unless your citations are relative to it.
- **The attribution is traceable.** The `src` label and `sha256` (first 8 hex of
  the content digest) ride in the rendered prompt bytes the agent-call event
  already records — so a reader of the trace can recover what the model saw and
  confirm it against the source. When the content came from a host file-read, that
  read is its own recorded host call, and the hash ties the two together.

A nil / missing input degrades to a no-op (no empty block), so a typo'd variable
fails visibly empty rather than wrapping a `<reference>` around nothing.

## Recipes

- **Add project context to a story** — overlay a prompt, `{% extends "@story/…" %}`,
  fill the `spec_project_context` hole. Run with `--prompt-overlay`.
- **Embed a document as cited evidence** — bring its bytes into a `{{ args.x }}`
  (a host file-read or a passed arg) and `{{ args.x | reference:"<source>" }}` to
  inline it line-numbered with traceable attribution.
- **Share a rubric fragment across prompts** — put it under a `shared:` dir,
  `{% include "@shared/rubric.md" %}` from each prompt that needs it.
- **Override one block, inherit the rest** — overlay extends the base and
  overrides a single `spec_` block; everything else falls through.
- **Swap a whole prompt (legacy)** — in an import, `overrides.prompts` still
  replaces a child prompt wholesale (see [`imports.md`](imports.md)); reach for
  it only when you mean to replace, not extend.

## For meta / improvement agents

The `story-author` agent (`/meta story edit`) is told that `spec_*` defaults
are provisional and that the fix for a project-specific gap is to **specialize
the block in an overlay, not edit the story's base prompt**. This keeps
self-improvement targeted at the seams the author marked rather than baking one
project's specifics into shared logic.

## Authoring a specialization surface

When you write a story prompt, mark the sections a project will plausibly need
to change:

1. Make project-specific context a **hole** (`{% block spec_<name> %}{% endblock %}`).
2. Make a generic-but-likely-to-change section a **provisional default**
   (`spec_` block with a working body).
3. Leave structural scaffolding as plain text or non-`spec_` blocks.
4. Verify the surface with `kitsoki prompts spec`.

A prompt with no `spec_` blocks is still valid — it simply exposes no
specialization surface and behaves exactly as before.

## Long-running agent handoffs: report first, return a pointer

An agent that edits code, runs a repair loop, authors a multi-file artifact, or
otherwise spends a long time in a mutable workspace must not be asked to put
its full narrative into a required field in its final structured response. That
last response is the least reliable point to require a large blob: it is easy
to truncate, loses useful evidence if the agent dies, and makes the handoff
hard to inspect while work is still in progress.

Use this contract instead:

1. Tell the agent its report location before it begins, under the run's
   `.artifacts/<story-or-run>/` directory.
2. Tell it to create the report after the first useful diagnosis and update it
   as it edits, tests, or encounters a blocker — not just at final submission.
3. Have the final schema carry a `report_path` (required for new write-capable
   flows) plus small typed facts such as status, changed files, commands, and
   blockers. Keep any `summary_markdown` to a one- or two-sentence checkpoint
   note; it must never be the only durable report.
4. Pass the path to downstream agents and show it in the checkpoint view. A
   reviewer can then read the report or diff even if the final agent response
   is short or fails after the work is complete.

For a short, read-only classification or a lightweight yes/no judge, an inline
summary is still appropriate. The rule applies when the agent's work itself is
long or mutable. Add a no-LLM flow fixture that returns the path and proves the
checkpoint exposes it.

## Load-time validation

`kitsoki run` resolves and parses every story prompt's `{% extends %}` /
`{% include %}` / `@import` references at startup and aborts with a located
message if one is missing, malformed, an unknown `@import` alias, or a
self-reference — so a broken extension fails fast at load rather than only when
that agent effect first fires. It also rejects an overlay that **overrides a
block no base in its `{% extends %}` chain declares** (a typo'd or renamed
block name) — pongo2 would otherwise ignore it, silently dropping the
specialization. Examples:

```
Error: prompt extension: 1 invalid prompt reference(s):
  - prompt "prompts/diagnose.md": ... @story/prompts/nope.md unable to resolve template
```

Templated prompt paths (those built from `{{ … }}`, resolved per turn) are
skipped — they can't be resolved statically.

## Backward compatibility

- A story with no overlay and no blocks renders byte-identically to the
  pre-extension `render.Pongo` path; existing cassettes are unaffected.
- `imports.overrides.prompts` keeps working as a whole-file swap.
- Adopting prompt extension is opt-in per story — it happens only once an
  author carves `spec_` blocks. Existing stories keep working untouched.

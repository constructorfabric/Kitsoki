# Stories

Everything you need to write a kitsoki story — the YAML state machines
under `../../stories/<name>/`. *Audience: story authors.*

A story is a directed cyclic graph of **rooms** (and **phases** that
template repeated rooms), each room a **state** with **intents**,
**slots**, **guards**, **transitions**, and **effects**, all operating
over a typed **world**. The runtime drives the graph; the LLM is called
only for narrow interpretive sub-tasks. (For *why* it is built this
way, see [`../architecture/concept.md`](../architecture/concept.md).)

---

## The model

- **[`architecture.md`](architecture.md)** — the front door: a single
  end-to-end walk through rooms, phases, intents, turns, room hooks,
  views, and how the agent plugs into intent routing and the agent
  rooms (`/meta`, off-path). Start here for the whole shape, then
  follow the cross-links into the deep dives below.
- **[`state-machine.md`](state-machine.md)** — the complete vocabulary:
  rooms, phases, states, intents, slots, world, guards, transitions,
  effects, and the orchestrator's turn loop. The reference you will
  return to most.
- **[`authoring.md`](authoring.md)** — the how-to: a minimal `app.yaml`,
  the authoring loop, common patterns and mistakes, synonyms, host
  calls, and scaling a story up.
- **[`story-qa.md`](story-qa.md)** — the story QA runbook: deterministic
  flows, graph/render review, host-call contracts, failure-path coverage,
  and the `stories/cherny-loop` worked case study.
- **[`imports.md`](imports.md)** — composing apps across files and
  repos via the `imports:` block; aliased namespaces, world
  projection, host rebinding, exits, and the `/warp` operator smoke
  test.
- **[`prompts.md`](prompts.md)** — prompt extension: specialize a
  generic story's prompts for a project via an overlay that
  `{% extends %}` the base and fills `spec_` blocks, instead of
  forking the story. The `prompts:` config, `--prompt-overlay`, and
  `kitsoki prompts spec`.

## Presentation and interaction

- **[`story-style.md`](story-style.md)** — how a story should *look* and
  *read*: blocks, typed view elements, colors, action menus, narration
  voice, placeholders, and the [text-only rule](story-style.md#38-the-view-must-read-as-plain-text)
  every view must satisfy. The short guide; copy Oregon Trail when in
  doubt. (How those elements render is [`../tui/`](../tui/README.md).)
- **[`choice-widget.md`](choice-widget.md)** — the choice widget
  cookbook: single-select, multi-select, and form modes, with patterns
  drawn from production stories. (Its on-screen rendering lives in the
  [`../tui/`](../tui/README.md) section.)
- **[`meta-mode.md`](meta-mode.md)** — persistent sidebar conversations
  with named agents (story-author, kitsoki-engineer): declaring
  `agents:` and `meta_modes:`, slash commands, and how edits land in
  the story directory.

## Long-running work and operations

- **[`background-jobs/`](background-jobs/README.md)** — long-running
  handlers, inbox notifications, and mid-flight clarifications:
  [`authoring`](background-jobs/authoring.md),
  [`runtime`](background-jobs/runtime.md),
  [`testing`](background-jobs/testing.md),
  [`recipes`](background-jobs/recipes.md), and
  [`troubleshooting`](background-jobs/troubleshooting.md).
- **[`delivery-loop.md`](delivery-loop.md)** — the shipped `deliver` / `fleet` /
  `ship-it` stack for decomposing accepted work into deterministic, verified
  delivery runs.
- **[`deliver.md`](deliver.md)** — `deliver` in depth: the canonical
  decomposition story's graph, manifest contract, `dev-story` reachability,
  and per-surface (engine/web/VS Code) no-LLM proofs.
- **[`bugs.md`](bugs.md)** — filing story and kitsoki bug reports
  (`/meta story bug`, `kitsoki bug create`), the on-disk format, and
  target resolution.
- **[`product-journey-qa.md`](product-journey-qa.md)** — the universal
  persona/scenario QA campaign story: project-owned personas/scenarios, the
  `campaign_*` product verbs, the local-vs-GitHub finding-sink policy, worker
  backend receipts, and the Slidey rollup.
- **[`ci.md`](ci.md)** — story-native Capsule CI: the normalized trigger/world
  contract, typed verdict, reference rooms, least-authority writer/reviewer
  boundaries, and GitHub adapter model.

## See also

- **[`../recipes/`](../recipes/README.md)** — copy-paste starting
  points for the patterns above. Start here if you already know the
  pattern you want.
- **[`../tracing/testing.md`](../tracing/testing.md)** — how to lock
  your story's behaviour with deterministic flow tests.
- **[`story-coverage-mining.md`](story-coverage-mining.md)** — drive a
  story's tests + features from real transcripts (mine → map → author).
  Worked flagship: `tools/session-mining/examples/git-ops/`.
- **The `host.*` reference** lives under architecture:
  [`../architecture/hosts.md`](../architecture/hosts.md).
- **The Starlark experience** for deterministic glue and bounded CodeAct loops:
  [`../architecture/starlark.md`](../architecture/starlark.md).
- **The authoritative schema**: `kitsoki docs app-schema` (source at
  [`../embedded/app-schema.md`](../embedded/app-schema.md)). Use it as
  field reference after the conceptual docs, not as the first read.

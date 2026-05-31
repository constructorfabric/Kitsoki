# Kitsoki documentation

Welcome. This is the navigation hub for the documentation tree.

For the elevator pitch and quickstart, see the top-level
[`../README.md`](../README.md). If you are new to the project, read
[`architecture/concept.md`](architecture/concept.md) first — it is the
thesis the rest of the docs build on.

The tree is organised into four reference sections plus a recipes
area. Each section has its own `README.md` index.

---

## Sections

### 🏛 [`architecture/`](architecture/README.md) — the engine and its boundaries

How kitsoki works under the hood: control inversion, progressive
determinism, the package map and turn loop, persistence, and the
external-world boundaries (hosts, oracle plugins, transports, the
routing stack). Also the contributor guide. *Audience: architects and
people changing the kitsoki codebase.*

### 📖 [`stories/`](stories/README.md) — the authoring model

How to write a story: the `app.yaml` state-machine vocabulary (rooms,
phases, states, intents, slots, world, guards, transitions, effects),
the authoring loop, composition via imports, the visual/narrative
style guide, the choice widget, sidebar meta-mode agents, background
jobs, and bug filing. *Audience: story authors.*

### 🔬 [`tracing/`](tracing/README.md) — trace, test, debug, replay

The session trace is the authoritative state; everything else derives
from it. This section covers the trace format, the two test modes,
host cassettes, the `kitsoki turn` probe, and how to replay and debug
a session. *Audience: anyone testing, debugging, or developing a story.*

### 🖥 [`tui/`](tui/README.md) — the terminal UI

The single-pane chat TUI: the block rendering pipeline, typed
view-elements + pongo2, the `/command` surface, engine-event observers,
and how to write TUI rendering regression tests. *Audience: contributors
working on the UI; authors wanting to understand how their views render.*

### 🧑‍🍳 [`recipes/`](recipes/README.md) — copy-paste patterns

Short, task-oriented recipes for common authoring patterns: add an
intent, gate a destructive effect, branch on a host call, collect a
form, write a flow test, run a background job. Each links back to the
reference docs. *Audience: authors who know what they want to do and
want the shortest correct path.*

---

## Reference (embedded in the binary)

The files under [`embedded/`](embedded/) are compiled into the `kitsoki`
binary via `//go:embed` and served by `kitsoki docs <topic>`. They are
field-reference / LLM-prompt material — narrative and design rationale
live in the sections above.

| Topic | Where |
|---|---|
| Authoritative `app.yaml` schema | `kitsoki docs app-schema` (or [`embedded/app-schema.md`](embedded/app-schema.md)) |
| LLM-facing operator manual | `kitsoki docs llm-guide` (or [`embedded/llm-guide.md`](embedded/llm-guide.md)) |
| Implement a prose proposal against `app.yaml` | `kitsoki docs apply-proposal` (or [`embedded/apply-proposal.md`](embedded/apply-proposal.md)) |
| Markdown shape produced by `kitsoki render` | `kitsoki docs render-format` (or [`embedded/render-format.md`](embedded/render-format.md)) |

## Worked examples and per-story references

- [`case-studies/`](case-studies/README.md) — worked examples of
  progressive determinism applied to real workflows. Start with
  [`case-studies/bug-fix.md`](case-studies/bug-fix.md): how a
  prompt-driven agent loop became the multi-room `bugfix` pipeline.
- **Per-story READMEs** — each story under `../stories/` ships its own
  authoritative reference. Notable ones:
  [`../stories/kitsoki-dev/README.md`](../stories/kitsoki-dev/README.md)
  (dogfood operator walkthrough),
  [`../stories/bugfix/README.md`](../stories/bugfix/README.md)
  (the bugfix pipeline),
  [`../stories/pr-refinement/README.md`](../stories/pr-refinement/README.md),
  [`../stories/docs-review/README.md`](../stories/docs-review/README.md)
  (the meta-story that audits these docs against the code at HEAD).

## Historical material

- [`proposals/`](proposals/README.md) — proposal documents in design or
  partially shipped; kept for design context. A shipped proposal keeps
  its design discussion here while the user-facing reference moves into
  one of the sections above (e.g. the semantic-routing proposal's
  reference now lives at
  [`architecture/semantic-routing.md`](architecture/semantic-routing.md)).
- [`competitive-analysis/`](competitive-analysis/README.md),
  [`reviews/`](reviews/), and [`features/mvp.md`](features/mvp.md) —
  research, code-review records, and the MVP scope list.

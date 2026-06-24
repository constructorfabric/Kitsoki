# Start Here

Kitsoki has several kinds of documentation because different readers need
different paths. Use this page to pick the shortest useful route.

## Pick Your Path

| You are | Read in order |
|---|---|
| Evaluating the idea | [`../README.md`](../README.md) -> [`architecture/concept.md`](architecture/concept.md) -> [`case-studies/bug-fix.md`](case-studies/bug-fix.md) |
| Running kitsoki locally | [`../README.md`](../README.md#quickstart) -> [`getting-started.md`](getting-started.md) -> [`tracing/testing.md`](tracing/testing.md) |
| Writing a story | [`stories/architecture.md`](stories/architecture.md) -> [`recipes/`](recipes/README.md) -> [`stories/authoring.md`](stories/authoring.md) -> [`embedded/app-schema.md`](embedded/app-schema.md) |
| Contributing to kitsoki | [`../CONTRIBUTING.md`](../CONTRIBUTING.md) -> [`architecture/overview.md`](architecture/overview.md) -> [`architecture/developer-guide.md`](architecture/developer-guide.md) |
| Debugging a session | [`tracing/README.md`](tracing/README.md) -> [`tracing/testing.md`](tracing/testing.md) -> [`tracing/trace-format.md`](tracing/trace-format.md) |
| Working on UI | [`web/README.md`](web/README.md) or [`tui/README.md`](tui/README.md) -> [`tui/rendering-tests.md`](tui/rendering-tests.md) |

## What Not To Start With

- `docs/embedded/` is authoritative reference material embedded in the binary.
  Use it when you need exact field shapes, not as a first read.
- `docs/proposals/` is design work in progress or deferred context. It is useful
  when implementing a proposal, but it is not the product manual.
- Per-story READMEs under `stories/` are the best references for those specific
  stories after you understand the general story model.

## Fast Mental Model

The runtime owns the workflow graph. The LLM is called only at named,
traceable decision points: routing a turn, extracting structured data, or doing
bounded agent work. The trace records the result so the session can be replayed,
tested, and audited.

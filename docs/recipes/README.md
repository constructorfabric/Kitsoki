# Recipes

Short, task-oriented patterns for things you do all the time when
authoring a kitsoki story. Each recipe is a minimal correct snippet
plus a pointer to the reference doc that owns the full contract.

These are starting points, not specifications. When a field name or
edge case matters, the authoritative sources are the schema
(`kitsoki docs app-schema`) and the linked reference under
[`../stories/`](../stories/README.md), [`../architecture/`](../architecture/README.md),
and [`../tracing/`](../tracing/README.md).

---

| Recipe | You want to… |
|---|---|
| [Add an intent](add-an-intent.md) | Recognise a user action and move between rooms |
| [Confirmation gate](confirmation-gate.md) | Make the user confirm before an irreversible effect |
| [Host call & branch](host-call-and-branch.md) | Run a `host.*` call and handle success vs failure |
| [Collect a form](choice-form.md) | Gather several typed fields in one submission |
| [Flow test + cassette](flow-test-with-cassette.md) | Lock behaviour with a deterministic test |
| [Background job](background-job.md) | Run long work off the turn and notify on completion |
| [Studio MCP async smoke](studio-mcp-async-smoke.md) | Prove background completion, inbox teleport, and chat-work reacquisition over studio MCP |
| [Studio MCP GitHub inbox smoke](studio-mcp-github-inbox-smoke.md) | Prove GitHub issue/PR intake and reacquisition over studio MCP |
| [Studio MCP dogfood](studio-mcp-dogfood.md) | Drive Kitsoki through its own MCP surface from Claude, Codex, or another agent |
| [Repo history training for a new repo](repo-history-training-new-repo.md) | Turn historical bug fixes into deterministic oracles, readiness reports, and live-cell commands |
| [Repo history training with gears-rust](repo-history-training-gears-rust.md) | Use the private/heavy Rust reference path for the external bake-off harness |
| [Project prompt overlay](prompt-overlay-example/README.md) | Specialize a generic story's prompts for a project without forking it |

## Larger worked examples

For end-to-end examples rather than single patterns, see the
[case studies](../case-studies/README.md) — start with
[`bug-fix.md`](../case-studies/bug-fix.md), which traces how a
prompt-driven agent loop became a multi-room deterministic pipeline —
and the per-story READMEs under [`../../stories/`](../../stories/).
The background-jobs section has its own deeper
[recipes page](../stories/background-jobs/recipes.md).

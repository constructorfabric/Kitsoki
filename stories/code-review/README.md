# code-review — review a teammate's open PR

The code-review pipeline from the [bug-fix case
study](../../docs/case-studies/bug-fix.md). The **reviewing-a-teammate**
flavor — not a self-loop on
your own PR. Triggered from the dev-story inbox when an external
"PR awaiting your review" notification arrives.

```
idle → list_pending → review_pr → comment → decide → @exit:reviewed
```

Each `_awaiting_reply` follows the canonical §6 checkpoint shape:
post + inbox + LLM-judge + emit_intent. The `decide` room's `accept`
arc is polymorphic — it reads `world.decision_artifact.decision` to
decide whether to approve or request changes, so the LLM-judge
auto-fire path works without forking the state graph.

Reviewed exits also write `.artifacts/code-review/<pr-id>/report.md`,
`summary.json`, and `deck.slidey.json` from the structured review and
decision artifacts. The deck is deterministic; LLM output only enters through
the schema-validated checkpoint artifacts.

## Standalone

```
kitsoki run stories/code-review/app.yaml --warp scenarios/pr_142.yaml
```

## Imported

Parent stories (`stories/dev-story/`) import code-review behind the
`rev` alias. The dev-story `inbox.yaml` room's `pick_review` intent
projects a notification's `pr_id` / `pr_title` / `pr_author` into the
import's `world_in:` and enters at `entry: idle`.

## Exits

| Name | Description | `requires:` keys |
|---|---|---|
| `reviewed` | Final review posted (approve / request_changes) and deterministic report/deck written. | `decision_artifact` |
| `dismissed` | User explicitly dismissed (e.g. wrong reviewer). | — |
| `abandoned` | User or LLM bailed. | — |

## Visible rooms

| Room | Substates | Checkpoint? | On `accept` |
|---|---|---|---|
| `idle` | one atomic | n/a | `list_pending` (via `start`) |
| `list_pending` | one atomic | n/a | `review_pr_executing` (via `pick_pr` / `proceed`) |
| `review_pr` | `_executing`, `_awaiting_reply` | yes — `review_summary_artifact` | `comment_executing` |
| `comment` | `_executing` only | no | `decide_executing` (via `proceed`) |
| `decide` | `_executing`, `_awaiting_reply` | yes — `decision_artifact` | `@exit:reviewed` (via `approve` / `request_changes`) |

## Report Artifacts

| Key | Meaning |
|---|---|
| `report_path` | Human-readable reviewed report. |
| `summary_path` | Structured input used by the deterministic deck renderer. |
| `deck_path` | Standardized Slidey code-review status deck. |

## Intents

| Intent | Slots | Description |
|---|---|---|
| `start` | — | Boot from idle to list_pending. |
| `proceed` | — | Advance the current room. |
| `pick_pr` | `id` | Select a PR from the pending list. |
| `accept` | — | Accept a checkpoint artifact; advance. |
| `refine` | (opt) `feedback` | Re-execute the current room. |
| `approve` | — | Final decision: approve. |
| `request_changes` | — | Final decision: request changes. |
| `dismiss` | — | Dismiss the review request (e.g. wrong reviewer). |
| `quit` | — | Abandon the review. |

## See also

- [`docs/case-studies/bug-fix.md`](../../docs/case-studies/bug-fix.md)
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
- [`stories/implementation/`](../implementation/) — the implementation
  pipeline that handoffs to `pr-refinement`.
- [`stories/pr-refinement/`](../pr-refinement/) — author-side PR
  refinement (NOT code-review; this is what you do for YOUR PR).

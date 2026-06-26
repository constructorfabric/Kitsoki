# pr-refinement — first-class PR refinement pipeline

A reusable kitsoki story implementing the PR-refinement pipeline from
the [bug-fix case study](../../docs/case-studies/bug-fix.md). Per that
design, pr-refinement is **its own story** — not a library, not an
`include:` directory. It has its own `app.yaml`, its own rooms, its
own flows, and runs standalone.

Standalone:

```
kitsoki run stories/pr-refinement/app.yaml
```

Imported (see Wave 2's `stories/dev-story/app.yaml` or
`stories/bugfix/app.yaml` once it imports the tail in a follow-up
commit).

## Contract

### Entry state

`open_pr` — the operator (or the importer's `entry:` setting) starts
the pipeline by opening (or recognising) the PR. Set on import via
`entry: open_pr`.

### Exits

| Name | Description | `requires:` keys | Typical world_out |
|---|---|---|---|
| `merged` | PR landed. Pipeline succeeded. | `pr_url` | Parent stories project `pr_url` into their own `last_pr_url` / set `status: merged`. |
| `abandoned` | User or LLM bailed (`quit`). | (none) | Parent stories usually route to a `main` / inbox state. |
| `pushback_resolved` | Review pushback addressed; parent may resume monitoring or treat as merged. | (none) | Reserved for Wave 3 — Wave 2's flows do not use it. |

Standalone (no parent) load synthesises `__exit__merged`,
`__exit__abandoned`, and `__exit__pushback_resolved` terminals so
`kitsoki run` and `kitsoki test flows` both terminate cleanly.

### Visible rooms

| Room | Substates | Checkpoint? | On `accept` / `proceed` |
|---|---|---|---|
| `open_pr` | one atomic | no | `ci_monitoring` (via `proceed`) |
| `ci_monitoring` | one atomic (with `on_enter` poll) | no | branches on `world.ci_state`: success → `merge_executing` (or `resolve_comments` if `pending_comments > 0`); failure → `diagnose_executing` |
| `diagnose` | `_executing`, `_awaiting_reply` | yes — `diagnose_artifact` | `re_push` |
| `resolve_comments` | one atomic | no | `re_push` (via `resolve`) or `ci_monitoring` (via `proceed`) |
| `re_push` | one atomic | no | `ci_monitoring` (via `proceed`) |
| `merge` | `_executing`, `_awaiting_reply` | yes — judge_verdict only | `@exit:merged` |

### `world_in:` keys (parent → child)

The importer projects these from its own world. All have type+default
in `app.yaml`'s `world:` block so the child loads standalone for tests.

| Key | Type | Used by | Default |
|---|---|---|---|
| `ticket_id` | string | Every post title and `phase_id:`. | `""` |
| `ticket_title` | string | Views / status posts. | `""` |
| `thread` | string | The transport's thread identifier. | `""` |
| `workspace_id` | string | Reserved. | `""` |
| `workdir` | string | `iface.vcs.{commit,push,open_pr,diff}` arg. | `""` |
| `base_branch` | string | `iface.vcs.open_pr.base`. | `"main"` |
| `feature_branch` | string | `iface.vcs.push.remote`-target. | `""` |
| `pr_id` | string | Pre-seeded for `--warp` scenarios; otherwise bound by `open_pr`. | `""` |
| `pr_url` | string | Pre-seeded for `--warp` scenarios. | `""` |
| `pr_title` | string | `iface.vcs.open_pr.title`. | `""` |
| `pr_body` | string | `iface.vcs.open_pr.body`. | `""` |
| `judge_mode` | string | `human` \| `llm` \| `llm_then_human` — see Judge polymorphism below. | `"human"` |
| `judge_confidence_threshold` | float | Floor for auto-firing the LLM's verdict. | `0.8` |
| `merge_strategy` | string | `squash` \| `merge` \| `rebase`. | `"squash"` |
| `ci_poll_budget` | int | Max ci_monitoring loops before the operator must intervene. | `5` |

### `world_out:` keys (child → parent on exit)

| Key | Type | Description |
|---|---|---|
| `pr_url` | string | Required by `@exit:merged`. Bound by `open_pr.on_enter` from `iface.vcs.open_pr`. |
| `pr_id` | string | Bound by `open_pr.on_enter`. |
| `merge_sha` | string | The merged commit's SHA. Bound by `merge_executing.on_enter` from `iface.vcs.merge`. |
| `status` | string | `"merged"` after `@exit:merged`; `"open"` on `@exit:abandoned`. |
| `cycle` | int | Total diagnose / refine cycles consumed. |
| `diagnose_artifact` | object | Last CI-failure diagnosis. |
| `report_path` | string | Markdown PR close-out report under `.artifacts/pr-refinement/<run>/report.md`. |
| `summary_path` | string | Structured report data consumed by the deterministic Slidey deck generator. |
| `deck_path` | string | Deterministic Slidey report deck under `.artifacts/pr-refinement/<run>/deck.slidey.json`. |

On merge close-out, `merge_awaiting_reply` invokes
`stories/pr-refinement/scripts/pr_report.py`. The script consumes only
typed story world fields and schema-validated artifacts, writes review
artifacts under `.artifacts/pr-refinement/<run>/`, and builds the
Slidey deck through `tools/report-deck/deterministic_deck.py`; no LLM
is asked to draft the deck.

### Intent surface

| Intent | Slots | Description |
|---|---|---|
| `open` | — | Stay in `open_pr` (re-runs the `open_pr` on_enter). |
| `monitor` | — | Re-poll CI from `ci_monitoring`. |
| `proceed` | — | Advance from a non-checkpoint room. |
| `retry` | — | Re-run / re-push after a failure. |
| `resolve` | — | Post a follow-up comment and re_push (from `resolve_comments`). |
| `merge_now` | — | Skip ci_monitoring's poll loop; jump straight to `merge_executing`. |
| `accept` | (opt) `author`, `feedback` | Accept a checkpoint artifact. |
| `refine` | (opt) `feedback` | Re-execute the current `_executing` room. |
| `quit` | — | Bail; exits via `@exit:abandoned`. |
| `look` | — | Re-render the current view. |

### `host_interfaces:` contract

Four capability surfaces — same as bugfix minus `workspace` (the
working tree is already established when pr-refinement enters).

| Iface | Ops | Default binding |
|---|---|---|
| `ticket` | `search`, `get`, `comment`, `transition`, `list_mine` | `host.local_files.ticket` |
| `vcs` | `branch`, `diff`, `commit`, `push`, `open_pr`, `pr_status`, `pr_comment`, `merge` | `host.git` |
| `ci` | `run_tests`, `build`, `remote_status` | `host.local` |
| `transport` | `post` | `host.append_to_file` |

Note: `vcs.merge` is new in Wave 2 (not in the original contract §2.2).
See the "Wave 2 contract additions" appendix at the end of
`docs/proposals/notes/dev-story-implementation-contract.md`.

### Host requirements

Standalone Wave 2 needs every iface's default handler plus
`host.inbox.add` and the agent verb handlers below. The flow fixtures
stub them all with canned envelopes; Slice β shipped most of these in
Wave 1.

| Handler | Status | File |
|---|---|---|
| `host.local_files.ticket` | Wave 1 | `internal/host/localfiles_ticket.go` |
| `host.git` (prefix-fallback for `vcs.*` including `merge`) | Wave 1 | `internal/host/git_vcs.go` |
| `host.local` | Wave 1 | `internal/host/local_ci.go` |
| `host.append_to_file` | Wave 1 | `internal/host/append_file_transport.go` |
| `host.inbox.add` | Wave 1 | `internal/host/inbox_add.go` |
| `host.agent.decide` | agent-split Phase 8 | `internal/host/agent_decide.go` |

The host registry's prefix-fallback lets each default handler back
every op on the iface; per-op handlers can be added later without
touching the YAML. The `vcs.merge` op uses the same prefix-fallback —
the bare `host.git` handler is invoked for `host.git.merge` calls in
flow fixtures (the handler's `data:` envelope just returns the
canned `sha`).

### Agent-split persona table (Phase 8)

This story's agent calls are all verdict-producing judgments. The
`diagnoser` persona additionally does structured CI failure analysis
(still classified `decide` because the output is a JSON artifact schema
with no file writes — see agent-split proposal §6 table).

| Persona | Verb | Phases |
|---|---|---|
| `diagnoser` | `decide` | `diagnose_executing` CI-failure analysis |
| `judge` | `decide` | `diagnose_awaiting_reply` and `merge_awaiting_reply` checkpoint verdicts |

Both personas carry `tools: []` — they evaluate context provided in the
prompt, no file access needed.

## Judge polymorphism

Identical to bugfix. Two checkpoints (`diagnose_awaiting_reply` and
`merge_awaiting_reply`) follow the canonical contract §6 shape, each
calling `host.agent.decide` (agent: `judge`) when `judge_mode != 'human'`.

| Mode | Behaviour at every checkpoint |
|---|---|
| `human` | Post + inbox-mirror; wait for an explicit reply intent. |
| `llm` | Post + inbox-mirror + `host.agent.decide`. Confident verdict auto-fires via `emit_intent:`. |
| `llm_then_human` | Same as `llm` for the auto-fire path; uncertain or low-confidence verdicts hold the state for a human. |

## File layout

```
stories/pr-refinement/
  app.yaml                                — manifest (this story's loadable surface)
  README.md                               — this file
  rooms/
    open_pr.yaml                          — opens PR via iface.vcs.open_pr
    ci_monitoring.yaml                    — polls iface.vcs.pr_status; branches on ci_state
    diagnose.yaml                         — _executing + _awaiting_reply (LLM-judge)
    resolve_comments.yaml                 — round-trip review comments
    re_push.yaml                          — commit + push fix
    merge.yaml                            — _executing + _awaiting_reply (final merge)
  prompts/
    diagnose_executing.md                 — artifact-producing
    judge_diagnose.md                     — LLM-judge for diagnose_awaiting_reply
    judge_merge.md                        — LLM-judge for merge_awaiting_reply
  schemas/
    judge_verdict.json                    — { verdict, intent, reason, confidence }
    diagnose_artifact.json                — root cause + fix description
  flows/                                  — deterministic flow fixtures (host stubs only)
    happy_human.yaml                      — open → CI green → merge (human)
    ci_fails_diagnose.yaml                — open → CI fails → diagnose → re_push → merge
    comments_round_trip.yaml              — open → CI green → comments → resolve → re_push → merge
    happy_llm_then_human.yaml             — happy path with LLM diagnosing
```

## See also

- [`docs/case-studies/bug-fix.md`](../../docs/case-studies/bug-fix.md)
  — the full design (pr-refinement as first-class story).
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
  — the contract (Wave 2 appendix at the bottom).
- [`docs/stories/imports.md`](../../docs/stories/imports.md) — imports authoring
  reference.
- [`stories/bugfix/`](../bugfix/) — the upstream story that hands off
  here.

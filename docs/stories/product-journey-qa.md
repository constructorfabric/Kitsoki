# `product-journey-qa` — the universal product QA campaign

`stories/product-journey-qa` is the reusable, project-agnostic campaign
surface for persona/scenario QA: any Kitsoki user can define personas and
free-form scenarios for their own project, launch Codex/Kitsoki agents
through Studio MCP, capture web/TUI/docs/product-site evidence, file credible
findings through the repo-wide bug-filing pipeline, optionally send bounded
work to a local/arena/VM worker, and keep a Slidey rollup current from
retained artifacts. *Audience: operators running a standing QA campaign on
their own project, and story authors extending the campaign surface.*

It wraps the deterministic `tools/product-journey/run.py` runner — no-LLM by
construction: every `host.run` call is a subprocess into the runner or the
native `kitsoki bug` / `kitsoki gitops` CLIs, so flow fixtures stub the
runner's stdout instead of mocking an LLM. The full intent-by-intent
reference (every `campaign_*` and underlying verb, its slots, and what it
binds) lives in
[`stories/product-journey-qa/README.md`](../../stories/product-journey-qa/README.md)
— keep that as the source of truth; this page is the narrative overview.
For the day-to-day "how do I run this on my project" walkthrough, see
[`../guide/development/agentic-qa-campaigns.md`](../guide/development/agentic-qa-campaigns.md).

## Catalog: personas and scenarios are project-owned data

`tools/product-journey/personas.json` and `tools/product-journey/scenarios.json`
are the source of truth for who is testing (persona: starting surface, first
question, evidence emphasis, escalation trigger, finding bias) and what they
are testing (scenario: transport axis, capture routes, quality gate). They
are not a Kitsoki-only fixture list — a project registers its own personas
and scenarios the same way, and `tools/product-journey/README.md` documents
the catalog schema and the standing-campaign guidance for adding coverage
without overfitting to one project. `campaign_scenarios` (a story world
default) names the universal set a fresh campaign plans against:
`docs-to-mcp-first-run`, `agent-launch-experience`, `remote-worker-campaign`,
and `campaign-rollup-review`.

## The `campaign_*` product verbs

The story exports narrow `campaign_*` intents as thin, product-facing wrappers
over the existing runner/story gates, so an operator has one vocabulary
instead of pasting runner commands together:

| Verb | Wraps | Purpose |
|---|---|---|
| `campaign_plan` | `start` | Plan a run from the shared persona/scenario catalog |
| `campaign_attach` | `attach` | Attach captured evidence and refresh the deck/summary |
| `campaign_worker` | (native) | Record/import a local, arena, or VM worker readiness receipt |
| `campaign_issue_fix` | `file_local_findings` / `autonomous_fix` | File findings per the declared finding-sink policy (below) |
| `campaign_stats` | `stats` | Filed/fixed/reopened/similar issue statistics |
| `campaign_deck_refresh` | `review` | Regenerate the Slidey rollup and readiness summary |
| `campaign_tick` | (native) | Advance the next due standing-campaign control artifact |

Each verb keeps the full underlying gate reachable directly (`start`,
`attach`, `file_findings`, `autonomous_fix`, `stats`, `review`) for diagnostics
and for callers that need the non-campaign shape.

## Finding-sink policy: local by default, GitHub by explicit opt-in

`campaign_issue_fix` is gated by a declared `finding_sink` policy —
`local-artifact` (the default; no slots required) or `github` (explicit
opt-in). This is the repo-wide local-developer-bug-loop convention (see
`AGENTS.md` and [`bugs.md`](bugs.md)) applied to campaign findings:

- **`local-artifact` (default).** `tools/product-journey/run.py
  --file-local-findings` files every credible `issue` finding as a local
  ticket via `kitsoki bug create --sink local-artifact --target kitsoki`,
  recording the returned path as `item.local_ticket` in `findings.json`. No
  GitHub filing or gh-agent repair is touched. The ticket format is the same
  markdown-plus-frontmatter documented in [`bugs.md`](bugs.md); review the
  pile with `kitsoki bug list --sink local-artifact --target kitsoki`.
- **`github` (explicit opt-in).** `campaign_issue_fix
  finding_sink=github ticket_repo=owner/repo
  gh_agent_public_base_url=<url>` delegates to `autonomous_fix`: evidence-backed
  GitHub filing, gh-agent repair, independent verification, and issue
  close-out — the full loop documented in
  [`stories/product-journey-qa/README.md`](../../stories/product-journey-qa/README.md).

Both sinks satisfy the `findings-filed` review/validate gate for the findings
they resolve. The GitHub-only gate chain (gh-agent drain, fix/triage/verify
evidence, run URLs, integration landing, issue close-out, autonomous-fix
report) is scoped to credible findings still unresolved by the local sink, so
a campaign run that only ever used the default local sink is never forced
through GitHub filing/fixing it never requested — see
`tools/product-journey/run.py`'s `credible_findings_requiring_github` and
`tools/product-journey/local_findings_test.py` for the exact contract.

Use the GitHub sink when the finding itself is a remote-collaboration
handoff boundary: a hosted gh-agent repair job, or a finding meant for
project maintainers outside the local dogfood loop. Keep the local sink for
iterative campaign stabilization findings about your own project.

## Worker backend

`campaign_worker` records a readiness receipt (`campaign-worker-receipt.json`
/ `.md`) naming the backend (`local`, `arena`, or `vm`), worker id, scenario
scope, budget, and readiness status, and imports artifacts from a completed
worker run into the campaign bundle. This is the stable seam a real VM/arena
dispatcher plugs into without changing the campaign run/deck contract — a
blocked or unready receipt stays visible and conservative rather than
implying evidence capture happened. See
`tools/product-journey/campaign_worker_test.py` for the no-LLM contract
proof.

## Evidence, review, and the Slidey rollup

Campaign runs reuse the full product-journey evidence pipeline: attach
evidence, record findings/blockers, `review` scores readiness (fails closed
on missing evidence, unrouted weakness findings, or unresolved credible
issues), `validate` catches stale/inconsistent bundles without rewriting
them, and `campaign_deck_refresh` regenerates `deck.slidey.json` — the
canonical human-facing rollup, conservative whenever any evidence, playback,
validation, issue, or worker gate is red. Markdown/JSON stay the canonical
machine and audit artifacts; see
[`../media/README.md`](../media/README.md) for where generated campaign
decks and evidence belong versus what may be promoted into `docs/decks/`.

## No-LLM gates

Every `campaign_*` intent is proven with a deterministic flow fixture under
`stories/product-journey-qa/flows/` (`campaign_surface.yaml`,
`campaign_tick.yaml`, `campaign_finding_sink_local.yaml`) that stubs
`host.run` by call id — `kitsoki test flows stories/product-journey-qa`
never calls a live LLM, `gh`, or a real worker. The runner-level contracts
(finding-sink filing idempotency, worker receipt writer, marathon smoke
ledger) are additionally proven by `tools/product-journey/*_test.py`, most
directly `local_findings_test.py` for the finding-sink policy and
`campaign_worker_test.py` for the worker receipt.

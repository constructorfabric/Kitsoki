# Agentic QA campaigns

A walkthrough for running a standing persona/scenario QA campaign against
your own project with `stories/product-journey-qa`. For the conceptual
model (catalog, verbs, finding-sink policy, worker backend, rollup) see
[`../../stories/product-journey-qa.md`](../../stories/product-journey-qa.md);
for the exhaustive intent-by-intent reference see
[`../../../stories/product-journey-qa/README.md`](../../../stories/product-journey-qa/README.md).
*Audience: operators launching or tuning a campaign on their own project.*

## 1. Register personas and scenarios

Add entries to `tools/product-journey/personas.json` and
`tools/product-journey/scenarios.json` for your project (or reuse the
universal set that ships by default — `workflow-author`,
`remote-ops-owner`, `enterprise-evaluator`, and the `docs-to-mcp-first-run`
/ `agent-launch-experience` / `remote-worker-campaign` /
`campaign-rollup-review` scenarios). `tools/product-journey/README.md`
documents the catalog schema. Validate before running anything:

```sh
python3 tools/product-journey/run.py --validate-corpus --json-output
```

## 2. Plan a run

```sh
kitsoki run stories/product-journey-qa/app.yaml
> campaign_plan project=<your-project> persona=<persona-id> seed=<any-string>
```

This plans a run bundle under `.artifacts/product-journey/<run-id>/` from the
universal campaign scenarios (or `scenario_scope=<comma list>` for a narrower
sweep), and writes `driver-plan.md` / `agent-brief.md` for a Codex/Kitsoki
agent to pick up through Studio MCP.

## 3. Capture evidence

Drive the plan through the reusable driver
(`.agents/agents/product-journey-qa-driver.md`), or attach evidence directly:

```text
campaign_attach scenario=<id> evidence_kind=browser_screenshot evidence_path=<path>
```

`review` reports readiness (`needs_evidence` until every scenario has
evidence or an explicit `blocker`); `validate` catches stale or inconsistent
bundles without rewriting them.

## 4. File findings — pick your sink

```text
campaign_issue_fix
```

With no slots, this files every credible `issue` finding as a **local
ticket** under `.artifacts/issues/bugs/` — no GitHub filing, no gh-agent
repair. This is the right default for iterative campaign stabilization on
your own project: review the pile with

```sh
kitsoki bug list --sink local-artifact --target kitsoki
kitsoki bug show <id> --sink local-artifact --target kitsoki
```

Opt into the GitHub sink explicitly when a finding is a real handoff
boundary — a hosted gh-agent repair job, or a report meant for maintainers
outside your local loop:

```text
campaign_issue_fix finding_sink=github ticket_repo=owner/repo gh_agent_public_base_url=https://your-gh-agent.example
```

This runs the full evidence-backed filing → gh-agent repair → independent
verification → issue close-out loop. Never mix the two by hand-filing —
`campaign_issue_fix` is the one entrypoint; see
[`../../stories/bugs.md`](../../stories/bugs.md) for the local ticket format
either sink writes.

## 5. Send bounded work to a worker (optional)

```text
campaign_worker backend=local worker_id=<id> status=ready
```

`backend` is `local`, `arena`, or `vm`. The receipt
(`campaign-worker-receipt.json` / `.md`) names readiness and imports
artifacts from a completed worker run into the campaign bundle. A blocked or
unready receipt stays visible in the run view instead of implying capture
happened — treat that as a signal to re-run the worker before trusting the
bundle.

## 6. Keep the rollup current

```text
campaign_stats
campaign_deck_refresh
```

`campaign_stats` derives filed/fixed/reopened/similar issue counts across
retained campaign artifacts (both sinks count as "filed"; only the GitHub
sink can report "fixed", since that requires GitHub issue state).
`campaign_deck_refresh` regenerates `deck.slidey.json`, the canonical
human-facing rollup — conservative whenever any evidence, playback,
validation, issue, or worker gate is red. Provide the `.slidey.json` link for
stakeholder review (VS Code Slidey previewer); promote a curated, retained-proof
deck into `docs/decks/` only per
[`../../media/README.md`](../../media/README.md).

## 7. Run it on a cadence

```text
campaign_tick
```

Advances the next due standing-campaign control artifact through the
existing autonomous-marathon due path — the verb a local or VM worker calls
on a schedule without copying runner commands together. Pair with
`autonomous_watchdog` (checked automatically before any filing) so a stalled
driver heartbeat blocks the loop with a human-reviewable artifact instead of
silently going quiet.

## No-LLM proof

Everything above is exercised by deterministic flow fixtures — no live LLM,
`gh`, or worker call:

```sh
go run ./cmd/kitsoki validate stories/product-journey-qa/app.yaml
go run ./cmd/kitsoki test flows stories/product-journey-qa/app.yaml
python3 tools/product-journey/local_findings_test.py
python3 tools/product-journey/campaign_worker_test.py
```

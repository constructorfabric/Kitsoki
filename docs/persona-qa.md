# Persona QA

> This page covers the narrow, single-scenario x transport-axis check
> (`stories/scenario-qa`). For the full persona x scenario x standing-campaign
> pipeline — project-owned catalogs, the `campaign_*` product verbs, the
> local-vs-GitHub finding-sink policy, worker dispatch, and the Slidey rollup —
> see [`stories/product-journey-qa.md`](stories/product-journey-qa.md) and
> [`guide/development/agentic-qa-campaigns.md`](guide/development/agentic-qa-campaigns.md).
> Both reuse this page's evidence contracts and `tools/product-journey`
> runner.

Persona QA is a Kitsoki story workflow, not a separate tool the operator should
hunt for under `tools/`. The canonical surface is:

```sh
kitsoki run @kitsoki/scenario-qa
```

`@kitsoki/persona-qa` is a story alias for the same story — use whichever name
you remember; see [Naming](#naming) below.

## Naming

One product, four names — keep them straight:

| Name | What it is |
|---|---|
| **Persona QA** | The product. This doc is the canonical reference. |
| **`scenario-qa`** | The story you run: `kitsoki run @kitsoki/scenario-qa` (alias `@kitsoki/persona-qa`, [`stories/scenario-qa/README.md`](../stories/scenario-qa/README.md)). |
| **`product-journey-qa`** | The broader story for persona x scenario x 10-repo matrix sweeps, marathons, and autonomous fixing ([`stories/product-journey-qa/README.md`](../stories/product-journey-qa/README.md)). |
| **`product-journey`** | The deterministic runner backend both stories drive: `tools/product-journey/run.py` ([`tools/product-journey/README.md`](../tools/product-journey/README.md)). |
| **`persona_qa`** | A shared support kit (schemas, completion-state conversion, deck generation, no-LLM tests) — not an operator surface; see [Why `tools/persona_qa` Exists](#why-toolspersona_qa-exists). |

## Quickstart (5 minutes)

One prompt in, a verdict table and a deck out:

```sh
kitsoki run @kitsoki/scenario-qa
> check whether the onboarding tour renders on web transport=web
> report
```

`check` plans a run bundle, drives the transport(s) with the reusable
[`product-journey-qa-driver`](../.agents/agents/product-journey-qa-driver.md)
agent, and independently judges the captured evidence. Add `transport=tui,web`
or `transport=all` to check more than one transport — every leg drains
automatically by default (add `pause=each-leg` to pause after each leg for an
explicit `next transport` instead). `report` folds every recorded leg into
`.artifacts/product-journey/<run-id>/report.md` (the verdict table) and
`deck.slidey.json` (a Slidey deck of the run). For the fuller persona x
scenario x 10-repo matrix workflow, see
[`stories/product-journey-qa/README.md`](../stories/product-journey-qa/README.md)
and the [`product-journey-qa`](../.agents/skills/product-journey-qa/SKILL.md)
skill instead — this quickstart covers the narrow single-scenario `scenario-qa`
surface. The [`scenario-qa`](../.agents/skills/scenario-qa/SKILL.md) skill
covers the same narrow surface with the driving-agent contract.

From the story, use natural prompts or explicit `key=value` qualifiers:

```text
preview required user input affordance transport=web,tui target=<project-id>
preview scenario=<catalog-scenario-id> transport=all
check bugfix on all transports
report
main room
```

That is the whole one-prompt path: `check` on however many transports drains
every leg on its own and lands on `report` in one turn — no `next transport`
in between. The explicit forms still work the same way:

```text
check scenario=<catalog-scenario-id> transport=tui,web persona=<persona-id> target=<project-id>
check whether settings validation persists transport=web target=<project-id>
```

Add `pause=each-leg` to any `check` to opt back into the old per-leg ceremony
— useful when a human wants to inspect each transport's evidence before
continuing, or when an MCP-driven caller wants to control the pace itself
with `session.submit next_leg` between legs:

```text
check scenario=<catalog-scenario-id> transport=all pause=each-leg
next transport
next transport
report
```

The resting room also accepts unmatched prose as a `check_request`, so an
operator can type the behavior they want checked without first adding it to the
catalog. Use `scenario=<id>` when the request should bind to a catalog scenario;
otherwise the remaining prose becomes an ad-hoc scenario description. Supported
transport values are `all`, `tui`, `web`, `vscode`, `cli`, or a comma list.

`preview` is side-effect-free for both ad-hoc and catalog requests. For an
ad-hoc request it parses the same plain prose and qualifiers as `check`, uses a
generic transport carrier only to resolve the requested evidence contracts, then
renders the operator's requested behavior in the preview. It does not create a
run bundle, launch capture, or call an LLM.

`check` creates the run bundle under `.artifacts/product-journey/<run-id>/`,
then drives one transport-pinned check at a time. By default (`pause=auto`)
a multi-transport check drains every leg in one turn — `check bugfix on all
transports` produces a finished report with no further prompting. Add
`pause=each-leg` to pause after each transport instead, so the operator (or
an MCP caller submitting `next_leg` itself) can inspect evidence before
continuing with `next transport`.
`report` folds the recorded driver and judge outcomes into:

- `report.md` for the per-transport verdict table.
- `deck.slidey.json` for the deterministic Slidey deck.

The closeout report keeps the result counts at the top, exposes `report.md` as
the primary clickable summary artifact in the web/TUI `kv` renderer, and offers
`main room` to return to the Scenario QA start screen without discarding the
last run.

### Parallel legs (opt-in, via arena)

Add `parallel=true` to any multi-transport `check` to hand the resolved legs
to arena's `persona-qa` job type instead of driving them one at a time:

```text
check bugfix on all transports parallel=true
```

`rooms/parallel.yaml` replaces the serial `execute → judge → recording` loop
with one `host.run` call to `tools/arena/scripts/run_scenario_qa_legs_parallel.py`,
which drives every leg concurrently through the deterministic, no-LLM
persona-QA replay-smoke path (`tools/persona_qa/kit.py`) and folds each leg's
`CompletionState` — the same contract `tools/arena/arena/plugins/persona_qa.py`
scores containerized arena cells from — into the exact drive_result/
judge_result shape `scripts/record_leg_result.star` expects
(`tools/persona_qa/completion.py::to_scenario_qa_leg_result`). The SAME
`record_leg_result.star`/`build_report.star` scripts the serial loop uses then
fold every leg's result into `world.leg_results` in one batch call, so
`report.md` / `deck.slidey.json` are generated by identical code whether the
legs were driven serially or in parallel — the verdict and cause columns read
the same either way.

Use it when a `transport=all` check feels slow and a fast, zero-spend
proof-of-life across every transport leg is enough; it needs `tools/arena/` in
the checkout but nothing else (no Docker, browser image, or live
credentials — this path never opens a live agent session). It is
transport-BLIND: each leg is scored from a cassette-backed
`(project, persona, scenario)` replay-smoke proof, not a transport-specific
visual capture, so it complements rather than replaces the serial path's
per-transport visual evidence. Fail-closed authorization semantics are
inherited unchanged: a leg that needs live/interpretive drive still reports
`degraded-evidence` with a stated cause, exactly as it would serially with no
`profile=`. Default (`parallel=false`) is the serial loop above — parallel is
opt-in, never a silent substitute.

## Transport Contract

One scenario can drive any transport it declares without a custom harness path.
The story passes `transport=tui`, `web`, `vscode`, `cli`, a comma list, or
`all` into `tools/product-journey/run.py`. The runner expands each applicable
scenario into scenario x transport checks and writes the stable route into
`driver-plan.json`.

Each transport check carries:

- `leg_id`, `scenario`, `transport`, and `visual_surface`.
- The primary story and natural task prompt.
- Required MCP capabilities and resolved driver tools.
- The evidence contract and proof level.
- Stable open, observe, and act entrypoints.
- A no-substitution policy: demo, placeholder, synthetic, or unrelated media
  cannot satisfy proof.

VS Code checks are bridge-level proof by default (`visual.open kind=vscode` —
a runstatus-webview stand-in, never a genuine editor window) and carry a
second, opportunistic **editor-level** tier on top of that floor: when a real
VS Code + kitsoki extension is linked to the kitsoki process (the same
`internal/ide.Link` the TUI's `/ide` command drives) AND the leg's driven
`primary_story` itself issues a `host.ide.get_open_editors` /
`get_diagnostics` / `get_selection` call while advancing (not every story
does — `stories/bugfix`'s `validating` room does), that call appends a
`ide.context_captured` trace event proving a real editor was queried. A
post-drive capture of that event (`post_drive_editor_evidence_ref`, see
`tools/persona_qa/transports.py`'s `editor_evidence_contract`) is what
`scripts/record_leg_result.star` requires before a `vscode` leg can score a
genuine `pass`; a leg with only the mandatory bridge-level recapture stays
`degraded-evidence` with an explicit `cause`, because a bridge-only capture
is not what a user selecting `transport=vscode` reasonably expects. Real
pixel-level proof of the VS Code window itself (a screenshot of the actual
editor UI) is NOT attainable deterministically today — the dev-workflows
matrix's Mode A investigation found it needs a real extension
launch+packaging harness no story host call can reach — so that remaining
gap stays honestly unimplemented rather than faked; see the filed local
ticket under `.artifacts/issues/bugs`. CLI checks are terminal-level proof:
command transcript, exit code, cwd, and trace references, not visual state.
TUI and web checks are frame-level proof by default.

## Authorization & Harness (fail-closed)

`profile=<name>` is the ONLY live authorization. Omitting it keeps every leg
of a `check` on `harness: "replay"` for its whole nested session — never a
per-step judgment call by the driver agent, and never something the operator
finds out about only after the fact from a mysterious `degraded-evidence`
verdict. The contract runs in both directions:

- **No silent replay-only fallback.** When a leg looks interpretive (a
  catalog scenario with `natural_utterances` — free-text phrasing a cassette
  can't cover — or any ad-hoc description) and no `profile=` was supplied,
  `scripts/plan_legs.star` computes that fact deterministically at plan time
  and names it per leg, up front, before any leg is driven — visible in the
  `plan` and `execute` rooms' "Live authorization notes" and attached to the
  leg itself (`live_authorization_note`) so the driver agent sees it too.
- **No silent replay-miss-goes-live.** A `session.new` call opened with
  `harness: "replay"` hard-fails the instant it dispatches a `host.agent.*`
  call (converse/decide/task/ask/extract/search) with no matching cassette
  episode — the MCP session runtime itself refuses to fall through to a live
  agent (`internal/mcp/studio/session_runtime.go`'s replay-agent-miss
  handler). This is enforced in code the driver cannot opt out of; the
  driver's job is only to report that hard error as the leg's blocker, never
  to retry the same step by opening a new session with `harness: "live"`
  without a supplied `profile=`.
- **No unexplained degraded verdict.** Every leg whose verdict is not `pass`
  carries a `cause` (`stories/scenario-qa/scripts/record_leg_result.star`) —
  missing evidence, a reported blocker (including a replay-miss's hard error
  text), no live authorization, an unauthorized live harness, or the judge's
  own grounding — shown in `report.md`'s Cause column and the report room's
  transport-verdict lines. Nothing is left as a bare `degraded-evidence` with
  no stated why.
- **No unauthorized live self-authorization.** If a driver ever reports it
  used `harness_used: "live"` for a leg while this check never supplied
  `profile=`, `record_leg_result.star` deterministically forces that leg's
  verdict to `degraded-evidence` with an explicit policy-violation cause,
  regardless of what the driver or judge otherwise reported. Prompt
  instructions (`.agents/agents/product-journey-qa-driver.md`,
  `stories/scenario-qa/prompts/drive_leg.md`) state the same rule, but this
  deterministic check is what actually enforces it.

## Evidence And Decks

The story never treats a driver self-report as proof. The driver captures what
it can, then a read-only judge grades the captured evidence. Missing, degraded,
or fake media becomes `degraded-evidence` or a blocker, not a pass.

`deck.slidey.json` is generated from existing run artifacts. The same run
bundle and flags produce stable JSON bytes. Scenario QA report decks derive
session replay scenes from recorded leg results (`playback_path`, `rrweb_path`,
`video_path`, or playback-looking `evidence_refs`). Matrix and retained-run
decks still derive playback scenes from `media-manifest.json`. Demo or
placeholder media remains visible as blocked evidence rather than being treated
as proof.

Committed examples:

- [persona-qa-kitsoki-example.slidey.json](decks/persona-qa-kitsoki-example.slidey.json)
  shows missing playback surfaced as blocked evidence rather than fake video.
- [persona-qa-slidey-architect-review.slidey.json](decks/persona-qa-slidey-architect-review.slidey.json)
  reviews Slidey from the lens of a software architect who frequently makes
  technical presentations.

Maintainer-only deterministic regeneration for those fixture decks still uses
the internal deck adapter because the source is a retained run fixture, not a
live story session:

```sh
python3 tools/persona_qa/kit.py deck \
  --run-dir tools/persona_qa/examples/runs/kitsoki-product-review \
  --out docs/decks/persona-qa-kitsoki-example.slidey.json \
  --title "Kitsoki Persona QA Example"

python3 tools/persona_qa/kit.py deck \
  --run-dir tools/persona_qa/examples/runs/slidey-architect-review \
  --out docs/decks/persona-qa-slidey-architect-review.slidey.json \
  --title "Slidey Architect Review"
```

When a generated deck is committed under `docs/decks/`, keep referenced rrweb
clips under `docs/decks/assets/<deck-id>/` and bundle the viewer for the product
site deck gallery.

## Why `tools/persona_qa` Exists

`tools/persona_qa` is not the product surface. It is a deterministic support
package used by the story, runner, arena adapters, and tests for work that does
not belong in YAML:

- Public schema templates for portable persona/scenario/driver catalogs.
- Completion-state conversion shared with arena and CI scoring.
- Deck generation from retained run fixtures.
- UI QA/UI review verdict adapters.
- No-LLM unit tests for portable kit compatibility.

The story owns orchestration, operator pacing, evidence capture, judging, and
deck/report close-out. The Python package owns pure data transforms and
compatibility adapters.

## Public Contracts

Versioned schemas live under `schemas/persona-qa/v1/`:

- `config.schema.json`
- `persona.schema.json`
- `scenario.schema.json`
- `driver-manifest.schema.json`
- `run-bundle.schema.json`
- `leg-result.schema.json`
- `transport-suite.schema.json`
- `review.schema.json`

The shared completion-state contract is
`schemas/completion-state.schema.json`.

## No-LLM Gates

Automated checks should stay deterministic:

```sh
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki test flows stories/scenario-qa/app.yaml
python3 tools/product-journey/transport_axis_test.py
python3 tools/product-journey/scenario_qa_report_test.py
python3 tools/persona_qa/tests/test_kit_cli.py
python3 tools/persona_qa/tests/test_deck_cli.py
```

Do not call a live LLM from tests. Put replay inputs under fixtures and record
honest blockers when proof evidence cannot be captured.

## History

Persona QA's exploratory-walkthrough posture (drive a story as a skeptical
persona, judge the rendered screen, report findings) originated in the
`story-qa-agent` / `qa-agent-skill` proposals. Their substrate shipped as the
MCP studio (`docs/architecture/mcp-studio.md`) and the `.agents/skills/story-qa/`
skill + `docs/stories/story-qa.md` guide; their persona/scenario/transport
product surface shipped as this story. Both proposals were deleted per the
proposal lifecycle once their content was covered here and in those docs.

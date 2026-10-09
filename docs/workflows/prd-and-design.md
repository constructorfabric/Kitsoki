# PRD → design workflow

Turn a raw idea into a **PRD** (what/why/for whom), then a **design /
proposal** (how) — publishable, reviewable markdown, not a chat transcript
that evaporates. Two stories back this: [`stories/prd/`](../../stories/prd/README.md)
(the PRD pipeline: idea → clarify → brief → references → draft → publish to
`docs/prd/<slug>.md`) and dev-story's **design pipeline** (the `design*`
rooms: discovery+brief → existing-state → completeness → references → draft
→ publish to `docs/proposals/<slug>.md`, plus a linking feature ticket). The
two chain together as the **PRD → Design walk** — see
[`stories/dev-story/README.md#prd--design-walk`](../../stories/dev-story/README.md#prd--design-walk)
for the full room-by-room contract, world keys, and the
[`flows/prd_to_design.yaml`](../../stories/dev-story/flows/prd_to_design.yaml)
no-LLM proof. Both pipelines are conversational: you describe the idea in
plain language, an agent asks follow-ups, and each stage is a **confirm-gated
review** of a written artifact — never a silent auto-publish.

You do not have to go through a PRD first — `design*` is reachable directly
(ad hoc, via the `idea` intent) when you already know the "how" and just
want a design doc. The PRD stage exists for the "what/why" work that should
happen first for larger asks.

## Prerequisite

Your repo must be onboarded (see [`../getting-started.md`](../getting-started.md))
so `docs/prd/` and `docs/proposals/` (or your project's configured
equivalents — see dev-story's
["doc profile" section](../../stories/dev-story/README.md#doc-profile--targeting-an-external-project))
exist as publish targets.

## TUI

The primary surface today. From your onboarded instance (verbatim from
[`stories/dev-story/README.md`'s own PRD → Design demo](../../stories/dev-story/README.md#demo-prd--design-judge_modehuman)):

```
kitsoki run                       # or: kitsoki run .kitsoki/stories/<id>-dev/app.yaml
> prd                             # landing → prd.idle (discovery chat opens)
> I want a CLI for X              # discovery conversation (prd__discuss)
> prd__start                      # distil idea → prd.search (prior-art gate)
> prd__confirm                    # no overlap → prd.clarifying (questions posed)
                                   # (or prd__change_existing / prd__override_new
                                   #  if the scout flags an overlap)
> developers; time-to-first-success   # answer (prd__answer); last answer auto-advances
> prd__confirm                    # brief → prd.references
> prd__confirm                    # references → prd.drafting (PRD authored)
> prd__accept                     # publish docs/prd/<slug>.md → prd_published
> continue                        # → design intake, seeded "Author a design from the PRD at …"
> <describe / refine>             # the design pipeline takes over: search → brief → draft → publish
```

From there the design pipeline runs the same conversational shape
(discovery+brief → existing-state → completeness → references → draft →
publish); see the
[`design*` row of dev-story's rooms table](../../stories/dev-story/README.md#rooms)
for its room-by-room detail. `design_done` then offers
`implement` (drives the linked feature ticket straight into the impl
pipeline — see [`decompose-and-implement.md`](decompose-and-implement.md))
or `decompose` (drives it into `deliver` instead).
The same publish turn writes `.artifacts/roadmap/progress.yaml`; direct
implementation also writes start/completion events and links the child progress
artifact on return. See [`roadmap-ledger.md`](roadmap-ledger.md) for the
proposal, docs, feature catalog, product-site, and demo checks that keep
roadmap updates deterministic.

Standalone (no dev-story hub, just the PRD pipeline on its own):

```
kitsoki run stories/prd/app.yaml
```

— see [`stories/prd/README.md`](../../stories/prd/README.md) for its full
walkthrough, world contract, and intent surface (this is the authoritative
reference; the steps above are dev-story's wrapper around it).

**Verify the commands above yourself** (no LLM):

```
go run ./cmd/kitsoki test flows stories/prd/app.yaml --flows 'stories/prd/flows/*.yaml'
go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows 'stories/dev-story/flows/prd_to_design.yaml'
```

## Web

Drivable — `kitsoki web` runs the identical dev-story/prd orchestrator over
HTTP, so every room and intent above exists. **Current caveat:** free-text
composer routing (typing the phrase instead of clicking the button/quick
action) is unfinished for this pipeline, so lean on the rendered choice
buttons rather than typed prose for now. See
[`../tui/web-ui.md`](../tui/web-ui.md) for the shared session/SSE
mechanics.

## VS Code

Drivable inside the editor webview — same backend as web, so the same
caveat applies. There is a recorded demo spec
([`tools/vscode-kitsoki/tests/vscode-prd-demo.e2e.spec.ts`](../../tools/vscode-kitsoki/tests/vscode-prd-demo.e2e.spec.ts))
proving the PRD walk renders correctly in the embed. **Current caveat:** the
extension does not yet auto-discover an onboarded repo's story directory —
point `kitsoki.storiesDir` at your instance's `stories/` (or the dev-story
hub) by hand; see
[`../tui/vscode-extension.md`](../tui/vscode-extension.md#build-and-run).

## gh-agent

**Not yet supported.** There is no label→PRD/design route in
[`internal/ghagent/router.go`](../../internal/ghagent/router.go) today — a
`@kitsoki` mention cannot start or advance a PRD/design run. See
[`../architecture/github-agent.md`](../architecture/github-agent.md) for what
gh-agent *does* dispatch today (bug-fix issue routes) and its honesty
contract (a route with no plan gets an honest stub ack, never a fake "done").

## Standing proof status

See the `prd-proposal` row of
[`../testing/dev-workflow-matrix.md`](../testing/dev-workflow-matrix.md) for
the current mechanical/experience verdict per surface and repo (kitsoki-dev
vs. gears-rust).

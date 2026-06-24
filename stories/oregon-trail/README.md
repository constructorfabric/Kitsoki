# Oregon Trail — the kitsoki feature-coverage example app

`stories/oregon-trail/` ports the 1985 MECC *Oregon Trail* to a single
kitsoki manifest. It exists for three load-bearing reasons:

1. **Every reviewer already understands the game.** You can read a
   leg of `phases.yaml` and predict what the state machine should do
   before running it. Show kitsoki to a non-engineer in ninety seconds
   without first explaining a fake bug tracker / deploy pipeline /
   internal codebase.
2. **No external state.** OT runs entirely on the YAML, a SQLite
   file, and — in narrated mode only — the local `claude` CLI. There
   is no Jira to fake, no OAuth to mock, no HTTP server to stand up,
   no API key to provision. `kitsoki test flows` runs in deterministic
   mode with zero LLM cost, zero secrets, and zero network. That makes
   OT the safest engine fixture in the tree: when the engine breaks,
   the failure is in the engine, not in some adjacent stub.
3. **The port deliberately stretches across nearly every kitsoki
   primitive** — compound states, phase templates, proposals,
   background jobs with mid-flight clarifications, persistent chat
   rooms, transport posts, MCP-validated typed JSON, parallel regions,
   timeouts, named agents, in-view menu helpers — so the same manifest
   doubles as a feature catalogue.

This combination is the whole point. The cloak example covers a
third of the surface with no external state; dev-story covers a
different third *with* external state (`host.workspace_manager.get`,
real chat rooms, real transports); OT is the largest pure-engine
fixture that exercises the union. If you're prototyping a new
kitsoki feature, you want to see it work here first, before you
wire the real external system.

Where this README overlaps with the authoritative docs —
[`docs/stories/state-machine.md`](../../docs/stories/state-machine.md),
[`docs/stories/authoring.md`](../../docs/stories/authoring.md),
[`docs/tracing/testing.md`](../../docs/tracing/testing.md),
[`docs/architecture/hosts.md`](../../docs/architecture/hosts.md),
[`docs/architecture/transports.md`](../../docs/architecture/transports.md),
[`docs/stories/background-jobs/README.md`](../../docs/stories/background-jobs/README.md) —
defer to those. The byte-exact schema view is in
[`APP.md`](./APP.md), produced by `kitsoki render`.

---

## 1. Play it

```bash
go run ./cmd/kitsoki run stories/oregon-trail/app.yaml
```

To capture a turn-by-turn trace:

```bash
go run ./cmd/kitsoki run stories/oregon-trail/app.yaml \
    --trace stories/oregon-trail/last-run.trace
```

Two modes share the same state graph; the toggle is one world key.

```bash
# Deterministic (default). Zero LLM cost. RNG is
# (miles_traveled + rng_counter) % 100. Every flow fixture runs
# this way.
go run ./cmd/kitsoki run stories/oregon-trail/app.yaml

# Narrated. Same graph; event prose, party-naming, illness
# diagnosis, and the wagon-master chat go through host.agent.*.
go run ./cmd/kitsoki run stories/oregon-trail/app.yaml \
    --world '{"narration": true}'
```

Every event substate's `on_enter:` carries paired `when:
world.narration` / `when: not world.narration` arms that fork into
the canned-prose branch or the agent-call branch. The graph is
identical either way — narrated mode is decoration over a
deterministic core.

To skip the intro entirely and drop into a primed mid-game state for
smoke testing (e.g. the imported bandit encounter at Chimney Rock),
use a checked-in "warp basis":

```bash
go run ./cmd/kitsoki run stories/oregon-trail/app.yaml \
    --warp scenarios/chimney_robbery.yaml
```

Available scenarios live in [`scenarios/`](./scenarios/); the same
file can also be loaded interactively from the TUI via
`/warp file:scenarios/chimney_robbery.yaml`. Both routes share the
same loader — see [`../../docs/stories/imports.md` §Operator tooling](../../docs/stories/imports.md#operator-tooling-warp-and---warp).

---

## 2. Game model — at a glance

You outfit a wagon at Independence, MO, with a fixed budget that
varies by profession (banker $1600 / carpenter $800 / farmer $400).
You buy oxen, food, bullets, clothing sets, and spare wagon parts
(wheels, axles, tongues), pick a departure month, and name a party
of five. Every choice is exposed as a slotted intent the LLM (in
narrated mode) or the test runner (in deterministic mode) can drive.

You then traverse 7 legs through 8 landmarks — Independence → Kansas
River Crossing (102 mi) → Fort Kearney (304 mi) → Chimney Rock
(554 mi) → Fort Laramie (640 mi) → South Pass (932 mi) → Snake River
Crossing (1182 mi) → Willamette Valley (1500 mi). Each leg is the
same shape: an `_executing` compound state that ticks miles and may
fire one of five random events (disease, breakdown, weather,
encounter, supply loss), then an `_awaiting_reply` checkpoint state
at the landmark where you choose `continue` / `rest` / `hunt` /
`consult_guide` / `restart_from { stage }` / `quit` / `enter_fort`
/ `approach_river`.

You win by reaching Willamette with at least one party member alive.
You lose by running out of food, drowning in a river, exhausting
every illness-`treat` retry, pushing past South Pass too late in the
year and starving through the winter, or running out of legs to
abandon. Distances are MECC's; party-of-five and the departure-month
mechanic come from the same 1985 release.

---

## 3. Game feature → kitsoki app feature map

This section reads the game mechanics first, naming the kitsoki
primitive each one exercises and pointing at the file. Pair each row
with [`APP.md`](./APP.md) (the byte-exact schema render) when you
want to see the resulting compiled graph.

### 3.1 Trail traversal

| Game mechanic | Kitsoki primitive | Where |
|---|---|---|
| Repeat the same "travel → arrive → choose" shape 7 times | `phase_templates:` + `phases.graph:` with parameterised states (`{leg_id}_executing`, `{leg_id}_awaiting_reply`, `{leg_id}_error`) | [`phases.yaml`](./phases.yaml) §`trail_leg` template + §7 instances |
| Landmark checkpoint menu (continue / quit / rest / hunt / consult / restart) | `checkpoint_intents:` merged into every `_awaiting_reply` by the expander | [`app.yaml`](./app.yaml) §`checkpoint_intents:` |
| Back-arc retry of a failed leg | Phase `next:` graph with `on_failure:` arcs + `cycle_budgets: { on_failure: 2 }` | [`phases.yaml`](./phases.yaml) §`phases.graph` |
| "Go back to Fort Kearney" teleport | `restart_from { stage }` checkpoint intent with `enum` slot mapped to each leg's `_executing` state | [`app.yaml`](./app.yaml) §`checkpoint_intents.restart_from`; [`phases.yaml`](./phases.yaml) `_awaiting_reply.on.restart_from` |
| Daily miles modulated by pace × terrain × month | Templated `set:` expressions reading `world.pace`, `tpl.terrain`, `world.month`; daylight-percent table folds in via `int()` rounding | [`phases.yaml`](./phases.yaml) `traveling.continue` arms |
| Calendar rollover (day > 30 → month++; dec → jan ticks year) | Plain `set:` arithmetic on `world.day` / `world.month` / `world.year` | [`phases.yaml`](./phases.yaml) `traveling.continue` arms |
| Snow-blocked South Pass (winter months) | Guard on `world.month` routes `_awaiting_reply.continue` to a separate atomic state with its own `wait_for_spring` / `give_up` arms | [`rooms/snow_blocked.yaml`](./rooms/snow_blocked.yaml); routed from `phases.yaml` `leg_e_awaiting_reply.continue` |
| Severe weather kind biased by month | `event_weather.on_enter` ternary on `world.month`: snow in nov–feb, heavy_rain in mar–may, hail/fog in jun–aug, heavy_rain/fog in sep–oct | [`phases.yaml`](./phases.yaml) `event_weather.on_enter`; flow [`flows/weather_by_month.yaml`](./flows/weather_by_month.yaml) |
| Forage quality surfaced in the trail view | View-template ternary on `world.month`: rich (may/jun), fair (apr/jul), sparse (mar/aug), drying (sep/oct), none in winter — couples to the daylight modifier so the player can see why pace varies | [`phases.yaml`](./phases.yaml) `traveling.view` |
| Land at a fort (resupply) | `_awaiting_reply.enter_fort` guarded on `tpl.ends_at_fort` → `fort` room | [`rooms/fort.yaml`](./rooms/fort.yaml); [`phases.yaml`](./phases.yaml) |
| Reach a river | `_awaiting_reply.approach_river` guarded on `tpl.ends_at_river` → `river_crossing` with `set:` of `river_depth_ft` / `river_width_ft` (modulated by month) | [`rooms/river_crossing.yaml`](./rooms/river_crossing.yaml); [`phases.yaml`](./phases.yaml) |
| Wagon master gives up if you idle at a landmark | `Timeout: { after: "10d", target: "{{ phase.next.continue }}" }` on every `_awaiting_reply` | [`phases.yaml`](./phases.yaml) `_awaiting_reply.timeout`; flow [`flows/landmark_timeout.yaml`](./flows/landmark_timeout.yaml) |

### 3.2 Random events

| Game mechanic | Kitsoki primitive | Where |
|---|---|---|
| Five event kinds (disease / breakdown / weather / encounter / supply loss) selected from one roll | Compound `_executing` state with substates `event_disease` / `event_breakdown` / … selected by guarded `traveling.continue` arms that read `(miles + rng_counter) % 100` | [`phases.yaml`](./phases.yaml) §"Event roll" |
| Each event has its own resolution verbs (treat / wait_out / move_on, repair / wait_out, etc.) | Per-substate `on:` map; wildcard `*` deflects unrelated verbs while sick | [`phases.yaml`](./phases.yaml) event substates |
| You can only retry "treat" / "repair" twice before the patient dies / the part is lost | Manual cycle-budget pattern: `current_event_attempts` counter, incremented on each retry, guarded `< 2` on the success arm and `>= 2` on the default arm | [`phases.yaml`](./phases.yaml) `event_disease.treat` / `event_breakdown.repair` |
| Illness lingers after the substate exits (party member is still convalescing) | World keys (`illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`) declared on the trail leg's `relevant_world:` so they stay surfaced in the location indicator | [`app.yaml`](./app.yaml) world schema; [`phases.yaml`](./phases.yaml) `_executing.relevant_world:` |
| Random species at hunt time (bison or elk?) | Background-job mid-flight `host.RequestClarification`; an `action_required` inbox notification; resume via `answer_clarification` | [`rooms/hunt.yaml`](./rooms/hunt.yaml); [`rooms/inbox.yaml`](./rooms/inbox.yaml); flow [`flows/hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml) |

### 3.3 Stores, forts, and rivers — the proposal lifecycle

| Game mechanic | Kitsoki primitive | Where |
|---|---|---|
| Draft a basket at Matt's, see the total, accept or refine | `proposals.buy_supplies` (schema → draft → reviewing → executing → done); proposal-host room reproduces the lifecycle as compound substates | [`proposals.yaml`](./proposals.yaml) `buy_supplies`; [`rooms/general_store.yaml`](./rooms/general_store.yaml) |
| Tiny purchases skip review | `policy.auto_accept_if: "$proposal.total_cost < 5"` | [`proposals.yaml`](./proposals.yaml); flow [`flows/buy_proposal_auto_accept.yaml`](./flows/buy_proposal_auto_accept.yaml) |
| "I want 7 oxen not 6" without restarting the basket | `refine_purchase` intent self-stays in `reviewing` and merges the supplied slots against the current draft using `?? world.proposal_*` defaults | [`rooms/general_store.yaml`](./rooms/general_store.yaml) `reviewing.refine_purchase`; flow [`flows/buy_proposal_refine.yaml`](./flows/buy_proposal_refine.yaml) |
| Buy again after a purchase completes | `policy.repeatable: true` + `done.repeat` arm back into `idle` | [`proposals.yaml`](./proposals.yaml); [`rooms/general_store.yaml`](./rooms/general_store.yaml) |
| Forts charge more than Independence | World scalar `local_price_pct` (100 at the general store, 150 at forts); read by `propose_purchase` cost math | [`app.yaml`](./app.yaml) `world.local_price_pct`; [`rooms/fort.yaml`](./rooms/fort.yaml) |
| Pick ford / caulk / ferry / wait at a river and wait for the outcome | `proposals.river_strategy` with `policy.require_confirm: true` and `execute: { background: true }` so the cross runs as a 0.2-second background job | [`proposals.yaml`](./proposals.yaml) `river_strategy`; [`rooms/river_crossing.yaml`](./rooms/river_crossing.yaml); flow [`flows/river_ford_drown.yaml`](./flows/river_ford_drown.yaml) |
| Shallow vs deep river crossings are mechanically different | Compound `river_crossing` with `initial:` template choosing `shallow` / `mid` / `deep` from `world.river_depth_ft` at entry | [`rooms/river_crossing.yaml`](./rooms/river_crossing.yaml) |

### 3.4 Hunt, rest, and the wagon-master chat

| Game mechanic | Kitsoki primitive | Where |
|---|---|---|
| Hunt sends you out for N hours | `hunt_idle.shoot` dispatches `host.run` with `background: true`, binds `last_job_id`, transitions to `hunt_running`; `on_complete:` reads `last_job_result.stdout_json` and credits food / debits bullets | [`rooms/hunt.yaml`](./rooms/hunt.yaml); flow [`flows/hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml) |
| Rest restores health and burns food and time | Same background-job shape as hunt — `host.run sleep N`; `on_complete:` restores `health_avg`, advances `world.day`, debits food | [`rooms/rest_room.yaml`](./rooms/rest_room.yaml); flow [`flows/rest_in_camp.yaml`](./flows/rest_in_camp.yaml) |
| Mid-hunt "two bison and one elk — which?" prompt | `host.RequestClarification` mid-job; the inbox surfaces `action_required`; the player answers via `answer_clarification` and the job resumes | [`rooms/hunt.yaml`](./rooms/hunt.yaml); [`rooms/inbox.yaml`](./rooms/inbox.yaml); flow [`flows/hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml) |
| Ask the wagon master and have him remember the last conversation | `mode: conversational` room with `host.chat.resolve` / `list` / `create` / `fork` / `archive` / `rename` / `suggest_title` / `resolve_ref` keyed by `(oregon-trail, trail_guide, world.profession)` | [`rooms/trail_guide.yaml`](./rooms/trail_guide.yaml); flow [`flows/trail_guide_smoke.yaml`](./flows/trail_guide_smoke.yaml) |
| Side-question on the trail that doesn't change game state | Engine-provided `off_path:` block: `/freeform` trigger, banner, `/onpath` return, scoped to its own chat thread with the `frontier_guide` persona | [`app.yaml`](./app.yaml) §`off_path:`; engine path is generic |
| Edit the story from inside the running game (`/meta story`) | `meta_modes.story:` points at the built-in `story-author` agent — multi-turn chat with full filesystem tool access against `stories/oregon-trail/`; orchestrator mtime-walks the dir and hot-reloads the app when the agent edits a YAML or prompt file | [`app.yaml`](./app.yaml) §`meta_modes.story:` |
| Long-form trail-master conversation (`/meta-consult`) | `meta_modes.consult:` reuses the `wagon_master` agent from §3.4 against a persistent chat keyed by `(oregon-trail, meta:consult, state)`; same voice as the in-game wagon master, just at the meta layer | [`app.yaml`](./app.yaml) §`meta_modes.consult:` |

### 3.5 Narration and named personas

| Game feature | Kitsoki primitive | Where |
|---|---|---|
| Period-flavored prose for events / landmarks | `host.agent.ask` invoked from event substates' `on_enter:` when `world.narration`; result bound into `world.last_event_prose` / `world.last_landmark_prose`; view prefers the prose over the canned line | [`phases.yaml`](./phases.yaml) `event_*.on_enter`; [`prompts/`](./prompts/) `event_*.md` + `landmark_arrival.md` |
| "Diagnose the illness" returns structured data, not prose | `host.agent.decide` with a schema that pins `{ illness, severity, treatment }` via the submit tool; `on_error: {{ tpl.id }}_error` covers handler failure | [`phases.yaml`](./phases.yaml) `event_disease.on_enter`; [`mcp/illness.json`](./mcp/illness.json); [`prompts/event_disease.md`](./prompts/event_disease.md); flow [`flows/disease_with_mcp.yaml`](./flows/disease_with_mcp.yaml) |
| Different voices for different surfaces in one app | Top-level `agents:` block; each agent call passes `agent: <name>` and the engine threads the agent's `system_prompt` through to the handler | [`app.yaml`](./app.yaml) §`agents:` (`frontier_guide`, `wagon_master`, `party_namer`, `trail_narrator`, `frontier_doctor`) |
| Off-path persona = "weathered frontier guide" | `off_path.agent: frontier_guide` references the same named-agent primitive | [`app.yaml`](./app.yaml) §`off_path:` |
| Wagon-master chat speaks like a wagon master | `host.agent.converse` called with `agent: wagon_master` per turn | [`rooms/trail_guide.yaml`](./rooms/trail_guide.yaml) |
| Theme-based party generation ("name the party after the Jackson 5") | `generate_names` intent dispatches `host.agent.ask` with `agent: party_namer`; deterministic branch uses a CSV lookup table | [`rooms/intro.yaml`](./rooms/intro.yaml) `generate_names`; flow [`flows/party_naming_narrated_agent.yaml`](./flows/party_naming_narrated_agent.yaml) |

### 3.6 Transport posts — multi-surface play

| Game feature | Kitsoki primitive | Where |
|---|---|---|
| Every arrival shows up as a "Trail Diary" entry | `host.transport.post` to `tui` on every `_awaiting_reply.on_enter`; `phase_id:` is templated per leg for de-dup on re-entry | [`phases.yaml`](./phases.yaml) `_awaiting_reply.on_enter`; flow [`flows/trail_diary_smoke.yaml`](./flows/trail_diary_smoke.yaml) |
| Death obituary posts to the same thread | `ended_lost.on_enter` posts a final entry | [`rooms/ended.yaml`](./rooms/ended.yaml) |
| Same state machine runs on a Jira ticket | The transport is configured at session-create time (`kitsoki session create --key jira:OT-1`); no manifest change | (engine-side; see [`docs/architecture/transports.md`](../../docs/architecture/transports.md)) |

### 3.7 Engine surfaces with no game equivalent

| Surface | Kitsoki primitive | Where |
|---|---|---|
| Aliased sub-story composition with private worlds | `imports:` — Oregon Trail's `bandits` encounter is a three-layer chain (`oregon-trail` → `frontier_event` → `robbery`) with `world_in:` / per-exit `set:` projections, `host_bindings:` rebinding through to the grandchild, intent re-export both directions, and a state/intent/prompt override triplet. Full reference: [`../../docs/stories/imports.md`](../../docs/stories/imports.md). | [`app.yaml`](./app.yaml) §`imports.frontier`; flows `flows/robbery_*.yaml` |
| Parallel state with cross-region `emit:` | `world_clock` compound with `type: parallel` and two sibling regions (`weather`, `calendar`); weather `on:` arms emit `precip_heavy` / `snow_starts`, calendar `on:` arms bind the witnesses into world | [`rooms/world_clock.yaml`](./rooms/world_clock.yaml); flow [`flows/parallel_weather.yaml`](./flows/parallel_weather.yaml) |
| `Effect.When` for "only fire if narration is on" | `when:` on individual `on_enter` effects — paired with a `not world.narration` arm for the deterministic side | [`phases.yaml`](./phases.yaml) `event_*.on_enter` |
| `Slot.Default` filling | `propose_crossing` declares `confidence` as `required: false, default: 50`; the engine fills the default into the slot bag before effects run so the templated cost math doesn't crash on a bare `slots.confidence` | [`intents.yaml`](./intents.yaml) `propose_crossing`; flow [`flows/river_ford_no_confidence.yaml`](./flows/river_ford_no_confidence.yaml) |
| In-view menu helpers | `available("intent_id")` / `blocked_reason("intent_id")` template helpers render the green / red action menus | [`rooms/intro.yaml`](./rooms/intro.yaml) `view:` |
| `expect_jobs:` test-runner assertion | Locks down which jobs were dispatched in a turn (handler / cmd / argv) so refactors that silently re-wire a job fail loudly | [`flows/hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml); [`flows/river_ford_no_confidence.yaml`](./flows/river_ford_no_confidence.yaml) |
| Friendly clarification rendering | The runtime renders `host.RequestClarification` requests through a templated view so the player sees a prompt, not raw protocol JSON | [`rooms/inbox.yaml`](./rooms/inbox.yaml); flow [`flows/hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml) |
| Replay-coercion for typed slots in recordings | `recording.yaml` carries `slots: { quantity: "6" }` (string from the LLM); the engine coerces to `int` on replay so the deterministic harness doesn't choke | [`recording.yaml`](./recording.yaml) |

---

## 4. Recipes — porting these patterns to your own app

Pick the pattern that matches your problem and copy the Oregon Trail
shape. Every recipe below has a working implementation in this
directory; the "real-world fits" column names the places these
primitives earn their keep outside a wagon game.

The reason OT is useful as the *prototype* of each pattern (rather
than the real app it ports to) is that OT proves the kitsoki primitive
expresses the shape **without** the external dependencies a real
target would need. A real "buy supplies" is opening a PR — which needs
a code host, an auth token, a webhook listener. A real "river
crossing" is picking a deploy strategy — which needs a deploy
controller, real infrastructure, observability. OT lets you settle
the *state-machine* design — the rooms, the proposal lifecycle, the
guard hints, the failure arcs — against zero external systems. Once
the shape is right, swapping in the real handler for `host.run "true"`
is a one-line change. Each recipe below names both the OT instance
*and* the real-world systems you'd be wiring against when you port —
so you can copy the shape first, replace the surface second.

### 4.1 Buy-supplies proposal — *draft → review → accept → execute*

**OT shape.** `proposals.buy_supplies` declares the schema, draft
prompt, refine prompt, executor, and policy. The hosting room
(`general_store`) is a compound with `idle` / `drafting` /
`reviewing` / `executing` / `done` substates. `propose_purchase`
writes to a set of `world.proposal_*` keys; `accept_purchase` on
`reviewing` runs the actual world mutation (debit money, credit
each inventory key). `policy.auto_accept_if` short-circuits the
review for trivial baskets; `policy.repeatable: true` arms a
`repeat` intent on `done`.

**Abstract pattern.** "Player drafts a structured action, the system
costs / validates it, the player accepts (or refines), the system
executes." The draft state holds rich preview text; the execute
state runs the irreversible step.

**Real-world fits.**

- **Open a pull request.** Draft title + body + reviewers + labels;
  the reviewing state shows the rendered diff and the PR-template
  checklist; auto-accept if the PR is a docs-only change under N
  lines; execute calls `gh pr create`.
- **File a Jira ticket.** Slot-fill `project / summary / issuetype
  / description`; review with a synthetic preview; auto-accept for
  trusted reporters; execute via the Jira REST API.
- **Queue a deploy.** Slot-fill `service / version / env /
  rollout_strategy / notes`; reviewing shows the cost / risk model;
  require explicit confirm; execute calls the CD trigger.
- **Send a Slack message with @-mentions.** Slot-fill `channel /
  text / mentions / thread_id`; reviewing renders the message as
  Markdown with @-resolution; auto-accept short messages from the
  same user.

### 4.2 Trail-leg phase template — *one shape, instantiated N times*

**OT shape.** `phase_templates.trail_leg` declares one parameterised
template with parameters `from_landmark` / `to_landmark` /
`distance_mi` / `terrain` / `ends_at_river` / `ends_at_fort` /
`arrival_hint` / `river_base_depth_ft` / `river_width_ft`. The
template defines three states (`_executing`, `_awaiting_reply`,
`_error`) and the expander stamps out 7 instances driven by
`phases.graph`. `checkpoint_intents:` from `app.yaml` are merged
into every `_awaiting_reply`; `cycle_budgets: { on_failure: 2 }`
caps retries per leg.

**Abstract pattern.** "Repeated pipeline stage with a checkpoint
between iterations." The template is the unit of reuse; the
checkpoint is the place to surface human-visible status and
intervention verbs.

**Real-world fits.**

- **Per-environment promotion in CD.** Each environment (`dev` /
  `staging` / `prod`) is a phase instance with the same template
  (deploy → smoke-test → approve/deny). `next.on_failure:` rolls
  back; `cycle_budgets:` caps redeploys per stage.
- **ML training-eval cycles.** Each `(train, eval)` pair is a phase
  instance parameterised by hyperparameters; the checkpoint shows
  the eval metrics and offers `continue` / `revert` / `adjust`.
- **Multi-step approval workflows.** Each approver is a phase
  instance; the `_awaiting_reply` checkpoint is the approver's
  inbox view.

### 4.3 River-crossing compound + background execute — *pick a strategy, run it async*

**OT shape.** `river_crossing` is a compound with `initial:` chosen
from `world.river_depth_ft` (shallow / mid / deep). Each substate
hosts `propose_crossing` which writes the proposal draft; on
`accept_crossing` the room dispatches a background `host.run` (the
"cross") with `on_complete:` consuming `world.last_job_result` to
classify the outcome (`safe` / `swept_supplies` / `drowned`).

**Abstract pattern.** "User picks a strategy from N typed options;
the system runs it asynchronously; the player observes the outcome
and reacts." Slot value is the discriminator; the background-job
result is the outcome.

**Real-world fits.**

- **"Run the test suite with strategy X."** `strategy: enum [
  fail-fast, retry-flaky, full-rerun ]`; background job is the
  test runner; `on_complete:` formats the failure list.
- **"Build with caching X."** `cache: enum [ cold, warm, paranoid
  ]`; background job is the build; `on_complete:` posts the
  timings and artifact link.
- **"Deploy with rollout strategy X."** `rollout: enum [ blue-green,
  canary, all-at-once ]`; background job is the orchestrator call;
  `on_complete:` reads the new revision and reports.

### 4.4 Disease MCP-typed diagnosis — *force the LLM to return validated JSON*

**OT shape.** `event_disease.on_enter` carries a narrated arm
(`when: world.narration`) that invokes `host.agent.decide`
with `agent: frontier_doctor`, prompt path
`prompts/event_disease.md`, and an MCP server that registers the
`submit` tool. [`mcp/illness.json`](./mcp/illness.json) defines the
JSON schema (`illness` enum × `severity` 1-5 × `treatment` enum).
The handler validates the tool-call arguments; the result is bound
into `world.illness_kind` / `illness_severity` / `illness_treatment`.
`on_error: {{ tpl.id }}_error` redirects on handler failure.

**Abstract pattern.** "Make the LLM return data we can switch on,
not prose we have to re-parse." The MCP submit tool is the LLM's
*only* output channel for that turn; the validator rejects
non-conforming calls.

**Real-world fits.**

- **Triage classification.** Schema is `{ category, severity,
  owner_hint }`; the agent reads the incident text and submits a
  triage row.
- **Severity scoring.** Schema is `{ severity: 1-5, rationale_tag:
  enum }`; downstream alerting routes on the integer.
- **PII redaction tag set.** Schema is `{ spans: [{ start, end,
  kind: enum }] }`; the editor uses the spans directly.

### 4.5 Wagon-master conversational chat — *persistent advisor scoped to a key*

**OT shape.** `trail_guide` is a compound with `mode: conversational`
substates. `trail_guide_list.on_enter` calls `host.chat.list` with
`scope_key: "{{ world.profession }}"` so each profession sees its
own chat history. `ask_question` either creates a new chat
(`host.chat.create`) or continues an existing one
(`host.chat.resolve_ref` from a positional / prefix / ULID ref);
each turn calls `host.agent.converse` with the resolved `chat_id`.
Auto-titling fires at turn 3 via `host.chat.suggest_title`.

**Abstract pattern.** "Persistent advisor scoped to a user, project,
or topic — history survives sessions, conversations are
addressable, and the model speaks in a consistent voice via a
named agent."

**Real-world fits.**

- **Ops runbook assistant** scoped by service name; each service
  gets its own thread of advice, and the agent persona is "SRE
  pair with this service's runbook in context."
- **Design-doc reviewer** scoped by doc ID; the assistant remembers
  what it suggested last time you edited the same doc.
- **On-call interpreter** scoped by user; conversations remember
  the runbook style each on-call engineer prefers.

### 4.6 Background hunt with mid-flight clarification — *long-running job pauses to ask one structured question*

**OT shape.** `hunt_running.on_enter` dispatches a `host.run`
background job (stubbed in flow fixtures via `host_handlers:` +
`delay:`). The handler may call `host.RequestClarification(ctx,
schema)` mid-flight, which posts an `action_required` inbox
notification. The player teleports to `inbox` (with
`push_history: false`), reads the prompt, and dispatches
`answer_clarification` with the chosen slot; the handler resumes
the job with the answer; `on_complete:` proceeds normally.

**Abstract pattern.** "Long-running automated job pauses to ask the
user one structured question, then resumes." The clarification is
typed so the resume path doesn't have to re-prompt-engineer the
LLM.

**Real-world fits.**

- **Schema-migration approval.** Job scans the diff; mid-run, asks
  "this column has nullable rows — drop them, default-fill, or
  abort?"
- **Ambiguous auto-fix.** Long lint-fix run pauses to ask "two
  candidates for this name — picked X or Y?"
- **Mid-publish version bump.** "I'm about to publish; do you want
  me to bump the major (breaking) or minor?"

### 4.7 Snow-blocked South Pass — *block on a real-world condition, consume resources while waiting*

**OT shape.** [`rooms/snow_blocked.yaml`](./rooms/snow_blocked.yaml)
is an atomic state reached when `leg_e_awaiting_reply.continue`
sees `world.month` in `[oct, nov, dec, jan, feb]`. `wait_for_spring`
self-transitions, draining food and health, advancing day / month /
year, until `world.month == march` (next tick is april), at which
point it transitions back to `leg_e_awaiting_reply`. If food
crosses zero before spring, it lands in `ended_lost` instead.

**Abstract pattern.** "Block on a real-world condition that's not
under the user's control; the user can wait (consuming a budget)
or give up." The waiting state is its own state — not a busy loop
— so the engine can show the player progress and let them quit.

**Real-world fits.**

- **Wait for an upstream PR merge.** Self-transition every poll
  interval; consume "review-window" time budget.
- **Wait for a green build.** Self-transition until a polled
  status equals `passing`; give up after N minutes.
- **Wait for a flaky service to recover.** Self-transition consumes
  a per-minute cost budget; the player can give up and reroute.

### 4.8 Off-path frontier_guide — *side conversation that doesn't change on-path state*

**OT shape.** `app.yaml` declares
`off_path: { trigger: "/freeform", banner, return: "/onpath",
agent: frontier_guide }`. The engine handles the rest: anywhere
the player types `/freeform`, the run forks into a side
conversation scoped to the off-path chat thread, with the
`frontier_guide` agent's `system_prompt` applied. `/onpath`
returns. On-path world is untouched.

**Abstract pattern.** "Ask the assistant a generic question without
leaving the current task." The side conversation is its own chat
scope so it doesn't pollute the on-path agent's context.

**Real-world fits.**

- **"What's the API signature for X?"** mid-PR draft — answer
  without losing the PR title and body you've been writing.
- **"Translate this stack trace"** mid-debug-session, without
  losing the active hypothesis context.
- **"What does flag --foo do?"** mid-deploy-plan.

### 4.9 Trail-diary transport posts — *render session history on an external thread*

**OT shape.** Every `_awaiting_reply.on_enter` calls
`host.transport.post` with a templated `phase_id:` (`{{ tpl.leg_id
}}_arrive`), a title, and a Markdown body. The same effect lights
up at every event resolution, death, and win. Configured to `tui`
in flow fixtures; switch to `jira` by binding the session to a
Jira issue key at session-create time and the same state machine
posts to the ticket instead.

**Abstract pattern.** "A session's history is readable as comments
on an external thread the team already watches." `phase_id:`
de-dups on re-run; `bot_marker:` filters out the assistant's own
posts.

**Real-world fits.**

- **Drive a multi-step task from a Jira ticket.** Every state
  transition posts a comment; the assignee follows along.
- **Render a deploy timeline as Slack thread updates.** Each
  pipeline stage posts; the dedup ensures retries don't double
  the timeline.
- **Append a runbook trace to a PagerDuty incident.** The session
  thread becomes the incident timeline.

### 4.10 Parallel weather + emit — *two orthogonal state axes, cross-region events*

**OT shape.** `world_clock` is `type: parallel` with two sibling
compound regions: `weather` (dry → rain → snow transitions) and
`calendar` (a witness region binding the cross-region intents).
The `weather` region's `on:` arms include `emit: precip_heavy` and
`emit: snow_starts` effects; the `calendar` region has matching
`on:` map entries that set `world.precip_observed` /
`world.snow_observed`.

**Abstract pattern.** "Two state axes evolve independently in the
same session; transitions in one axis emit events the other axis
listens for."

**Real-world fits.**

- **Connection-state + idle-timer.** One region tracks `connected
  → disconnected`; the other ticks an idle timer. Idle-timer
  reset on incoming-message events emitted from the connection
  region.
- **Build-pipeline + test-pipeline.** Build emits `artifact_ready`;
  test listens and starts. Build-fail emits `build_failed`; test
  region cancels.
- **Chat-presence + chat-typing.** Presence emits `online` /
  `offline`; typing region resets indicators on transitions.

### 4.11 Timeout on idle — *auto-progress when the user is idle past a threshold*

**OT shape.** Every `_awaiting_reply` carries `timeout: { after:
"10d", target: "{{ phase.next.continue }}" }`. The phase expander
substitutes `phase.next.continue` per leg; the orchestrator
dispatches the timeout fire when virtual time exceeds the
threshold. `flows/landmark_timeout.yaml` exercises the arc via
`advance_clock: "11d"`.

**Abstract pattern.** "Auto-progress the state machine if the user
hasn't replied within a deadline."

**Real-world fits.**

- **Escalate a stale review request.** `timeout: { after: "48h",
  target: escalate }` on every `_awaiting_reply` for the original
  reviewer.
- **Expire an unanswered approval.** Approval request times out
  to `cancelled` after the SLA window.
- **Close a stalled debug session.** A debug-room times out to a
  cleanup state after 30 idle minutes.

### 4.12 Refine in place — *edit a draft without losing context*

**OT shape.** `general_store.reviewing.refine_purchase` is a
self-transition that merges supplied slots against the existing
draft using `?? world.proposal_*` defaults (so omitting a slot
keeps the current value). `proposal_refine_count` is incremented
for diagnostics. The state never leaves `reviewing`, so the player
sees the updated draft on the next render.

**Abstract pattern.** "Tweak a draft without re-opening the
drafting state." Slot-level merge preserves keys the user didn't
mention.

**Real-world fits.**

- **Tweak a PR title without re-opening the PR draft.** `refine
  { title?: string, body?: string }`; only the supplied field
  changes.
- **Adjust a deploy-plan parameter** (`canary_pct` from 10 to 25)
  without re-keying the rest.
- **Edit a search query in place** without resetting filters.

### 4.13 Named agents — *different personas in one app*

**OT shape.** `app.yaml` declares five agents
(`frontier_guide`, `wagon_master`, `party_namer`, `trail_narrator`,
`frontier_doctor`). Each agent invocation passes `agent: <name>`;
the engine threads the agent's `system_prompt` (and tools, if
declared) through to the handler. `off_path.agent:` references the
same primitive — the old inline `persona:` shortcut is now a
binding into the `agents:` block.

**Abstract pattern.** "One app, multiple voices. Each surface picks
its agent by name; the agent definition centralises the system
prompt and tool surface."

**Real-world fits.**

- **Triage agent / runbook agent / code-review agent / on-call
  agent** all in one repo-companion tool. Triage uses a strict
  classifier prompt; runbook uses a step-by-step walking prompt;
  code-review uses a critical-reviewer prompt; on-call uses a
  calm-incident-shepherd prompt.
- **Internal vs external persona.** `agent: support_external` for
  customer-facing replies; `agent: support_internal` for the same
  conversation's internal notes thread.

### 4.14 Engine-side menu helpers — *show what the user can and can't do*

**OT shape.** Templates inside `view:` blocks call `available("intent
_id")` and `blocked_reason("intent_id")` to render an action list
with the available actions in plain text and the blocked ones with
a one-line reason. `rooms/intro.yaml`'s `start_journey` is the
canonical example — gated until the party is named, profession is
chosen, and a month is picked.

**Abstract pattern.** "Tell the user not just what to do, but what
*can't* be done right now and why." The menu is generated from the
state's `on:` map plus its guards; the author doesn't hand-maintain
a parallel "available actions" list.

**Real-world fits.**

- **"You can't deploy yet — build hasn't passed."** Same primitive,
  shows the reason next to the greyed-out verb.
- **"Approve" disabled until tests are green** — the menu helper
  reports `tests_passing == false` as the blocker.

### 4.15 Meta-mode side conversations — *edit the app from inside the app*

**OT shape.** `meta_modes.story:` points at the built-in
`story-author` agent. `/meta story` opens a persistent chat against
that agent with full filesystem tool access scoped to the story
directory. The user can say *"the wagon master should sound a little
gruffer when the team is low on food"* and the agent edits
`stories/oregon-trail/app.yaml` directly; the orchestrator detects
the YAML change via an mtime walk and hot-reloads the app. A second
mode `meta_modes.consult:` reuses the `wagon_master` agent at the
meta layer — same voice as the in-game trail_guide chat, scoped to
`(app, meta:consult, state)` so a conversation that started at the
Kansas River resumes there.

**Abstract pattern.** Persistent, persona-tagged side conversations
that *can* edit application state. Distinct from `off_path:` (which
is a one-shot agent dip and explicitly cannot touch state) and
from `mode: conversational` rooms (which are chats *inside* the
story graph). Meta modes live above the graph and can fork the app
itself.

**Real-world fits.**

- **"Tweak this PR title"** mid-review — `/meta refine` opens a
  long-form chat with an agent scoped to the PR repo dir; the agent
  edits the PR title / description; the kitsoki app hot-reloads
  with the new draft.
- **Runbook iteration** — `/meta runbook` opens a chat with a
  domain-tuned agent (`agent: ops-engineer`); the agent reads
  recent incident transcripts via its tool surface and proposes
  edits to the runbook YAML; the running session re-loads with
  the new playbook.
- **Multi-persona debugging** — declare `agents: { triage, fix, postmortem }`
  and `meta_modes: { triage, fix, postmortem }` pointing at each.
  The user switches between meta-modes mid-session for the
  appropriate persona; each retains its own persistent chat.
- **Story rehearsal vs. story authoring** — the on-path graph is
  rehearsal (rules + state). `/meta story` is authoring
  (edit the rules in place). The split keeps the running game
  immutable while allowing structured edits.
- **What OT proves without external state.** A real
  "edit a PR" meta-mode needs a code-host token, a webhook listener,
  and a clean rollback if the agent's edit breaks something. OT lets
  you settle the *meta-mode shape* — when to persist, how to scope
  the chat, what the return message looks like, whether the agent
  can write the app dir — entirely against the YAML on disk. Swap
  the agent's tools for code-host operations afterward.

---

## 5. File layout

```
stories/oregon-trail/
  app.yaml                 — meta, hosts allow-list, world schema,
                             agents:, off_path:, checkpoint_intents:,
                             include: globs
  phases.yaml              — trail_leg phase template + 7 instances,
                             event substates, traveling.continue
                             guarded arms, cycle budgets, snow-blocked
                             routing, narrated-mode agent calls,
                             host.transport.post emissions
  proposals.yaml           — buy_supplies + river_strategy proposal
                             kinds; schema, draft/refine prompts,
                             execute (sync no-op for buy; background
                             host.run for river), policy
  intents.yaml             — intent library: ~20 named intents with
                             slots, examples, format hints
  recording.yaml           — (state, input) → (intent, slots) table
                             for the static / replay harness
  rooms/
    intro.yaml             — atomic; party naming, profession,
                             month, start_journey gating
    general_store.yaml     — proposal-host for buy_supplies (idle /
                             drafting / reviewing / executing / done)
    fort.yaml              — proposal-host for buy_supplies with
                             local_price_pct: 150
    river_crossing.yaml    — compound with shallow / mid / deep
                             substates; hosts river_strategy
    hunt.yaml              — hunt_idle / hunt_running; background
                             job + mid-flight clarification
    rest_room.yaml         — rest_idle / rest_running; background
                             sleep
    trail_guide.yaml       — mode: conversational + host.chat.*;
                             trail_guide_list / trail_guide_active
                             / trail_guide_active_new
    inbox.yaml             — pending notifications;
                             answer_clarification resume;
                             teleport-back with push_history: false
    snow_blocked.yaml      — atomic; wait_for_spring loop or give_up
    world_clock.yaml       — compound type: parallel; weather +
                             calendar regions; emit:-driven
                             cross-region propagation
    ended.yaml             — ended_won + ended_lost terminals
  prompts/
    buy_draft.md           — basket-drafting prompt (narrated mode)
    buy_refine.md          — basket-refining prompt
    event_*.md             — one per event kind: disease, breakdown,
                             weather, encounter, supply_loss
    landmark_arrival.md    — landmark arrival prose prompt
    name_party.md          — party-naming prompt
  mcp/
    illness.json           — MCP submit-tool schema for the typed
                             illness diagnosis
    party_names.json       — MCP submit-tool schema for the typed
                             five-name party roster
  flows/                   — Mode 2 fixtures; see §6
  intents/                 — Mode 1 fixtures; see §6
  drive_scripts/
    win.txt                — plain-text driver script for the
                             future `kitsoki drive` CLI
  APP.md                   — `kitsoki render` output; byte-exact
                             schema view
  README.md                — this file
```

---

## 6. Test fixtures

Every shipped flow fixture pins one feature surface. The set covers
the union of mechanics in §3 with no overlap by design.

All fixtures run in deterministic mode with **zero external
dependencies**: no LLM, no network, no real Jira / Slack / code-host
account. The narrated-mode flow (`narrated_smoke.yaml`) stubs every
`host.agent.*` call via `host_handlers:` so it too runs in CI without
secrets. `kitsoki test flows stories/oregon-trail/app.yaml` is the
single command — it works on a laptop with `go` installed, no other
setup.

### 6.1 Flow fixtures (Mode 2 — state-machine assertion)

| Fixture | What it pins |
|---|---|
| [`approach_river.yaml`](./flows/approach_river.yaml) | Phase-parameter river geometry: `river_depth_ft` / `river_width_ft` set from the leg's `tpl.river_base_depth_ft` × month-modulated snowmelt; lands in the correct `shallow`/`mid`/`deep` substate. |
| [`buy_proposal_auto_accept.yaml`](./flows/buy_proposal_auto_accept.yaml) | `policy.auto_accept_if: total_cost < 5` bypasses `reviewing` for $1 baskets. |
| [`buy_proposal_refine.yaml`](./flows/buy_proposal_refine.yaml) | `refine_purchase` self-stays in `reviewing` and merges supplied slots against the existing draft. |
| [`buy_then_breakdown.yaml`](./flows/buy_then_breakdown.yaml) | `accept_purchase` credits every inventory key implied by the basket (wheels / clothing / bullets — not just oxen and food). |
| [`disease_with_mcp.yaml`](./flows/disease_with_mcp.yaml) | Narrated `event_disease.on_enter` round-trips through `host.agent.decide` with stubbed typed-JSON `submitted:` payload; world keys are bound. |
| [`host_failure_recovery.yaml`](./flows/host_failure_recovery.yaml) | `on_error:` redirect: stubbed `host.run` / `host.agent.ask` infra error routes to `{{ tpl.id }}_error`. |
| [`hunt_with_clarification.yaml`](./flows/hunt_with_clarification.yaml) | Full background-hunt lifecycle: submit → mid-flight `RequestClarification` → inbox `action_required` → `answer_clarification` → resume → complete; `expect_jobs:` and `expect_inbox:` both assert. |
| [`illness_status_visible.yaml`](./flows/illness_status_visible.yaml) | `illness_*` world keys are surfaced on the trail leg's `relevant_world:` so they persist into the next leg's view. |
| [`illness_twice.yaml`](./flows/illness_twice.yaml) | `event_disease.on_enter` zeroes `current_event_attempts` so a second illness later in the trail still allows the cycle-budget retries. |
| [`landmark_timeout.yaml`](./flows/landmark_timeout.yaml) | `advance_clock: "11d"` at `leg_a_awaiting_reply` fires the `Timeout:` arm; lands on the next leg. |
| [`loader_negative_background.yaml`](./flows/loader_negative_background.yaml) | Disabled placeholder — `background: true` inside `on_complete:` rejection (needs `expect_load_error:` runner support). |
| [`loader_negative.yaml`](./flows/loader_negative.yaml) | Disabled placeholder — undeclared `relevant_world:` key rejection (same runner gap). |
| [`losing_starvation.yaml`](./flows/losing_starvation.yaml) | Starvation guard on `traveling.continue` routes to `ended_lost`. |
| [`month_mechanics.yaml`](./flows/month_mechanics.yaml) | Three arcs in one fixture: snow-blocked South Pass routing; `wait_for_spring` loop; pass-opens-on-april return. |
| [`weather_by_month.yaml`](./flows/weather_by_month.yaml) | Pins the month-biased weather-kind selector in `event_weather.on_enter`: a december-seeded weather event resolves to `snow` regardless of the raw rng roll. |
| [`narrated_smoke.yaml`](./flows/narrated_smoke.yaml) | Narrated-mode round-trip: every event substate's narrated arm lights up; stubbed `host.agent.ask` returns canned prose. |
| [`parallel_weather.yaml`](./flows/parallel_weather.yaml) | `world_clock` parallel region's weather → calendar `emit:` propagation; both witnesses flip. |
| [`party_naming_narrated_agent.yaml`](./flows/party_naming_narrated_agent.yaml) | Narrated `generate_names` threads `agent: party_namer` through to the handler. |
| [`party_naming.yaml`](./flows/party_naming.yaml) | Deterministic `name_party` / `name_member` / `generate_names` all populate `party_member_*` and `party_names`. |
| [`plague_at_chimney.yaml`](./flows/plague_at_chimney.yaml) | Two failed `treat` attempts exhaust the cycle budget; party loses a member; event resolves to traveling. |
| [`rest_in_camp.yaml`](./flows/rest_in_camp.yaml) | Rest background sleep, `on_complete:` restores health, advances day, debits food. |
| [`river_ford_drown.yaml`](./flows/river_ford_drown.yaml) | `river_strategy` proposal in `river_crossing.deep`; ford → drown outcome from the background-job result. |
| [`river_ford_no_confidence.yaml`](./flows/river_ford_no_confidence.yaml) | `Slot.Default` filling: `propose_crossing` without `confidence` doesn't crash the templated cost math. |
| [`trail_diary_smoke.yaml`](./flows/trail_diary_smoke.yaml) | `host.transport.post` fires at landmark arrival, event resolution, and death. |
| [`trail_guide_smoke.yaml`](./flows/trail_guide_smoke.yaml) | Persistent chat: list → ask (creates) → ask (continues) → back. Stubs `host.chat.*` + `host.agent.converse`. |
| [`winning_deterministic.yaml`](./flows/winning_deterministic.yaml) | The canonical happy path: 15 turns, Independence → Willamette, every leg's no-event default arm. |

Running `go run ./cmd/kitsoki test flows stories/oregon-trail/app.yaml`
ships 23 / 23 PASS (the two `loader_negative*` fixtures are
deliberately disabled placeholders until the runner gains
`expect_load_error:`).

### 6.2 Intent fixtures (Mode 1 — LLM intent routing)

| Fixture | State | What it pins |
|---|---|---|
| [`ask_question.yaml`](./intents/ask_question.yaml) | `trail_guide.trail_guide_list` | Wagon-master question-slot extraction; "ask what to do at the kansas river crossing" routes to `ask_question { question: ... }`. |
| [`buy_supplies.yaml`](./intents/buy_supplies.yaml) | `general_store.idle` | Free-form basket → `propose_purchase { items, total_cost }`. |
| [`checkpoint_verbs.yaml`](./intents/checkpoint_verbs.yaml) | `leg_c_awaiting_reply` | `continue` / `quit` / `restart_from { stage }` / `consult_guide` plus adversarial "bare-go". |
| [`consult_guide.yaml`](./intents/consult_guide.yaml) | `leg_a_awaiting_reply` | Period + modern phrasings route to `consult_guide`. |
| [`ford_caulk_ferry.yaml`](./intents/ford_caulk_ferry.yaml) | `river_crossing.shallow` | Disambiguates `ford` / `caulk` / `ferry`. |
| [`hunt_vs_rest.yaml`](./intents/hunt_vs_rest.yaml) | `leg_b_awaiting_reply` | "Let's hold up here" routes to `rest`, not `hunt`. |
| [`name_party_theme.yaml`](./intents/name_party_theme.yaml) | `intro` | Themed party-naming patterns route to `generate_names`, not bare `name_party`. |
| [`pick_profession.yaml`](./intents/pick_profession.yaml) | `intro` | Casual "make me a banker" phrasings route to `pick_profession`. |
| [`refine_purchase.yaml`](./intents/refine_purchase.yaml) | `general_store.reviewing` | "Actually 250 lbs of food" routes to `refine_purchase { food: 250 }`. |
| [`restart_from.yaml`](./intents/restart_from.yaml) | `leg_e_awaiting_reply` | "Go back to Fort Kearney" routes to `restart_from { stage: kearney }`. |

Running `go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml
--harness static` ships 50 / 50 PASS.

---

## 7. Demo commands

```bash
# Render the player-facing Markdown view (this directory's APP.md).
go run ./cmd/kitsoki render -o stories/oregon-trail/APP.md \
    stories/oregon-trail/app.yaml

# State-machine visualization.
go run ./cmd/kitsoki viz stories/oregon-trail/app.yaml \
    --out demo/oregon-trail.dot
go run ./cmd/kitsoki viz stories/oregon-trail/app.yaml --mermaid \
    --out demo/oregon-trail.mmd

# PNG (requires Graphviz; not bundled in this branch's dev container).
#   dot -Tpng demo/oregon-trail.dot -o demo/oregon-trail.png

# Animated GIF of the winning playthrough.
go run ./cmd/kitsoki record stories/oregon-trail/app.yaml \
    --flow stories/oregon-trail/flows/winning_deterministic.yaml \
    -o demo/oregon-trail-win.gif

# Run the flow + intent fixtures.
go run ./cmd/kitsoki test flows   stories/oregon-trail/app.yaml
go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml \
    --harness static
```

[`drive_scripts/win.txt`](./drive_scripts/win.txt) is a plain-text
script of human-typed inputs ready for the future (unshipped)
`kitsoki drive` CLI — see
[`docs/proposals/ai-collaboration-proposal.md`](../../docs/proposals/ai-collaboration-proposal.md).

Pre-built artifacts live under [`../../demo/`](../../demo/):
`oregon-trail.dot`, `oregon-trail.mmd`, `oregon-trail-win.gif`.

---

## 8. Known engine gaps

These are the four real follow-ups still open after the
implementation closed the three §9 proposal gaps (`type: parallel`,
`Timeout:`, MCP illness schema). Anyone touching the engine should
read these first.

- **`internal/app/phases.go::substString` ternary substitution.** The
  expander's `templateRE` regex matches bare `{{ tpl.X }}` and
  `{{ phase.next.X }}` placeholders but does not walk ternary
  expressions inside `{{ … }}` blocks. So an expression like
  `{{ tpl.ends_at_river ? 'shallow' : 'deep' }}` leaves `tpl.X`
  unbound at runtime. Worked around by passing per-leg
  `arrival_hint:` and `river_base_depth_ft:` as plain template
  parameters; the regression pin is
  [`flows/approach_river.yaml`](./flows/approach_river.yaml).
- **`fireTimeout` does not run `resolveInitial`.** The orchestrator's
  timeout fire path lands on a bare compound target (e.g.
  `leg_b_executing`) without drilling into its `initial:`
  substate. [`flows/landmark_timeout.yaml`](./flows/landmark_timeout.yaml)
  asserts the bare-state landing on purpose; flipping to the
  drilled-in landing is the follow-up.
- **`world_clock` is freestanding, not root-parallel with
  `trail_phases`.** The proposal's design called for the world
  clock to run *alongside* the trail as a sibling region under a
  parallel root. Promoting it would require a root refactor (the
  current `root:` is a single atomic intro state). Phase I left
  `world_clock` as a standalone compound parallel region that
  [`flows/parallel_weather.yaml`](./flows/parallel_weather.yaml)
  seeds directly into.
- **`world_override:` collides with seed events on Turn 0.** Flow
  fixtures using `world_override:` on the first asserted turn can
  race the orchestrator's seed-event chain. Parked workaround: put
  `world_override:` on a later turn, or use the runner's
  `initial_world:` block.

The first two are real orchestrator bugs that should land in a
follow-up branch; the third is a deliberate scope decision (the
artifact still exercises every primitive — `flows/parallel_weather.yaml`
is the canonical proof); the fourth is a test-runner ergonomic.

---

## 9. References and sources

The port targets the 1985 MECC Apple II release as the canonical
reference. In order of authority:

1. Rawitsch, Heinemann, Dillenberger. *The Oregon Trail.* MECC,
   1971. Public-domain BASIC listings archived on the
   [Internet Archive](https://archive.org/details/the-oregon-trail-by-mecc)
   and reproduced in *Creative Computing*. Authoritative behaviour
   reference for the core loop (pace / rations / hunt / trade /
   rest).
2. *The Oregon Trail (1985 video game).* Wikipedia.
   <https://en.wikipedia.org/wiki/The_Oregon_Trail_(1985_video_game)>.
   Canonical landmark list, distances, event catalogue,
   party-of-five default.
3. *The Oregon Trail (series).* Wikipedia.
   <https://en.wikipedia.org/wiki/The_Oregon_Trail_(series)>.
4. NPS, *Oregon National Historic Trail*.
   <https://www.nps.gov/oreg/>. Geographic ground truth for
   landmark mileage; MECC distances diverge by ~±20 mi and MECC
   is the fidelity target.
5. Mattie, John D. *The Oregon Trail Diaries* (compiled emigrant
   journals, 1841–1869). Period vocabulary for narrated-mode
   prompts (*"axle tree broken,"* *"cholera,"* *"a band of
   Sioux"*).
6. Pohjola, Mike. Reverse-engineered MECC mechanics,
   <https://mikolaj.com/oregon-trail-data/>. Probability tables
   for events by pace × rations × month. Sanity-check only.
7. Lussenhop, Jessica. "Oregon Trail: How three Minnesotans
   forged its path." *MinnPost*, 2011.
   <https://www.minnpost.com/politics-policy/2011/01/oregon-trail-how-three-minnesotans-forged-its-path/>.
   Design intent.
8. Smithsonian Magazine. "The Oregon Trail: How the Iconic
   Computer Game Was Made," 2017.
   <https://www.smithsonianmag.com/innovation/oregon-trail-how-iconic-computer-game-was-made-180962319/>.
9. *Oregon Trail* (historical). Wikipedia.
   <https://en.wikipedia.org/wiki/Oregon_Trail>.
10. Firth, Roger. *Cloak of Darkness.*
    <http://www.firthworks.com/roger/cloak/>. Inspirational
    precedent — a small canonical game spec used to demo a tool.
    Oregon Trail is the bigger sibling.
11. In-tree kitsoki examples — `testdata/apps/cloak/`,
    `testdata/apps/dev-story/`, `testdata/apps/proposal_smoke/`,
    `testdata/apps/background_jobs/`. Each covers a slice of the
    engine; Oregon Trail covers the union.

Sources 1–9 inform the game model and narrative; sources 10–11
inform the shape of the kitsoki port. The 1971 BASIC source is
public domain; the 1985 art and music are not redistributed —
only rules, landmark names, and event names are reproduced.
Narrated-mode prose is LLM-generated at runtime; nothing
copyright-encumbered is checked in.

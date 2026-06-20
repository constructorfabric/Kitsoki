# off-ramp-demo — the no-match door, showcased

The smallest complete demo of the **agent off-ramp**: a discovery menu that
answers off-menu questions *in place* instead of bouncing them back to the
menu.

## What the off-ramp is

In a room that opts in with `agent_off_ramp:`, a free-text utterance the
router can't map to any declared intent is handed to a voiced
`host.agent.converse` turn — and the free-form answer comes back as a
`ModeOffPath` outcome **without advancing the state machine or mutating
world**. The room stays put; the same menu is there next turn. The *decision*
to off-ramp is deterministic (a room flag × a no-match code); the converse
*answer* is interpretive (and recorded). It is the **no-match door** into the
same free-form mechanism `off_path:` reaches through its typed-trigger door.

A load-time invariant: the off-ramp is **rejected on a `terminal: true` or
`mode: conversational` state**, so it belongs on a normal resting menu room —
exactly the `desk` room here.

## The story

One non-conversational discovery room, `desk` ([rooms/desk.yaml](rooms/desk.yaml)),
with:

- a real menu of three declared intents — **browse**, **status**, **about** —
  that visibly transition (plus `quit` / `look`), and
- the implicit free-text composer, with `agent_off_ramp:` in the **struct
  form**: a friendly-guide `persona:` and a `banner: "(off the menu — just
  answering)"` so the off-ramp voice is distinct.

Pick a menu item and you transition normally. Ask a question the menu can't
answer and the off-ramp voices a helpful reply in place.

## Run it (deterministic, no LLM)

Free-text routing uses the **replay** harness; the off-ramp's converse voice is
stubbed by a **host cassette**. The two combine:

```sh
kitsoki web \
  --harness replay \
  --recording stories/off-ramp-demo/assets/recording.yaml \
  --host-cassette stories/off-ramp-demo/assets/converse-cassette.yaml \
  --stories-dir stories \
  --addr 127.0.0.1:7799
```

Open http://127.0.0.1:7799, start an **Off-Ramp Demo** session, and you land in
the `desk` room.

- **On the menu:** click `browse` / `status` / `about` — each transitions and
  updates "last visited".
- **Off the menu:** type **`why should I trust an AI with my project?`** in the
  composer. The router can't map it to any intent, so the off-ramp fires: the
  guide answers in place, the menu is unchanged, and you're still in `desk`.

### How the deterministic posture works

- [`assets/recording.yaml`](assets/recording.yaml) — a single `clarify: true`
  entry: in `desk`, the off-menu input maps to a **no-match** (the replay
  harness returns a `*ClarifyResponse`), which fires the off-ramp.
- [`assets/converse-cassette.yaml`](assets/converse-cassette.yaml) — one
  `host.agent.converse` episode returning a fixed, on-theme answer (read from
  `Result.Data.answer`), so the rendered frames are byte-stable.
- Menu picks are **explicit intents** (`runstatus.session.submit`) — they need
  no recording entry and no harness.

## The contract a renderer consumes

On an off-ramp hit the turn result serializes as:

```json
{ "mode": "offpath", "view": "<the guide's answer>",
  "state": "desk", "allowed_intents": ["browse","status","about","quit","look"] }
```

`state` is the unchanged resting room and `allowed_intents` is the same menu,
echoed unchanged — so the web SPA renders the answer as an agent bubble and the
menu persists. Persisted events on a hit: `OffPathEntered{reason:"off_ramp",
error_code:"LLM_CLARIFICATION"}`, `OffPathQuestion`, `OffPathAnswer`; there is
**no** `TurnEnded(rejected)` and **no** transition.

## Files

| Path | Role |
|---|---|
| [`app.yaml`](app.yaml) | manifest — one discovery room, minimal world/intents |
| [`rooms/desk.yaml`](rooms/desk.yaml) | the `desk` off-ramp room + its menu targets |
| [`views/base.pongo`](views/base.pongo) | app-wide base view |
| [`assets/recording.yaml`](assets/recording.yaml) | the clarify (no-match) that fires the off-ramp |
| [`assets/converse-cassette.yaml`](assets/converse-cassette.yaml) | the stubbed converse answer |
| [`flows/menu.yaml`](flows/menu.yaml) | no-LLM flow: the menu intents transition |

## See also

- [`docs/stories/state-machine.md` §11](../../docs/stories/state-machine.md) —
  off-path / the off-ramp.
- [`stories/routing-demo/`](../routing-demo/) — the four-tier router this
  off-ramp sits downstream of.

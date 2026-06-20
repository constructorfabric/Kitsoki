# Runtime: Live Operator Handoff (hybrid human + bot)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../line-messenger-channel.md

## Why

A customer channel is rarely fully automated. Most real deployments are
**hybrid**: the story handles the routine path, and a human steps in for the
long tail — a complaint, an edge-case booking, a question the agent shouldn't
guess at. The epic already covers the *narrow* hand-off: an agent calls
`mcp__operator__ask` and the operator-ask bridge surfaces a **structured
question** on the merchant console (`docs/architecture/operator-ask.md`). But
two things that hybrid operation needs are missing:

1. **Free-form takeover.** The operator wants to *chat directly* with the
   customer — type prose that goes straight to the LINE conversation — not only
   answer a multiple-choice agent question. While they do, **the bot must not
   reply over them**: inbound customer messages have to stop auto-driving turns.
2. **Notification of intervention requests.** When an agent asks for a human (or
   a customer messages while a human is handling), the operator must be *pulled
   in* — a notification, not a dashboard they have to be already watching.

Today the ingress (slice 1) auto-drives every inbound message, and the only
operator action on a session is a structured `transition` drive
(`hybrid-session-driving.md`). There is no "pause automation, I've got this"
mode and no push toward the operator.

## What changes

A session-level **handling mode** — `auto` (the story drives) vs `human` (the
operator drives) — that the ingress consults before routing, plus an
**operator-message** path that posts free-form prose to the customer through the
LINE transport (slice 2) attributed as the operator, and **notification events**
that pull the operator in. One sentence: **a session can be flipped to human-
handling, during which inbound customer messages are recorded and notified but
not auto-routed, and the operator's typed replies are delivered and traced as
operator turns.**

## Impact

- **Code seams:**
  - a reserved session/world attribute `handling_mode` (`auto` | `human`)
    read by the slice-1 ingress before it drives a turn;
  - the ingress, in `human` mode, records the inbound customer message to the
    trace and emits a notification event **instead of** routing
    (`internal/channel/` ingress, slice 1);
  - an operator-message send path: free-form text → LINE transport `Post`
    (slice 2) under the writer lock, recorded as an operator-origin datapoint;
  - notification events on the existing operator-ask / SSE feed
    (`internal/runstatus/server/operator_questions.go`).
- **Vocabulary:** the `handling_mode` attribute + an `handoff` effect a story can
  fire to request a human + two notification event kinds (table below).
- **Stories affected:** none required — `auto` is the default and existing
  stories never see it. A story *may* opt in by firing `handoff`.
- **Backward compat:** additive, default `auto`. With no console attached the
  notification is a no-op and the session stays automated (the headless
  contract, CLAUDE.md).
- **Tui spillover:** the operator **chat composer**, the notification surface,
  and the take-over / hand-back buttons live in the slice-4 console
  ([`line-channel-console.md`](line-channel-console.md)); this slice owns the
  engine seam + events they drive.
- **Docs on ship:** `docs/architecture/channels.md` handoff section,
  `docs/architecture/operator-ask.md` cross-link (structured ask vs. free-form
  takeover).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| attribute | `handling_mode` | `auto` \| `human` (default `auto`) | per session; read by ingress before routing |
| effect | `handoff` | `{reason?: string}` | a story fires it to set `human` + notify (e.g. from a `decide` that says "escalate") |
| host/seam | operator send | `(ctx, session, text, operator) → messageId` | free-form prose → LINE transport `Post`; traced as operator origin |
| event | `handoff.requested` | `{session, reason, by: agent\|operator\|story}` | notification trigger; rides the operator-ask SSE feed |
| event | `handoff.message` | `{session, src, text}` | a customer message arrived while `human` — record + notify, do not route |

## The model

```
inbound customer message (slice 1 ingress)
        │  read handling_mode(session)
        ├─ auto  ──▶ semroute → turn  (the normal path)
        └─ human ──▶ record + emit handoff.message ──▶ notify operator   (NO turn)

operator (slice-4 console):
   [take over] ─▶ handling_mode = human; emit handoff.requested(by: operator)
   type prose ─▶ operator-send ─▶ LINE transport Post (writer lock) ─▶ trace(operator origin)
   [hand back] ─▶ handling_mode = auto  (optionally re-route the last customer message)

agent path (unchanged + linked):
   mcp__operator__ask ─▶ operator-ask bridge ─▶ console question   (structured)
   story effect `handoff` ─▶ handling_mode = human; emit handoff.requested(by: agent|story)
```

**Interpretive vs. deterministic:** the *decision* to escalate, when a story
makes one, is the story's existing `host.agent.decide` (recorded as it already
is). Everything this slice adds — reading the mode, suppressing routing,
delivering operator prose, emitting notifications — is **deterministic**. No new
interpretive decision type; the moat is untouched.

The customer experiences no seam: bot replies and operator replies arrive in the
same LINE conversation. The **trace is the transcript** — each turn carries its
origin (story / agent / operator / customer), so the console transcript and any
audit show who said what.

## Decision recording

No new *decision*. New **lifecycle/transcript** facts: `handoff.requested` and
`handoff.message` events, and an operator-origin tag on operator-sent messages
(so a turn's principal — already resolved as `slots.author` via the hybrid-
driving identity contract — extends to free-form sends, not just structured
drives). The customer message recorded during `human` mode lands as an inbound
datapoint with no transition, preserving the full conversation in the trace even
when the bot didn't act.

## Engine seams & invariants

- **Mode read is on the hot path.** The ingress checks `handling_mode` *before*
  acquiring the turn — a `human`-mode session must never spin up an LLM turn for
  an inbound message. Cheap attribute read, no story load needed beyond the
  session.
- **Writer-lock serialization holds.** Operator sends and (paused) inbound
  recording both go through `store.WithWriterLock` — an operator message and a
  late auto-turn can't interleave. Flipping the mode is itself a locked write.
- **Resume safety.** On hand-back, the optionally-replayed last customer message
  routes through the normal `auto` path; if the story already advanced (operator
  drove a structured turn meanwhile) the replay is a no-op by dedup.
- **No-console ⇒ no escalation.** With no operator surface attached, `handoff`
  has no one to notify; the engine logs it and the story proceeds on `auto` (or
  the story's deterministic fallback), exactly as `mcp__operator__ask` degrades
  headless (CLAUDE.md). A story that *requires* a human in a path should gate it,
  not assume one is present.

## Backward compatibility / migration

Additive, default `auto`. Existing stories, cassettes, the poll bridge, and the
other transports are unchanged. The `handoff` effect and `handling_mode`
attribute are opt-in; a story that never uses them behaves exactly as today.

## Tasks

```
## 1. Engine
- [ ] 1.1 handling_mode session attribute (default auto); locked read/write
- [ ] 1.2 Ingress (slice 1): consult mode; human → record + emit handoff.message, no route
- [ ] 1.3 `handoff` effect → set human + emit handoff.requested
- [ ] 1.4 Operator-send seam: free-form text → LINE transport Post, traced operator-origin
- [ ] 1.5 handoff.requested / handoff.message on the operator-ask SSE feed

## 2. Verification
- [ ] 2.1 Unit: mode gate — inbound in human mode records + notifies, drives no turn; auto mode routes
- [ ] 2.2 Unit: operator-send posts via a cassette LINE transport, trace shows operator origin
- [ ] 2.3 Flow fixture: auto → handoff → (inbound recorded, not routed) → operator send → hand-back → auto routes again
- [ ] 2.4 Headless: handoff with no console logs + proceeds (no hang)

## 3. Document
- [ ] 3.1 channels.md handoff section + operator-ask.md cross-link; trim/delete this proposal; update epic slice row
```

## Verification

All no-LLM. The mode gate is a direct unit test on the ingress driver (human
mode ⇒ zero turns driven, one `handoff.message` emitted; auto ⇒ routed).
Operator-send is tested against the cassette LINE transport (slice 2) asserting
the recorded operator-origin datapoint. The full auto→handoff→operator→hand-back
cycle is an intent/event-only flow fixture. The headless-degradation case is a
nil-console unit test asserting no hang and a logged `handoff`.

## Open questions

1. **`handling_mode`: world key or session metadata?** A world key lets a story
   *read* its own handling state (e.g. a room that renders "an agent is with
   you" when `human`); session metadata keeps it out of the story's data model.
   *Lean: a reserved world key, so stories can react and it rides the existing
   world snapshot/trace — mirroring `slots.author`.*
2. **Notification delivery beyond the web console.** Web SSE + a browser
   notification covers an operator with the console open. Should an intervention
   request also push to email / a LINE OA the *operator* follows / the
   `PushNotification` surface? *Lean: web + browser notification in v1; external
   push (so the operator need not keep a tab open) as a fast follow — note it,
   don't build it.*
3. **Auto-timeout back to bot.** Should `human` mode auto-expire to `auto` after
   N minutes idle so a forgotten takeover doesn't strand a customer? *Lean: yes,
   configurable per channel, default off — surfaced in the console.*

## Non-goals

- The console chat composer / notification UI — slice 4 (this is its engine seam
  + events).
- Routing customer free text — that's `internal/semroute` via slice 1; this slice
  only *suppresses* it in `human` mode.
- A full agent-assist/co-pilot surface (suggested replies the operator edits) —
  a possible follow-up, out of scope here.

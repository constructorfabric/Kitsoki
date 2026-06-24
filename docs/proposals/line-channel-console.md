# TUI: LINE Channel Console (kitsoki as web presence)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../line-messenger-channel.md
**Depends on:** slice 1 (ingress/factory + channel config), slice 5 (handoff seam + events)

## Why

The epic positions kitsoki as the merchant's **web presence** — the place a
merchant authors the story, turns it on, and watches it serve customers. Slices
1–3 give the engine, the channel, and the stories, but a merchant has no way to
**provision** a channel (paste credentials, bind a story, get the webhook URL) or
to **see** the live customer sessions it's driving. Today the web UI
(`tools/runstatus/`, `internal/runstatus/server/`) is a *single-operator run
inspector*: it lists sessions and drives one. It has no notion of a channel, no
credential surface, and no "these N sessions are all customers of this channel"
grouping. Without this slice, slice 1's factory has no front door and the
operator-ask hand-off (epic Shared decision 4) has nowhere to land.

## What changes

A **Channels** area in the existing runstatus web UI with two surfaces:

1. **Provision an existing Official Account** — register the merchant's LINE OA:
   name, story binding, `channelSecret` + `channelAccessToken` (write-only,
   stored as injected secrets, epic Shared decision 3), the generated webhook URL
   `…/channels/line/:channelId` to paste into the LINE Developers console, a
   "verify" round-trip, and a checklist reminder to **disable the OA's built-in
   auto-reply** so kitsoki is the sole responder (epic Shared decision 5). No
   new-account flow — the merchant brings the OA they already run.
2. **Console (hybrid desk)** — a live list of the customer sessions a channel is
   driving (keyed `line:<channelId>:<src>`, epic Shared decision 1), each opening
   the **existing** `StoryViewer`/trace view (the transcript). On a session the
   merchant can:
   - **answer a structured agent ask** — the existing operator-ask inbox, now
     grouped by channel and **notified** (browser notification + a badge) so the
     merchant is pulled in rather than polling;
   - **take over** — flip the session to `human` handling (slice 5), type
     **free-form prose** in a chat composer that posts straight to the customer's
     LINE conversation, then **hand back** to automation. While `human`, the bot
     is paused and inbound customer messages show in the composer (a
     `handoff.message` event) instead of auto-replying.

One sentence: **the merchant's home — provision an existing OA and run it as a
hybrid human + bot desk — built on the existing runstatus RPC + SSE surface plus
slice 5's handoff events.**

## Impact

- **UI seams:** new Vue routes/views in `tools/runstatus/src/` (a `Channels`
  view + a per-channel `Console`), reusing the shipped `StoryViewer.vue` and the
  operator-questions SSE feed (`internal/runstatus/server/operator_questions.go`,
  `docs/architecture/operator-ask.md`).
- **RPC additions:** `runstatus.channels.list` / `.register` / `.verify`, a
  `runstatus.channels.sessions` listing sessions by channel, and the hybrid-desk
  pair `runstatus.channels.session.takeover` / `.handback` (slice-5
  `handling_mode`) + `.send` (slice-5 operator-send) — thin wrappers over the
  slice-1 channel config, `store` external-key lookup, and slice-5 seams. Mirrors
  the existing `runstatus.sessions.*` shape (`server.go:39`). Notifications ride
  the existing operator-questions SSE feed extended with `handoff.*` events.
- **Backend:** the channel config store (registration + secret storage) is the
  home for the credentials slice 1 reads; this slice owns it.
- **Stays typed + pongo2 / Vue:** no hand-rolled rendering; the console reuses
  the typed-view path the rest of the web UI uses.
- **Docs on ship:** `docs/tui/` channel-console page; the provisioning + webhook-
  URL flow referenced from `docs/architecture/channels.md` (slice 1).

## The surface

```
/channels                         ┌──────────────────────────────────────────┐
  ├─ [+ Add Official Account]     │ Channel: "Sakura Golf"  ● live    🔔 (2)  │
  │     name, story ▾,            │ webhook: …/channels/line/sakura   [verify]│
  │     channelSecret  (write)    │ ☑ auto-reply disabled in LINE OA Manager  │
  │     accessToken    (write)    │ ──────────────────────────────────────────│
  │                               │ Live customer sessions (line:sakura:*)     │
  └─ list of channels ● status    │  U1f3…  proposing_slot  2m   [open] 🔔 ask │
                                  │  Ua92…  ● human-handled just now [open]    │
                                  │ ──────────────────────────────────────────│
                                  │ Session U1f3…   [take over] / [hand back]  │
                                  │  customer: "do you do gluten-free?"        │
                                  │  bot:      "Here are our courses…"          │
                                  │  ┌──────────────────────────────────────┐ │
                                  │  │ type a reply to the customer…   [send]│ │
                                  │  └──────────────────────────────────────┘ │
                                  └──────────────────────────────────────────┘
        [open] ──▶ existing StoryViewer / trace (the transcript)
        [answer ask] ─▶ existing operator-ask answer RPC (resolves the agent's ask)
        [take over] ─▶ handling_mode=human (slice 5); composer sends prose to LINE
        🔔 ─▶ browser notification on handoff.requested / new operator ask
```

Opening a customer session reuses the shipped story-editor/viewer surface
(`docs/tui/story-editor.md`, `StoryViewer.vue`) and the hybrid-driving operator
identity (`hybrid-session-driving.md`) — a merchant can drive a structured turn
*or* take over with free-form prose, both serialized with the webhook under the
writer lock. The operator inbox is the *existing* operator-ask web feed, grouped
by channel and now **notified**; the chat composer + take-over/hand-back drive
slice 5's `handling_mode` + operator-send seam.

## Reuse / build

| Surface | Reuse | Build |
|---|---|---|
| Per-session view/trace (transcript) | `StoryViewer.vue`, `runstatus.session.*` | — |
| Structured agent ask | operator-ask web feed (`operator_questions.go`) | group by channel + **browser notification** |
| Operator structured drive | hybrid-driving identity + writer lock | — |
| **Free-form chat composer** | LINE transport (slice 2) + writer lock | composer view; `channels.session.send` RPC → slice-5 operator-send |
| **Take over / hand back** | slice-5 `handling_mode` seam | toggle + `human-handled` badge; consume `handoff.*` events on SSE |
| **Intervention notification** | operator-ask SSE + `handoff.requested` (slice 5) | browser-notification + per-channel badge |
| Channel registration + secrets | injected-secret pattern | channel config store + RPC |
| Session-by-channel list | `store.LookupByKey` reverse / key-prefix scan | `channels.sessions` RPC |
| Webhook URL + verify + auto-reply checklist | slice-1 handler | "verify" round-trip + setup checklist |

## Verification

- RPC unit tests (`internal/runstatus/server`) mirroring the existing
  `write_test.go` / `identity_test.go` style: `channels.register` stores config
  + secrets (write-only — never echoed back), `channels.sessions` lists by key
  prefix, `channels.verify` reports handler reachability, `takeover`/`handback`
  flip `handling_mode`, `send` reaches the (cassette) LINE transport. No LLM.
- A Vue unit/component test for the Channels view + console list + chat composer
  (mirrors `tools/runstatus/tests/unit/`).
- A Playwright walk (mirrors the existing specs under
  `tools/runstatus/tests/playwright/`): provision a fake channel, see a seeded
  customer session appear, receive a seeded **intervention notification**, open
  the session, **take over and send a free-form reply**, hand back — all against
  a recorded backend, no LINE account, no LLM.

## Tasks

```
## 1. Backend
- [ ] 1.1 Channel config store (register; write-only secret storage); the home slice 1 reads
- [ ] 1.2 RPC: channels.list / register / verify / sessions (thin over config + store keys)
- [ ] 1.3 RPC: channels.session.takeover / handback / send (over slice-5 handling_mode + operator-send)
- [ ] 1.4 handoff.* events relayed on the operator-questions SSE feed

## 2. Web UI
- [ ] 2.1 /channels view: add-existing-OA form (story ▾, secret/token write-only, webhook URL, auto-reply checklist)
- [ ] 2.2 Per-channel console: live customer-session list (SSE), human-handled badge, [open] → StoryViewer
- [ ] 2.3 Operator inbox grouped by channel + browser notification on ask / handoff.requested
- [ ] 2.4 Chat composer + [take over]/[hand back]: free-form send → channels.session.send; inbound-while-human shown

## 3. Verification
- [ ] 3.1 RPC tests (register/list/verify/sessions/takeover/handback/send; secret never echoed)
- [ ] 3.2 Vue component test (composer + notification); Playwright hybrid walk against a recorded backend (no LINE, no LLM)

## 4. Document
- [ ] 4.1 docs/tui channel-console page; trim/delete this proposal; update epic slice row
```

## Open questions

1. **Secret storage.** Where do `channelSecret`/`channelAccessToken` live —
   the SQLite store (encrypted-at-rest?) or an injected secrets file the config
   references? *Lean: a secrets file/env the channel config references by key, so
   secrets never touch the trace or the store DB; the store holds only the
   reference.*
2. **Multi-merchant / auth.** This console assumes a trusted single operator (no
   tenant ACL today). Multi-merchant SaaS is out of scope; do we at least gate
   the channel routes behind the existing `X-Kitsoki-Actor` identity? *Lean:
   reuse the existing actor header; full multi-tenant auth is a separate epic.*

## Non-goals

- The webhook handler / session factory itself — slice 1 (this is its front
  door + config home, not the handler).
- Multi-tenant SaaS auth / billing — separate epic (question 2).
- Customer-facing UI — the customer is on LINE; this console is merchant-only.

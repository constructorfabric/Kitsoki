# TUI: LINE Channel Console (kitsoki as web presence)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../line-messenger-channel.md

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

1. **Provision** — register a LINE channel: name, story binding, `channelSecret`
   + `channelAccessToken` (write-only, stored as injected secrets), and the
   generated webhook URL `…/channels/line/:channelId` with a "verify" round-trip.
2. **Console** — a live list of the customer sessions a channel is driving
   (keyed `line:<channelId>:<src>`, epic Shared decision 1), each opening the
   **existing** `StoryViewer`/trace view; plus the **operator-ask inbox** so a
   merchant answers a customer-facing agent's question (the question targets the
   merchant, not the customer).

One sentence: **the merchant's home — provision a channel and watch/assist the
sessions it spawns — built on the existing runstatus RPC + SSE surface.**

## Impact

- **UI seams:** new Vue routes/views in `tools/runstatus/src/` (a `Channels`
  view + a per-channel `Console`), reusing the shipped `StoryViewer.vue` and the
  operator-questions SSE feed (`internal/runstatus/server/operator_questions.go`,
  `docs/architecture/operator-ask.md`).
- **RPC additions:** `runstatus.channels.list` / `.register` / `.verify`, and a
  `runstatus.channels.sessions` listing sessions by channel — thin wrappers over
  the slice-1 channel config + `store` external-key lookup. Mirrors the existing
  `runstatus.sessions.*` shape (`server.go:39`).
- **Backend:** the channel config store (registration + secret storage) is the
  home for the credentials slice 1 reads; this slice owns it.
- **Stays typed + pongo2 / Vue:** no hand-rolled rendering; the console reuses
  the typed-view path the rest of the web UI uses.
- **Docs on ship:** `docs/tui/` channel-console page; the provisioning + webhook-
  URL flow referenced from `docs/architecture/channels.md` (slice 1).

## The surface

```
/channels                         ┌─────────────────────────────────────┐
  ├─ [+ New LINE channel]         │ Channel: "Sakura Golf"  ● live        │
  │     name, story ▾,            │ webhook: …/channels/line/sakura  [verify]│
  │     channelSecret  (write)    │ ─────────────────────────────────────│
  │     accessToken    (write)    │ Live customer sessions (line:sakura:*) │
  │                               │  U1f3…  proposing_slot   2m ago  [open]│
  └─ list of channels ● status    │  Ua92…  choosing         just now [open]│
                                  │ ─────────────────────────────────────│
                                  │ Operator inbox (2)                     │
                                  │  "Customer asks about gluten-free…" [answer]│
                                  └─────────────────────────────────────┘
        [open] ──▶ existing StoryViewer / trace for that session
        [answer] ─▶ existing operator-ask answer RPC (resolves the agent's ask)
```

Opening a customer session reuses the shipped story-editor/viewer surface
(`docs/tui/story-editor.md`, `StoryViewer.vue`) and the hybrid-driving operator
identity (`hybrid-session-driving.md`) — a merchant can take over and drive a
turn, serialized with the webhook under the writer lock. The operator inbox is
the *existing* operator-ask web feed, grouped by channel.

## Reuse / build

| Surface | Reuse | Build |
|---|---|---|
| Per-session view/trace | `StoryViewer.vue`, `runstatus.session.*` | — |
| Operator question hand-off | operator-ask web feed (`operator_questions.go`) | group by channel |
| Operator takeover drive | hybrid-driving identity + writer lock | — |
| Channel registration + secrets | injected-secret pattern | channel config store + RPC |
| Session-by-channel list | `store.LookupByKey` reverse / key-prefix scan | `channels.sessions` RPC |
| Webhook URL + verify | slice-1 handler | "verify" round-trip button |

## Verification

- RPC unit tests (`internal/runstatus/server`) mirroring the existing
  `write_test.go` / `identity_test.go` style: `channels.register` stores config
  + secrets (write-only — never echoed back), `channels.sessions` lists by key
  prefix, `channels.verify` reports handler reachability. No LLM.
- A Vue unit/component test for the Channels view + console list (mirrors
  `tools/runstatus/tests/unit/`).
- A Playwright walk (mirrors the existing specs under
  `tools/runstatus/tests/playwright/`): provision a fake channel, see a seeded
  customer session appear, open it, answer a seeded operator question — all
  against a recorded backend, no LINE account, no LLM.

## Tasks

```
## 1. Backend
- [ ] 1.1 Channel config store (register; write-only secret storage); the home slice 1 reads
- [ ] 1.2 RPC: channels.list / register / verify / sessions (thin over config + store keys)

## 2. Web UI
- [ ] 2.1 /channels view: list + new-channel form (story ▾, secret/token write-only, webhook URL)
- [ ] 2.2 Per-channel console: live customer-session list (SSE), [open] → StoryViewer
- [ ] 2.3 Operator inbox grouped by channel; [answer] → existing operator-ask answer RPC

## 3. Verification
- [ ] 3.1 RPC tests (register/list/verify/sessions; secret never echoed)
- [ ] 3.2 Vue component test; Playwright walk against a recorded backend (no LINE, no LLM)

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

# Epic: LINE Messenger as a Customer-Interaction Channel

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 5 (0/5 shipped)

## Why

kitsoki today drives *internal* operator workflows: a session is one root
cause — a Jira ticket, a PRD idea, a bug — driven by a known operator through
the TUI, the web UI, or a Jira/Bitbucket thread. Every surface assumes a
**single, identified principal** and a session that is **pre-created** before
anyone interacts with it (`store.CreateSession`, then `BindExternalKey`).

A whole class of valuable applications inverts that: a **merchant authors a
story once** (a web store, a golf/restaurant/hotel booking flow, a customer-
assistance desk) and then **many anonymous end-customers** each hold their own
running instance of it — reached not through a dev console but through the chat
app they already have open. In the APAC markets kitsoki targets, that app is
**LINE**: businesses run their storefront and bookings as a LINE Official
Account, and customers tap a message rather than open a browser.

The pieces to serve that already exist in fragments — a pluggable inbound
`Bridge` (`internal/transport/inbound`), an output-only `Transport` registry
(`internal/transport/transport.go`), per-session external-key routing
(`store.LookupByKey` / `BindExternalKey`), an operator-ask bridge for human-in-
the-loop, and the hybrid drive-vs-transport model
([`hybrid-session-driving.md`](hybrid-session-driving.md)). What's missing is the
**channel**: a push (webhook) ingress, a LINE output transport, the *get-or-
create-per-customer* session model, and an admin surface to provision it. This
epic adds them, with kitsoki as the engine and **web presence** (the merchant's
authoring + console home) and LINE as the **customer surface**.

## What changes

Once every slice ships, a merchant can:

1. **Author** a customer-facing story (`stories/line-store/`,
   `stories/line-booking/`) the same way they author any kitsoki story — rooms,
   intents, typed views, host calls — with no LINE-specific code in the YAML.
2. **Provision** a LINE channel from the kitsoki web console using an **existing
   LINE Official Account** they already run: paste the channel secret + access
   token from the LINE Developers console, bind the OA to a story, point its
   webhook at the URL kitsoki gives them (and disable the OA's default auto-
   reply). No new account, no migration of their followers — kitsoki slots in
   behind the OA they have.
3. Then **every customer who messages the LINE Official Account** transparently
   gets their own session: the first inbound event *creates* a session bound to
   `line:<channel>:<lineSourceId>`; subsequent messages route to it. Customer
   free text is resolved to story intents by the **same semantic router the TUI
   uses** (`internal/semroute`) — the story author owns the customer vocabulary
   per room. Story output (prompts, view, quick-reply buttons) is posted back to
   LINE through the output transport.
4. **Watch and assist (hybrid)**: the merchant sees every live customer session
   in a web console and runs it as a **hybrid human + bot** desk. When a
   customer-facing agent needs a human, the existing **operator-ask bridge**
   (`docs/architecture/operator-ask.md`) surfaces the structured question to the
   *merchant* — and the merchant is **notified**, not left to watch a dashboard.
   Beyond that narrow ask, the merchant can **take over and chat directly**: flip
   a session to human-handling (which pauses the bot so it never replies over the
   operator), type free-form prose straight into the LINE conversation, then hand
   back to automation. The customer sees one seamless conversation.

The end state: **kitsoki is the application engine and the merchant's web
presence; LINE is one (pluggable, generic) customer channel** — running fully
automated, fully human, or any hybrid in between — layered on the existing
inbound/transport seams, not a fork of the turn loop.

## Impact

- **Spans:** runtime (webhook ingress + session factory; LINE transport),
  story (the example commerce/booking stories), tui (the channel console).
- **Net surface:**
  - one new inbound HTTP server (webhook receiver) + a **get-or-create**
    session router — the only genuinely new engine concept;
  - one new `transport.Transport` impl (`internal/transport/line_transport.go`),
    registered in the existing `Registry` (`transport.go:96`);
  - two example stories under `stories/`;
  - new web console routes + Vue views in `tools/runstatus/`;
  - per-channel credential config (secret + access token) held **outside** story
    YAML, injected like other secrets.
- **Reuses, unchanged:** the orchestrator turn loop, semantic routing, the
  writer lock, external-key store, operator-ask, typed views. The webhook feeds
  the *existing* `inbound.Driver` contract (`inbound.go:65`); transports stay
  output-only.
- **Docs on ship:** `docs/architecture/transports.md` (a LINE section + the
  get-or-create model), `docs/architecture/channels.md` (new — the customer-
  channel concept, generalized beyond LINE), `docs/stories/line-store.md` /
  `line-booking.md`, `docs/tui/` console page.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Webhook ingress + session factory | runtime | LINE-signed webhook → get-or-create session per `line:<channel>:<src>` → drive raw text through semroute under the writer lock | — | Draft | [`line-webhook-ingress.md`](line-webhook-ingress.md) |
| 2 | LINE output transport | runtime | `transport.Transport` for the LINE Messaging API: reply-token fast path + push fallback; typed view → text + quick-reply | 1 (shares channel config) | Draft | [`line-transport.md`](line-transport.md) |
| 3 | Commerce + booking example stories | story | `stories/line-store/` and `stories/line-booking/`, composing slices 1–2 with existing hosts only | 1, 2 | Draft | [`line-commerce-stories.md`](line-commerce-stories.md) |
| 4 | LINE channel console (web presence) | tui | Provision an existing OA (creds + story binding + webhook URL), watch live sessions, **chat composer + notifications** | 1, 5 | Draft | [`line-channel-console.md`](line-channel-console.md) |
| 5 | Live operator handoff | runtime | `handling_mode` (auto\|human): pause auto-routing, deliver free-form operator prose to LINE, emit intervention notifications | 1, 2 | Draft | [`line-operator-handoff.md`](line-operator-handoff.md) |

## Sequencing

```
#1 (runtime: ingress + session factory) ──┬──▶ #3 (story: store + booking)
                                          │
#2 (runtime: LINE transport) ────────────┤
   (parallel with #1; #3 needs both)     │
                                          ├──▶ #5 (runtime: handoff)  needs #1 ingress + #2 send
                                          │
#1, #5 ───────────────────────────────────┴──▶ #4 (tui: console)  drives factory + creds + handoff UI
```

Slice **1 is the spine** — the get-or-create session factory is the one novel
engine concept and everything else layers on it. Slice 2 (output transport) can
be built in parallel against a stub channel config and is independently testable
with an HTTP cassette. Slice 3 needs 1 + 2 to run end-to-end. Slice 5 (handoff)
adds the `human` mode + operator-send seam over 1's ingress and 2's transport.
Slice 4 (console) is the front door for all of it — provisioning + monitoring
(needs 1) and the chat composer + notification UI (needs 5's events).

## Shared decisions

These span slices; each child defers here rather than re-deciding.

1. **Customer-session key scheme** — `line:<channelId>:<lineSourceId>`, where
   `channelId` identifies the merchant's bound LINE Official Account (one
   channel ↔ one story) and `lineSourceId` is the LINE `userId` / `groupId` /
   `roomId`. This namespacing **is** the multi-customer model: one channel hosts
   many customers; the same person in two channels gets two sessions. It rides
   the existing `(transport, thread)` store key (`external_keys.go:41,92`) with
   `transport = "line"`, `thread = "<channelId>:<lineSourceId>"`.

2. **Customer free text routes through `internal/semroute`, not a bridge
   classifier.** The existing `inbound.PrefixClassifier` (deterministic
   `continue` / `refine:` prefixes) is for *operators* replying on a ticket;
   customers won't type prefixes. A LINE message is submitted as **raw input**
   and resolved to a room intent by the same semantic router the TUI/web use, so
   the story author declares the customer's intent vocabulary per room and owns
   it declaratively. (Slice 1, Open question 1, weighs whether to extend the
   `inbound.Classifier` interface to carry raw text or to bypass the bridge's
   classifier entirely.)

3. **Credentials live outside story YAML.** A channel's `channelSecret` (HMAC
   for webhook signature verification) and `channelAccessToken` (Messaging API
   auth) are per-channel config in a channel store, injected like other secrets
   — never in `app.yaml`. Stories stay portable and shareable; the same story
   serves any merchant's channel.

4. **Human-in-the-loop targets the merchant, not the customer — and has two
   shapes.** (a) *Structured ask:* a customer-facing agent calls
   `mcp__operator__ask` and the operator-ask bridge
   (`docs/architecture/operator-ask.md`) surfaces the question on the merchant
   console — the merchant is **notified**, not made to watch. (b) *Free-form
   takeover (slice 5):* the merchant flips a session to `human` handling, which
   **pauses auto-routing** (the bot won't reply over the operator), and types
   prose straight to the customer through the LINE transport, then hands back to
   `auto`. Both preserve the CLAUDE.md headless contract: no operator surface ⇒
   no ask tool, no notification target ⇒ the agent proceeds on its own / the
   story's deterministic fallback. The customer never sees a question or a seam —
   bot and operator replies share one conversation.

5. **Bring-your-own Official Account.** The merchant already runs a LINE OA;
   kitsoki **slots in behind it**, it does not create or replace it. Provisioning
   is: register the OA's `channelSecret` + `channelAccessToken`, point the OA's
   webhook at kitsoki's URL, disable the OA's built-in auto-reply (so kitsoki is
   the only responder). No follower migration, no second account. The console
   (slice 4) surfaces exactly these steps.

6. **The channel is generic; only LINE is built.** Slices 1 and 4 are written
   against a `Channel` abstraction (signed webhook + push/reply transport +
   source-id extraction) so WhatsApp / Messenger / Telegram are later transports
   behind the same seam — but this epic ships **only** the LINE binding and does
   not abstract speculatively beyond what one second platform would obviously
   need.

## Cross-cutting open questions

1. **Customer-session lifecycle / GC.** One session per customer accumulates
   indefinitely (a storefront could see thousands). Options: idle-expiry +
   archive (reuse `host.chat.archive` semantics); a TTL on the external-key
   binding; explicit `done`-state reaping. *Lean: idle-expiry that archives the
   trace and unbinds the key, so a returning customer starts fresh — with an
   opt-in "resume within N hours" for bookings.* Decided per-story in slice 3,
   but the reaping mechanism is engine work — flag if it grows past config.

2. **Public reachability of the webhook.** LINE POSTs to a public HTTPS URL;
   `kitsoki web` is typically local. Out of scope to *build* hosting — but the
   console (slice 4) must surface the bound webhook path and the docs must show
   the tunnel/reverse-proxy pattern. *Lean: document, don't build; the console
   shows the path + a "verify webhook" round-trip button.*

3. **Rich-message fidelity.** LINE Flex Messages can render cards/carousels;
   typed views are richer than plain text. How much to map in v1? *Lean: text +
   **quick-reply buttons derived from the room's intents** in v1 (the single
   highest-value mapping — it turns intents into tappable choices); Flex
   carousels for product/availability lists deferred to a follow-up.* Owned by
   slice 2.

## Non-goals

- **Building public ingress / TLS / a hosting product.** The operator supplies a
  reachable URL (tunnel, reverse proxy, deploy). We receive and verify the
  webhook; we don't terminate it.
- **Payments.** Checkout/booking confirmation links out to LINE Pay / a payment
  provider; in-engine payment processing is a separate concern, not this epic.
- **A general bot framework.** No NLU layer, no dialog-manager DSL — customer
  routing is the *existing* semantic router over the *existing* room intents.
  The story is the bot.
- **Other chat platforms.** The seam is built generic (Shared decision 6) but
  only LINE is implemented here.
- **Replacing the `inbound.PrefixClassifier`.** It stays for operator ticket
  replies (`hybrid-session-driving.md`); LINE simply doesn't use it.

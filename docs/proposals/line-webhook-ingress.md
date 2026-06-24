# Runtime: LINE Webhook Ingress + Per-Customer Session Factory

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../line-messenger-channel.md

## Why

Every inbound path kitsoki has today assumes the session **already exists**. The
poll bridge (`internal/transport/inbound/inbound.go`) drives turns against a
session a human pre-created and bound; `runstatus.session.attach`
(`hybrid-session-driving.md`) binds a *live* web session to an *existing*
persisted one. There is **no path where an external event creates a session for
a previously-unknown principal.** That is exactly what a customer channel needs:
the first time an anonymous LINE user messages a merchant's Official Account,
there is nothing to attach to — the session must be born from the event.

Two other gaps block it. (a) Inbound is **poll-only** — `Bridge.Run` ticks a
`Source.Poll` on a timer (`inbound.go:158`); LINE is **push** (webhook POST),
and a customer expects a reply in seconds, not on the next 30s poll. (b) The
`inbound.Classifier` resolves an intent *before* the turn from a fixed prefix
grammar (`PrefixClassifier`); a customer types "do you have a 7am tee time
Saturday", not `refine: ...`.

## What changes

A new **webhook ingress**: an HTTP handler that verifies a LINE-signed request,
extracts each event's source id, and routes it through a **session factory** —
*get session for `line:<channel>:<src>`, or create-and-bind one* — then submits
the message **as raw free text** to that session's orchestrator under the writer
lock. Customer text is resolved to a room intent by the engine's own semantic
router (`internal/semroute`), the same path the TUI uses — no prefix classifier.

One sentence: **an external event with no prior session creates one, keyed by
the customer's channel identity, and drives it through the normal turn loop.**

## Impact

- **Code seams:**
  - new `internal/channel/` (or `internal/transport/inbound/webhook.go`) — the
    signed-webhook handler + event model, channel-agnostic;
  - new **session factory** seam: `GetOrCreate(ctx, key, storyDef) → SessionID`
    over `store.LookupByKey` + `store.CreateSession` + `store.BindExternalKey`
    (`internal/store/external_keys.go:41,92`, `sqlite.go:127`);
  - mounts on the `kitsoki web` HTTP server next to `/rpc`
    (`internal/runstatus/server/server.go`) as `POST /channels/line/:channelId`;
  - the existing `inbound.Driver` (`inbound.go:65`) is reused for the drive —
    extended to carry raw input (see Vocabulary).
- **Vocabulary:** one new store/router seam + a raw-text driver method; no new
  story-author YAML in this slice (channel binding is config, slice 4).
- **Stories affected:** none existing; enables slice 3.
- **Backward compat:** purely additive. No webhook configured ⇒ no handler
  mounted ⇒ today's behavior is unchanged. The poll bridge is untouched.
- **Docs on ship:** `docs/architecture/transports.md` (get-or-create model),
  new `docs/architecture/channels.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| seam | `SessionFactory.GetOrCreate` | `(ctx, SessionKey, *app.AppDef) → (SessionID, created bool, err)` | atomic under the store; `created` lets the caller fire `start` on a fresh session |
| driver | `inbound.RawDriver` | `SubmitRaw(ctx, text, author string) error` | new method (or interface) — submits free text for engine-side routing, vs. the existing pre-classified `SubmitIntent` (`inbound.go:65`) |
| http | `POST /channels/line/:channelId` | LINE webhook body → `200` fast-ack | signature-verified; async-drives so the HTTP reply returns inside LINE's timeout |
| config | per-channel `channelSecret` | injected secret | HMAC-SHA256 key for `X-Line-Signature` verification (Shared decision 3) |

## The model

```
LINE  ──POST /channels/line/<ch>──▶ [verify X-Line-Signature (HMAC, channelSecret)]
                                          │  (reject 401 on mismatch)
                                          ▼
                                   for each event in body:
                                     src = userId|groupId|roomId
                                     key = line:<ch>:<src>
                                          │
                              SessionFactory.GetOrCreate(key, storyDef)
                                     │ exists → SessionID          │ new → CreateSession + BindExternalKey
                                     └───────────────┬─────────────┘   (then: drive `start` intent)
                                                     ▼
                                   writer-lock(SessionID) → RawDriver.SubmitRaw(text, src)
                                                     ▼
                            orchestrator turn  ──semroute(text)──▶ intent ──▶ transition
                                                     ▼
                                   view/prompt output ──▶ (slice 2: LINE transport push/reply)
```

**Interpretive vs. deterministic:** the only interpretive step is
`semroute(text) → intent`, which is the *existing* recorded routing decision —
no new decision type is introduced. Signature verification, source-id
extraction, get-or-create, and the writer lock are all deterministic.

The HTTP handler **fast-acks** (`200` immediately after verification + enqueue)
and drives the turn asynchronously, because the turn may call an LLM and LINE
times the webhook out. Delivery of the reply is the transport's job (slice 2),
keyed by the same session — not the HTTP response body.

## Decision recording

No new decision type. The customer-text routing lands as the existing semroute
datapoint in the session trace; the get-or-create is a deterministic store
event. One **new lifecycle fact worth tracing**: that a session was *born from a
channel event* (vs. operator-created) — record the originating `channelId` +
masked `src` on session creation so the console (slice 4) and trace can show
provenance. This is a trace-field addition, not a new event kind.

## Engine seams & invariants

- **Atomic get-or-create.** Two near-simultaneous events from the same customer
  (LINE can batch) must not create two sessions. `GetOrCreate` resolves under
  the store's uniqueness on `(transport, thread)` — `BindExternalKey` already
  rejects a key "already taken by another session"
  (`external_keys_test.go:56`); the factory treats that race as "lookup wins."
- **Fresh-session bootstrap.** On `created == true`, the factory drives the
  story's entry intent (`start`) before the customer's text, so the first
  message lands in a real room, not `idle`. Idempotent re-entry is already a
  load-time contract (`docs/stories/state-machine.md`, the `once:`/idempotent-
  on-enter work) — the factory relies on it.
- **Writer-lock serialization.** Every drive goes through the per-session writer
  lock (`store.WithWriterLock`), so a webhook turn serializes with a console
  operator turn (slice 4) exactly as the poll bridge serializes with a browser
  turn today (`hybrid-session-driving.md`, gap 2).
- **Load-time:** a channel bound to a story whose entry intent isn't `start`, or
  to a missing story file, fails fast at channel-registration (slice 4), not on
  the first customer message.

## Backward compatibility / migration

Additive and opt-in. The handler is only mounted when a channel is registered.
No existing story, cassette, transport, or the poll bridge changes. The new
`RawDriver.SubmitRaw` is a new method on the cmd-layer driver
(`cmd/kitsoki/inbound_driver.go`); the existing `SubmitIntent` path is untouched,
so operator ticket replies keep working.

## Tasks

```
## 1. Webhook + verification
- [ ] 1.1 LINE signature verification (HMAC-SHA256 over raw body vs. X-Line-Signature) with channelSecret
- [ ] 1.2 Event model: parse LINE webhook payload → []ChannelEvent{src, text, replyToken}
- [ ] 1.3 POST /channels/line/:channelId handler mounted on the web server; fast-ack 200

## 2. Session factory
- [ ] 2.1 SessionFactory.GetOrCreate over LookupByKey + CreateSession + BindExternalKey; atomic race handling
- [ ] 2.2 Fresh-session bootstrap: drive `start` before first customer text
- [ ] 2.3 Provenance: record channelId + masked src on session creation

## 3. Drive
- [ ] 3.1 inbound.RawDriver / SubmitRaw: free text → semroute → turn, under the writer lock
- [ ] 3.2 Async drive (turn off the HTTP request path); reply delivery handed to the transport (slice 2)

## 4. Verification
- [ ] 4.1 Unit: signature verify (valid/invalid/replay), event parse (user/group/room source)
- [ ] 4.2 Factory test: first event creates+binds+starts; second routes to same session; concurrent-create race → one session
- [ ] 4.3 Flow fixture: a recorded webhook body drives a stub story to a non-idle state, no LLM (nil/cassette harness)

## 5. Document
- [ ] 5.1 docs/architecture/channels.md (the channel concept) + transports.md get-or-create section
- [ ] 5.2 Trim/delete this proposal; update the epic slice row
```

## Verification

All no-LLM. Signature + event-parse are pure unit tests. The factory is tested
against the real SQLite store with direct `SubmitIntent`/`SubmitRaw` calls (no
harness), asserting one session per key under concurrent create. The end-to-end
webhook → non-idle-state path uses a recorded LINE webhook body fixture and a
trivial deterministic story (entry `start` + one keyword-routed intent), driven
with a nil/cassette harness so semroute resolves without a live LLM.

## Open questions

1. **Raw text into the bridge: extend `Classifier` or bypass it?** The
   `inbound.Classifier` interface returns a pre-resolved `(intent, slots)`
   (`inbound.go:55`). Options: (a) add a `RawDriver` that skips the classifier
   and submits text for engine-side semroute; (b) add a passthrough classifier
   that yields a synthetic "raw" intent the engine then routes. *Lean: (a) — a
   distinct `SubmitRaw` driver method keeps the operator-prefix classifier and
   the customer-free-text path cleanly separate (epic Shared decision 2).*
2. **Where does the factory live** — `internal/channel/`, or fold into
   `internal/transport/inbound/`? *Lean: a new `internal/channel/` package, so
   "inbound poll bridge" and "push channel ingress" stay distinct concepts;
   both reuse `store` + the cmd driver.*
3. **Fast-ack vs. synchronous reply.** LINE supports a `replyToken` valid for a
   short window — replying within the webhook is the cheap path (no push quota).
   But async drive can outlive the token. *Lean: async-drive + push by default;
   opt-in reply-token fast path only for stories whose entry turn is
   LLM-free — decided with slice 2.*

## Non-goals

- The LINE **output** transport (reply/push delivery) — slice 2.
- Channel **provisioning** UI and credential storage — slice 4 (this slice reads
  a channel config; it does not manage it).
- Session GC / idle expiry — epic cross-cutting question 1.

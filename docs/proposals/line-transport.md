# Runtime: LINE Output Transport

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../line-messenger-channel.md

## Why

A story's output reaches an external surface through a `transport.Transport`:
`host.transport.post` resolves `key.Transport` in the registry and calls
`Post` (`internal/transport/transport.go:66,135`). Two transports ship today —
`jira_transport.go`, `bitbucket_transport.go` — both REST posters to a comment
thread. There is **no transport that delivers to a chat app**, so a story driven
by a LINE customer (slice 1) has no way to answer them. The customer sends; the
engine routes and advances; the reply has nowhere to go.

## What changes

A `transport.Transport` implementation, `id() == "line"`, that delivers a
rendered turn to a LINE conversation via the **Messaging API**: a `replyToken`
fast path when the turn answers a just-received message inside the token window,
falling back to **push** (`POST /v2/bot/message/push`) keyed by the customer's
`lineSourceId`. It maps a kitsoki **typed view** to LINE message objects —
text in v1, with the room's **intents rendered as quick-reply buttons** so the
customer taps a choice instead of guessing the phrasing.

One sentence: **the output half of the LINE channel — typed view → LINE
message(s), registered in the existing transport registry, reached by
`host.transport.post` unchanged.**

## Impact

- **Code seams:** new `internal/transport/line_transport.go` implementing
  `Transport` (`transport.go:66`); registered via `Registry.Register`
  (`transport.go:96`) in `cmd/kitsoki/registry.go` alongside Jira/Bitbucket.
- **Vocabulary:** no new story-author surface — `host.transport.post` with
  `key.Transport: "line"` reaches it; the customer's `lineSourceId` rides
  `SessionKey.Thread` (epic Shared decision 1) and/or `Message.Extra` (the
  `replyToken`), exactly as Bitbucket carries PR coords in `Extra`
  (`transport.go:46`).
- **Stories affected:** none existing; consumed by slice 3.
- **Backward compat:** additive — one more registered transport. Existing
  transports and `host.transport.post` semantics (de-dup by `PhaseID`,
  `BotMarker`) are unchanged.
- **Docs on ship:** `docs/architecture/transports.md` (LINE row + quick-reply
  mapping), `docs/architecture/hosts.md` if any post arg surfaces.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| transport | `line` | `Post(ctx, key, msg) → messageId` | `key.Transport == "line"`, `key.Thread == "<channelId>:<src>"` |
| extra | `reply_token` | `Message.Extra["reply_token"]` | set by slice-1 ingress for the reply fast path; absent ⇒ push |
| mapping | typed view → LINE | `text` + `quickReply.items[]` | v1: leading prose as text, room intents → quick-reply buttons; Flex deferred |

## The model

```
turn output (typed view + active intents)
        │
host.transport.post  key=line:<ch>:<src>  Extra{reply_token?}
        ▼
LineTransport.Post:
    msgs = render(typedView)            # text bubble(s)
    msgs[last].quickReply = intents → quick-reply buttons   # tappable choices
        │ reply_token present & fresh → POST /v2/bot/message/reply  (no push quota)
        └ else                         → POST /v2/bot/message/push   (keyed by src)
        ▼
    return LINE response message id  (opaque, for trace)
```

Rendering is **deterministic** — a pure function from the typed view + active
intent list to LINE message JSON, fully cassette-testable. The only I/O is the
authenticated HTTPS POST (channel access token, Shared decision 3), recorded via
an HTTP cassette like the Jira/Bitbucket transport tests.

**Quick-reply derivation** is the highest-value mapping (epic question 3): the
room's currently-available intents become tappable buttons whose payload is the
intent's canonical phrase, so the customer's tap routes deterministically
through semroute. This is the one piece that makes a chat surface feel like an
app rather than a prompt.

## Engine seams & invariants

- **Registry contract:** `ID() == "line"` must match the `SessionKey.Transport`
  the ingress and stories use; `Register` panics on a duplicate id
  (`transport.go:96`) — init-time safety.
- **BotMarker:** the transport prepends `Message.BotMarker` like the others
  (`transport.go:140`). LINE has no thread-readback (the poll bridge doesn't read
  LINE), so the marker is cosmetic here — but kept for consistency and to avoid
  echo if a future LINE *source* reads history.
- **Token freshness:** a `reply_token` is single-use and short-lived; the
  transport treats a reply failure as a signal to fall back to push, not a hard
  error, so a slow turn still reaches the customer.
- **Quota & rate:** push is quota-limited per LINE plan; `Post` surfaces a
  rate-limit response as a typed error the orchestrator records (no silent drop).

## Backward compatibility / migration

Purely additive. Registering one more transport changes nothing for Jira /
Bitbucket / TUI. Stories that never post to `line` are unaffected. The cassette-
recorded tests run with no network and no LINE account.

## Tasks

```
## 1. Transport
- [ ] 1.1 LineTransport implementing transport.Transport (ID/Post/Close); channel access token via injected config
- [ ] 1.2 typed view → LINE message render (text bubbles); deterministic, no I/O
- [ ] 1.3 Quick-reply: active intents → quickReply.items with canonical-phrase payloads
- [ ] 1.4 reply-token fast path with push fallback; rate-limit → typed error

## 2. Wire-up
- [ ] 2.1 Register "line" in cmd/kitsoki/registry.go (behind channel config presence)
- [ ] 2.2 Slice-1 ingress passes reply_token through Message.Extra

## 3. Verification
- [ ] 3.1 Unit: render text + quick-reply from a fixture typed view (golden JSON)
- [ ] 3.2 HTTP-cassette test: Post → reply API, then push fallback when token rejected (mirrors jira_transport_test.go)
- [ ] 3.3 Registry test: "line" resolves; duplicate-register panics

## 4. Document
- [ ] 4.1 transports.md LINE row + quick-reply mapping; trim/delete this proposal; update epic slice row
```

## Verification

All no-LLM, no live LINE account. Rendering is golden-JSON unit-tested. The POST
path uses the same HTTP-cassette discipline as `jira_transport_test.go` /
`bitbucket_transport_test.go` — record once against the LINE API shape, replay
deterministically, assert reply→push fallback. Registry resolution and the
duplicate-register panic are direct unit tests.

## Open questions

1. **Flex Messages in v1?** A product list or availability grid is a natural
   Flex carousel. *Lean: text + quick-reply only in v1 (epic question 3);
   add a typed-view → Flex mapper as a follow-up once slice 3 shows the shapes
   that actually recur (product card, time-slot list).*
2. **Multi-bubble vs. single message.** A long view can exceed LINE's per-
   message limit and reads better as several bubbles. *Lean: split on the typed
   view's block boundaries, cap at LINE's 5-message reply limit, push the
   remainder.*

## Non-goals

- Inbound (webhook) handling — slice 1. This transport is **output-only**, per
  the transport contract (`transport.go:66`).
- LINE Pay / payment messages — epic non-goal.
- Flex/carousel rendering — deferred (question 1).

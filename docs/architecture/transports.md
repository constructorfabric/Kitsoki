# Transports & Sessions

A **transport** is an output adapter onto an external surface. The TUI
transcript is one transport; Jira ticket comments and Bitbucket PR
comments are two more. The same room — state graph, intents, phases,
checkpoints — works no matter which transport carries the conversation.
Because most of those surfaces are plain-text comment threads, every
story must also be fully usable as text — see
[§7](#7-every-story-must-work-text-only).

A **session** is one running instance of a state machine for one app.
Sessions are persistent and are addressed two ways:

- by **session ID** — an internal ULID; the canonical key.
- by **external key** — `transport:thread`, e.g. `jira:PLTFRM-12345`,
  `tui:<uuid>`. The `(transport, thread)` index in the store makes
  singleton lookup cheap, and the session writer lock guarantees
  serial execution.

This is what makes kitsoki usable as the conversational workflow engine behind a
comment-thread bot.

---

## 1. The Transport interface

```go
type Transport interface {
    ID() string                           // "tui", "jira", "bitbucket", …
    Post(ctx, key SessionKey, msg Message) (string, error)
    Close() error
}
```

`SessionKey` is `(Transport, Thread)`. `Message` carries `PhaseID`,
`Title`, `Body`, `Attachments`, and a `BotMarker` (default `"[kitsoki]"`)
that polling external drivers use to filter their own output.

The interface is deliberately output-only. Posting an artifact and
*driving* the state machine are two orthogonal axes — see
[§8 Driving vs transport](#8-driving-vs-transport). Inbound events
reach a session either through `cmd/kitsoki session continue` (an
external driver such as `loop.py` polls and posts each comment as a
turn) or through the in-process **inbound bridge** (§8.2). No
`Transport` implementation ever grows a read path.

Source: [`internal/transport/transport.go`](../../internal/transport/transport.go).

---

## 2. Built-in transports

| ID | Implementation | Notes |
|---|---|---|
| `tui` | `internal/transport/tui_transport.go` | Local mirror of the transcript pane. Used by `kitsoki run`. |
| `jira` | `internal/transport/jira_transport.go` | Posts via the Jira REST API. Uses `internal/transport/jira_markdown.go` to convert Markdown → Jira wiki markup. De-dups by `PhaseID` so re-running a phase doesn't double-post. |
| `bitbucket` | `internal/transport/bitbucket_transport.go` | Posts a comment to a Bitbucket Server PR thread via the REST API. Bearer-token auth; defaults to the Acronis ZTA proxy. See §2.1. |

Each implementation reads its config (URL, auth, default account) from
environment variables; see the source file's package comment for the
exact list.

To add another transport, see
[`developer-guide.md` §5.3](../guide/development/developer-guide.md#53-adding-a-new-transport).

### 2.1 Bitbucket transport

Posts a single comment to a Bitbucket Server pull-request thread. The
shape mirrors the Jira transport so `host.transport.post` dispatches
into either driver without special-casing — only the routing args
differ.

**Authentication.** Bearer personal-access token. The session builder
resolves it in this order:

1. `$BITBUCKET_TOKEN` (test overrides, CI).
2. `~/.config/acronis/bitbucket-token` (the standard Acronis location
   shared with `tools/loopy`).

If neither source yields a token the transport is silently omitted from
the registry — the session continues without `bitbucket` available.

**Endpoint.** `POST <base>/rest/api/1.0/projects/<pr_project>/repos/<pr_slug>/pull-requests/<pr_id>/comments`
with body `{"text": "<body>"}`. `<base>` defaults to the Acronis ZTA
proxy mount at `https://localhost:3128/bitbucket` (override via
`$BITBUCKET_BASE_URL`). The default HTTP client skips TLS verification
because the proxy presents a self-signed cert; supply
`BitbucketConfig.HTTPClient` to opt back in.

**Routing args.** Unlike Jira (where `SessionKey.Thread` is the issue
key and that's all the API needs), Bitbucket needs three coordinates
to identify a PR. They flow in via the `host.transport.post` call:

```yaml
- invoke: host.transport.post
  with:
    transport:  "bitbucket"
    thread:     "{{ world.jira_key }}"   # correlation only; not in URL
    pr_project: "DBI"
    pr_slug:    "loopy"
    pr_id:      "302"
    title:      "Phase A complete"
    body:       "Result: {{ world.result }}"
```

`pr_project`, `pr_slug`, `pr_id` are non-reserved args and reach the
transport via `Message.Extra` (see `internal/host/transport_post.go::collectExtras`).
`SessionKey.Thread` is kept for orchestrator-side correlation
(typically the Jira ticket key) and plays no role in the REST URL.

**Body format.** `[kitsoki] *<title>*\n\n<body>` — the bot-marker
prefix is prepended so polling drivers (today `loop.py`) can filter
out kitsoki's own posts. The same convention as the Jira transport
(see §6); Bitbucket renders comments as Markdown so `*<title>*`
becomes emphasis.

**Registration.** Automatic when a token is discoverable. See
`cmd/kitsoki/session.go::buildTransportRegistry` and
`loadBitbucketToken`.

---

## 3. Posting from a state machine

Use `host.transport.post` from inside an effect:

```yaml
hosts:
  - host.transport.post

effects:
  - invoke: host.transport.post
    with:
      transport: "{{ world.transport }}"
      thread:    "{{ world.thread }}"
      phase_id:  "phase_a"
      title:     "Phase A complete"
      body:      "Result: {{ world.result }}"
```

This is the path used by phase templates — the template substitutes
`{{ tpl.id }}` into `phase_id:` so every instantiation gets a unique,
de-duppable ID.

The TUI transport is special-cased: when `kitsoki run` is in the
foreground, the orchestrator renders the new state's `view:` template
into the TUI transcript pane every turn. External transports
(`jira`, future `bitbucket`) are explicit — only fired by
`host.transport.post` invocations declared in the app's effects.

---

## 4. Sessions keyed by transport

```sh
# Create a session keyed by an external thread.
kitsoki session create --app app.yaml --key jira:PLTFRM-12345

# Drive one turn from the outside.
kitsoki session continue --app app.yaml --key jira:PLTFRM-12345 \
    --raw "Looks good. Continue."

# Or with a structured intent.
kitsoki session continue --app app.yaml --key jira:PLTFRM-12345 \
    --intent continue --slots '{}'

# Inspect.
kitsoki session show --app app.yaml --key jira:PLTFRM-12345

# Rebind: attach an additional external key to the same session.
kitsoki session bind-key --app app.yaml --id <session-id> \
    --key bitbucket:DBI/repo/pulls/42
```

Exit codes for `kitsoki session continue`:

| Code | Meaning |
|---|---|
| 0 | Turn applied. |
| 1 | Generic error (parse, validation, host failure). |
| 75 | `EX_TEMPFAIL` — another process holds the writer lock. Back off and retry. |

External drivers (`loop.py`, future webhook receivers) treat 75 as a
"come back later" signal — exactly the contract Postfix and other Unix
tools use for the same reason.

---

## 5. Phases that pause for an external reply

The phase-template `checkpoint:` flag marks a phase as one that
*waits* for an inbound reply before advancing. After the executing
state posts its message, the phase moves to `<id>_awaiting_reply` —
where the only intents available are the **checkpoint intents**
declared at the top level:

```yaml
checkpoint_intents:
  continue:
    description: "Approve this phase and advance."
  refine:
    description: "Re-run this phase with feedback."
```

When a comment arrives via `kitsoki session continue`, the harness
translates it into one of those intents and the phase resumes.
This is how a single-process kitsoki instance can host a multi-day
conversation across a handful of Jira tickets.

---

## 6. Bot output filtering

When kitsoki posts to Jira via `host.transport.post`, the Body is
prepended with `BotMarker` (default `[kitsoki]`). Polling external
drivers — anything that fetches the comment thread and feeds new
comments back into kitsoki — must filter on the marker so they don't
echo kitsoki's own output back as user input.

This is the single most-bitten gotcha when wiring up an external
transport. The marker is configurable per-transport in `app.yaml`;
when you change it, also change the corresponding filter in the
external driver.

---

## 7. Every story must work text-only

Most surfaces are plain-text comment threads — a Jira comment, a
Bitbucket PR comment, a Slack/Teams message, an email reply. The TUI
and web UI, with their color and interactive widgets, are the
exception, not the rule. So the contract is stronger than "the state
graph runs on any transport" (the opening claim of this doc):
**every story must be fully drivable and legible with nothing but
text.** A room that only works in the TUI is broken on Jira.

There are two sides to it:

- **Output.** A room's `view:` is authored as typed elements — the
  canonical form — and each surface *projects* them. The TUI paints
  color and widgets; a text transport serializes the same element tree
  to Markdown (the Jira transport then converts Markdown → wiki markup,
  §2). Nothing essential — narration, status, or the available actions
  — may live *only* in color, a `choice:` widget, or a `media:` embed,
  because those collapse to plain text or vanish entirely on a comment
  thread. Write the view so its Markdown projection alone tells the
  user where they are and what they can do.

- **Input.** Every intent must be reachable by typing. The semantic
  router (free text → intent) and the deterministic `PrefixClassifier`
  (`continue` / `refine: …` / `jump_to …`, §8.2) are the text-only
  input path; a `choice:` widget is a TUI/web affordance layered on
  top, never the only way to fire an intent. A room whose only path
  forward is a click cannot be advanced from a Jira comment. On the web,
  where a `choice:`/`form` widget would otherwise hide the text box,
  `InputBar.vue` renders a persistent, de-emphasized free-text **floor**
  beneath the widget — mirroring the TUI's `Tab → chat` escape — so
  arbitrary text always routes through `session.turn` (semantic router →
  off-ramp) regardless of what typed-view elements the room declares.

This is why `choice:` short-circuits the router but never replaces it,
and why the action menu must render unconditionally as text — the
comment-thread user has only the text. The authoring rules that keep a
view honest on a text surface live in
[`../stories/story-style.md` §3.5](../stories/story-style.md#35-the-view-must-always-render-to-something-visible)
and [§3.8](../stories/story-style.md#38-the-view-must-read-as-plain-text).

### 7.1 Known gaps (the contract is not yet enforced)

The contract above is the design intent; an audit (2026-06) found the
runtime does not yet guarantee it. Until these close, text-only safety
rests on author discipline — which holds across shipped stories today,
but nothing prevents a regression:

- **The static `choice:` footer advertises keyboard affordances that
  don't exist on a text surface.** The body the Jira/Bitbucket
  transports emit ends with `[↑/↓ move • Enter pick • Tab chat • Esc
  cancel]`, and form-mode emits bare `____` underlines, with no "reply
  with `intent field=value`" instruction. The labels *are* shown, so the
  room is drivable, but the footer misleads — a transport-aware
  footer/serialization is its own change.
- **No load-time lint enforces text-only safety.** Nothing rejects a
  room whose only affordance is a widget with no typeable intent path,
  or a `media:` whose meaning isn't mirrored in prose. The invariant is
  documented but unchecked.

---

## 8. Driving vs transport

**Transport** (outbound) and **drive** (inbound) are orthogonal. A
transport decides *where artifacts go*; driving decides *who advances
the FSM*. The same session can be driven from several sources at once
while its artifacts mirror to a write-only transport:

```mermaid
flowchart LR
    loop["loop.py<br/>session continue"]
    browser["browser<br/>session.submit"]
    inbound["inbound bridge<br/>poll -> intent"]
    intent["intent<br/>+ author"]
    post["host.transport.post"]
    jira["jira"]
    bitbucket["bitbucket"]
    tui["tui"]

    loop --> intent
    browser --> intent
    inbound --> intent
    intent --> post
    post --> jira
    post --> bitbucket
    post --> tui
```

Every drive source resolves an **operator identity** and records it as
the turn's author, so a session driven by three sources reconstructs
into one ordered, attributed intent log.

### 8.1 Operator identity

A drive turn records who took it. The runstatus web server resolves the
author with this precedence and injects it as the reserved `author`
slot before the turn (`server.WithDefaultActor`,
`internal/runstatus/server/server.go`):

1. `X-Kitsoki-Actor` request header (a fronting proxy / future auth layer);
2. an explicit `actor` field on the drive RPC;
3. the server's configured default (`kitsoki web --actor <name>`).

A story consumes it via `slots.author` (e.g. bugfix's
`last_reply_author: "{{ slots.author ?? 'human' }}"`). If a story
*gates* a turn on an author ACL (reads `allowed_authors` in a guard)
but the server has no configured identity, the registry **fails fast at
session start** rather than letting a browser turn record the anonymous
fallback and silently bounce off the guard
(`SessionRegistry.checkAuthorIdentity`). No story ships such a guard
today — `allowed_authors` is declared but unread.

### 8.2 The inbound bridge

[`internal/transport/inbound`](../../internal/transport/inbound) is the
in-process counterpart to the external `loop.py` poller. A `Bridge`
ties three injected seams:

- a **`Source`** that reads new replies for one `(transport, thread)`
  (concrete Jira / Bitbucket REST sources plug in here);
- a **`Classifier`** — the default `PrefixClassifier` is deterministic
  (no LLM): `continue` / `refine: <text>` / `restart_from <state>` /
  `jump_to <state>`;
- a **`Driver`** that advances the session under the writer lock.

Each cycle filters the BotMarker self-echo (§6), filters by author,
de-dups by reply id, classifies, and drives a turn — best-effort, so a
transient failure (e.g. the writer lock is held) retries next poll
rather than dropping the reply.

### 8.3 Co-driving one persisted session

`kitsoki web` attaches a live session to an **existing persisted
session** by external key
(`runstatus.session.attach {story_path, key}` →
`SessionRegistry.AttachExternal`): it looks the key up in the shared
`--db` store (creating + binding when absent) and drives it under the
per-session **writer lock**, so a browser, the inbound bridge, and a
separate `kitsoki session continue` process serialise rather than
interleave — a loser gets `ErrSessionBusy` (`EX_TEMPFAIL`) and retries.

Live SSE reflects turns the web process itself drives. A turn another
**process** commits is visible on the next session reload (read from the
shared store), not pushed over SSE: the trace JSONL takes an exclusive
flock, so two processes cannot share one live trace stream. A
cross-process live stream (teeing store events to a shared, lock-free
trace reader) is the remaining engine work.

---

## 9. Pointers

- Source: [`internal/transport/`](../../internal/transport/),
  [`internal/transport/inbound/`](../../internal/transport/inbound/).
- Session store: [`internal/store/`](../../internal/store/) and
  [`internal/store/external_keys.go`](../../internal/store/external_keys.go).
- The `loop.py` external driver lives in a separate repo and is the
  reference inbound poller; for the live bug-fix flow design see
  [`../stories/bugfix/README.md`](../../stories/bugfix/README.md).
- CLI reference: `kitsoki session --help`, `kitsoki chat --help`.

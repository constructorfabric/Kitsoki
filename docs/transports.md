# Transports & Sessions

A **transport** is an output adapter onto an external surface. The TUI
transcript is one transport; Jira ticket comments and Bitbucket PR
comments are two more. The same room — state graph, intents, phases,
checkpoints — works no matter which transport carries the conversation.

A **session** is one running instance of a state machine for one app.
Sessions are persistent and are addressed two ways:

- by **session ID** — an internal ULID; the canonical key.
- by **external key** — `transport:thread`, e.g. `jira:PROJ-12345`,
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

The interface is deliberately output-only in v1. Inbound events come
in via `cmd/kitsoki session continue` — an external driver (today
`loop.py`) handles polling and posts each new comment as a session
turn. Webhooks are a future inbound surface.

Source: [`internal/transport/transport.go`](../internal/transport/transport.go).

---

## 2. Built-in transports

| ID | Implementation | Notes |
|---|---|---|
| `tui` | `internal/transport/tui_transport.go` | Local mirror of the transcript pane. Used by `kitsoki run`. |
| `jira` | `internal/transport/jira_transport.go` | Posts via the Jira REST API. Uses `internal/transport/jira_markdown.go` to convert Markdown → Jira wiki markup. De-dups by `PhaseID` so re-running a phase doesn't double-post. |
| `bitbucket` | `internal/transport/bitbucket_transport.go` | Posts a comment to a Bitbucket Server PR thread via the REST API. Bearer-token auth; defaults target an enterprise ZTA-proxy deployment. See §2.1. |

Each implementation reads its config (URL, auth, default account) from
environment variables; see the source file's package comment for the
exact list.

To add another transport, see
[`developer-guide.md` §5.3](developer-guide.md#53-adding-a-new-transport).

### 2.1 Bitbucket transport

Posts a single comment to a Bitbucket Server pull-request thread. The
shape mirrors the Jira transport so `host.transport.post` dispatches
into either driver without special-casing — only the routing args
differ.

**Authentication.** Bearer personal-access token. The session builder
resolves it in this order:

1. `$BITBUCKET_TOKEN` (test overrides, CI).
2. `~/.config/kitsoki/bitbucket-token` (on-disk fallback; override
   via `$BITBUCKET_TOKEN` in your own setup).

If neither source yields a token the transport is silently omitted from
the registry — the session continues without `bitbucket` available.

**Endpoint.** `POST <base>/rest/api/1.0/projects/<pr_project>/repos/<pr_slug>/pull-requests/<pr_id>/comments`
with body `{"text": "<body>"}`. `<base>` defaults to an enterprise
ZTA-proxy mount at `https://localhost:3128/bitbucket` (override via
`$BITBUCKET_BASE_URL` to point at a vanilla Bitbucket Server).  The
default HTTP client skips TLS verification because the proxy presents
a self-signed cert; supply `BitbucketConfig.HTTPClient` to opt back
in.

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
kitsoki session create --app app.yaml --key jira:PROJ-12345

# Drive one turn from the outside.
kitsoki session continue --app app.yaml --key jira:PROJ-12345 \
    --raw "Looks good. Continue."

# Or with a structured intent.
kitsoki session continue --app app.yaml --key jira:PROJ-12345 \
    --intent continue --slots '{}'

# Inspect.
kitsoki session show --app app.yaml --key jira:PROJ-12345

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

## 7. Pointers

- Source: [`internal/transport/`](../internal/transport/).
- Session store: [`internal/store/`](../internal/store/) and
  [`internal/store/external_keys.go`](../internal/store/external_keys.go).
- CLI reference: `kitsoki session --help`, `kitsoki chat --help`.

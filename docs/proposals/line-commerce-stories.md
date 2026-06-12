# Story: LINE Commerce + Booking Example Stories

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../line-messenger-channel.md

## Why

The epic's runtime slices (webhook ingress, LINE transport) are substrate — they
prove nothing until a real merchant story rides them. We need worked examples
that (a) demonstrate the customer-channel pattern end-to-end and (b) become the
copy-me templates a merchant clones, the way `stories/bugfix/` and
`stories/dev-story/` are the templates for internal pipelines. The two highest-
value shapes from the brief are a **web store** (browse → cart → checkout) and a
**booking** (golf/restaurant/hotel availability → reserve → confirm). Both are
ordinary kitsoki stories — the novelty is entirely that their *surface* is LINE,
not the TUI, and that they are authored against existing hosts only.

## What changes

Two new stories under `stories/`, each a small, deterministic state machine with
no LINE-specific YAML — they are channel-agnostic and would run identically in
the TUI. The only "LINE-ness" is config (slice 4 binds the channel) and the fact
that intents are phrased to route well from customer free text + quick-reply
taps (epic Shared decision 2). One sentence: **two reference customer stories,
structurally a guided form + a confirm/decline checkpoint, composing existing
hosts only.**

## Impact

- **Net-new:** `stories/line-store/` (~4 rooms) and `stories/line-booking/`
  (~4 rooms), prompts, one schema each, flow fixtures, READMEs.
- **Engine/host changes:** none — composes existing mechanisms (intake choice,
  `host.starlark.run` for catalog/availability lookups via HTTP cassette,
  `host.oracle.decide` only where genuine interpretation is needed). The channel
  delivery is the epic's slices 1–2; **this slice adds no engine surface.**
- **Docs on ship:** `docs/stories/line-store.md`, `docs/stories/line-booking.md`,
  the `stories/*/README.md` entries.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Greet + show menu/categories | room view with intents → quick-reply buttons | epic slice 2 mapping; view shape per `stories/oregon-trail/rooms/general_store.yaml` |
| Look up catalog / availability | `host.starlark.run` + HTTP cassette (deterministic) | `starlark` skill; cassette discipline per `jira_transport_test.go` |
| Add to cart / pick a slot | typed intent with slots (item id, date, party size) | intent slot validation, `internal/intent` |
| Confirm / decline checkpoint | `accept` / `change` / `cancel` + cycle budget | `stories/bugfix/rooms/proposing.yaml` checkpoint intent set |
| Interpret a fuzzy request ("something cheap and spicy") | `host.oracle.decide` over the catalog, schema-bounded | `decide` verb, narrowest-fit (CLAUDE.md oracle vocabulary) |
| Hand off to a human | `mcp__operator__ask` → merchant console | `docs/architecture/operator-ask.md`; epic Shared decision 4 |

## Story graph

```
# line-store
idle ── start ──▶ browsing ── add(item) ──▶ browsing        (loop: keep shopping)
                     │                          │
                     │ checkout                 │ checkout
                     ▼                          ▼
                  reviewing ── confirm ──▶ @exit:ordered      (→ payment link)
                     │  └─ change ─▶ browsing
                     └─ cancel ─▶ @exit:abandoned

# line-booking
idle ── start ──▶ choosing ── pick(date, party) ──▶ checking_availability
                     ▲                                    │ (host.starlark availability)
                     │ change                             ▼
                     └──────────────────────────────  proposing_slot
                                                          │ confirm ─▶ @exit:booked  (→ confirmation)
                                                          │ change ─▶ choosing
                                                          └ cancel ─▶ @exit:abandoned
```

`proposing_slot` / `reviewing` are the **checkpoint rooms** — they lift the
bugfix `accept`/`change`/`cancel` intent set.

## World schema (sketch)

```yaml
# line-store
world:
  catalog:        { type: object, default: {} }   # host.starlark lookup result
  cart:           { type: list,   default: [] }    # [{item_id, qty}]
  order_total:    { type: int,    default: 0 }
  review_cycle:   { type: int,    default: 0 }
  review_budget:  { type: int,    default: 5 }
  abandon_reason: { type: string, default: "" }
```

```yaml
# line-booking
world:
  resource:       { type: string, default: "" }    # course / table / room
  requested_date: { type: string, default: "" }
  party_size:     { type: int,    default: 0 }
  offered_slots:  { type: list,   default: [] }     # availability lookup
  chosen_slot:    { type: object, default: {} }
  booking_cycle:  { type: int,    default: 0 }
  booking_budget: { type: int,    default: 5 }
```

`exits:` — store: `ordered: { requires: [cart] }`, `abandoned: {}`; booking:
`booked: { requires: [chosen_slot] }`, `abandoned: {}`.

## Per-room detail

### `browsing` (store) — show catalog, accumulate a cart

- **`on_enter`:** `host.starlark.run` catalog lookup (HTTP cassette) → `bind:
  catalog`; idempotent (guard on `catalog` populated, per the `once:` contract).
- **Intents:** `add(item_id, qty)` (loops back, mutates `cart` + `order_total`),
  `remove(item_id)`, `checkout` → `reviewing`, `quit` → `@exit:abandoned`.
- **View:** renders `catalog` as a list; active intents → quick-reply buttons.

### `reviewing` (store) — confirm checkpoint

- **Intents:** `confirm` → `@exit:ordered` (emits a payment/confirmation link),
  `change` → `browsing`, `cancel` → `@exit:abandoned`; `review_cycle` gate →
  abandon at budget. Lifts `stories/bugfix/rooms/proposing.yaml`.

### `checking_availability` / `proposing_slot` (booking)

- **`on_enter`:** `host.starlark.run` availability lookup keyed by `resource` +
  `requested_date` + `party_size` → `bind: offered_slots`.
- **Intents:** `pick(slot_id)` → set `chosen_slot` → `confirm`/`change`/`cancel`
  checkpoint; no-availability path offers nearest alternatives.

### Net-new files

```
stories/line-store/
├── app.yaml
├── rooms/{browsing,reviewing}.yaml
├── prompts/{greet,review}.md
├── schemas/order.json
├── flows/{happy_path,change_loop,abandon}.yaml
└── README.md
stories/line-booking/
├── app.yaml
├── rooms/{choosing,checking_availability,proposing_slot}.yaml
├── prompts/{greet,propose}.md
├── schemas/booking.json
├── flows/{happy_path,no_availability,abandon}.yaml
└── README.md
```

## Flow fixtures

Mode-2, intent-only, no-LLM, CI-fast.

- `happy_path` — store: `start → add → checkout → confirm → @exit:ordered`;
  booking: `start → pick → confirm → @exit:booked`.
- `change_loop` / `no_availability` — `change` re-enters the browse/choose room
  and increments the cycle; booking's no-availability path offers alternatives.
- `abandon` — `cancel`/budget exhaustion → `@exit:abandoned` with reason.

The `host.starlark` catalog/availability lookups run from HTTP cassettes so the
fixtures need no network.

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml + rooms with typed views + world schema for both stories
- [ ] 1.2 schemas/{order,booking}.json; starlark catalog/availability glue + cassettes; stub prompts

## 2. Lock the graph
- [ ] 2.1 Probe each room: kitsoki turn --state <room> --intent <x> --world @w.json
- [ ] 2.2 Flow fixtures pass (happy_path, change_loop/no_availability, abandon)

## 3. Live + document
- [ ] 3.1 kitsoki run each story end-to-end in the TUI (channel-agnostic proof)
- [ ] 3.2 Run one end-to-end over LINE once slices 1–2 land (cassette LINE transport)
- [ ] 3.3 READMEs + docs/stories/{line-store,line-booking}.md; trim/delete this proposal; update epic slice row
```

## Open questions

1. **Catalog/availability source.** Real store via `host.starlark` HTTP to a
   merchant API, or a fixture catalog in-repo for the example? *Lean: fixture
   catalog + cassette for the shipped examples; document the `host.starlark`
   swap-in point for a real backend.*
2. **Payment.** `@exit:ordered`/`booked` emits a link out (LINE Pay / Stripe);
   in-flow payment is an epic non-goal. *Lean: emit a placeholder link + record
   the order; real payment is a follow-up.*
3. **One story with two modes vs. two stories?** *Lean: two stories — they read
   clearer as copy-me templates and share nothing but the checkpoint pattern.*

## Non-goals

- Any new engine/host/widget — that's slices 1–2; if a story wants one, it's a
  runtime slice, not this.
- Production catalog/inventory/payment integration (question 1, 2).
- LINE-specific YAML in the stories — they stay channel-agnostic (epic Shared
  decision 2).

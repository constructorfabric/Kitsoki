# Frontier Event — a wrapper sub-story for layered composition

A reusable "scouting + sub-event" wrapper. Used as the middle layer in
the canonical three-layer composition demo:

```
consumer (e.g. oregon-trail)  →  frontier_event  →  robbery
```

The wrapper adds a `scouting` lead-in beat, then drops into whichever
inner sub-story it imports — here that's `stories/robbery/`. Authors
plug their own scouting prose / rolls into `scouting` and let the
inner sub-story do the heavy lifting.

This story exists primarily to exercise:

- proposal §6 intent re-export (`exports.intents: [scout]`)
- proposal §7 exits with `requires:` (each of three exits requires
  `encounter_kind`; the inner robbery's effects satisfy them via the
  per-exit `set:` projection)
- proposal §11.2 multi-layer binding composition — the inner robbery's
  `narrator` interface is reachable from a grandparent importer via
  the alias-prefixed binding `bandits__narrator`

## Contract

### Entry state

`scouting` — the scouting beat. Default `entry:` on import.

### Exits

| Name | `requires:` |
|---|---|
| `resolved` | `encounter_kind` (set by the inner robbery's paid/routed exit projection) |
| `member_lost` | `encounter_kind`, `member_lost` |
| `fled` | `encounter_kind` |

The inner robbery's `paid`/`routed` exits both map to `@exit:resolved`;
`killed` maps to `@exit:member_lost`; `fled` chains through directly.

### `world_in:`

| Key | Type | Default | Purpose |
|---|---|---|---|
| `party_money` | int | 0 | Forwarded to the inner robbery. |
| `party_alive` | int | 5 | Forwarded to the inner robbery. |
| `threat_level` | int | 2 | Forwarded to the inner robbery. |
| `scouting_intel` | string | "" | Shown in the scouting view; opportunity for the importer to inject location-specific text. |

### Exports

`exports.intents: [scout]` — the `scout` intent can be lifted into the
consumer's intent table via `imports.<alias>.intents.import: [scout]`,
exposing it under the bare name in the consumer's own states.

### `host_interfaces:` — `narrator`

Single-op `announce` interface; default binding `host.run`. Separate
from the inner robbery's `narrator` iface (which the consumer can
rebind via `bandits__narrator`).

### Host requirements

`host.run` (plus the iface-derived `host.run.announce`, satisfied by
the registry's prefix-fallback).

## See also

- `stories/robbery/README.md` — the inner sub-story this wrapper imports.
- `stories/oregon-trail/app.yaml` — the canonical three-layer consumer.
- `docs/imports.md` — `host_interfaces` and multi-layer composition
  (the surface this story is built to exercise).

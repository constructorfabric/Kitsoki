# Robbery — an importable bandit-encounter sub-story

A self-contained kitsoki story exercising the story-imports proposal
(`docs/imports.md`). A masked rider demands
tribute; the player chooses to pay, fight, or flee. Each path has a
named exit the importer maps to whatever follow-up state fits.

Standalone:

```
kitsoki run stories/robbery/app.yaml
```

Imported (see `stories/frontier_event/` for a wrapper example, or
`stories/oregon-trail/app.yaml` for a three-layer composition).

## Contract

### Entry state

`encounter` — the initial bandit-meets-wagon room. Set on import via
`entry: encounter` (default; matches the standalone `root:` too).

### Exits

| Name | Description | `requires:` keys | Typical world_out |
|---|---|---|---|
| `paid` | Tribute paid; party rides on. | `paid_amount` | Importer deducts `paid_amount` from its own money. |
| `routed` | Bandits driven off without losses. | (none) | Optional reward; usually no projection. |
| `killed` | A party member died fighting. | `member_lost` (must be true) | Importer decrements `party_alive`. |
| `fled` | Escaped without engaging. | (none) | Optional pace cost. |

Authors targeting `@exit:paid` must `set: { paid_amount: ... }` in the
transition's effects — the loader's `requires:` check enforces this at
load time.

### `world_in:` keys (parent → child)

The importer projects these from its own world. All have type+default
in robbery's `world:` block so the child can be loaded standalone for
testing.

| Key | Type | Used by | Default |
|---|---|---|---|
| `party_money` | int | The pay-affordability guard. | `0` |
| `party_food_lbs` | int | Reserved for future variants. | `0` |
| `party_alive` | int | The fight-survival guard. | `5` |
| `threat_level` | int (1-3) | Scales the tribute amount and the fight difficulty. | `2` |

Example:

```yaml
world_in:
  party_money:    "{{ world.money }}"
  party_alive:    "{{ world.party_alive }}"
  threat_level:   "{{ world.miles_traveled < 500 ? 1 : (world.miles_traveled < 1200 ? 2 : 3) }}"
```

### Intent surface

The library defines four intents, all exposed for the importer's intent
table via `exports.intents`:

| Intent | Description | Slots |
|---|---|---|
| `pay` | Pay the bandits off. | — |
| `fight` | Fight the bandits. | — |
| `flee` | Try to outrun them. | — |
| `look` | Look around. (not exported) | — |

Importers may re-export any of `pay`/`fight`/`flee` into their own
intent table via `imports.<alias>.intents.import: [...]`. The `look`
intent stays child-scoped.

### `host_interfaces:` — `narrator`

One declared capability surface:

| Op | Input | Output | Description |
|---|---|---|---|
| `announce` | `{ message: string }` | `{ ok: bool }` | Open the scene. Fires on `encounter` entry. |
| `close` | `{ summary: string }` | `{ ok: bool }` | Close the scene. Fires on every exit transition. |

**Default binding:** `host.run` (no-op echo via `cmd:` arg).

**Rebinding from an importer:** set `host_bindings.narrator: <handler>`
on the import. The handler is dispatched as `<handler>.<op>` (e.g.,
`host.diary.announce` if you bind to `host.diary`); the host registry
falls back to the bare `<handler>` when no per-op handler is registered.

The two-op surface exercises proposal §11.1 "multi-op interfaces" — a
host implementation can register one handler that branches on op, or
two handlers that dispatch separately.

### Host requirements

Standalone: `host.run` only.

The narrator iface contributes `host.run.announce` and `host.run.close`
implicitly via the resolver's default binding; the host registry's
prefix-fallback (story-imports §11) maps both to the registered
`host.run` handler.

Importers using `hosts: declared` mode need to enumerate `host.run` in
their own `hosts:` block; the iface-implied handler names are added
automatically by the loader at final resolution.

### `overrides:`

Importers may patch:

- `overrides.states.encounter` — whole-state replacement (loud,
  predictable; full state body must be redeclared).
- `overrides.intents.{pay,fight,flee,look}` — replace one intent
  definition. Useful for extending `examples:` lists for better
  deterministic matching.
- `overrides.prompts."prompts/encounter_intro.md"` — substitute the
  prompt file referenced in the `announce` invocation's `with: prompt:`
  arg. The rewrite walks every Effect.With["prompt"] in the child's
  state tree.

See `stories/oregon-trail/app.yaml` (imports.frontier.overrides) for a
worked example.

## File layout

```
stories/robbery/
  app.yaml                 — manifest (this story's loadable surface)
  prompts/
    encounter_intro.md     — default narrator prompt; importable for override
  flows/
    paid.yaml              — standalone deterministic flow: pay → @exit:paid
    fought_killed.yaml     — fight under-strength → @exit:killed
    fled.yaml              — flee → @exit:fled
  README.md                — this file
```

## See also

- `docs/imports.md` — the imports authoring reference this story is
  built to exercise.
- `stories/frontier_event/` — a wrapper sub-story that imports
  `robbery` and adds a scouting beat. Demonstrates multi-layer composition.
- `stories/oregon-trail/app.yaml:258` — three-layer composition
  (oregon-trail → frontier_event → robbery) with rebindings, overrides,
  and intent surface mapping.

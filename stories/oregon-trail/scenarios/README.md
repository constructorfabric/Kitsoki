# Warp-basis scenarios

YAML files in this directory are **warp bases**: small descriptors that
the TUI's `/warp file:<path>` command (or the `--warp <path>` flag on
`kitsoki run`) loads to drop a running session into a primed mid-game
state. Useful for repeatable manual / smoke testing without playing
the trail from Independence.

## Usage

Interactive (from inside the TUI):

```
/warp file:scenarios/chimney_robbery.yaml
```

(Path resolves relative to the loaded app — `stories/oregon-trail/` —
so the relative form above works regardless of cwd.)

At session boot (skip intro entirely):

```
kitsoki run stories/oregon-trail/app.yaml --warp scenarios/chimney_robbery.yaml
```

The teleport runs before the first TUI frame so the operator lands at
the primed state immediately.

## Authoring

```yaml
name:        "Short human label"      # optional; echoed to transcript
description: "What this scenario is for"  # optional
state:       <state-path>             # required, dot-separated
world:                                # optional, overrides for Teleport.Slots
  key: value
  ...
```

The flow-fixture-style `initial_state` + `initial_world` keys are also
accepted, so a flow fixture (test_kind: flow) can double as a warp
basis verbatim. This means a recorded smoke test can be loaded as a
boot-time warp without conversion.

## Catalogue

| File | Lands at | Useful for |
|---|---|---|
| `chimney_robbery.yaml` | `leg_c_awaiting_reply` (Chimney Rock, threat 2) | testing the imported `frontier` sub-story's pay/fight/flee paths |
| `south_pass_fight.yaml` | `leg_e_awaiting_reply` (South Pass, threat 3, party of 4) | exercising the `@exit:killed` member-loss path |

Add new scenarios by dropping a YAML file here and updating the table
above.

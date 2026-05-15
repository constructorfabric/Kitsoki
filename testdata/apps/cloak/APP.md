# Cloak of Darkness

**Version** 0.1.0 · _by Roger Firth (spec), ported to Kitsoki_ · License: Spec in public domain; this port CC0

## Overview

- App ID: `cloak-of-darkness`
- Entry room: [`foyer`](#room-foyer)
- Rooms: 6
- Intents: 6
- World variables: 3

## State Diagram

```mermaid
flowchart LR
  bar["bar"]
  bar__dark["bar.dark"]
  bar__lit["bar.lit"]
  cloakroom["cloakroom"]
  ended["ended"]
  foyer["foyer"]
  bar__dark -->|*| bar__dark
  bar__dark -->|go [slots.direction == 'north']| foyer
  bar__dark -->|go (default)| bar__dark
  bar__lit -->|go [slots.direction == 'north']| foyer
  bar__lit -->|go (default)| bar__lit
  bar__lit -->|look| bar__lit
  bar__lit -->|read_message| ended
  cloakroom -->|go [slots.direction == 'east']| foyer
  cloakroom -->|go (default)| cloakroom
  cloakroom -->|hang_cloak [world.wearing_cloak == true]| cloakroom
  cloakroom -->|hang_cloak (default)| cloakroom
  cloakroom -->|look| cloakroom
  cloakroom -->|wear_cloak [world.wearing_cloak == false]| cloakroom
  cloakroom -->|wear_cloak (default)| cloakroom
  foyer -->|go [slots.direction == 'south']| bar
  foyer -->|go [slots.direction == 'west']| cloakroom
  foyer -->|go [slots.direction == 'north']| foyer
  foyer -->|go (default)| foyer
  foyer -->|look| foyer
```

## World Variables

| Name | Type | Default | Values |
|---|---|---|---|
| `disturbance` | `int` | `0` |  |
| `message_rumpled` | `bool` | `false` |  |
| `wearing_cloak` | `bool` | `true` |  |

## Intents

### <a id="intent-drop-cloak"></a> `drop_cloak` — Drop the cloak

Drop the cloak on the floor wherever you're standing.

- Priority **10**
- Hidden (not shown in default menu)
- Examples: `drop the cloak`, `discard the cloak`

### <a id="intent-go"></a> `go` — Go

Move in a compass direction.

- Priority **100**
- Examples: `go south`, `head north`, `walk east`, `n`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `direction` | `enum` | yes |  | `north`, `south`, `east`, `west`, `up`, `down` | Which compass direction to move. |

### <a id="intent-hang-cloak"></a> `hang_cloak` — Hang the cloak

Hang the cloak on the hook in the cloakroom.

- Priority **80**
- Examples: `hang the cloak`, `hang my cloak on the hook`, `put the cloak up`

### <a id="intent-look"></a> `look` — Look around

Describe the current room again.

- Priority **20**
- Examples: `look`, `look around`, `describe the room`

### <a id="intent-read-message"></a> `read_message` — Read the message

Read the message scrawled in the sawdust on the bar floor.

- Priority **90**
- Examples: `read the message`, `read the writing`, `read it`

### <a id="intent-wear-cloak"></a> `wear_cloak` — Wear the cloak

Put the cloak back on.

- Priority **70**
- Examples: `wear the cloak`, `put on the cloak`, `take the cloak`

## Rooms

### <a id="room-bar"></a> `bar`  _(compound)_

A small bar off the foyer. Lit when the cloak is hung up; dark otherwise.

**Initial child**: `{% if world.wearing_cloak %}dark{% else %}lit{% endif %}`

**Shows world**: `wearing_cloak`, `disturbance`

### <a id="room-bar-dark"></a> `bar.dark`

Too dark to see. Fumbling around increases disturbance.

**Shows world**: `disturbance`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | `*` |  | `.` | _hint: It's pitch dark -- you can't see to do that._ · increment `disturbance += 1` · say "You can't see a thing." |
| 2 | [`go`](#intent-go) | `slots.direction == 'north'` | `../../foyer` |  |
| 3 | [`go`](#intent-go) | _default_ | `.` | _hint: You can't see well enough in the dark to head that way. Try going north, or finding a way to make light._ · increment `disturbance += 1` · say "Blundering around in the dark isn't a good idea." |

### <a id="room-bar-lit"></a> `bar.lit`

The bar, lit now the cloak is hung up. A message is scrawled on the floor.

**Shows world**: `disturbance`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`go`](#intent-go) | `slots.direction == 'north'` | `../../foyer` |  |
| 2 | [`go`](#intent-go) | _default_ | `.` | say "You can't go that way from the bar." |
| 3 | [`look`](#intent-look) |  | `.` |  |
| 4 | [`read_message`](#intent-read-message) |  | [`ended`](#room-ended) | set `message_rumpled = "{{ world.disturbance > 2 }}"` |

### <a id="room-cloakroom"></a> `cloakroom`

A small cloakroom with a single hook. Exit east.

**Shows world**: `wearing_cloak`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`go`](#intent-go) | `slots.direction == 'east'` | [`foyer`](#room-foyer) |  |
| 2 | [`go`](#intent-go) | _default_ | [`cloakroom`](#room-cloakroom) | say "You can't go that way from here." |
| 3 | [`hang_cloak`](#intent-hang-cloak) | `world.wearing_cloak == true` | [`cloakroom`](#room-cloakroom) | set `wearing_cloak = false` · say "You hang the cloak on the hook." |
| 4 | [`hang_cloak`](#intent-hang-cloak) | _default_ | [`cloakroom`](#room-cloakroom) | _hint: You aren't wearing the cloak -- nothing to hang._ · say "You aren't wearing the cloak." |
| 5 | [`look`](#intent-look) |  | [`cloakroom`](#room-cloakroom) |  |
| 6 | [`wear_cloak`](#intent-wear-cloak) | `world.wearing_cloak == false` | [`cloakroom`](#room-cloakroom) | set `wearing_cloak = true` · say "You take the cloak from the hook." |
| 7 | [`wear_cloak`](#intent-wear-cloak) | _default_ | [`cloakroom`](#room-cloakroom) | _hint: You're already wearing the cloak._ · say "You're already wearing the cloak." |

### <a id="room-ended"></a> `ended`  _(terminal)_

The journey is over.

**Shows world**: `message_rumpled`, `disturbance`

### <a id="room-foyer"></a> `foyer`  _(root)_

The entrance hall of the opera house. South leads to the bar; west to the cloakroom.

**Shows world**: `wearing_cloak`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`go`](#intent-go) | `slots.direction == 'south'` | [`bar`](#room-bar) |  |
| 2 | [`go`](#intent-go) | `slots.direction == 'west'` | [`cloakroom`](#room-cloakroom) |  |
| 3 | [`go`](#intent-go) | `slots.direction == 'north'` | [`foyer`](#room-foyer) | _hint: The weather outside is awful -- you've only just arrived._ · say "You've only just arrived, and besides, the weather outside seems to be getting worse." |
| 4 | [`go`](#intent-go) | _default_ | [`foyer`](#room-foyer) | say "You can't go that way." |
| 5 | [`look`](#intent-look) |  | [`foyer`](#room-foyer) |  |

## Off-path Escape Hatch

- Trigger: `/freeform`
- Banner: "*** off the path -- responses do not affect your story ***"
- Return: `/onpath`

---

_Generated from `app.yaml` by `kitsoki render`. Do not edit this file directly — edit `app.yaml` and re-run `kitsoki render`. See `kitsoki docs apply-proposal` for the LLM-driven proposal workflow._

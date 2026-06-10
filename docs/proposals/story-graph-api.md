# Runtime: Story Graph API

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [story-editor-view.md](story-editor-view.md)

## Why

The web editor (slices 2 and 3 of this epic) needs to answer three
questions the engine can answer but doesn't yet expose over HTTP:

1. **Room order** — given the entry point, which rooms are reachable and
   in what BFS-depth order? Authors think in terms of "early rooms" and
   "late rooms"; the YAML map gives neither.
2. **Room structure** — for a given room, what is its hook (`on_enter:`
   effects), which world keys does it read/write, what intents are
   locally defined, and what are its typed-view elements?
3. **Oracle contracts** — which oracle calls does the room make (via
   `on_enter:` or intent arcs), and for each call, what is the expected
   input shape, output schema, and the name of the cassette used in
   flow-test mode?

None of this requires the orchestrator. The `app.App` interface already
carries the loaded, validated story; this slice adds a read path from
`app.App` → JSON for the editor frontend.

## What changes

New package `internal/app/graph` with two exported types:

- `RoomList` — BFS traversal of `App` from `App.InitialState()`,
  returning rooms in ascending average-distance order, each annotated
  with its distance, on_enter effects, resolved world keys, intents, and
  typed view.
- `OracleContract` — per-oracle-call descriptor: call kind (ask/decide/
  extract/converse/task), the prompt template path, the declared output
  schema (if any), and the cassette key the flow fixture would use.

New read RPCs in `internal/runstatus/server/`:

| RPC | Returns | Notes |
|---|---|---|
| `GET /editor/rooms` | `[]RoomSummary` (id, label, distance, has_oracle) | ordered by BFS distance |
| `GET /editor/rooms/{id}` | `RoomDetail` (full structure) | id = URL-encoded state path |
| `GET /editor/rooms/{id}/oracles` | `[]OracleContract` | oracle calls declared in this room |

These endpoints are read-only, require no session, and do not touch the
orchestrator. They are gated by a new `WithApp` middleware (parallel to
`WithDriver`) so `kitsoki status serve` — which has no loaded `App` —
returns `code=notSupported`.

## Vocabulary changes

No new story-YAML effects or world keys. The change is entirely in the
Go API and HTTP surface.

## The model

### BFS distance

Starting from `App.InitialState()`, walk the state graph via
`State.On[intent][i].Target` transitions. For compound states, descend
into `State.States` to enumerate children; treat the compound parent as
a passthrough node (it has no view of its own). For parallel states,
each child contributes independently.

Average distance = mean of all shortest-path distances from the entry
point to the room across all possible paths. If a room is reachable via
two paths of length 2 and 4, its distance is 3.0. Rooms unreachable
from entry get distance `+Inf` and sort last.

The BFS is pure graph walking over `app.App` — no I/O, no LLM, fully
deterministic and fast (<1ms for any realistic story).

### Room structure

`RoomDetail` carries:

```go
type RoomDetail struct {
    ID          string        // URL-safe state path
    Label       string        // description or derived from path
    Distance    float64       // average BFS distance from entry
    OnEnter     []EffectSpec  // on_enter effect list, verbatim
    WorldKeys   []WorldKey    // keys read/written by on_enter + view template
    Intents     []IntentSpec  // locally-declared intents (not global lib)
    Transitions []TransitionSpec // arcs: intent → target rooms
    View        app.View      // typed view elements (serialised to JSON)
}
```

`WorldKey` carries `{Name, Type, Direction}` where direction is `read`,
`write`, or `readwrite`, derived by static analysis of the effect list
and view template references (conservative: any reference = read,
any `set:` = write).

### Oracle contracts

`OracleContract` per oracle call in `on_enter:` or intent arcs:

```go
type OracleContract struct {
    Kind         string // ask | decide | extract | converse | task
    PromptPath   string // path to the .md template, relative to app root
    OutputSchema string // JSON schema string if declared, "" otherwise
    CassetteKey  string // the key used in flow-test cassettes for this call
    EffectIndex  int    // position in on_enter, or arc index for intent arcs
}
```

`CassetteKey` is derived by the same key-generation logic already used
in `internal/oracle/` for cassette matching, so the workbench can load
the right cassette file without guessing.

## Decision recording

This slice adds no new trace events — it reads `app.App` only. No
interpretive decisions are made. Any oracle calls exercised via the
workbench (slice 3) will record to the active trace if a session is
live; that's slice 3's concern.

## Engine seams & invariants

- `internal/app/graph/` — new package, pure functions over `app.App`.
  Load-time invariants: none new. If `App.LookupState` returns false for
  a transition target, the BFS skips it (broken transitions are caught by
  existing load-time validation).
- `internal/runstatus/server/editor.go` — new file, three handlers.
  Wired into the server's router alongside the existing handlers
  (`server.go:~line 80` mux setup). The `WithApp` middleware stores the
  loaded `app.App` in the request context, populated at
  `kitsoki web` startup.

## Backward compatibility

Additive only. Existing `kitsoki status serve` (no app loaded) returns
`notSupported` on all `/editor/*` routes. No existing RPCs change.
No story files or cassettes change.

## Tasks

```
## 1. Graph package
- [ ] 1.1 `internal/app/graph/bfs.go` — BFS traversal; `RoomList(app) []RoomSummary`
- [ ] 1.2 `internal/app/graph/detail.go` — `RoomDetail(app, stateID)`; world-key
          static analysis (conservative: grep effect list + view template refs)
- [ ] 1.3 `internal/app/graph/oracle.go` — `OracleContracts(app, stateID)`:
          walk on_enter + intent arcs for oracle host calls; derive CassetteKey
          using the same keying logic as `internal/oracle/` cassette lookup
- [ ] 1.4 Unit tests over `stories/prd/app.yaml` and `stories/cloak/app.yaml`:
          assert room order, world-key attribution, and cassette key derivation;
          no LLM, no I/O

## 2. Editor RPCs
- [ ] 2.1 `internal/runstatus/server/editor.go` — three handlers; `WithApp` middleware
- [ ] 2.2 Wire into router (server.go mux); gated by `WithApp` presence
- [ ] 2.3 Handler tests: load prd fixture, call each endpoint, assert JSON shape;
          assert `kitsoki status serve` (no app) returns notSupported

## 3. Document
- [ ] 3.1 Add editor RPC surface to `docs/tui/web-ui.md` under a new
          "Editor endpoints" section; trim/delete this proposal once slices 2+3 ship
```

## Verification

A reviewer can confirm without an LLM:

```
kitsoki web stories/prd/app.yaml &
curl http://localhost:<port>/editor/rooms | jq '.[].label'
# expect rooms in BFS order from stories/prd/app.yaml entry point

curl http://localhost:<port>/editor/rooms/clarifying/oracles | jq '.'
# expect oracle contracts for the clarifying room's on_enter calls
```

## Open questions

1. **World-key static analysis depth** — conservative (any template
   reference = read) is safe but may over-report. Options: (a)
   conservative read + explicit `set:` for write (simple, always correct),
   (b) full effect-list interpreter (accurate but complex). *Lean: (a)
   for v1; the editor labels over-attributed keys as "possibly read."*

2. **`WithApp` vs. reusing `WithDriver`** — `WithDriver` carries the
   live orchestrator, which implies an active session. The editor reads
   only the static `app.App`. Options: (a) separate middleware (clean),
   (b) embed app in the driver. *Lean: (a) — separates concerns; the
   app can be served even if the orchestrator isn't started.*

## Non-goals

- **No YAML write path** — read-only graph introspection only.
- **No live graph update** — the graph is computed at server start from
  the loaded `app.App`; a file-watch / hot-reload is a follow-on.
- **Not a full static analyser** — world-key attribution is
  conservative; accurate dataflow analysis is a future slice.

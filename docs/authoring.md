# Authoring Guide

How to write a kitsoki application. The companion documents:

- [`state-machine.md`](state-machine.md) — the conceptual model.
- `kitsoki docs app-schema` — the authoritative YAML reference.
- [`testing.md`](testing.md) — flow and intent fixtures.
- [`hosts.md`](hosts.md) — every built-in host handler.

If you only read one source other than the schema, read that.

---

## 1. Anatomy of an app

```
testdata/apps/cloak/
├── app.yaml             single source of truth (or top of an include tree)
├── recording.yaml       (state, input) → (intent, slots) for replay/static harnesses
├── flows/
│   ├── winning.yaml     Mode 2 deterministic flow tests
│   └── losing.yaml
├── intents/
│   └── go_intents.yaml  Mode 1 intent-recognition fixtures (optional)
└── prompts/             optional — Markdown prompt templates for host.oracle.ask
    └── shell_repair.md
```

Kitsoki reads only the YAMLs. Markdown prompts under `prompts/` are
referenced by `host.oracle.ask` via relative path. Anything else is
your test/documentation infrastructure.

---

## 2. Minimal runnable app

```yaml
app:
  id: tiny
  version: 0.1.0
  title: "Tiny App"

world:
  counter: { type: int, default: 0 }

intents:
  increment:
    description: "Add one to the counter."
    examples: ["add one", "++", "bump"]
  show:
    description: "Show the counter."
    examples: ["show", "what's the count?"]

root: main

states:
  main:
    view: |
      counter = {{ world.counter }}
    on:
      increment:
        - target: main
          effects:
            - increment: { counter: 1 }
      show:
        - target: main
```

Save as `tiny.yaml`, then:

```sh
kitsoki run tiny.yaml                    # opens TUI; auto-picks harness
kitsoki test flows tiny.yaml             # no fixtures yet → exits 0
kitsoki viz tiny.yaml                    # writes tiny-viz.dot
```

---

## 3. Authoring loop

The loop most authors settle into:

1. **Sketch the graph in YAML.** Start with rooms and intents. Use
   placeholder views.
2. **`kitsoki inspect`** or **`kitsoki turn`** to probe. `kitsoki turn` is
   especially useful — one stateless turn, no DB, JSON output.
3. **Add a flow fixture** under `flows/`. Use `intent:` blocks (no LLM
   needed) to lock the state logic.
4. **`kitsoki test flows`** until green.
5. **Add intent fixtures** for natural-language inputs. Run
   `kitsoki test intents --harness static` to lock pass-rates.
6. **`kitsoki viz`** to sanity-check the graph shape.
7. **`kitsoki render -o APP.md`** to produce review-friendly docs.
8. **`kitsoki run`** to play it for real.

The first four steps catch the vast majority of mistakes before the
LLM ever sees the app.

---

## 4. Top-level fields (cheat sheet)

The full reference is `kitsoki docs app-schema`. The high-altitude shape:

```yaml
app:        { id, version, title, author, license }
world:      { <name>: VarDef }                   # typed key/value bag
intents:    { <name>: Intent }                   # global intent library
root:       <state-name> | <inline-State>        # initial state
states:     { <name>: State }                    # the graph
off_path:   { trigger, banner, return }          # optional escape hatch
hosts:      [ <host-name>, … ]                   # allow-list
proposals:  { <name>: ProposalKind }             # draft → review → execute
phase_templates: { <name>: PhaseTemplate }       # repeated-room compression
phases:     { template, graph }                  # template instantiations
checkpoint_intents: { <name>: { description } }  # injected into _awaiting_reply states
include:    [ <glob>, … ]                        # merge other YAMLs
```

What lives where:

- **Reusable intents** → top-level `intents:`. State `on:` maps reference them by name.
- **State-specific intent overrides** → `states.X.intents:`.
- **Anything that touches the network or filesystem** → `host.*` invocation, declared in the top-level `hosts:` allow-list.
- **Anything that survives a turn** → `world:` with a typed default.
- **Anything per-turn that the user supplied** → `slots:` on the intent.

---

## 5. Common patterns

### 5.1 Catch-all transitions

Always end an intent's transition list with a `default: true` so the
machine never has to emit `GUARD_FAILED` for a benign case:

```yaml
on:
  go:
    - when: "slots.direction == 'south'"
      target: bar
    - when: "slots.direction == 'west'"
      target: cloakroom
    - default: true
      target: foyer
      effects:
        - say: "You can't go that way."
```

### 5.2 Wildcard intent handler

Inside a state where any non-listed intent should behave the same way,
bind `"*"`:

```yaml
on:
  "*":
    - target: .                # stay
      effects:
        - increment: { disturbance: 1 }
        - say: "It's too dark to do that."
```

`target: .` means "stay in the same atomic state".

### 5.3 Calling a host

```yaml
hosts:
  - host.run

states:
  shell_idle:
    on:
      run:
        - target: shell_done
          effects:
            - invoke: host.run
              with:
                cmd: "git status"
                cwd: "{{ world.workspace_root }}"
              bind:
                last_output: stdout
                last_code:   exit_code
              on_error: shell_error
            - say: |
                ```
                {{ world.last_output }}
                ```
```

### 5.4 LLM-backed effect

`host.oracle.ask` runs `claude -p` against a prompt template file with
templated `{{ args.X }}` placeholders; bind `stdout` back into world.
Full contract and the `ask_with_mcp` / `talk` variants in
[`hosts.md`](hosts.md). End-to-end worked example (shell-repair) in
`kitsoki docs llm-guide` §11.1 LLM-backed effects.

### 5.5 Background job

```yaml
hosts:
  - host.run

world:
  result:      { type: string, default: "" }
  last_job_id: { type: string, default: "" }

states:
  running:
    on_enter:
      - invoke: host.run
        with:
          cmd: "long-running-script.sh"
        background: true
        bind: { last_job_id: job_id }
        on_complete:
          - set: { result: "{{ world.last_job_result.stdout }}" }
          - say: "Job complete. Output: {{ world.result }}"
```

When the job finishes, the orchestrator fires the `on_complete:`
effects in a synthetic turn and posts an inbox notification. Full
lifecycle in [`background-jobs/`](background-jobs/README.md).

### 5.6 Posting to a transport

```yaml
hosts:
  - host.transport.post

effects:
  - invoke: host.transport.post
    with:
      transport: "{{ world.transport }}"   # "tui" / "jira" / "bitbucket"
      thread:    "{{ world.thread }}"      # PLTFRM-12345 / PR/repo/42 / session-uuid
      phase_id:  "phase_a"
      title:     "Phase A complete"
      body:      "Result: {{ world.result }}"
```

The transport handles markup conversion (Markdown → Jira wiki for
Jira, etc.). See [`transports.md`](transports.md) for the registry.

### 5.7 Template interpolation: how complex values render

`{{ ... }}` expressions inside YAML strings are evaluated by the
`expr-lang` engine against `world` and `slots`. How the result is
spliced back into the surrounding string depends on its type:

| Value type | Rendering |
|---|---|
| string, int, float, bool | Go's `fmt %v` (the usual default). |
| `map[string]any`, `[]any` | `encoding/json.Marshal` — sorted keys for maps, compact form. |
| nil | empty string. |
| anything else | `%v` (fallback). |

The map/slice case matters when you pass a structured world slot
into a host call argument or a prompt:

```yaml
- invoke: host.run
  with:
    cmd: "consume.py"
    args: ["--input", "pr_status={{ world.pr_status }}"]
```

If `world.pr_status` is `{state: "FAILED", build: "..."}`, the
rendered arg is `pr_status={"build":"...","state":"FAILED"}` —
parseable JSON, sorted keys, ready for the downstream CLI. Without
the JSON rule it would render as Go's `map[build:... state:FAILED]`
repr, which no standard parser can read.

On marshal failure (cyclic graph, unsupported type) the renderer
falls back to `%v` so a corrupt slot doesn't crash the template.
Implemented in
[`internal/expr/expr.go::anyToString`](../internal/expr/expr.go).

---

## 6. Scaling up: includes, phases, proposals, imports

For non-trivial apps, four features compress the YAML:

- **Includes** — `include: ["rooms/*.yaml"]` merges other YAMLs into
  the manifest. Duplicate state or intent names error at load. Use for
  same-app file splitting.
- **Imports** — `imports: { <alias>: { source: ./bugfix } }` embeds
  another *app* as an aliased sub-story. Private world; explicit
  projections through `world_in:` and per-exit `set:`; named exits;
  state/intent/prompt overrides; rebindable `host_interfaces:`. Use
  for cross-repo composition and reusable mini-stories. Full reference:
  [`imports.md`](imports.md).
- **Phase templates** — declare a reusable room shape once, instantiate
  it once per phase. See
  [`state-machine.md` §9](state-machine.md#9-phase-templates-compressing-repeated-rooms).
- **Proposals** — declare a draft → review → execute lifecycle once,
  reuse it for every "user-confirms-then-runs" pattern. Schema in
  `app-schema.md` under `ProposalKind`.

Use them when you'd otherwise be copy-pasting the same five states.
Don't use them on a five-state app.

---

## 7. Authoring tooling

| Command | What it does |
|---|---|
| `kitsoki inspect` | Read-only JSON snapshot of a stored session. |
| `kitsoki turn` | One stateless turn. Great for probing (`--state X --intent Y --world …`). |
| `kitsoki viz` | DOT or Mermaid graph of the state machine. |
| `kitsoki render -o APP.md` | Markdown documentation derived from the YAML — overview, Mermaid diagram, transition tables. |
| `kitsoki test flows` | Mode 2 deterministic tests. |
| `kitsoki test intents` | Mode 1 intent pass-rate tests. |
| `kitsoki run --warp <path>` | Boot the TUI directly into a primed mid-game state from a YAML "warp basis". See [`imports.md`](imports.md#operator-tooling-warp-and---warp). |
| In-TUI `/warp` | Slash command equivalent. `/warp <state> world.X=Y` for inline; `/warp file:<path>` to load a basis. |
| `kitsoki docs apply-proposal` | LLM-facing guide for "implement this prose proposal against `app.yaml`". |
| In-TUI `Edit mode` | Hot-reload editing — see [`developer-guide.md` §8](developer-guide.md#8-hot-reload-edit-mode). |

`kitsoki render` is one-way: the Markdown never feeds back into the
engine. Re-run after every change to keep `APP.md` in sync.

---

## 8. Pitfalls

- **Forgot to declare a host.** The loader rejects `invoke: host.X`
  unless `hosts: [host.X]` is at the top level.
- **Default branch missing.** When no `when:` matches, the user gets
  `GUARD_FAILED`. Always provide a `default: true` arm.
- **`relevant_world: [foo]` for an undeclared world key.** The loader
  rejects this — `foo` must exist in `world:`.
- **State name collisions across includes.** The loader merges
  conservatively; rename one of them.
- **Background job referencing `world.last_job_result` outside
  `on_complete:`.** That variable is injected only into the
  completion turn; outside of it the value is empty.
- **Forgetting `default:` on the last transition for an intent
  inside a phase template.** The expander adds one for `cycle_budgets`,
  but for non-budgeted arcs it's your responsibility.
- **Editing `app.yaml` while a session runs without saving.** Hot
  reload triggers on the watched file's `mtime`; if your editor writes
  through a temp file (vim by default), enable backup-style writes or
  run `kitsoki run --no-watch` (when implemented) and reload by hand.

---

## 9. Where to next

- **The schema** — `kitsoki docs app-schema`.
- **Worked examples** — `testdata/apps/cloak`, `testdata/apps/dev-story`,
  `testdata/apps/proposal_smoke`, `testdata/apps/background_jobs`.
- **Embedded operator manual** — `kitsoki docs llm-guide`.
- **The state machine in depth** — [`state-machine.md`](state-machine.md).

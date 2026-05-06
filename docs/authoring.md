# Authoring Guide

How to write a hally application. The companion documents:

- [`state-machine.md`](state-machine.md) — the conceptual model.
- `hally docs app-schema` — the authoritative YAML reference.
- [`testing.md`](testing.md) — flow and intent fixtures.
- [`hosts.md`](hosts.md) — every built-in host handler.

If you only read one source other than the schema, read that.

---

## 1. Anatomy of an app

```
testdata/apps/cloak/
├── app.yaml             single source of truth (or top of an include tree)
├── oracle.yaml          (state, input) → (intent, slots) for replay/static harnesses
├── flows/
│   ├── winning.yaml     Mode 2 deterministic flow tests
│   └── losing.yaml
├── intents/
│   └── go_intents.yaml  Mode 1 intent-recognition fixtures (optional)
└── prompts/             optional — Markdown prompt templates for host.oracle.ask
    └── shell_repair.md
```

Hally reads only the YAMLs. Markdown prompts under `prompts/` are
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
hally run tiny.yaml                    # opens TUI; auto-picks harness
hally test flows tiny.yaml             # no fixtures yet → exits 0
hally viz tiny.yaml                    # writes tiny-viz.dot
```

---

## 3. Authoring loop

The loop most authors settle into:

1. **Sketch the graph in YAML.** Start with rooms and intents. Use
   placeholder views.
2. **`hally inspect`** or **`hally turn`** to probe. `hally turn` is
   especially useful — one stateless turn, no DB, JSON output.
3. **Add a flow fixture** under `flows/`. Use `intent:` blocks (no LLM
   needed) to lock the state logic.
4. **`hally test flows`** until green.
5. **Add intent fixtures** for natural-language inputs. Run
   `hally test intents --harness static` to lock pass-rates.
6. **`hally viz`** to sanity-check the graph shape.
7. **`hally render -o APP.md`** to produce review-friendly docs.
8. **`hally run`** to play it for real.

The first four steps catch the vast majority of mistakes before the
LLM ever sees the app.

---

## 4. Top-level fields (cheat sheet)

The full reference is `hally docs app-schema`. The high-altitude shape:

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

```yaml
hosts:
  - host.oracle.ask

# prompts/shell_repair.md
# Original command:
# {{ args.failed_cmd }}
# Exit: {{ args.exit_code }}
# Error:
# {{ args.last_error }}
#
# Produce the corrected command. Your final message is the literal
# replacement command and nothing else.

states:
  terminal_error:
    on:
      fix:
        - target: terminal_reviewing
          effects:
            - invoke: host.oracle.ask
              with:
                prompt_path: "prompts/shell_repair.md"
                failed_cmd:  "{{ world.proposal_cmd }}"
                exit_code:   "{{ world.last_exit }}"
                last_error:  "{{ world.last_error }}"
              bind:
                proposal_cmd: stdout
              on_error: terminal_error
```

`host.oracle.ask` runs `claude -p` with full tool access by default;
write the prompt as if you're talking to a Claude Code session. See
[`hosts.md`](hosts.md) for the full contract and the `ask_with_mcp` /
`talk` variants.

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

---

## 6. Scaling up: includes, phases, proposals

For non-trivial apps, three features compress the YAML:

- **Includes** — `include: ["rooms/*.yaml"]` merges other YAMLs into
  the manifest. Duplicate state or intent names error at load.
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
| `hally inspect` | Read-only JSON snapshot of a stored session. |
| `hally turn` | One stateless turn. Great for probing (`--state X --intent Y --world …`). |
| `hally viz` | DOT or Mermaid graph of the state machine. |
| `hally render -o APP.md` | Markdown documentation derived from the YAML — overview, Mermaid diagram, transition tables. |
| `hally test flows` | Mode 2 deterministic tests. |
| `hally test intents` | Mode 1 intent pass-rate tests. |
| `hally docs apply-proposal` | LLM-facing guide for "implement this prose proposal against `app.yaml`". |
| In-TUI `Edit mode` | Hot-reload editing — see [`developer-guide.md` §8](developer-guide.md#8-hot-reload-edit-mode). |

`hally render` is one-way: the Markdown never feeds back into the
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
  run `hally run --no-watch` (when implemented) and reload by hand.

---

## 9. Where to next

- **The schema** — `hally docs app-schema`.
- **Worked examples** — `testdata/apps/cloak`, `testdata/apps/dev-story`,
  `testdata/apps/proposal_smoke`, `testdata/apps/background_jobs`.
- **Embedded operator manual** — `hally docs llm-guide`.
- **The state machine in depth** — [`state-machine.md`](state-machine.md).

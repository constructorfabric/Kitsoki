# `kitsoki turn` — headless one-turn driver

`kitsoki turn` runs a single turn against an app. Two modes:

---

## 1. Trace-backed persistent turn (recommended for external drivers)

```sh
kitsoki turn --app <app.yaml> --trace <path.jsonl> --intent <name> [--slot k=v ...]
```

Load an existing trace (or create a fresh one), run one turn, append the new
events to the trace file, and write the new events to stdout as JSONL.

### Flags

| Flag              | Default   | Description                                                   |
|-------------------|-----------|---------------------------------------------------------------|
| `--app <path>`    | required  | Path to `app.yaml`.                                           |
| `--trace <path>`  | required  | JSONL trace file. Created if absent; appended if present.    |
| `--intent <name>` | required  | Intent name to invoke directly (skips the LLM harness).      |
| `--slot k=v`      | repeatable| Slot key=value pair. Repeat for multiple slots.              |

### Behaviour

1. If the trace file does not exist, create it (`session.header` + events for
   turn 1).
2. If the trace file exists, open it, validate the header, and fold the history
   into `(state, world, turn)` via `BuildJourney`.
3. Run one direct-intent turn (`SubmitDirect`) in the folded session.
4. Append the new events to the trace file.
5. Write the new events (and only the new events) to stdout as JSONL — drivers
   that want streaming don't have to diff the file.

### Exit codes

| Code | Meaning                                            |
|------|----------------------------------------------------|
| 0    | Intent accepted; session transitioned.             |
| 1    | Intent rejected (wrong state, guard failed, etc.). |
| 2    | Session reached a terminal state.                  |
| 3    | Infrastructure error (missing app, malformed slot, open failure, …). |

For exit 0–2 the outcome is self-describing via the JSONL events on stdout.
For exit 3, the error message is written to stderr.

### Examples

```sh
# First turn: foyer → cloakroom
kitsoki turn --app stories/cloak/app.yaml --trace /tmp/cloak.jsonl \
    --intent go --slot direction=west

# Second turn: cloakroom → ...
kitsoki turn --app stories/cloak/app.yaml --trace /tmp/cloak.jsonl \
    --intent hang_cloak

# Inspect the trace
kitsoki trace /tmp/cloak.jsonl
jq 'select(.kind=="machine.state_entered") | .state_path' /tmp/cloak.jsonl
```

---

## 2. Stateless one-shot probe

```sh
kitsoki turn <app.yaml> --state <path> --intent <name> [--slots JSON] [--world JSON]
kitsoki turn <app.yaml> --state <path> --input "free text" [--harness claude]
```

Run a single stateless turn without persisting anything. Prints a JSON
document describing the state transition, world diff, and view to stdout.
Nothing is written to disk. Useful for "what would happen if...?" probing.

### Flags (stateless mode)

| Flag               | Description                                                   |
|--------------------|---------------------------------------------------------------|
| `--state <path>`   | Starting state path (required).                               |
| `--intent <name>`  | Intent to invoke directly (mutually exclusive with --input). |
| `--input <text>`   | Free-text routed through the harness (requires --harness).   |
| `--slots <JSON>`   | Intent slots as inline JSON or `@file`.                       |
| `--world <JSON>`   | World overrides as inline JSON or `@file`.                    |
| `--harness <type>` | Harness for `--input`: `claude\|live\|replay`.               |
| `--recording <p>`  | Recording YAML for `--harness replay`.                        |

### Examples

```sh
kitsoki turn app.yaml --state cloakroom --intent hang_cloak
kitsoki turn app.yaml --state foyer \
    --input "head south" --harness replay --recording recording.yaml
kitsoki turn app.yaml --state cloakroom --intent look \
    --world '{"wearing_cloak": false}'
```

---

## See also

- `docs/trace-format.md` — full JSONL trace schema and event vocabulary.
- `kitsoki run` — interactive TUI session (uses the same EventSink trace internally).
- `kitsoki trace <path>` — pretty-print a trace file.

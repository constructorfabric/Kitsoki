# hally

Hally is a deterministic LLM orchestrator: a CLI tool that lets a human drive a
structured application with free-text input. The LLM translates natural language
into a finite alphabet of intents defined by the application author; a pure Go
state machine decides what happens next.

**Free-text in, deterministic transitions out.** No hallucinated flags, no
out-of-state actions, no surprise mutations. Every transition, guard, and world
effect is declared by the author in YAML.

Full design: [`design.md`](design.md).

## Status

**PoC — Stage 7 of 7 complete.** All core features are implemented:
working TUI, Cloak of Darkness demo, Mode 1/2 test runners, DOT visualizer.

## Quickstart

```sh
go build -o hally ./cmd/hally

# Play Cloak of Darkness — hally auto-selects the harness:
#   • If `claude` CLI is installed and logged in → uses ClaudeCLIHarness (no API key needed)
#   • Else if ANTHROPIC_API_KEY is set           → uses LiveHarness (direct SDK)
#   • Else                                       → needs --harness replay + --oracle
./hally run testdata/apps/cloak/app.yaml

# Explicitly use the claude CLI harness (recommended):
./hally run testdata/apps/cloak/app.yaml --harness claude

# Use the replay harness (deterministic, no LLM, requires oracle):
./hally run testdata/apps/cloak/app.yaml \
    --harness replay \
    --oracle testdata/apps/cloak/oracle.yaml

# Run Mode 2 flow tests (deterministic, no LLM, no cost)
./hally test flows testdata/apps/cloak/app.yaml

# Run Mode 1 intent tests (static harness — no LLM in PoC)
./hally test intents testdata/apps/cloak/app.yaml --harness static

# Emit a DOT graph of the app
./hally viz testdata/apps/cloak/app.yaml
dot -Tpng cloak-of-darkness-viz.dot -o graph.png
```

## Authentication

### Using Claude Code login (default)

If you have the `claude` CLI installed and are logged in (`claude login`), hally
uses it automatically via `claude -p`. No `ANTHROPIC_API_KEY` is needed. The
model and auth come from your standard Claude Code login. This is the default
when `claude` is on your `PATH`.

```sh
# No setup needed beyond having Claude Code installed and logged in:
./hally run testdata/apps/cloak/app.yaml
```

### Using the Anthropic API directly

Set `ANTHROPIC_API_KEY` to use the SDK directly and bypass the Claude CLI:

```sh
export ANTHROPIC_API_KEY=sk-ant-...
./hally run testdata/apps/cloak/app.yaml --harness live
```

### Static / offline (no LLM)

Use `--harness static` (for `hally test intents`) or `--harness replay` (for
`hally run`) to run fully deterministically without any LLM calls:

```sh
./hally run testdata/apps/cloak/app.yaml \
    --harness replay --oracle testdata/apps/cloak/oracle.yaml
./hally test intents testdata/apps/cloak/app.yaml --harness static
```

## Cloak of Darkness demo

The demo app (`testdata/apps/cloak/app.yaml`) is the "hello world" of
interactive fiction — three rooms, a velvet cloak, and a win/lose ending
determined by how many times you bumbled around in the dark.

```
demo/cloak.tape   — VHS tape for recording the demo GIF
demo/cloak.gif    — rendered GIF (generate with: vhs demo/cloak.tape)
```

VHS (`charmbracelet/vhs`) is required to generate the GIF. It was not
installed on the build machine; the `.tape` file is checked in instead.
Install VHS with: `go install github.com/charmbracelet/vhs@latest`
(also requires ffmpeg and chromium).

## Testing

Hally has two test modes:

### Mode 2: Deterministic flow tests (no LLM)

```sh
hally test flows <app.yaml> [--flows <glob>] [--oracle <path>] [--json <out>]
```

- Zero cost, runs on every PR.
- Each fixture is a YAML file (`test_kind: flow`) with a sequence of turns
  and per-turn assertions (`expect_state`, `expect_world`, `expect_view_matches`, etc.).
- Uses a replay oracle for `input:`-based turns; or structured `intent:` blocks
  that bypass the oracle entirely.

Example:

```sh
hally test flows testdata/apps/cloak/app.yaml
# Summary: 3/3 flows pass
```

### Mode 1: Intent pass-rate tests (LLM or static harness)

```sh
hally test intents <app.yaml> [--harness live|static] [--runs N] \
    [--dry-run] [--only <state>] [--emit-oracle <path>] [--baseline <path>]
```

- Measures how reliably the LLM maps user phrasings to the correct intents.
- Default harness: `static` (seeded from oracle, no LLM calls) when
  `ANTHROPIC_API_KEY` is not set; `live` otherwise.
- `--emit-oracle` compiles majority-vote results into a replay oracle for Mode 2.

Example:

```sh
hally test intents testdata/apps/cloak/app.yaml --harness static
# Summary: 15/15 fixtures pass
```

## Demo recording (`hally record`)

`hally record` replays a deterministic flow through the state machine and
encodes each state's view as an animated GIF.  The same flow YAML that drives
`hally test flows` also drives `hally record` — one source of truth.

```sh
hally record <app.yaml> --flow <flow.yaml|dir> [-o out.gif] [flags]
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--flow` | (required) | flow YAML file or directory |
| `-o` / `--out` | `<flow>.gif` | output path |
| `--width` | 2560 | frame width px |
| `--height` | 1800 | frame height px |
| `--theme` | molokai | `molokai`, `dracula`, or `light` |
| `--frame-ms` | 2500 | how long each frame shows (ms) |
| `--settle-ms` | 1500 | pause after each frame (ms) |
| `--oracle` | (optional) | oracle YAML for `input:` turns |

### Example

```sh
# Record the cloak-of-darkness winning path:
hally record testdata/apps/cloak/app.yaml \
  --flow testdata/apps/cloak/flows/winning.yaml \
  -o /tmp/cloak-win.gif

# Record all flows in a directory:
hally record myapp.yaml --flow myapp/flows/ -o demo.gif --theme dracula
```

### Rasterisation

Path B (minimal deps): `golang.org/x/image/font/basicfont` at 2× scale draws
monospace text onto an RGBA canvas.  ANSI escapes in view templates are stripped
before rendering.  Output is byte-reproducible: same flow + same flags = same
GIF bytes.

### vs. VHS

| | VHS | hally record |
|---|---|---|
| Source of truth | `.tape` + `oracle.yaml` (two files) | single flow YAML |
| External deps | vhs, ttyd, ffmpeg | none |
| Font/timing variance | yes | no |
| Also runs as test | no | yes (`hally test flows`) |

## Package layout

```
hally/
  cmd/hally/            CLI entrypoint (cobra: run, viz, trace, replay, test, serve)
  internal/
    app/                YAML loader, types, schema validation
    machine/            Pure state machine (XState-flavored, compound states)
    intent/             IntentCall, ValidationError, error-code enum
    expr/               expr-lang/expr wrapper + AST whitelist
    world/              Typed world snapshot
    store/              Event store + snapshots (modernc.org/sqlite, pure-Go)
    mcp/                MCP server (modelcontextprotocol/go-sdk v1)
    harness/            Live / Replay / Recording harness implementations
    tui/                Bubble Tea TUI (bubbletea + lipgloss + huh + glamour)
    viz/                Graphviz DOT exporter (emicklei/dot)
    testrunner/         Mode 1 + Mode 2 test runners + StaticHarness
    trace/              Replay + diff
  pkg/hallytest/        Public testing helpers for app authors
  demo/                 VHS tape + GIF
  testdata/             App definitions, flow fixtures, intent fixtures, oracle
```

## Build

Requires Go 1.25+. Single static binary; no CGO, no external runtime.

```sh
go build ./...          # build all packages
go vet ./...            # vet all packages
go test ./...           # run all tests
go test -race ./...     # run with race detector
go mod tidy             # keep go.mod clean
```

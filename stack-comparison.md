# Hally — Implementation Stack Comparison

**Status:** Draft v0.1
**Author:** Brad Smith
**Date:** 2026-04-20
**Scope:** Implementation stacks only. See `design.md` for the conceptual model, YAML DSL, MCP contract, and determinism story.

This document compares three candidate stacks for building the Hally PoC: (A) a clean-room Go implementation, (B) a LangGraph-anchored Python implementation, and (C) an assembled-from-pieces Python implementation without LangGraph. It is deliberately opinionated.

---

## TL;DR

**Recommendation: Approach A (Go, Charm-ecosystem TUI).** Hally is a deterministic orchestrator that spends almost all of its CPU on two things — running a pure state machine and rendering a multi-pane TUI — with the LLM call dominating wall-clock latency. The hard requirements (single-binary distribution, a native TUI with a transcript / menu / graph layout, a mature MCP server that the user hosts and agents connect into, sandboxed expression evaluation with fast compile+eval, embedded SQLite) land squarely in Go's sweet spot. Bubble Tea v2 ([v2.0.0-rc.2, March 2026](https://github.com/charmbracelet/bubbletea/releases)) plus the Charm ecosystem (Bubbles, Lipgloss, Huh, Glamour, VHS) is the state of the art for programmable terminal UIs, and the official MCP Go SDK shipped a stable v1.0 with semver guarantees in late 2025 ([modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)). **The single most important tiebreaker is distribution + determinism**: a statically-linked `hally` binary with no Python runtime is the difference between an evangelizable tool and a demo.

---

## Summary Comparison Table

| Dimension | A. Go (clean room) | B. LangGraph (Python) | C. Assembled Python | Winner |
|---|---|---|---|---|
| 1. Library maturity & ecosystem fit | First-class (FSM, expr, MCP, TUI all native) | Graft-ish (LangGraph is heterogeneous-first) | First-class, hand-assembled | **A** |
| 2. TUI quality & DX | Bubble Tea v2 + Bubbles + Lipgloss | Textual 7 | Textual 7 | **Tie A/C** (B is hamstrung by LangGraph's async model + poor TUI fit) |
| 3. MCP server story | Official `go-sdk` v1 stable + `mark3labs/mcp-go` | `python-sdk` v1, FastMCP 3.0 | `python-sdk` v1, FastMCP 3.0 | **Tie** (both ecosystems have stable official SDKs in 2026) |
| 4. LLM integration | anthropic-sdk-go v1.19+, cache + tool use complete | Excellent via anthropic Python + LangChain integrations | Direct `anthropic` SDK, cleanest surface | **C** by a hair |
| 5. YAML DSL + sandboxed guards | `expr-lang` / `cel-go` — excellent | `simpleeval` / `cel-python` — acceptable | `simpleeval` / `cel-python` — acceptable | **A** |
| 6. Persistence & replay | `modernc.org/sqlite` pure-Go, event sourcing trivial | LangGraph checkpointer gives you too much and the wrong shape | stdlib `sqlite3` + hand-rolled events | **A** |
| 7. Deployment / distribution | Single static binary | `pip install` + CPython + wheels | `pipx` / `uv tool install` | **A** (decisive) |
| 8. Performance & latency | Fastest cold start, smallest memory | Slowest (LangGraph import tax) | Moderate | **A** |
| 9. Testing story | table-tests, in-process MCP mock | LangGraph's test hooks help here | pytest + respx/vcrpy — cleanest | **Tie B/C** |
| 10. Author productivity (single dev) | Verbose but one language end-to-end | Fast start, slow when you fight the framework | Fast start, no framework fights | **C** |
| 11. Long-term maintenance | Static types + compiler enforce invariants | Framework lock-in; LangGraph changes often | Pydantic v2 types; flexible | **A** |
| 12. Cross-platform / terminal compat | Bubble Tea v2 has the best terminal handling in any language | Textual renders well but fights PTYs on Windows | Textual same as B | **A** |

Aggregate: **A wins 7, C wins 1, ties on 4.** That's a landslide for Go — but only if you value distribution and determinism over authoring speed.

---

## A. Go (Clean Room)

### Stack pieces

| Concern | Library | Version (Apr 2026) | Purpose |
|---|---|---|---|
| TUI framework | [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | v2.0.0-rc.2 (v1.3.10 stable) | Elm-architecture event loop with Cursed Renderer |
| TUI widgets | [charmbracelet/bubbles](https://github.com/charmbracelet/bubbles) | latest on v2 branch | viewport, list, textinput, spinner, table |
| TUI styling | [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | v2 | Declarative styles + layout primitives |
| Forms | [charmbracelet/huh](https://pkg.go.dev/github.com/charmbracelet/huh/v2) | v2 | Slot-filling modal forms |
| Markdown | [charmbracelet/glamour](https://github.com/charmbracelet/glamour) | latest | Render markdown views/transcripts |
| Recording | [charmbracelet/vhs](https://github.com/charmbracelet/vhs) | latest | Declarative demo GIFs |
| State machine | Hand-rolled over pure-data model, optionally [qmuntal/stateless](https://github.com/qmuntal/stateless) | v1.15+ (Feb 2026) | UML-statechart semantics, reentrant + dynamic transitions |
| Expression language | [expr-lang/expr](https://expr-lang.org/) | latest | Compile + eval guards and templates |
| MCP server | [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) | v1.x stable (March 2026) | Official, stable API with semver guarantee |
| LLM client | [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) | v1.19+ (Claude Opus 4.7, Autocompaction, advanced tools) | When we move off `claude -p` subprocess |
| YAML loader | [goccy/go-yaml](https://github.com/goccy/go-yaml) | 1.x (Jan 2026) | Comments + anchor-preserving reversible AST |
| JSON Schema | [santhosh-tekuri/jsonschema/v6](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6) | v6.x | Draft 2020-12 validation for slot payloads |
| SQLite | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | 1.46+ | Pure-Go, no cgo — enables `GOOS=windows` static cross-compile |
| Graph | [dominikbraun/graph](https://github.com/dominikbraun/graph) + [emicklei/dot](https://github.com/emicklei/dot) | latest | In-memory graph + DOT export |
| CLI | [spf13/cobra](https://github.com/spf13/cobra) | latest | Subcommands (`hally run`, `hally viz`, `hally trace`) |

### What you get free

- **Single static binary**. `go build` with CGO_ENABLED=0 and modernc.org/sqlite yields a ~15 MB self-contained executable. No CPython, no venvs, no wheels. Operators download and run — this matters more than anything else on a tool pitched at local CLI use.
- **Goroutines for IO + TUI concurrency**. The TUI program runs in a goroutine; MCP stdio handlers run in another; the orchestrator goroutine glues them. No `asyncio` coloring.
- **Native stdio MCP transport**. The official Go SDK handles stdio and streamable HTTP; we flip the normal embedding pattern (host embeds server) by making Hally the server and `claude -p` the embedder.
- **Compile-time enforcement** of the interfaces declared in `design.md` §12 (`App`, `Machine`, `Store`, `Harness`, `Visualizer`). A refactor breaks the build, not the CI run.
- **Expr's AST whitelist** is trivial — pass an `ast.Node` visitor that rejects everything outside our approved set, per the technique the Expr README documents.

### What you still build

- The YAML-to-state-machine compiler and linter.
- A small template engine (`{{ if world.foo }}…{{ end }}`) on top of Expr. Text/template is too heavy; we want the same sandbox as guards.
- The MCP tool handlers (`transition`, `clarify`, `describe_state`, `off_path`) — thin wrappers over the pure `Machine` interface.
- The TUI three-pane layout (transcript | menu | graph) with modal overlays for slot-filling, using Bubble Tea's `tea.Model` composition.
- Event-sourced SQLite store. ~300 lines.
- Mermaid/DOT emitters walking the compiled app.

### Code sketch (A): declaring and wiring one state's intent handling

```go
// app.yaml compiled down to this pure data; no methods on App.
type StateDef struct {
    Path StatePath
    View string // template; evaluated with expr later
    Handlers map[string][]Transition // intent name -> guarded transitions
}
type Transition struct {
    When    *expr.Program // nil means default
    Target  StatePath
    Effects []Effect
}

// Validate is the ONLY place that binds an LLM tool call to a decision.
func (m *machine) Validate(cur StatePath, w World, call IntentCall) Result {
    s, _ := m.app.LookupState(cur)
    handlers, ok := s.Handlers[call.Intent]
    if !ok {
        return Result{Err: Err{Code: "INTENT_NOT_ALLOWED_IN_STATE",
            Allowed: m.AllowedIntents(cur, w)}}
    }
    intent, _ := m.app.LookupIntent(cur, call.Intent)
    if miss := intent.MissingSlots(call.Slots); len(miss) > 0 {
        return Result{Err: Err{Code: "MISSING_SLOTS", Missing: miss}}
    }
    env := map[string]any{"slots": call.Slots, "world": w.Map()}
    for _, tr := range handlers {
        if tr.When == nil { return Result{OK: tr} }
        out, err := expr.Run(tr.When, env)
        if err == nil && out == true { return Result{OK: tr} }
    }
    return Result{Err: Err{Code: "GUARD_FAILED"}}
}
```

### Risks / friction points

- **Bubble Tea v2 is not yet GA.** v1.3.10 is the last stable tag; v2.0.0-rc.2 shipped in March 2026 with a renamed module path and renderer rewrite ([v2 changelog](https://github.com/charmbracelet/bubbletea/releases)). We should target v2 — the Cursed Renderer is a meaningful upgrade — but expect minor churn before GA.
- **Module size**. Bubble Tea + Bubbles + Lipgloss + Glamour + Huh imports a dozen Charm submodules; keep an eye on version alignment.
- **YAML ergonomics in Go are mediocre.** `goccy/go-yaml` is better than `go-yaml/yaml` (the latter is [essentially unmaintained](https://github.com/goccy/go-yaml)) but authors still hit YAML's classic surprises (`y`/`n`/`off` as bools pre-1.2, trailing-space meaning).
- **Testing the TUI** requires `teatest` patterns (golden-file snapshots of terminal frames). Learning curve but not a blocker.
- **Writing the expression language sandbox** — Expr gives us the tools (`Patch` visitor, restricted env) but we own the policy. A mistake here is a security bug.

---

## B. LangGraph (Python)

LangGraph 1.1.6 (April 2026) is the flagship Python graph orchestrator for LLM workflows ([LangGraph 1.0 GA, late 2025](https://github.com/langchain-ai/langgraph)). If Hally's graph semantics overlap, we inherit a lot: checkpointers, mermaid rendering, LangSmith tracing, HITL, streaming, time travel. The question is whether the overlap is real or deceptive.

### Stack pieces

| Concern | Library | Version | Purpose |
|---|---|---|---|
| Graph orchestration | [langgraph](https://github.com/langchain-ai/langgraph) | 1.1.6 | `StateGraph`, `Command`, interrupts, subgraphs |
| Checkpointer | [langgraph-checkpoint-sqlite](https://pypi.org/project/langgraph-checkpoint/) | latest | Thread-scoped SQLite persistence |
| Models schema | [pydantic](https://github.com/pydantic/pydantic) | 2.13.1 (Apr 2026) | State/slot validation, `Annotated` reducers |
| MCP server | [modelcontextprotocol/python-sdk](https://github.com/modelcontextprotocol/python-sdk) v1.x or [fastmcp 3.0](https://pypi.org/project/fastmcp/) (Feb 2026) | - | Serve the transition tool |
| LLM client | [anthropic](https://github.com/anthropics/anthropic-sdk-python) | 1.x | Direct access for prompt caching |
| TUI | [textual](https://github.com/Textualize/textual) | 7.0.3 (Jan 2026) | CSS-like reactive TUI |
| Expression eval | [cel-python](https://github.com/cloud-custodian/cel-python) or [simpleeval](https://github.com/danthedeckie/simpleeval) | latest | Guards |
| YAML | [ruamel.yaml](https://yaml.readthedocs.io/) | 0.18.x | Round-trip comments |
| Graph viz | `StateGraph.get_graph().draw_mermaid()` | built-in | Free mermaid export |
| Tracing | [LangSmith](https://smith.langchain.com/) | hosted | Per-turn traces |
| Tests | [pytest](https://docs.pytest.org/), [vcrpy](https://vcrpy.readthedocs.io/) | latest | LLM replay |

### What you get free

- **Checkpointers**: `SqliteSaver`/`AsyncSqliteSaver` snapshot state at every superstep keyed by `thread_id`. Close analog to our §8 event store.
- **Mermaid export out of the box**: `graph.get_graph().draw_mermaid_png()`.
- **HITL**: LangGraph's `interrupt()` matches the "surface a context-sensitive menu when the LLM retries exhaust" pattern from design §5.3.
- **Streaming + time travel**: free for debugging transcripts.
- **LangSmith tracing** if you adopt it.

### What you still build

- A YAML DSL → `StateGraph` *compiler*. LangGraph is a Python builder API, not a declarative schema loader. Every Hally state becomes a node, every transition becomes a conditional edge, and the compiler emits them.
- The MCP server, because LangGraph is not itself an MCP server.
- Intent-extraction node template (one function called from every state node).
- Slot-filling semantics — LangGraph has no concept of forms or re-prompting for missing slots.
- The progressive-disclosure menu, because LangGraph's graph introspection doesn't expose "which intents are eligible right now" without walking the compiled object yourself.
- Sandboxed guard evaluator (LangGraph's conditional edges are arbitrary Python callables — we do *not* want author-supplied Python).
- CLI + TUI.
- Off-path mode.
- The demo app.

### Mismatch analysis: homogeneous vs. heterogeneous graphs

LangGraph is optimized for *heterogeneous* DAGs: a classifier node, a retrieval node, a draft node, a review node, each doing a qualitatively different thing, wired by conditional edges. Its killer feature is that you write node bodies in arbitrary Python and compose them with `Send`, `Command`, interrupts, and parallelism.

Hally's graph is *homogeneous*: every state does the same three-step loop — (1) accept an intent + slot payload from the MCP tool call, (2) validate against the state's declared intents and guards, (3) apply effects and transition. The "node body" is a fixed function parameterized by the state's intent catalog. Nothing in LangGraph's API helps you express this; you end up wrapping every node with the same `lambda state: _run_intent(state, STATE_CATALOG["foyer"])`, at which point LangGraph is a dispatch table with extra checkpointer semantics.

Concretely:

- `StateGraph` wants a shared `TypedDict` / Pydantic state with reducers. Hally's `world` is per-app, typed at YAML load time, not at graph-construction time. You compile to Pydantic dynamically (possible, ugly).
- Conditional edges return the next node's string name. That's fine, but it means *every* transition — including guard-failure self-loops — becomes a conditional edge. The graph blows up visually when you export it.
- LangGraph's checkpointer stores the state dict per super-step. Hally's event log is *append-only per event kind* (§8), not a series of state snapshots. You'd adapt by writing custom events *inside* nodes, which means you've reimplemented the store anyway and the checkpointer becomes redundant.
- `Command` objects and interrupts are subtly harmful here: they introduce non-obvious control-flow paths. Our determinism table (design §6) wants the MCP tool call to be the one and only boundary between nondeterminism and deterministic logic. LangGraph encourages you to blur that.

**Conclusion: LangGraph solves a problem we don't have (durable heterogeneous agent workflows) and doesn't solve the problems we do have (YAML DSL compilation, slot filling, progressive disclosure menus).** Its checkpointer is the only genuine win, and we can replicate it in ~200 lines of SQLite.

### Code sketch (B): the same intent-extraction + transition in LangGraph

```python
from langgraph.graph import StateGraph, END
from pydantic import BaseModel
from typing import Literal, Annotated, Any

class HallyState(BaseModel):
    current: str
    world: dict[str, Any]
    pending_intent: dict | None = None

# One node per YAML state. Every node body is structurally identical.
def make_state_node(state_name: str, catalog: dict):
    def node(s: HallyState) -> HallyState:
        call = s.pending_intent
        handlers = catalog[state_name].get(call["intent"], [])
        for tr in handlers:
            if tr.guard is None or tr.guard.eval(s.world, call["slots"]):
                s.world = apply_effects(s.world, tr.effects)
                s.current = tr.target
                return s
        raise InvalidIntent(state_name, call["intent"])
    return node

def route(s: HallyState) -> str:
    return s.current  # conditional edge returns next node name

g = StateGraph(HallyState)
for name, handlers in compile_yaml("cloak.yaml").items():
    g.add_node(name, make_state_node(name, handlers))
    g.add_conditional_edges(name, route)   # every state routes via current
g.set_entry_point("foyer")
app = g.compile(checkpointer=SqliteSaver.from_conn_string("./sess.db"))
```

Compare this to the Go sketch: you can see LangGraph is adding *ceremony* (TypedDict, conditional edges, entry point, compile) without adding *semantics* we actually want.

### Risks / friction points

- **Framework lock-in**. LangGraph has breaking changes roughly quarterly; the 1.0 GA ([late 2025](https://github.com/langchain-ai/langgraph/releases)) helps, but pre-1.0 apps needed meaningful rewrites at each bump.
- **Python → TUI async tension**. Textual is `asyncio`-first; LangGraph's `.invoke()` is sync and `.ainvoke()` is async. You pick one, or you bridge with `run_in_executor`.
- **`interrupt()` and `Command`** are tempting but muddy the determinism contract. Disciplined teams avoid them; a solo dev will reach for them at 11 PM and regret it.
- **Checkpointer schema changes** across LangGraph versions are called out in the docs as a migration hazard ([persistence docs](https://docs.langchain.com/oss/python/langgraph/persistence)) — same problem as our §6, but their solution is your responsibility.

---

## C. Assembled Python (no LangGraph)

This is what you build if you want Python's ergonomics without grafting onto a framework whose sweet spot isn't yours.

### Stack pieces

| Concern | Library | Version | Purpose |
|---|---|---|---|
| Schemas | [pydantic](https://github.com/pydantic/pydantic) | 2.13.1 | Typed state, slots, world |
| MCP server | [fastmcp](https://pypi.org/project/fastmcp/) | 3.0 (Feb 2026) or [modelcontextprotocol/python-sdk](https://github.com/modelcontextprotocol/python-sdk) v1.x | Tool server |
| LLM client | [anthropic](https://github.com/anthropics/anthropic-sdk-python) | 1.x | Prompt cache, tool use |
| Persistence | stdlib `sqlite3` + [SQLModel](https://sqlmodel.tiangolo.com/) (optional) | - | Event-sourced journey log |
| Guards | [cel-python](https://github.com/cloud-custodian/cel-python) (preferred if interop matters) or [simpleeval](https://github.com/danthedeckie/simpleeval) (preferred if author-friendliness matters) | latest | Sandboxed expressions |
| Graph | [networkx](https://networkx.org/) | 3.6.1 | In-memory graph, export via [pydot](https://pypi.org/project/pydot/) |
| YAML | [ruamel.yaml](https://yaml.readthedocs.io/) | 0.18.x | Comment-preserving round-trip for authoring tools |
| TUI | [textual](https://github.com/Textualize/textual) | 7.0.3 (Jan 2026) | TUI |
| Tests | [pytest](https://docs.pytest.org/), [respx](https://lundberg.github.io/respx/) (HTTP mock), [vcrpy](https://vcrpy.readthedocs.io/) | latest | Record/replay LLM |

**Key library calls:**

- **FastMCP 3.0** vs **official python-sdk v1**: the [official python-sdk](https://github.com/modelcontextprotocol/python-sdk) originally absorbed FastMCP 1.0 in 2024, but jlowin's standalone FastMCP forked and pushed ahead. FastMCP 3.0 (Feb 2026) rebuilt its core around Providers and Transforms, and per its [announcement](https://pypi.org/project/fastmcp/) powers ~70% of MCP servers across all languages. **Use FastMCP 3.0 for Python**. It's ergonomically ahead of the official SDK and the maintainer has Anthropic's support.
- **simpleeval vs cel-python**: `simpleeval` is smaller, friendlier, has been actively released through 2025-2026, and is the faster path for "just evaluate `slots.direction == 'south'`." `cel-python` is more portable and matches what [cel-go](https://github.com/google/cel-go) does on the Go side, which matters if we want apps authored for Hally-Go to also run on Hally-Python. **Pick `simpleeval` for the PoC; plan to switch to `cel-python` if cross-runtime portability becomes a requirement.**
- **ruamel.yaml vs PyYAML**: ruamel preserves comments and round-trips cleanly ([ruamel docs](https://yaml.readthedocs.io/)), which matters when authoring tools modify YAML. PyYAML does not.
- **sqlite3 stdlib vs SQLModel**: stdlib is plenty. SQLModel is ORM sugar we don't need; event sourcing fits `cursor.execute("INSERT INTO events ...")` better than ORM models anyway.

### What you get free

- **Pydantic v2 validation** is the best structured-error story in any ecosystem. Throw it at a slot payload and `ValidationError.errors()` gives us exactly the payload shape design §5.2 demands.
- **`rich` / `textual.markdown`** render the view templates. Free.
- **Anthropic Python SDK** has the most complete surface: prompt caching, tool use, streaming, beta headers — fastest path to depending on a new feature Anthropic ships. Prompt caching shipped first on Python and TypeScript; the Go SDK followed ([Anthropic docs](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)).
- **pipx / uv tool install** puts `hally` on a user's path with one command. Not a static binary, but not terrible.

### What you still build

- The YAML loader + schema validation + linter (same as A and B).
- The orchestrator loop connecting TUI → MCP → state machine → store.
- The pure state machine (same as A; ~500 lines of Python).
- The sandboxed expression evaluator on top of simpleeval (whitelist of operators and names).
- The event store (fewer lines than Go's: stdlib `sqlite3` is pleasant).
- The graph exporter (networkx → pydot → DOT → optional mermaid).
- The Textual TUI with three panes + modal overlays.

### Code sketch (C): the same intent validation in assembled Python

```python
from pydantic import BaseModel, ValidationError
from simpleeval import EvalWithCompoundTypes
from typing import Any

class IntentCall(BaseModel):
    intent: str
    slots: dict[str, Any] = {}

class Machine:
    def __init__(self, app): self.app = app

    def validate(self, cur: str, world: dict, call: IntentCall):
        state = self.app.states[cur]
        if call.intent not in state.handlers:
            return {"ok": False, "error": {"code": "INTENT_NOT_ALLOWED_IN_STATE",
                    "allowed": list(self.allowed_intents(cur, world))}}
        intent = self.app.intent(cur, call.intent)
        missing = [s.name for s in intent.slots
                   if s.required and s.name not in call.slots]
        if missing:
            return {"ok": False, "error": {"code": "MISSING_SLOTS",
                    "missing_slots": missing}}
        env = {"slots": call.slots, "world": world}
        evaluator = EvalWithCompoundTypes(names=env)
        for tr in state.handlers[call.intent]:
            if tr.when is None or evaluator.eval(tr.when):
                return {"ok": True, "target": tr.target, "effects": tr.effects}
        return {"ok": False, "error": {"code": "GUARD_FAILED"}}
```

This is the cleanest sketch of the three. Pydantic does the slot-shape validation, simpleeval does the guard evaluation, and the machine stays pure. Python's dict-first ergonomics map 1:1 onto YAML's dict-first ergonomics.

### Risks / friction points

- **Distribution** is the Achilles heel. `pipx install hally` requires a working Python 3.11+, and Windows users hit PTY quirks with Textual.
- **Startup cost** — importing anthropic, pydantic, textual, networkx, and simpleeval is ~400 ms on a cold VM. Go is sub-10ms. For a CLI tool, that difference is felt on every invocation.
- **Simpleeval security caveat**: the library's own README says ["many very clever people think the whole idea of trying to sandbox CPython is impossible"](https://github.com/danthedeckie/simpleeval). For Hally's trust model (YAML authors may not be operators, §11), we need a whitelist approach that treats the expression string as data. simpleeval does this; we still need a policy review.

---

## CLI/TUI Deep Dive

This is the dimension the user flagged as critical. Hally's TUI needs:

1. A persistent **transcript pane** (scrollable, markdown-rendered, with distinct styling for user vs. system vs. LLM).
2. A **menu pane** that updates every turn with the currently-allowed intents (the progressive-disclosure surface from design §7).
3. A **graph-viz pane** showing the app's state graph with the current state highlighted (the `?` overlay from design §9).
4. **Modal overlays** for slot-filling when the LLM calls `clarify` or the user picks an intent from the menu with missing slots.
5. **Slash-command handling** for `/freeform`, `/onpath`, `/menu`, `/trace`, `/quit`.
6. **Truecolor + Unicode + wide-character** rendering across Linux, macOS, and Windows Terminal / PowerShell.

### Bubble Tea (Go)

Bubble Tea uses the Elm Architecture: a `Model` (your state), `Update(msg) (Model, Cmd)`, and `View() string`. It is deeply event-driven — which maps directly onto Hally's event-sourced core. Every `tea.Msg` is structurally a `hally.Event` in our architecture; the same `Update` function that drives the TUI also writes to the event store.

- [**bubbles**](https://github.com/charmbracelet/bubbles) ships `viewport` (scrollable transcript), `list` (menu), `textinput` / `textarea` (prompt), `spinner`, `table`, `help`. All of these compose into a `tea.Model`. Coverage for Hally's panes: **complete**.
- [**lipgloss**](https://github.com/charmbracelet/lipgloss) v2 is a layout + styling DSL: `lipgloss.JoinHorizontal(lipgloss.Top, transcript, menu, graph)` literally writes the three-pane layout. Truecolor and adaptive (light/dark terminal) styling are first-class.
- [**glamour**](https://github.com/charmbracelet/glamour) renders the markdown view templates inside the transcript pane. Syntax highlighting via `chroma` comes free.
- [**huh**](https://pkg.go.dev/github.com/charmbracelet/huh/v2) is a form library specifically for slot-filling modals. It composes with Bubble Tea (`form.Run()` or embedded as a submodel). This matches design §7's slot-by-slot progressive disclosure exactly.
- [**vhs**](https://github.com/charmbracelet/vhs) is declarative terminal recording. A `.tape` file runs a scripted demo and emits a GIF; great for docs and CI-asserted screenshots.
- **Bubble Tea v2** (RC2 in March 2026) adds the **Cursed Renderer**, a ncurses-algorithm-inspired diff renderer, plus kitty-keyboard-protocol input handling (super+space, shift+enter, release events), and synchronized output (Mode 2026, discussed in [this GitHub discussion](https://github.com/charmbracelet/bubbletea/discussions/1320)). This matters because Claude Code-style shells depend on detecting modifier-heavy keybinds for navigation.

Most importantly: **Bubble Tea is what OpenCode is built with** ([per the v1.2.15 release notes, Feb 2026](https://opencode.ai/docs/tui/)) — the same class of product Hally targets. OpenCode has 100k+ stars and works across platforms; that is a powerful validation.

Notable Go TUI alternatives:

- [**tview / rivo**](https://github.com/rivo/tview): simpler, widget-based (less code for basic forms), but no layout DSL and less polished defaults. Good for internal tools, wrong aesthetic for a product TUI.
- [**gdamore/tcell**](https://github.com/gdamore/tcell): lower level; what tview and earlier Bubble Tea versions built on. Not a direct choice.

### Textual (Python)

Textual 7.0 (Jan 2026) is a serious competitor. It uses:

- **CSS-like styling** in `.tcss` files, live-reloadable via the devtools console.
- **Reactive attributes** via descriptors (`watch_current_state`, `compute_menu`). This matches the design's "menu is derived per turn" semantics better than Bubble Tea's explicit update loop.
- **Screens + modal screens** — first-class for the slot-filling overlay.
- **DataTable, Tree, Markdown, Syntax** widgets — rich enough for the transcript pane and graph-viz pane (though graph rendering needs custom drawing).
- Textual apps can **also run in the browser** via `textual-serve`. That is a free demo page for Hally, which is genuinely cool.

Textual's DX is superb — hot reload, inline dev console, type hints throughout, `pyproject.toml` config — and the [widget catalog](https://textual.textualize.io/guide/widgets/) covers everything Hally needs except ad-hoc graph drawing, which needs a custom `Widget` with a `render()` that emits styled `rich.segment.Segment`s.

Mermaid/graph rendering: neither stack renders mermaid *in-terminal* natively. Both ship a workaround (Bubble Tea: embed a DOT/mermaid text pane; Textual: same, or shell out to a headless render). Textual has an edge here via `rich`'s native Tree widget, which gives us a reasonable state-graph rendering without external tooling.

### Other Python options

- [**prompt_toolkit**](https://python-prompt-toolkit.readthedocs.io/): input-focused, used by IPython/ptpython. Good for shells, not for multi-pane TUIs. Mention for contrast.
- [**urwid**](http://urwid.org/): the grandparent of Python TUIs, still stable, but the API is pre-Textual and the ecosystem has largely migrated.

### What Claude Code / Aider / OpenCode actually use

- **Claude Code**: a custom React + Ink fork, with a full TypeScript Yoga layout port, per the [leaked source analysis](https://dev.to/minnzen/i-studied-claude-codes-leaked-source-and-built-a-terminal-ui-toolkit-from-it-4poh) and [this deep dive](https://deepwiki.com/farion1231/claude-code/10-ui-layer-(inkreact-terminal)). Not a framework we can adopt, but an architectural reference.
- **OpenCode**: [Bubble Tea](https://opencode.ai/docs/tui/), ~100k GitHub stars — the closest adjacent product we can compare to, built on the same stack we're evaluating.
- **Aider**: `rich` (not Textual) for prompt formatting + markdown rendering, driven as a plain CLI app rather than a full TUI. A CLI-first aesthetic; Hally is TUI-first.
- **OpenAI Codex CLI**: Rust, full-screen TUI, custom rendering. Not relevant for either candidate stack.

### Concrete answers to the dedicated sub-questions

1. **Multi-pane layout ergonomics**: **Bubble Tea + Lipgloss** by a small margin. `lipgloss.JoinHorizontal` + `JoinVertical` composes cleanly. Textual's grid CSS is strong, but you end up debugging layout in `.tcss` when you could be debugging it in the language.
2. **Modal overlays for slot-filling**: **Textual** by a small margin — modal screens are idiomatic. Bubble Tea's `huh` is excellent but requires submodel composition patterns that are non-obvious at first.
3. **Mermaid/graph viz in-terminal**: **Tie, both lean on external tooling**. Textual has a native Tree widget that helps for smaller graphs; Bubble Tea has nothing built-in but the `lipgloss.Place` API makes custom drawing feasible.
4. **Recording / demo tooling**: **Bubble Tea wins via VHS**. [VHS](https://github.com/charmbracelet/vhs) is declarative, CI-friendly, and produces publishable GIFs with zero effort. Textual works with asciinema but asciinema recordings are interactive-playback, not embeddable-in-READMEs.
5. **Claude Code-style UX**: neither matches Ink/React, but the closest thing to a "shell with chat" aesthetic in open source today is **OpenCode (Bubble Tea)**. That alone argues for Bubble Tea.

### TUI verdict

**Bubble Tea wins on distribution, recording, and existing-product validation (OpenCode).** Textual wins on developer ergonomics (hot reload, CSS, browser-export). For Hally specifically — a product with a demo story (GIFs in README), a determinism story (event-driven update loop), and an OpenCode-shaped aesthetic — Bubble Tea is the better fit.

---

## Decision Matrix

| Priority | Winner | Why |
|---|---|---|
| Speed to PoC | **C** (Python, no framework) | Dynamic typing, REPL, Pydantic takes care of slot validation in ~20 lines. A/B take 2-3× longer. |
| Long-term maintainability | **A** (Go) | Static types, compiler catches refactor regressions, single-language stack, fewer moving parts. |
| Single-binary distribution | **A** (Go) | The only stack that produces one. Decisive. |
| Team-size-of-one now, maybe-two-in-a-year | **A** (Go) | Onboarding reads like "know Go, read the interfaces." Python's answer is "know Python, know Pydantic, know Textual, know FastMCP, know simpleeval, know…" |
| Authoring / demo story | **A** (Go, via VHS) | The GIFs write themselves. |
| Best-in-class LLM feature coverage | **C** (Python) | Anthropic ships Python first; new features land there first. |
| Lowest turn latency | **A** (Go) | Everything that isn't the LLM call is faster. |
| Most correct sandboxed DSL | **A** (Go) | `expr-lang` + AST whitelist is the cleanest story. `simpleeval` is close but Python's introspection makes sandboxing harder. |
| Checkpointer / replay semantics that someone else owns | **B** (LangGraph) | LangGraph gives you the most mature third-party checkpointer. But we need the *wrong* shape. |
| Least risk of framework lock-in | **A or C** | Both avoid framework dependencies for core logic. |

**Aggregate: A wins 6, B wins 1, C wins 2, ties on 1.**

---

## Recommendation

**Build Hally in Go with Bubble Tea v2, expr-lang, the official MCP Go SDK, and modernc.org/sqlite.**

### Criteria

- Single-binary distribution is non-negotiable. Hally is pitched at local-CLI operators; requiring a Python environment is a 10× adoption tax.
- The state machine is the system. Go's type system enforces the `Machine`/`Store`/`Harness` interfaces from design §12 at compile time; Python's would enforce them at runtime (at best) or never (at worst).
- Bubble Tea v2 is the most TUI-mature stack in any language today for a Claude-Code-adjacent product. OpenCode's success with the same stack validates this.
- The MCP Go SDK v1 hit stable semver in late 2025 and continues active development in collaboration with Google ([release notes](https://github.com/modelcontextprotocol/go-sdk/releases)), which eliminates a previous concern about Go-side MCP tooling being behind Python.

### Tiebreakers (in order)

1. **Distribution.** The sum of all other dimensions still can't beat "one binary, no runtime."
2. **Compile-time interface enforcement.** The design doc commits us to a kernel-like `Machine` type; Go lets the compiler police that.
3. **VHS.** The ability to script GIF demos trivially is a force multiplier for a product that lives or dies by its README.

### What would flip the decision

- **If** we decided Hally needed to be embeddable in Jupyter notebooks or a Python-first analytical workflow — **switch to C**. The assembled Python stack loses on distribution but wins on the embed-in-another-tool axis.
- **If** we decided the demo apps would be authored *in Python* (via a Python DSL instead of YAML) — **switch to C**. Bridging Python-authored apps into a Go runtime is painful.
- **If** the MCP Go SDK regressed or froze — **switch to C with FastMCP 3.0**, which remains the most active MCP server framework in any language.
- **If** Anthropic shipped a feature in Python that we couldn't reproduce in Go within a release cycle — **re-evaluate**. Today this is a non-issue; the Go SDK has caught up for everything except the newest betas.

### What we explicitly reject

- **LangGraph (Approach B).** It gives us checkpointers we don't want in the shape we want, demands heterogeneous-graph conventions we don't have, and adds a framework dependency for a problem we can solve with ~500 lines of Python or Go. The only scenario where LangGraph wins is "we want LangSmith tracing out of the box," and we can add OpenTelemetry on our own schedule.

---

## Migration Paths

If we pick A and regret it:

- **A → C** is roughly a rewrite but of a tractable shape: the YAML DSL, state machine, MCP tool surface, and event store translate line-for-line. The TUI is the largest rewrite because Bubble Tea and Textual don't share idioms. Plan 4-6 weeks solo.
- **A → B** is an A → C port followed by a C → B adaptation (wrap the state machine as `StateGraph` nodes). Not worth doing unless we discover we need LangGraph-specific features.

If we pick C and regret it (most likely regret: distribution):

- **C → A** benefits from the fact that Pydantic schemas, YAML syntax, and MCP tool shapes are language-agnostic. The state machine is ~500 lines either way. The TUI is a full rewrite. 3-5 weeks solo.
- **C → B** is a refactor inside Python; cheaper than either rewrite but low expected value.

If we pick B and regret it (most likely):

- **B → C** is the easiest: drop LangGraph, keep Pydantic/FastMCP/Textual, reimplement the graph walker. ~1 week.
- **B → A** is a full rewrite.

The cheapest safety is to keep the YAML DSL, MCP tool schemas, and event schemas **language-neutral and documented** from day one. If the runtime changes, the apps don't.

---

## Sources

- Bubble Tea releases and v2 Cursed Renderer: <https://github.com/charmbracelet/bubbletea/releases>
- Bubble Tea synchronized output (Mode 2026) discussion: <https://github.com/charmbracelet/bubbletea/discussions/1320>
- Bubbles widget library: <https://github.com/charmbracelet/bubbles>
- Lipgloss styling: <https://github.com/charmbracelet/lipgloss>
- Huh v2 forms: <https://pkg.go.dev/github.com/charmbracelet/huh/v2>
- Glamour markdown renderer: <https://github.com/charmbracelet/glamour>
- VHS terminal recorder: <https://github.com/charmbracelet/vhs>
- Textual 7.0 and releases: <https://github.com/Textualize/textual/releases>
- Textual documentation: <https://textual.textualize.io/>
- MCP Go SDK v1 stable: <https://github.com/modelcontextprotocol/go-sdk>
- MCP Go SDK v1.0.0 release: <https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.0.0>
- mark3labs/mcp-go: <https://github.com/mark3labs/mcp-go>
- MCP Python SDK: <https://github.com/modelcontextprotocol/python-sdk>
- FastMCP 3.0 (Feb 2026): <https://pypi.org/project/fastmcp/>
- LangGraph 1.x: <https://github.com/langchain-ai/langgraph>
- LangGraph persistence / checkpointer docs: <https://docs.langchain.com/oss/python/langgraph/persistence>
- LangGraph StateGraph reference: <https://reference.langchain.com/python/langgraph/graph/state/StateGraph>
- Anthropic Go SDK v1.19 (Claude Opus 4.5 / 4.7, advanced tools): <https://github.com/anthropics/anthropic-sdk-go/releases>
- Anthropic Python SDK: <https://github.com/anthropics/anthropic-sdk-python>
- Anthropic prompt caching docs: <https://platform.claude.com/docs/en/build-with-claude/prompt-caching>
- Pydantic 2.13 release: <https://pydantic.dev/articles/pydantic-v2-12-release>
- Pydantic releases: <https://github.com/pydantic/pydantic/releases>
- expr-lang: <https://expr-lang.org/>
- google/cel-go: <https://github.com/google/cel-go>
- simpleeval: <https://github.com/danthedeckie/simpleeval>
- cel-python (cloud-custodian): <https://github.com/cloud-custodian/cel-python>
- qmuntal/stateless: <https://github.com/qmuntal/stateless>
- looplab/fsm: <https://github.com/looplab/fsm>
- modernc.org/sqlite: <https://pkg.go.dev/modernc.org/sqlite>
- goccy/go-yaml: <https://github.com/goccy/go-yaml>
- santhosh-tekuri/jsonschema v6: <https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6>
- NetworkX: <https://networkx.org/>
- OpenCode (Bubble Tea-based, validation reference): <https://opencode.ai/docs/tui/>
- Claude Code UI internals analysis: <https://dev.to/minnzen/i-studied-claude-codes-leaked-source-and-built-a-terminal-ui-toolkit-from-it-4poh>
- Claude Code Ink/React terminal UI deep dive: <https://deepwiki.com/farion1231/claude-code/10-ui-layer-(inkreact-terminal)>
- SQLite in Go benchmarks (modernc vs mattn): <https://datastation.multiprocess.io/blog/2022-05-12-sqlite-in-go-with-and-without-cgo.html>
- cvilsmeier/go-sqlite-bench (2026 update): <https://github.com/cvilsmeier/go-sqlite-bench>

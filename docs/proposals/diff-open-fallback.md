# Runtime: `host.diff.open` — review a diff in the IDE, or the system diff viewer

**Status:** **Phase A shipped + adopted.** The `host.diff.open` host call —
surface resolver (IDE → difftool → none), IDE verdict capture, view-only
difftool fallback, gate-decision recording — has landed with unit coverage
(`internal/host/diff_open.go`, `diff_open_test.go`), is documented in
[`docs/architecture/hosts.md` → `host.diff.open`](../architecture/hosts.md#hostdiffopen--review-a-change-in-the-best-surface),
and is **adopted in the bugfix `reviewing` room** (`reviewing_external`) with
four flow fixtures covering every surface. **Only two items remain** (this file
is trimmed to them): the Phase B turn-suspend gate (responsive surface; today
the turn blocks during review, like a long `host.run`) and pinning the editor's
accept/reject **return** wire shape from a live socket (ide-integration #1) —
until then `parseDiffVerdict` + the stub define the contract.
**Kind:**   runtime
**Epic:**   [`review-externally.md`](review-externally.md)

## Why

A story can open a diff today only if VS Code is attached, via
`host.ide.open_diff` — and even then v1 is **non-blocking and throws away
the operator's accept/reject** (`internal/host/ide_handlers.go:298-340`;
called out as deferred follow-up #2 in `ide-integration.md`). When no IDE is
connected there is **no diff path at all**: `bugfix` and `dev-story` review
changes by printing a summary into the terminal or opening a file read-only
(`host.ide.open_file` in `stories/dev-story/rooms/design_refine.yaml:155`).

Operators reviewing a real code change want a real diff in a real diff
viewer, and want their accept/reject to *mean something* — advance vs.
re-draft. This slice makes "open this change for review, and tell me what
they decided" a first-class host call that uses the best surface available:
the connected IDE (with a captured verdict) or a system difftool (view-only).

## What changes

A new **`host.diff.open`** host call. Given the change, it resolves a
**surface** by capability and returns a typed result:

1. **IDE connected** (`world.ide.connected`): open the diff via the existing
   `openDiff` MCP tool and **capture the operator's accept/reject**, returned
   as `{verdict: "accept"|"reject", reviewed: true, surface: "ide"}` and
   recorded as a gate decision.
2. **No IDE, difftool available:** shell a blocking system difftool
   (resolved per Open question 2), returning `{verdict: null, reviewed:
   true, surface: "difftool:<name>"}` — view-only, no accept/reject signal.
3. **Neither:** `{verdict: null, reviewed: false, surface: "none"}` so the
   room falls back to inline rendering.

The one sentence: **`host.diff.open` rents the best available diff surface
to a story and records a verdict when — and only when — the surface can
produce one.**

The "we edited *X* — want to review the diff?" interaction the user wants is
then a plain room over this host call: an `on_enter` summary ("we edited
these N files"), a yes/no choice, and a transition that invokes
`host.diff.open` and branches on `verdict` (accept → advance; reject →
refine; `null` → ask accept/refine as a normal intent). See **The model**.

## Impact

- **Code seams:** `internal/host/ide_handlers.go:298` (reuse the
  `openDiff` plumbing + `host.WithIDELink` + `world.ide.connected` seeding);
  `internal/host/handlers.go:44-186` (reuse the `host.run` blocking
  `exec.CommandContext` machinery for the difftool fallback); the registry
  in `internal/host/handlers.go:360-364`.
- **Vocabulary:** one host call, no new world keys (verdict lands via
  `bind:`). Table below.
- **Stories affected:** none change behavior; opt-in. One story (`dev-story`
  `design_refine`, or `bugfix`) adopts the pattern as the worked example.
- **Backward compat:** `host.ide.open_diff` stays; `host.diff.open` becomes
  the front door and calls into it for the IDE path. Existing stories and
  cassettes unchanged.
- **Docs on ship:** `docs/architecture/hosts.md` (`host.diff.open`),
  `docs/stories/state-machine.md` (only if the turn-suspend variant lands).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.diff.open` | `{path, new_text}` **or** `{paths: [...], base: "HEAD"}` → `{verdict, reviewed, surface}` | opens a diff in IDE (verdict) or system difftool (view-only); blocks until reviewed |
| world key | `world.ide.connected` | `bool` | **existing** (`ide_handlers.go`); gates the IDE-vs-difftool branch |

Two input modes, both supported:

- **Mode A — review already-applied edits (headline):** the agent already
  wrote the change (e.g. `agent.task` with Edit, which returns
  `files_changed` + `final_diff`). Pass `{paths, base: "HEAD"}`; the surface
  shows working-tree-vs-base. Accept = keep; reject = revert (Open
  question 4). This is the "we edited this, review it" case.
- **Mode B — review proposed content before applying:** pass `{path,
  new_text}` not yet on disk; the IDE's `openDiff` shows it; accept writes,
  reject discards. This is the native `openDiff` shape.

## The model

```
room ──"review the diff?"──▶ yes ──▶ invoke host.diff.open ──┬─ surface=ide      ──▶ verdict ∈ {accept,reject}  ──▶ branch on verdict
                                                             ├─ surface=difftool ──▶ verdict=null, reviewed=true ──▶ ask accept/refine (normal intent)
                                                             └─ surface=none     ──▶ reviewed=false              ──▶ render diff inline / open_file
```

The interpretive decision (does the operator accept the change?) is made by
a **human** at the IDE surface and recorded; everything else — surface
resolution, the difftool subprocess, the branch — is **deterministic** and
replayable. The view-only fallback deliberately produces **no** verdict from
the host call; the operator's subsequent `accept`/`refine` intent is the
recorded decision exactly as it is in every story today.

### Design fork: synchronous-await vs. turn-suspend

`ide-integration.md` #2 says verdict capture "needs a turn-suspend gate the
engine lacks." That is true for the *responsive* version, but note two
existing precedents that make a **synchronous** first cut viable:

- `host.ide.open_diff` **already awaits** the `openDiff` ws response
  (`ide_handlers.go:328`) — it just discards the verdict. The verdict is
  already on the wire; v1 chose to ignore it.
- `host.run` **already blocks the whole turn** on a subprocess via
  `exec.CommandContext`. The difftool fallback (`code --wait -d`) is exactly
  a `host.run`-shaped blocking subprocess. Blocking a turn on an external
  program is therefore an *established* host-call behavior, not a new engine
  concept.

So:

- **Phase A — synchronous-await (no new engine seam):** `host.diff.open`
  awaits the `openDiff` response (IDE) or the difftool exit (fallback),
  returns the verdict. The turn blocks and the writer lock is held while the
  operator reviews — consistent with a long `host.run`, but it freezes the
  operator surface during a possibly-minutes-long review. Ships verdict
  capture immediately.
- **Phase B — turn-suspend/resume (the `ide-integration.md` #2 gate):** the
  turn parks after the invoke, releases the writer lock, the TUI shows a
  "reviewing in <surface>…" state and stays responsive, and an async resume
  re-enters the turn with the verdict. The genuine new engine seam; model it
  on the existing clarification pause (`host.jobs.answer_clarification`
  resuming a paused job). Better UX, more work.

*This proposal targets Phase B (the epic chose verdict capture with a
responsive surface) but recommends landing Phase A first* so the host call,
the result shape, the difftool resolver, and the room pattern are all proven
before the suspend seam is cut. See Open question 1.

## Decision recording

When the IDE returns a verdict, record it as a gate decision (the moat): a
`gate_decided`-shaped event with `surface: "ide"`, `verdict`, and the diff
identity (paths / title / base) so the decision is reconstructable. For the
view-only difftool and the `none` path, **the host call emits no verdict
event** — the operator's next intent is the recorded decision via the normal
routing/transition events. Recording a fabricated "accept" for a view-only
surface would be a lie in the trace (`tools/runstatus/CLAUDE.md`: the trace
must always be correct); we don't.

If Phase B lands, the suspend/resume boundary itself wants a trace marker
(turn suspended → resumed-with-verdict) — that is a small `tracing` concern;
flag it then.

## Engine seams & invariants

- **IDE path:** wrap `IDEOpenDiffHandler` (`ide_handlers.go:298`); resolve
  the link from ctx (`host.WithIDELink`); the `openDiff` arg/return shape is
  still `TODO(schema)` (`ide_handlers.go:314`) — this slice **depends on**
  `ide-integration.md` #1 (one real-socket capture of `openDiff`'s accept/
  reject return) before the verdict is trustworthy. Until then the stub
  server defines the contract and tests run against it (no live editor).
- **Difftool path:** reuse the `host.run` subprocess machinery
  (`handlers.go:44`); blocking `exec.CommandContext` with the resolved argv.
- **Suspend gate (Phase B only):** new seam in the turn loop where effects
  dispatch — park after the `invoke`, persist a "suspended" journal marker,
  release the writer lock, resume on the ws callback. Cite and mirror the
  jobs-clarification pause.
- **Load-time invariant:** `host.diff.open` must be in the story's `hosts:`
  allowlist (existing mechanism). A room that `bind:`s `verdict` should
  handle `null` (the view-only case); *lean: document this contract rather
  than hard-fail at load in v1* — a lint warning is the most we'd add.

## Backward compatibility / migration

`host.ide.open_diff` is unchanged and remains callable; `host.diff.open` is
new and opt-in. Phase A adds no engine concept (it behaves like a blocking
`host.run`/`open_diff`), so all existing stories, flows, and cassettes pass
untouched. The cassette/stub for the IDE path mirrors whatever
`ide-integration.md` #1's capture pins.

## Tasks

```
## 1. Engine — SHIPPED (Phase A)
- [x] 1.1 host.diff.open handler: surface resolver (ide → difftool → none)
- [x] 1.2 IDE path: wrap openDiff, return the captured verdict (Phase A, synchronous)
- [x] 1.3 Difftool resolver + blocking exec via the host.run machinery
- [x] 1.4 Result shape {verdict, reviewed, surface}; register in handlers.go
- [x] 1.5 Gate-decision recording for the IDE verdict (none for view-only)
- [ ] 1.6 (Phase B) turn-suspend/resume gate, modelled on jobs-clarification — REMAINING

## 2. Verification (no live editor, no real claude, no network)
- [x] 2.1 Stub-ws unit: IDE path returns accept and reject verdicts
- [x] 2.2 Unit: difftool path shells a fake difftool, returns reviewed+null
- [x] 2.3 Unit: no-IDE-no-difftool returns reviewed:false
- [x] 2.4 Flow fixtures: review-diff room branches on each surface
        (stories/bugfix/flows/review_diff_{ide_accept,ide_reject,difftool_viewonly,no_surface}.yaml)
- [ ] 2.5 Depends-on: capture real openDiff verdict wire shape (ide-integration #1) — REMAINING
        (parseDiffVerdict + the stub pin the contract until then)

## 3. Adopt + document
- [x] 3.1 Adopt the review-diff room in bugfix (reviewing → reviewing_external)
- [x] 3.2 Document host.diff.open in docs/architecture/hosts.md
- [x] 3.2b Document the post-bind-emit room pattern in docs/stories/state-machine.md
        (authoritative home for emit_intent routing; story-style.md is view-only)
```

## Verification

A reviewer confirms it without an LLM:

- `kitsoki turn --state <review> --intent accept --world @w.json` against a
  story wired to `host.diff.open`, with the stub ws server seeded to return
  `accept` then `reject`, asserting the bound `verdict`.
- A flow fixture stubbing `host.diff.open` by invoke-id for all three
  surfaces, asserting the room lands in `advance` / `refine` / inline-render
  respectively, and that a legacy story (no `host.diff.open`) still passes.
- Difftool path: point `$KITSOKI_DIFFTOOL` at a script that records its argv
  and exits 0; assert argv + `surface: "difftool:…"` + `verdict: null`.

No test needs a real editor or `claude`.

## Open questions

1. **Phase A or straight to Phase B?** *Lean: ship Phase A (synchronous,
   verdict captured, surface freezes during review — consistent with a long
   `host.run`) first, then cut the Phase B suspend seam for a responsive
   surface.* The epic chose the responsive end state; this is sequencing,
   not scope.
2. **Difftool resolution order.** *Lean: explicit `$KITSOKI_DIFFTOOL argv`,
   else `git difftool` (honoring `git config diff.tool`), else
   `code --wait -d <old> <new>` if `code` is on PATH, else `surface:
   "none"`.*
3. **Revert-on-reject for Mode A.** When the operator rejects already-applied
   edits, who reverts — a `git checkout`/`git restore` the room invokes via
   `host.run`, or does the room just route to `refine` and leave the working
   tree dirty? *Lean: leave reversion to the story (explicit `host.run`), so
   the host call stays a pure "show + report verdict."*
4. **Does this blur the moat?** It adds a human-in-the-IDE decision point;
   that is fine — it's recorded and the execution around it is deterministic.
   The only risk is Phase B's suspend seam touching the turn loop; that
   wants the same care as the jobs-clarification pause and is called out
   above.

## Non-goals

- An in-TUI diff viewer/pager — we push out to external tools.
- JetBrains diff parity (`ide-integration.md` #4) and IDE auto-connect
  (`ide-integration.md` #5).
- The `.md` link rendering and `/open` command — that was the tui-md-links
  slice, now shipped: [`docs/tui/README.md`](../tui/README.md).

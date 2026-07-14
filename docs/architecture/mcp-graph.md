# MCP graph — an MCP-only agent works any object graph

`kitsoki mcp-graph` is a stdio [MCP](https://modelcontextprotocol.io) server
that lets an agent with **no Bash, no file read/write** — only its declared
MCP tools — answer arbitrary questions about a kitsoki object graph, propose
reviewable changes, explain how the graph developed, and file a durable
friction report. The identical tool family also mounts on the
[studio server](mcp-studio.md) (`kitsoki mcp`) so a human's Claude Code
session gets `mcp__kitsoki__graph.*` beside the story/vcs tools. This
document is the server-wide invariants companion to `mcp-studio.md`: budgets,
error codes, the actor ceiling, and the catalog-binding model — see
[`docs/proposals/graph-mcp.md`](../proposals/graph-mcp.md) for the full design
rationale and work-plan history (P1–P6).

Implementation: [`internal/mcp/graphsrv/`](../../internal/mcp/graphsrv/)
(server, tools, budgets, error vocabulary, feedback sinks) and
[`cmd/kitsoki/mcp_graph.go`](../../cmd/kitsoki/mcp_graph.go) (the standalone
`mcp-graph` subcommand); the studio mount lives in
[`internal/mcp/studio/graph_tools.go`](../../internal/mcp/studio/graph_tools.go),
wired from `cmd/kitsoki/mcp.go`'s `mcpCmd()`.

## Topology: two doors, one tool family

`internal/mcp/graphsrv` exposes `RegisterGraphTools(srv, deps, mode)` and
`RegisterFeedbackTools(srv, deps)` as free functions — not `Server` methods —
so two independent servers can mount the exact same tools with zero drift:

1. **`kitsoki mcp-graph`** — a dedicated server (the mcp-validator/codeact aux
   -server pattern). An MCP-only agent pays for `tools/list` every turn, so a
   focused ~15-tool server is cheaper than attaching the full ~86-tool studio
   server, and its argv is a hard capability ceiling: a caller cannot
   self-escalate a mode or catalog binding it wasn't launched with.
2. **The studio server (`kitsoki mcp`)** — mounts the same family via
   `graph_tools.go`'s `(*Server).registerGraphTools`, unconditionally, on
   every studio server construction. A human's Claude Code session (or any
   sub-agent that auto-attaches `mcp__kitsoki__*`) gets the graph family
   without a second MCP server entry.

Both doors carry the **same steward gate** (see below) and the **same
catalog-arg schema** (alias-only — see "Catalog binding"). Deliberately, the
studio mount does *not* accept a raw `catalog_path` fork the way an
early draft of the plan sketched as an option for "an audience with file
tools anyway" — one arg shape per tool keeps the schema surface (and the
`tools/list` byte budget) identical on both doors, and neither door lets a
caller point the graph family at an unbound filesystem path.

## Catalog binding: startup aliases, never raw paths

`mcp-graph --catalog [alias=]<path>` (repeatable; first is default) and the
studio mount's `kitsoki mcp --catalog [alias=]<path>` bind a fixed set of
catalog aliases at server construction. Every tool's optional `catalog` arg
selects among those bound aliases; a raw filesystem path is always rejected
(`VALIDATION`, distinguished heuristically from a typo'd alias —
`UNKNOWN_CATALOG` — by `looksLikeFilesystemPath`). No `--catalog` at all
probes `pog/catalog.yaml` under the server's cwd; if that's absent too, the
server still starts with zero bound catalogs, and every graph/feedback tool
call returns `{ok:false, code:"NO_CATALOG", hint}` — errors are data, never a
transport failure or an absent tool.

The studio mount always registers the graph family, even with zero bound
catalogs (no `--catalog` passed to `kitsoki mcp`): the alternative — skipping
registration so an unconfigured studio session's `tools/list` doesn't carry a
permanently-`NO_CATALOG` family — was considered and rejected for
consistency with `mcp-graph`'s own "the server still starts" contract (plan
§3.2): a static tool list a session can inspect and reason about beats one
that silently varies with how the operator happened to launch it.

Per-call reload from disk (engine semantics unchanged); no server-side
catalog cache.

## Scoped sessions: a baked-in catalog subset

`--scope [alias=]<scope.yaml>` (repeatable, one per bound alias; studio
mount: `--graph-scope`) restricts a session to a deterministic subset of a
catalog. Like the catalog binding and the actor ceiling, the scope lives in
server argv and is resolved at construction — **no tool argument can widen,
narrow, or drop it**, so the restriction holds at the engine level rather
than as prompt guidance an LLM could ignore. Every `graph.*` call on a
scoped alias carries the spec into its `host.graph.*` op; the member set is
re-resolved against the freshly loaded catalog on each call (consistent with
the no-cache reload model above), so it deterministically tracks catalog
edits.

The scope file is a YAML selector
([`internal/graph/scope.go`](../../internal/graph/scope.go) —
`ParseScopeSpec` rejects unknown keys, and at least one of
`roots`/`types`/`include` is required):

```yaml
roots: [feature-payments]   # BFS start nodes (must exist)
direction: out              # out (default) | in | both
depth: 2                    # hops from roots; omit = unlimited, 0 = roots only
edges: [requirements, acceptance]  # optional traversal edge allowlist
types: [decision]           # additionally include every node IsA these types
include: [adr-0007]         # additionally include these exact ids
exclude: [noisy-node]       # remove after expansion AND block traversal through
```

Membership = BFS(roots) ∪ IsA(types) ∪ include, minus exclude. Changeset
nodes are **always in scope** (unless explicitly excluded) so a scoped
session can read back the lifecycle of its own proposals.

Semantics, split by read/write:

- **Reads** operate on a pruned view (`internal/graph.ApplyScope`, applied
  at the single `loadCatalogArg` choke point every `host.graph.*` read op
  shares): `find`/`open` counts, `lint`, `type` censuses, and `neighbors`
  walks see only member nodes, and edges targeting a pruned node are
  dropped from the view. `graph.open` reports a `scope` block
  (`{active, member_count, total_node_count, pruned_edges, spec}`) and a
  guide line saying the session is scoped. `graph.get` on an out-of-scope
  id lands in `missing` with `out_of_scope: true` (suggestions only ever
  name in-scope ids); `graph.neighbors` from an out-of-scope root errors
  `OUT_OF_SCOPE`.
- **Writes** are gated against the **full** catalog
  (`internal/graph.ScopeWriteViolations` via the guards in
  [`internal/host/graph_scope.go`](../../internal/host/graph_scope.go)) —
  a pruned view must never reach a path that writes the catalog back, since
  that would delete every out-of-scope node. `graph.propose` rejects
  operations that modify/remove/rename/retype an out-of-scope node
  (`OUT_OF_SCOPE`); `added` operations are always allowed (creating a node
  never damages out-of-scope content, and one connected to the scope becomes
  a member on the next resolve); `registry_type_*` operations are always
  rejected in a scoped session (the type registry is catalog-wide).
  `graph.apply`/`authorize`/`withdraw` apply the same gate to the target
  changeset's parsed operations.

Failure posture: a malformed scope file, an unknown root/include/type/edge,
or a scope bound to an unbound alias **fails server construction** — a scope
must never silently degrade to "unscoped". The studio mount fails closed the
other way too: an unparseable `--graph-scope` mounts the family with zero
catalogs (`NO_CATALOG` everywhere) rather than mounting the catalogs
unscoped. Unknown `exclude` ids alone are ignored, so a scope file survives
the deletion of a node it excludes.

Known limits (by design, documented rather than hidden): scope is a focus
and blast-radius guardrail, not a security boundary — out-of-scope *ids* can
still appear in `graph.history` rows and changeset operation listings
(lifecycle surfaces are not pruned per-operation), but out-of-scope node
*content* is unreachable and out-of-scope nodes are immutable through the
session. Lint on a scoped view runs against the pruned graph, so
membership-shaped lints (e.g. orphan-feature) can fire for nodes whose
anchoring edge was pruned.

## The actor ceiling

`--actor <name>` (mirrored by the studio mount's `--graph-actor`) is a
server-side identity ceiling stamped onto every write-tool call
(`authored_by`, `authorized_by`) and checked by `graph.withdraw`'s
own-changeset gate in propose mode
(`checkWithdrawOwnership`, [`tools_graph_write.go`](../../internal/mcp/graphsrv/tools_graph_write.go)).
It is honor-system, not authentication — an agent can never assert a
*different* identity than the one the server was launched with, because the
actor lives in server argv, not in any tool's arguments. `writeCtx`
([`tools_graph_write.go`](../../internal/mcp/graphsrv/tools_graph_write.go))
threads it via `host.WithActor` on every write call.

Steward mode is the second half of the ceiling. Modes:

| mode | read family | `graph.propose`/`withdraw`/`changeset` | `graph.apply` | `graph.authorize` |
|---|---|---|---|---|
| `read` | yes | not registered | not registered | not registered |
| `propose` (default) | yes | yes (withdraw: own changesets only) | dry-run only | registered, rejected `STEWARD_ONLY` |
| `steward` | yes | yes (withdraw: any changeset) | dry-run or live | yes |

Nothing an agent does can silently self-authorize a change it proposed:
`graph.authorize` and a real (`dry_run:false`) `graph.apply` both require
`--mode steward` at *runtime*, not just at tool registration — a defense in
depth against a caller that somehow reached the handler in propose mode.

The studio mount's `--graph-steward` flag is the same gate applied a second
time, deliberately: the plan's red-team amendment states the gate "must
exist on BOTH construction sites or it exists on neither" — a sub-agent that
auto-attaches the studio server via the `mcp__kitsoki__*` naming convention
must not get steward powers just because the *human's* long-lived session
happens to run with `--graph-steward` for their own convenience. Set
`--graph-steward` only on a studio server you trust every attaching caller
with.

Propose-mode write tools **never** pass caller-supplied `provenance` through
to `host.graph.propose` — `writeCtx` only sets `host.WithSteward(ctx, true)`
in steward mode, so a propose-mode caller's provenance is silently stripped
by the engine regardless of what's in the wire args (provenance-carrying
proposals auto-authorize per the engine's `write_policy` allowlist, and
accepting it from an agent would hollow out the human gate). The
catalog-sink feedback proposal (below) follows the identical rule even when
the server itself is running in steward mode — see "Feedback channel."

## Write routing: direct vs capsule

Kitsoki-convention repos protect the primary checkout (pinned to `main`,
read-mostly) and land local work on `staging/local` through managed
clone-backed capsule workspaces (`scripts/dev-workspace.sh`, AGENTS.md). A
graph server whose write tools mutate the bound catalog **in place** would
violate that convention — so write materialization is routable, per bound
catalog ([`writevia.go`](../../internal/mcp/graphsrv/writevia.go)):

- **`direct`** — the historical behavior: `host.graph.*` writes land in the
  bound catalog's working tree.
- **`capsule`** — the write lands in a managed workspace: on the first write
  the server runs the catalog repo's own `scripts/dev-workspace.sh create`
  (workspace under `<repo>/.capsules/workspaces/graph-mcp-<pid>`, based on
  the staging branch), the engine op runs against the workspace copy, and
  every successful write is `commit`ted (DCO sign-off is the script's own
  contract) and `merge`d into the staging branch **without teardown** — the
  workspace stays alive for the server's lifetime, and once it exists every
  read for that catalog routes to it too, so a proposed changeset is visible
  to the `graph.get`/`changeset`/`apply` calls that follow. The primary
  checkout is never touched.

Resolution precedence, per bound catalog:

1. `--write-via direct|capsule` (`kitsoki mcp`: `--graph-write-via`) — a
   server-level override for every bound catalog;
2. otherwise (`--write-via auto`, the default) the catalog repo's checked-in
   `.kitsoki/project-profile.yaml`:

   ```yaml
   graph:
     write_via: capsule   # or direct
     gate: "git diff --check"   # optional dev-workspace.sh merge gate
   ```

3. otherwise, when the bound catalog is visibly read-only and its repository
   carries `scripts/dev-workspace.sh`, **`capsule`** — protected primary
   checkouts work without requiring a duplicate local profile setting;
4. otherwise **`direct`** — a repo with no `.kitsoki` profile (or a catalog
   outside any git repo) just edits in the working directory.

`--write-via direct` remains an explicit override. The protected-catalog
fallback checks mode bits without attempting a write, so it never probes or
weakens the protected checkout.

The default capsule merge gate is `git diff --check`, not the repo's full CI
gate: graph writes are already validated all-or-nothing by the engine (lint
regression gate, hazard guards) before any file changes, so the integration
gate only needs repo hygiene. A project can widen it via `graph.gate`.

Failure honesty: a workspace that cannot be created (no
`scripts/dev-workspace.sh` in a repo whose profile says `capsule`), or a
completed write that cannot be committed/merged, comes back as
`CAPSULE_WORKFLOW` whose hint names the workspace path/branch holding the
work. An engine *rejection* is never masked by a lifecycle warning — the
post-reject integrate is best-effort. Receipts and feedback artifacts keep
anchoring to the **primary** repo root (never a disposable workspace), so
`.artifacts/graph-mcp/` stays in one predictable place.

The dev-workspace.sh process seam is injectable (`Config.WorkspaceRunner`) —
tests drive the whole capsule route with a deterministic fake and never
spawn a real clone ([`writevia_test.go`](../../internal/mcp/graphsrv/writevia_test.go)).

## No-LLM, ever

Every handler in this package is deterministic Go: JSON args in, a
`host.graph.*` op invoked through a `host.Registry`, a JSON result out. There
is no live model call anywhere in `graphsrv`, in either mounted server, or in
its test suite — tests drive the server via the SDK's in-process
`NewInMemoryTransports` against fixture catalogs, never a real client.
Every tool invokes `host.graph.*` through the registry rather than calling
`internal/graph` directly, so CLI, `kit_call`, and Starlark share the exact
same engine surface this package exposes over MCP (plan §1's "all capability
lands as engine ops" constraint). `kitsoki graph propose`
([`cmd/kitsoki/graph_propose.go`](../../cmd/kitsoki/graph_propose.go)) is
the CLI twin of `graph.propose` over that same op — added 2026-07-13 after
dogfood friction where an MCP-server outage forced agents to hand-author
changeset YAML because the CLI had `lint`/`apply`/`query`/`materialize` but
no `propose`. It reads `{title, operations[, visibility]}` (or a bare
operations list plus `--title`) from a file or stdin, stamps `authored_by`
from `--actor`, and shares all of `Propose`'s id minting, guard fills, and
scratch-copy validation; like the MCP tool in propose mode it is never
steward-trusted, so input provenance can't trigger auto-authorize.

## Budgets

Every read/write tool response is byte-budgeted and marks truncation
**in-band** — never a silent drop, never a sidecar file an MCP-only agent
couldn't read anyway. `TruncateString`/`TruncateSlice`
([`budget.go`](../../internal/mcp/graphsrv/budget.go)) implement the cut;
named constants pin the plan's §3.3 numbers:

| tool | budget |
|---|---|
| `graph.open` | `BudgetGraphOpen` — 2KB |
| `graph.get` (per field) | `BudgetGraphGetField` — 2KB |
| `graph.get` (single-field refetch) | `BudgetGraphGetSingle` — 32KB |
| `graph.get` (overall) | `BudgetGraphGetTotal` — 24KB |
| `graph.find` (per page) | `BudgetGraphFindPage` — 8KB |
| `graph.neighbors` | `BudgetGraphNeighbors` — 10KB |
| `graph.propose`/`changeset`/`withdraw`/`apply`/`authorize` | 8KB each |

The golden `TestGraphServer_ToolsListByteCeiling`
([`tools_graph_test.go`](../../internal/mcp/graphsrv/tools_graph_test.go))
asserts the whole `tools/list` payload — every tool's name, description, and
JSON Schema combined — stays inside its byte ceiling; a schema-hygiene walk
(`TestGraphServer_ToolSchemasHaveNoBooleanLeaves`,
[`server_test.go`](../../internal/mcp/graphsrv/server_test.go)) additionally
guards against a reflected Go `any`/`bool`-leaf schema shape, since an
under-specified schema makes some MCP clients drop the entire tool list.

## Error-code vocabulary

Every tool failure is a teaching-shaped `{ok:false, code, error, hint,
if_stuck}` payload (`ErrorPayload`,
[`errors.go`](../../internal/mcp/graphsrv/errors.go)) — never a bare
transport error, and `if_stuck` always names `feedback.report` so the
channel is advertised at the moment of friction. The full vocabulary, with
each code's exact trigger, is documented as Go doc comments directly on the
constants in `errors.go`:

- `NO_CATALOG` — no catalog bound at startup.
- `UNKNOWN_CATALOG` — a `catalog` arg didn't match a bound alias.
- `UNKNOWN_NODE` / `UNKNOWN_TYPE` / `UNKNOWN_EDGE` — a referenced id/type/edge
  field doesn't exist; hints carry nearest-id suggestions or the type's edge
  vocabulary.
- `VALIDATION` — argument shape/semantic failure (including "raw path passed
  as `catalog`").
- `READ_ONLY_MODE` — a write-shaped call arrived at a `--mode read` server,
  or (P6) a catalog-sink feedback route was attempted in read mode.
- `STEWARD_ONLY` — `graph.authorize`, or `graph.apply` with `dry_run:false`,
  called on a non-steward server.
- `CATALOG_LINT_BLOCKED` — a write would add a *new* lint issue (pre-existing
  catalog dirt never blocks a write, only regressions).
- `NEEDS_CANONICALIZATION` — the catalog file isn't in yaml.v3 canonical
  form; writing through it would silently reflow a hand-wrapped block
  scalar.
- `NOT_YOUR_CHANGESET` — a propose-mode `graph.withdraw` call named a
  changeset authored by a different actor.
- `OUT_OF_SCOPE` — the call named (or a write would touch) a node that
  exists in the catalog but sits outside the session's baked scope (see
  "Scoped sessions"); only a differently-scoped session can reach it.
- `CAPSULE_WORKFLOW` — a capsule-routed write's workspace lifecycle failed
  (workspace create, commit, or merge into the staging branch); the hint
  names where the work physically is so nothing is silently lost.

`routing_errors[].code` on `feedback.report` reuses this same vocabulary
(currently only `READ_ONLY_MODE`, for the catalog sink's read-mode degrade)
rather than inventing a parallel error-code space for sink failures.

## Feedback channel

`feedback.report`/`feedback.list` are always available, in every mode,
because filing a friction report must never be gated behind write
permissions. `feedback.report` is contractually non-blocking: it always
returns `ok:true`; every sink problem — including a completely unresolvable
anchor catalog — comes back as a `routing_errors` entry, never a tool
error. Local capture (JSONL ledger + per-report markdown under
`.artifacts/graph-mcp/`, anchored to the bound catalog's **git repo root**,
never process cwd — `repoRootFor`,
[`feedbacksink.go`](../../internal/mcp/graphsrv/feedbacksink.go)) always
happens first and is always attempted regardless of `--feedback-sink`.

`--feedback-sink local|catalog|github` (mirrored by the studio mount's
`--graph-feedback-sink`) is evaluated at **`feedback.report` call time**
("flag-time"), not at authorize/apply time — a deliberate reading, in the
same spirit as P4's "no CLI authorize subcommand" deviation note: the plan
never pins the sink's trigger point to a later lifecycle event, and
flag-time is both the simpler reading of the literal
`--feedback-sink local|catalog|github` enum and consistent with how the
local sink already triggers (always, on every call, regardless of mode).

- **`catalog`** ([`routeFeedbackToCatalogSink`](../../internal/mcp/graphsrv/tools_feedback.go)):
  proposes — **never authorizes** — a changeset adding one new node, shaped
  per the catalog's own `feedback_routing: {type, fields, edges}` block
  (`internal/graph.Catalog.FeedbackRouting`, parsed but otherwise unconsulted
  before P6). No block, or a read-mode server, degrades to a
  `routing_errors` entry — never a hard failure. The proposal is built with
  `host.WithActor` only, deliberately never `host.WithSteward` — even when
  the server itself is running in steward mode — because the plan is
  explicit that "the server *proposes* — never auto-authorizes" for this
  case; the resulting changeset always lands in the ordinary human review
  queue.
- **`github`** ([`routeFeedbackToGithubSink`](../../internal/mcp/graphsrv/tools_feedback.go)):
  files an issue via the injected `IssueFiler` seam
  ([`issuefiler.go`](../../internal/mcp/graphsrv/issuefiler.go)). No
  configured filer, or a filing error, degrades to `routing_errors` — a
  GitHub outage must never fail a `feedback.report` call. Production wiring
  (`cmd/kitsoki/issue_filer.go`'s `ghGraphIssueFiler`) adapts the same native
  GitHub filer the studio server's own `issue.create` uses.

## Implementation links

- [`internal/mcp/graphsrv/`](../../internal/mcp/graphsrv/) — the tool family
  (read, write, feedback), budgets, error vocabulary, catalog binding, mode
  gating, receipts journal.
- [`cmd/kitsoki/mcp_graph.go`](../../cmd/kitsoki/mcp_graph.go) — the
  standalone `kitsoki mcp-graph` subcommand.
- [`internal/mcp/studio/graph_tools.go`](../../internal/mcp/studio/graph_tools.go)
  and [`internal/mcp/studio/server.go`](../../internal/mcp/studio/server.go) —
  the studio-server mount (P6).
- [`internal/graph/scope.go`](../../internal/graph/scope.go) and
  [`internal/host/graph_scope.go`](../../internal/host/graph_scope.go) —
  scope spec parsing/resolution, the pruned read view, and the write guards.
- [`cmd/kitsoki/mcp.go`](../../cmd/kitsoki/mcp.go) — `kitsoki mcp`'s
  `--catalog`/`--graph-scope`/`--graph-steward`/`--graph-actor`/
  `--graph-feedback-sink`/`--graph-write-via` flags.
- [`docs/proposals/graph-mcp.md`](../proposals/graph-mcp.md) — the full plan,
  design rationale, and P1–P6 work-plan history.

# Runtime: capsule ref sync and promotion

**Status:** v1 in progress. Local typed plan/classify/apply with stale and
fast-forward checks, sync lifecycle facts, and receipt-to-candidate gate binding
are available through Capsule CLI/MCP. The native `development` compatibility
provider preserves the existing protected rebase/gate workflow. `publish` is now
guarded behind an explicit remote publisher provider instead of local ref
mutation. Conflict plans now carry structured continuation tokens, required
story inputs, `capsule-sync-conflict/v1` path/tree artifacts, managed
`capsule-sync-integration/v1` integration instances, and continuation apply
from resolved integration commits; continuation abort can preserve a patch
artifact; credential-free local bare-remote fetch and publish providers ship for
tests/local development. Story-level resolver UX and the production remote
publish provider remain.
**Kind:**   runtime
**Foundation:** [`../guide/development/capsule-ci.md`](../guide/development/capsule-ci.md)

## Why

The current local workflow has sound safety properties but no reusable API.
`scripts/dev-workspace.sh merge` rebases a workspace onto the current target and
updates a local integration ref; `scripts/refresh-staging-local.sh` checks remote
freshness and reconciles staging with local `main`; `scripts/merge-to-main.sh`
runs a gate in a managed capsule before fast-forwarding protected `main`; and
`scripts/sync-main-from-remote.sh` isolates divergent remote reconciliation.
The contract is documented in [`../dev-workspaces.md`](../dev-workspaces.md#merge),
but a foreign project cannot use it without copying Kitsoki-specific scripts,
and an MCP-only agent cannot safely ask for the same operations.

The missing primitive is a typed, race-safe reconciliation plan. “Sync” is not
one `git pull`: workspace integration, staging refresh, protected promotion,
remote fetch, branch publication, and PR merge have different authorities and
failure modes.

## What changes

Add a `RefReconciler` beneath the Capsule control plane. Every mutating action is
two-phase:

1. **plan** observes exact refs, dirt, policies, and remote state and emits an
   immutable plan with a digest;
2. **apply** accepts that digest, rechecks every expected ref/generation, and
   performs only the named compare-and-swap updates.

The runtime recognizes four operations rather than a vague sync verb:

- `integrate`: workspace branch -> local integration branch;
- `refresh`: integration/staging branch -> current protected base, without
  promoting the protected ref;
- `promote`: verified integration/staging result -> protected local branch;
- `publish`: local branch -> remote branch/PR/merge provider.

Conflict resolution remains story-driven. The reconciler materializes an
isolated integration capsule, returns structured conflicts and a continuation
token, and waits. A resolver/reviewer story may use an LLM or human, record the
decision, stage a resolution, run the requested validation, and then call apply
with the continuation token.

## Impact

- **Code seams:** new `internal/capsule/reconcile` package; `VCSProvider` and
  `RemoteProvider` DI interfaces; control-plane CLI/MCP tools; adapters for the
  four current scripts.
- **Vocabulary:** observed ref set, reconciliation class, plan digest,
  continuation token, integrate/refresh/promote/publish, protected ref.
- **Stories affected:** git-ops and dev-story can consume typed plan/results;
  their LLM conflict resolver and lost-work reviewer remain the interpretive
  layer.
- **Backward compat:** scripts stay authoritative until operation-by-operation
  parity tests pass, then become wrappers. A project that declares no ref policy
  gets local workspace status only, never inferred publish authority.
- **Docs on ship:** `docs/guide/development/capsule-ci.md`,
  `docs/stories/git-ops-conflict-avoidance.md`, and the host/MCP references.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| runtime type | `ObservedRefs` | `{workspace_head, base, target, protected, remote_refs, dirty, generation}` | Exact input snapshot to classification |
| runtime type | `ReconcilePlan` | `{id, digest, operation, class, expected, steps, conflicts?, required_gate, required_effect}` | Immutable and reviewable |
| enum | `class` | `up_to_date|fast_forward|local_ahead|remote_ahead|diverged|dirty|conflicted|missing` | Shared across local and remote refs |
| MCP | `capsule.sync.plan` | `{workspace, operation, target?, remote?}` -> plan | Read/fetch effects are explicit in result |
| MCP | `capsule.sync.apply` | `{workspace, plan_digest, continuation?, gate_receipt?}` -> result | Fails stale; never silently replans |
| MCP | `capsule.sync.abort` | `{workspace, continuation, preserve?}` -> result | Preserve writes an artifact; discard needs authority |
| trace | `capsule.sync.observed|planned|applied|stale|conflicted|aborted` | ref/action facts | Receipt consumer joins them |

## The model

```text
workspace/base/target/remote refs
              │ observe (+ optional fetch)
              ▼
       deterministic classify
              ▼
 ReconcilePlan{expected refs, steps, digest}
       │ clean path              │ conflict path
       ▼                         ▼
 policy + gate check      integration capsule + conflicts
       │                         │
 compare-and-swap         story resolver/reviewer decision
       │                         │
       └──────────── apply with continuation ───────────▶ result/receipt
```

The reconciler never runs an LLM. Git/ref inspection, merge-base
classification, isolated rebase/merge preparation, changed-path overlap,
expected-ref checks, and ref updates are deterministic. Resolver and promotion
approval decisions are normal story gates.

## Decision recording

`capsule.sync.planned` records the observed ref OIDs, classification, operation,
required effect, gate id, plan digest, and deterministic step list.
`capsule.sync.applied` records old/new refs, commits created, provider calls,
and the plan digest. A stale apply emits `capsule.sync.stale` with only changed
facts and requires a new plan.

Conflict-resolution traces additionally reference:

- the integration workspace instance/generation;
- conflict file/tree digests;
- the resolver agent or human gate events;
- the independent lost-work review decision;
- validation receipt used to authorize continuation.

The plan event is not the decision to promote. The story's gate event supplies
that authorization and slice 5 joins the two.

## Engine seams & invariants

Provider seams:

```go
type VCSProvider interface {
    Observe(ctx context.Context, instance WorkspaceHandle, req ObserveRequest) (ObservedRefs, error)
    Prepare(ctx context.Context, plan ReconcilePlan) (PreparedChange, error)
    Apply(ctx context.Context, plan ReconcilePlan, prepared PreparedChange) (ApplyResult, error)
    Abort(ctx context.Context, prepared PreparedChange, disposition Disposition) error
}

type RemoteProvider interface {
    Fetch(ctx context.Context, remote RemoteRef) (FetchResult, error)
    Publish(ctx context.Context, plan PublishPlan) (PublishResult, error)
}
```

Invariants:

1. plan digests cover project/policy, instance generation, every expected ref,
   dirty-state digest, operation, target, remote, and required gate;
2. apply re-observes without mutation and fails if any covered fact changed;
3. local integrate/refresh/promote and external fetch/publish are separate effect
   grants; publish is never implied by a green run;
4. protected refs update only by fast-forward unless an explicit project policy
   names a non-fast-forward strategy and an operator authorization event;
5. a dirty workspace cannot integrate until its changes are committed or an
   explicit preserve/discard action is recorded;
6. conflict work occurs only inside a managed integration instance; the source
   and protected checkout are not resolution surfaces;
7. promotion requires the configured CI receipt for the exact candidate commit,
   story digest, environment lock, and plan digest;
8. remote credentials are injected only into the specific provider call and are
   never placed in the workspace or trace;
9. retries are idempotent by plan id/provider idempotency key.

## Backward compatibility / migration

Each shipped helper becomes one parity fixture before being wrapped:

| Current helper | Generic operation | Required parity |
|---|---|---|
| `dev-workspace.sh merge` | `integrate` | target-advanced rebase, gate-before-import, overlap refusal, teardown after success |
| `refresh-staging-local.sh` | `refresh` | remote-freshness stop, dirty preserve/discard policy, staging rebase/import |
| `merge-to-main.sh` | `promote` | managed-source check, exact gate, protected fast-forward, narrow guard repair |
| `sync-main-from-remote.sh` | `refresh/publish` plan with integration instance | remote-ahead/diverged classification, isolated conflict continuation, resolver + independent lost-work review |

Kitsoki's `staging/local` convention becomes project policy, not a built-in
branch name. A simpler project may integrate directly to an agent branch and
publish a PR; a protected local project may retain staging; both use the same
operations.

## Tasks

```text
## 1. Plan/apply substrate
- [x] 1.1 Define ObservedRefs, ReconcilePlan, plan hashing, operation/class enums, VCSProvider, RemoteProvider, and fakes
- [x] 1.2 Implement deterministic observe/classify for local branches, protected refs, upstream refs, dirt, and overlap
- [x] 1.3 Implement stale-safe prepare/apply/abort with compare-and-swap ref updates and idempotency keys
- [x] 1.4 Emit sync lifecycle facts and bind exact gate/CI receipt requirements

## 2. Conflict and remote seams
- [x] 2.1 Create integration instances and structured conflict/continuation artifacts
  - Shipped: diverged plans include `capsule-sync-continuation/v1` tokens, required resolver/reviewer/validation inputs, CLI/MCP `capsule-sync-conflict/v1` artifacts with merge base, candidate/target changed paths, overlaps, CLI/MCP `capsule-sync-integration/v1` managed integration instances under `.capsules/sync/`, continuation apply from resolved integration commits that preserve both histories, and abort with optional preserved patch artifact.
- [ ] 2.2 Add story-facing resolver/reviewer inputs; require independent lost-work verdict before continuation
  - Shipped: runtime continuation apply refuses to proceed without resolver decision, independent lost-work review, and validation receipt inputs.
  - Remaining: wire the git-ops/dev-story resolver and reviewer rooms to these inputs.
- [x] 2.3 Implement local bare-remote provider and credential-free fetch/publish tests
  - Shipped: local bare-remote fetch provider updates a named remote-tracking ref without credentials; local bare-remote publish provider checks the live remote-ref lease before pushing.
- [ ] 2.4 Add production git/PR provider with separate read/external-write grants and secret-reference injection

## 3. Adopt and retire duplication
- [ ] 3.1 Capture parity fixtures for all four Kitsoki helpers before wrapping them
- [ ] 3.2 Add capsule sync CLI/MCP tools and migrate git-ops/dev-story callers
- [ ] 3.3 Convert scripts to wrappers/project hooks, preserving operator recovery UX
- [ ] 3.4 Document project branch policies and promotion receipts; trim/delete this proposal
```

## Verification

Use reusable synthetic capsules (`clean-repo`, `diverged-remote`,
`rebase-conflict-ready`, `mid-rebase-conflict`, `dirty-index`) and bare local
remotes. Required cases include every classification, stale ref/generation,
concurrent apply (exactly one compare-and-swap wins), dirty overlap,
fast-forward-only protection, resolver success, lost-work review rejection,
gate receipt mismatch, publish denial, retry idempotency, and abort with
preserved patch artifact. All LLM resolver/reviewer responses are cassette or
schema stubs; no real remote or model is used in tests.

## Open questions

1. **Rebase vs. merge preparation** — runtime default or project policy. *Lean:
   project policy per operation; Kitsoki keeps rebase for integrate/refresh,
   while the plan always states the exact strategy.*
2. **Who signs promotion authorization** — CI story terminal event vs. a
   separate promotion story. *Lean: the configured CI receipt proves checks;
   protected policy may additionally require a separate human/LLM promotion
   gate.*
3. **PR merge ownership** — reconciler provider vs. GitHub-agent story. *Lean:
   provider exposes the deterministic effect, but a story owns the decision and
   GitHub-agent owns ingress/comment UX.*

## Non-goals

- Teaching the runtime to decide how conflicts should be resolved.
- Treating `git pull`, force push, or remote merge as a generic shell command.
- Replacing provider-native branch protection or required checks.
- Baking Kitsoki's `main`/`staging/local` names into the generic engine.

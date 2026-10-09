# Capsule CI

Capsule CI is Kitsoki's project-local CI mechanism. A project declares a
pipeline in `.kitsoki/ci.yaml`; the pipeline selects a story, environment, and
policy. The story is the CI graph: deterministic checks, review, agent tasks,
human checkpoints, and refinement are ordinary story rooms—not a second
`steps`/`needs` YAML language.

## Project files

```text
.kitsoki/
├── ci.yaml
├── environments/ci.yaml
└── capsules/              # optional project definitions
```

Existing root `capsules/<id>/capsule.yaml` fixtures remain compatible. A
project definition uses `capsule-definition/v1` and may select `synthetic`,
`self`, or a full-commit `pinned` source. Environment definitions use
`capsule-environment/v1`; `network: none` is the default and environment
resolution only probes tools—it never installs software. A `none` or `replay`
environment requires an executor with a real containment boundary; ambient host
execution is deliberately not eligible for either posture.

Kitsoki's checked-in `ci` environment declares `bootstrap-workspace` as the
bootstrap hook and grants project-scoped `go-build` plus
`runstatus-node-modules` caches. The environment lock includes `go.sum`,
`tools/runstatus/pnpm-lock.yaml`, the bootstrap command digest, and stable cache
keys, so local and remote CI can refuse drift instead of silently using a
different workspace setup.

## Local lifecycle

For the fast pre-staging path, use:

```sh
make capsule-ci-quick
```

This is the default gate for a managed workspace landing in `staging/local`
when no explicit `--gate` is supplied. It is deterministic and does not invoke
an LLM: it validates the declared story, replays checked-in flow/cassette
fixtures, runs focused short tests, and checks diff hygiene.

LLM review is a separate, explicit, spend-bearing policy decision. Configure an
allowed profile, positive budget, and unavailable-model fallback, and run it
only after this no-spend gate is green. Review prompts should inspect test
deletion, disabled or skipped coverage, weakened assertions, and changed test
scope. A failed or unavailable review is not promotion evidence, and ordinary
branch landing must never spend implicitly.

```sh
kitsoki capsule workspace create --id change-1 --definition development --owner developer
kitsoki capsule env resolve ci
kitsoki capsule ci plan change --workspace change-1
kitsoki capsule ci doctor change --workspace change-1 --json=false
kitsoki capsule ci run change --workspace change-1
kitsoki capsule ci status
kitsoki capsule ci status --job <job-id> --refresh
kitsoki capsule ci diagnose --latest --stall-after 2m --json=false
kitsoki capsule ci cancel --job <job-id>
kitsoki capsule cleanup plan --keep-runs 20
kitsoki capsule workspace commit --id change-1 --message "Implement change"
kitsoki capsule workspace integrate --id change-1 --gate "go test ./..." --teardown
```

`capsule ci run` seals source, story, environment, and policy identities into
an execution envelope, invokes the declared story's `run` intent, and accepts
only a matching `capsule-ci-verdict/v1` object in `world.ci_verdict`. A story
that cannot complete safely must emit `needs_input`. The reference
`stories/capsule-ci` reads `.kitsoki/project-profile.yaml`, runs its declared
`test` and `build` commands as deterministic story facts, writes bounded
evidence under `.artifacts/capsule-ci/checks/`, and parks when neither command
is declared.

`capsule ci doctor` is the no-spend readiness gate. It verifies the selected
pipeline, complete story closure, environment lock, workspace HEAD and
cleanliness, credential availability by environment-variable name, executor
capability/policy compatibility, bounded remote deadlines, disk headroom, and
hygiene debt. It may negotiate a remote worker's capability endpoint, but it
does not upload source, run the story, or launch an agent. HTTPS workers also
receive the exact prepared envelope at `/v1/capsules/validate`, so a green
doctor proves worker-side protocol/policy acceptance rather than only trusting
the advertised capability list.

Pipeline configuration, story closure, environment definition/lock inputs, and
local story execution are all resolved from the exact managed workspace named
by the source digest—not from a remembered or protected controller checkout.
Run records, traces, and receipts remain in the controller project's
`.capsules/ci` custody directory. This keeps a candidate change to its own CI
story/config reproducible locally and remotely without losing the operator's
stable evidence location.

Pipeline `executor:` is an immutable placement selection: `host`/`local` runs
through the host provider, `remote-fake` exercises the identical remote-worker
protocol without a network, `container-fake` exercises the Arena-style
container completion-state adapter without requiring Docker, and `container`
selects a caller-injected production container provider. A production
remote-worker name must be declared in the checked-in `remotes:` map; a story
cannot select or add one itself. The shipped `HTTPRemoteWorker` adapter accepts
only HTTPS endpoints, sends the sealed envelope to the worker, receives a
serialized typed verdict/result, and uses an ephemeral authorization header
callback. Credential values never enter the envelope, workspace, trace, or
receipt.

### Enforcement boundary

The environment lock network and sealed pipeline network must match exactly.
`external_write: deny` is accepted only for `network: none` or `replay`; a live
network cannot honestly make that guarantee in v1. The launcher also installs a
final host-effect guard after all story and test hooks: external host verbs fail
closed under `external_write: deny`.

`host` is an explicitly trusted compatibility placement. It has process
supervision but no portable egress confinement, so it advertises only
`network: live`; use `external_write: allow` and `agents.policy: deny` unless
you deliberately grant an agent. Container and remote placements may advertise
`none` or `replay` only when their Docker/network/VM deployment actually
enforces that boundary. The execution record proves that the executor accepted
the sealed policy against its declared capabilities; the deployment attestation
is not a substitute for a firewall, namespace, or VM policy.

Kitsoki's own checked-in local compatibility pipeline follows this rule. A
project that needs deterministic, no-egress CI should select `container` with
Docker `--network none`, or a remote worker deployed inside an independently
enforced none/replay boundary.

Remote source transport is content-addressed and separate from the envelope.
The controller refuses a dirty or untracked workspace and requires its exact
Git HEAD to match the sealed source digest. It creates a complete
`capsule-source-bundle/v1` Git bundle (256 MiB maximum by default), probes the
worker's source cache, and uploads it only on a miss. The worker verifies the
bundle digest and commit before materializing an isolated clone. It then
recomputes `capsule-story-closure/v1` over loaded manifests, imported stories,
prompts, Starlark, schemas, views, and the kit lock; a source or story mismatch
fails before story execution. Finally it re-resolves the sealed environment from
the materialized source and requires the complete lock digest to match;
lockfile, bootstrap, toolchain, devcontainer, or definition drift fails at
`verifying_environment`. The standalone `worker serve` command ships a host
tool probe. An image-backed remote deployment must embed/inject an independent
image resolver; the standalone worker refuses such a lock rather than trusting
the controller's tag-to-digest observation.

The checked-in reference `stories/capsule-ci` is covered as a no-LLM parity
fixture on `host`, `remote-fake`, and `container-fake`: all placements consume
the same sealed envelope, emit the same typed verdict, and park with
`needs_input` rather than fabricating a green gate before a project-specific
composition exists. The container adapter carries the shared
`completion-state/v1` metadata that Arena cells already use. The real Docker
backend follows Arena's model: mount the workspace at `/workspace`, mount an
executor result directory at `/results`, run a pinned image, and consume
`/results/result.json` containing the normalized executor result plus
`completion-state/v1`. Its default entrypoint is:

```sh
kitsoki capsule worker run \
  --envelope /results/envelope.json \
  --result /results/result.json
```

The Docker provider receives a workspace-path resolver from the caller; host
paths stay out of the sealed envelope. Its default capability is
`network: none`, enforced with Docker's `--network none`; it advertises
`network: replay` only when the embedding deployment supplies a supervised
replay network explicitly.

### HTTPS worker deployment

A production worker requires TLS, bearer authentication, a durable root, and
truthful capability declarations:

```sh
KITSOKI_CAPSULE_WORKER_TOKEN=... kitsoki capsule worker serve \
  --listen 0.0.0.0:7443 \
  --root /var/lib/kitsoki/capsule-worker \
  --tls-cert /etc/kitsoki/worker-cert.pem \
  --tls-key /etc/kitsoki/worker-key.pem \
  --token-env KITSOKI_CAPSULE_WORKER_TOKEN \
  --isolation vm \
  --network none,replay \
  --retain-terminal-runs 20 \
  --min-terminal-age 24h \
  --min-source-age 24h
```

The worker defaults to `--network live`; use repeated/comma-separated
`--network` values only for policies the deployment actually enforces. If a CI story is allowed to invoke a coding
agent, add `--agent-backend <backend>` and explicitly name only the provider
environment variables that the child may receive with `--pass-env`; the worker
child does not inherit the ambient environment. The certificate must contain
the worker host or IP in its SAN. For a private CA, set a project-relative
`ca_file` on the remote definition.

Worker state is durable under `<root>/sources/` and
`<root>/runs/<execution-id>/`. Each run directory contains `run.json`, the
sealed envelope/result, and `story-trace.jsonl`. The run record checkpoints
requested, source materialization, story closure, environment verification,
story execution, and terminal state with timestamps. Repeated submission of a
terminal execution id returns the cached result; `GET` exposes status and
`DELETE` requests cancellation.

Worker-root hygiene is part of the service lifecycle, not an external cron
assumption. `worker serve` runs one bounded cleanup pass before accepting
traffic and repeats it every 30 minutes by default. Each pass retains the 20
newest terminal runs, age-gates run and source removal for 24 hours, and removes
at most 50 run directories plus 50 source objects. Active, non-terminal, or
invalid run records are never removed. Invalid/uncertain run state blocks all
source deletion; otherwise only source bundles unreferenced by every remaining
run and aged since their last verified controller cache use are eligible. A
successful controller HEAD cache check atomically refreshes that retention age
before the following run submission, preventing cleanup from deleting the
source in that handoff window. Override the bounds with
`--cleanup-interval`, `--retain-terminal-runs`, `--min-terminal-age`,
`--min-source-age`, `--cleanup-max-run-deletes`, and
`--cleanup-max-source-deletes`.

Plan or apply the same policy offline:

```sh
kitsoki capsule worker cleanup --root <worker-root> --json=false
kitsoki capsule worker cleanup --root <worker-root> --apply --json=false
```

Every pass atomically writes a path-free `capsule-worker-cleanup/v1` history
record plus `cleanup/latest.json` under the worker root. Logs and execution
status expose only outcome/count/byte summaries; they never copy root paths,
execution/source identifiers, provider content, or error text.

Persisted Capsule CI runs are diagnosable without reading raw JSON by hand:

```sh
kitsoki capsule ci diagnose --job <job-id> --json=false
kitsoki capsule ci diagnose --latest --stall-after 2m --json=false
```

The diagnosis projection reads the run record and co-located trace sidecar,
reports the terminal error, executor failure kind, last executor event, trace
and receipt paths, last durable stage/activity, whether the executor span is
still open, a time-based stall classification, and copy-ready next commands.
The JSON form is
`capsule-ci-run-diagnosis/v1` and is provider-safe: it keeps local evidence as
artifact paths and summarizes only allowlisted executor fields such as
execution id, transport, remote host, request id, status, duration, error kind,
message, and exit code. Run records are checkpointed before preparation, while
running, and at every terminal outcome; trace, receipt, and run-record writes
use replace-on-success files so a process crash cannot leave a half-written JSON
document masquerading as evidence.

For a durable remote execution, `status --job <id> --refresh` queries the
worker's provider-neutral `capsule-execution-status/v1` record and persists that
fact locally. While a story is running, that status includes a strict
`capsule-agent-diagnostics/v1` projection of the worker JSONL trace: hashed call
references, lifecycle/process phases, numeric timings/counts, last activity,
and a typed stall hint. Prompt/response text, command arguments, provider error
text, environment values, and absolute paths remain only in the worker-local
trace. `capsule ci diagnose` consumes this projection, so a stalled remote job
can distinguish a dispatched host call, an agent awaiting process start, a
running/no-output process, provider activity, and a process awaiting call
completion. Cancellation is two-phase: `cancel` sends the worker request and
records `cancelling`/interrupted; only a later worker `cancelled` status becomes
a terminal local cancellation. A worker that already completed or failed is
reported honestly rather than rewritten as cancelled.

The reference story exposes the intended room graph directly: prepare,
deterministic checks, schema-bounded review, bounded writer/refine, and
adjudication. Its default direct-run path executes the project profile's
deterministic commands and emits a matching typed verdict. Optional review and
refinement remain explicit rooms; automated fixtures use cassettes and never a
live model.

The cassette-backed review fixture uses `host.agent.decide` with
`schemas/review-verdict.json` and `prompts/review.md`, but the flow replays the
agent response from `flows/cassettes/llm-review.cassette.yaml`. That keeps the
review path traceable and schema-bounded without invoking a live LLM in tests.

Generated project wrappers use the same deterministic project-check host as the
reference story. Offline service tests run the wrapper through host, fake
remote, and fake container placements and reject a wrapper that emits
mismatched digest fields.

GitHub ingress is an adapter around the same contract. A pull-request webhook
normalizes to `Trigger{kind:"pull_request", provider:"github", ...}` and the
check-run adapter projects a `capsule-ci-verdict/v1` run result to a GitHub
check conclusion. The adapter does not define a second pipeline result.
Automated tests use local HTTP servers and never call GitHub.

```sh
kitsoki capsule ci github trigger \
  --event pull_request \
  --payload payload.json \
  --pipeline change > trigger.json

kitsoki capsule ci run change \
  --workspace change-1 \
  --trigger trigger.json

kitsoki capsule ci github check \
  --project . \
  --job 01K... \
  --details-url https://ci.example/runs/01K...

GH_TOKEN=... kitsoki capsule ci github publish-check \
  --project . \
  --job 01K... \
  --repo owner/repo \
  --details-url https://ci.example/runs/01K...
```

`check` emits the JSON payload for offline inspection or an external publisher.
`publish-check` is the explicit network boundary: it posts
`POST /repos/{owner}/{repo}/check-runs` with `GH_TOKEN`/`GITHUB_TOKEN` or an
operator-selected `--token-env`. The token is never serialized into the run
record, trace, or command result.

`plan|run --trigger <file|->` accepts only the normalized `ci.Trigger` object,
defaults a missing requested pipeline to the command's pipeline, and rejects a
pipeline mismatch. Provider raw payloads remain external artifacts; the story
and receipt see the same bounded trigger contract for local and GitHub entry.

The Capsule MCP writer proof is also offline: a test-visible writer receives
only `capsule.*` tools, edits through `capsule.fs.write`, inspects local status,
commits through `capsule.vcs.commit`, and is denied raw argv without the
explicit `raw_exec` effect.

Example remote placement:

```yaml
remotes:
  remote-prod:
    endpoint: https://capsule-worker.example
    credential_env: KITSOKI_CAPSULE_WORKER_TOKEN
    ca_file: .kitsoki/certs/capsule-worker-ca.pem # optional private CA

pipelines:
  change:
    executor: remote-prod
```

`credential_env` stores only the environment variable name in the checked-in
file. If present, the variable must be set by the launching operator process;
the value is read only when the HTTP request is made and is sent as an
authorization header. `ca_file` must be project-relative, contain PEM
certificates, and cannot disable hostname/SAN verification.

The optional `--verdict file.json` run input is for an explicit external story
adapter. It is not an authority bypass: the verdict still has to match the
envelope and promotion eligibility is derived from its outcome and evidence.

## Least-authority agents

Run the standalone server when a coding agent should receive only one scoped
tool surface:

```sh
kitsoki capsule mcp --project /path/to/project --pipeline change --executor local --branch staging/local
```

It exposes opaque workspace handles and project-relative filesystem paths. The
agent cannot name arbitrary host paths, add a remote, obtain credentials, or
widen effects. The tool catalog itself is grant-shaped: workspace create/close,
file write, exec, commit, reconciliation, CI run/cancel, environment lock, and
cleanup apply are not registered unless their corresponding immutable startup
effect is present. Read-only project, definition, owned-workspace, bounded
filesystem, VCS status/diff, CI status/summary, cleanup-plan, and environment
verification tools remain available.

With `fs_write`, an agent may use `capsule.fs.write` or guarded
`capsule.fs.patch`. Patch requires the exact SHA-256 preimage of the file it
read and returns a fresh opaque generation, so stale agent edits fail rather
than overwriting a newer workspace change.

Every workspace operation checks both the opaque generation handle and the
server's lease owner. File operations canonicalize project-relative paths after
symlink evaluation; read/search skip verifier assets, `.git`, symlinks,
generated dependency trees, binary files, and oversized files, and enforce
bounded file/count/byte budgets. `--branch` is required for each reconciliation
target; omitting it denies synchronization while retaining the rest of the tool
surface. Remote publication and credential delivery are intentionally absent
unless a distinct trusted server is started with those effects.

Pipeline agent calls are sealed separately in the execution envelope. The
default is `agents.policy: deny`. `allow` is valid only with an explicit profile
allowlist, positive cost ceiling, and unavailable-model fallback; every
`host.agent.*` call must select an allowed profile, and the runtime enforces the
budget before and after dispatch. Story code and injected test hosts cannot
replace this guard because it is installed last.

## Environments and remote placement

`capsule env resolve|lock|verify` creates and verifies content-addressed
environment locks. The executor contract supports host and remote providers;
the repository ships host, deterministic fake-remote, and checked-in
HTTPS-remote selection. Remote workers own source materialization and story
execution for the sealed envelope they receive; the local controller validates
the returned typed verdict against that envelope before persisting receipts.

## Compatibility

The native `capsule workspace` commands are the general-project API available
to every project with `.kitsoki/`. Kitsoki contributors follow the repository's
stricter [`scripts/dev-workspace.sh` lifecycle](../../dev-workspaces.md), as
required by `AGENTS.md`; do not substitute raw Git or the native front door for
that protected-checkout workflow. The checked-in `development` compatibility
provider preserves the same clone, branch-target, bootstrap, rebase, and
primary-checkout safeguards for story/runtime callers while migration remains
in progress.

## Receipts and promotion

Capsule receipts are canonical `capsule-ci-receipt/v1` projections over a
sealed envelope, typed verdict, artifacts, and trace custody digest. A receipt
must verify, be promotion eligible, and bind to the promotion plan's exact
candidate before it can authorize promotion; a green-looking story response
alone cannot.

Local `integrate`, `refresh`, and `promote` reconciliation plans update refs
only through stale-safe fast-forward checks. `publish` is deliberately separate:
a publish plan carries the `remote_publish` effect and cannot apply through the
local Git reconciler. A remote publication provider must be explicitly granted
and injected before a publish plan can be applied.

When a stored plan is diverged, materialize the deterministic conflict input
and managed integration instance for a resolver/reviewer story before
attempting continuation:

```sh
kitsoki capsule sync conflicts --plan <digest>
kitsoki capsule sync integration --plan <digest>
kitsoki capsule sync continue \
  --plan <digest> \
  --resolver-decision <artifact> \
  --lost-work-review <artifact> \
  --validation-receipt <receipt>
kitsoki capsule sync abort --plan <digest> --preserve
```

The command writes a `capsule-sync-conflict/v1` artifact under
`.capsules/sync/` with the merge base, candidate/target changed paths, overlap
paths, required story inputs, and continuation token. The integration command
creates `.capsules/sync/<token>.integration`, checks out the candidate on a
resolution branch, attempts a no-commit target merge, and writes a
`capsule-sync-integration/v1` artifact with project-relative paths and conflict
status. After the resolver commits inside that managed integration instance,
continuation apply verifies the resolver decision, independent lost-work review,
validation receipt, unchanged original refs/generation, and that the resolved
commit preserves both candidate and target histories before updating the target.
Agents limited to Capsule MCP use `capsule.sync.conflicts`,
`capsule.sync.integration`, `capsule.sync.continue`, and `capsule.sync.abort`
for the same operations; all accept only server-owned plan digests and return
only project-relative artifact paths. Abort removes the managed integration
instance and, when requested, preserves a `.capsules/sync/<token>.abort.patch`
artifact before cleanup.

For credential-free local development and tests, `capsule sync apply` can inject
a local bare-remote fetcher/publisher:

```sh
kitsoki capsule sync fetch \
  --workspace <workspace> \
  --local-bare-remote /path/to/origin.git \
  --remote origin \
  --branch main
kitsoki capsule sync apply --plan <digest> --local-bare-remote /path/to/origin.git
```

The fetcher updates only the named remote-tracking ref. The publisher checks
the live bare-remote ref still matches the plan's expected target before
pushing the candidate commit. Production Git/PR publication is a separate
provider/grant path.

See [Capsule CI receipts](../../tracing/capsule-ci-receipts.md).

## Ongoing Cleanup

Capsule CI writes useful local evidence: run records, receipt sidecars, compact
controller traces, managed workspaces, and optional caches. Treat cleanup as a
normal part of the CI lifecycle, especially on developer machines where Go
build caches and repeated workspace runs can consume tens of gigabytes.

Start with a dry-run plan:

```sh
kitsoki capsule cleanup plan --keep-runs 20
```

The default inventory covers Capsule CI evidence and managed development
workspaces. It keeps the newest 20 run bundles and five newest clean terminal
workspaces, requires a workspace to be at least 24 hours old, and reports free
space against a 10 GiB floor. Workspace byte measurement is deliberately
shallow/fast by default because clone object databases may use shared hardlinks;
use `--measure-workspace-bytes` for a slower accounting pass. Pin evidence under
investigation with `--pin-workspace <id>` or a managed pin marker.

```sh
kitsoki capsule cleanup plan \
  --keep-workspaces 10 \
  --workspace-min-age 72h \
  --min-free-bytes 21474836480
```

Active, dirty, current, recent, pinned, staging, unmerged, ownership-ambiguous,
initializing, or process-open workspaces remain inventory entries with
`safe: false`. Legacy script-created clones are adoptable only when all four
Capsule/clone/manifests agree, Git is clean, the exact HEAD is contained in the
declared target, and activity can be ruled out. If `lsof` is unavailable or
inconclusive, cleanup fails closed for that candidate.

Project caches and the Go build/test cache remain explicit:

```sh
kitsoki capsule cleanup plan --include-capsule-cache --include-go-build-cache
kitsoki capsule cleanup apply --include-capsule-cache --include-go-build-cache
```

`apply` rebuilds the plan and rechecks generation, lifecycle, ownership, pins,
current workspace, Git cleanliness, merge reachability, and activity before
every removal. Changed candidates are `skipped`. Managed instances close
through their provider; legacy development clones tear down through
`scripts/dev-workspace.sh`, never direct directory deletion. Go build cache
cleanup goes through `go clean -cache -testcache` rather than deleting an
arbitrary directory, because that cache may live outside the project root;
recognized concurrent-clean races are retried and reported separately.

Agents limited to `kitsoki capsule mcp` use `capsule.cleanup.plan` for a
path-redacted hygiene dry run and `capsule.cleanup.apply` when their immutable
startup grant includes the `cleanup` effect. MCP cleanup intentionally stays
inside the project Capsule tree, hides or pins other owners' workspaces, and
does not clear host-global Go caches.

Projects can also make hygiene a required CI fact in `.kitsoki/ci.yaml`:

```yaml
cleanup:
  keep_runs: 20
  require_hygiene_check: true
  max_reclaimable_bytes: 1073741824 # 1 GiB

pipelines:
  change:
    cleanup:
      keep_runs: 10
```

When `require_hygiene_check` is true, `kitsoki capsule ci run` and
`capsule.ci.run` add a `capsule-hygiene` verdict check from the same cleanup
planner used by `kitsoki capsule cleanup plan`. If reclaimable state exceeds
`max_reclaimable_bytes`, the run is not promotion-eligible. CI does not delete
state as a side effect; deletion still goes through `cleanup apply` with the
appropriate CLI/operator/MCP authority. The Capsule MCP CI path remains
project-scoped for hygiene planning; host-global Go cache inspection is a
CLI/operator concern.

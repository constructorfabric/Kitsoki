# Hermetic capsules

Capsules are deterministic, reusable git/development states. They give tests,
story fixtures, benchmark runners, and agent workspaces one shared way to say:
open this repository state, prove it matches, run against it, then tear it down.

There is one capsule concept. Different executors may materialize it while the
substrate grows:

- **Core fixture capsules** live under `capsules/<name>/capsule.yaml` and are
  opened by `internal/capsule`, `internal/capsuletest`, and `kitsoki capsule`.
- **Repo-history capsules** are historical bug capsules consumed by
  `stories/repo-bakeoff` and `tools/bugfix-bakeoff/external`. Their manifests
  and hidden-oracle grading remain evaluation concerns, but source checkout is
  a native pinned-Git Capsule under the parent project's managed
  `.capsules/projects/` tree. The bakeoff code is an evaluation adapter, not a
  second workspace materializer or executor.

The shipped v1 is intentionally narrow and local-only:

- `internal/capsule` loads `capsules/<name>/capsule.yaml`, validates the schema,
  materializes synthetic git repositories with fixed author/date identity, writes
  `capsule-manifest.json`, verifies tree digests and probes, and sentinel-gates
  cleanup.
- `internal/capsuletest.Open(t, name)` opens capsules under `t.TempDir()` and
  registers cleanup for Go tests.
- `kitsoki capsule open|verify|close` exposes the same behavior from the CLI.
- `kitsoki capsule workspace` opens managed source workspaces through the
  control plane. Synthetic fixtures, project `self` sources, pinned Git
  sources, and the Kitsoki development compatibility provider all share opaque
  handles, generation checks, lifecycle facts, and least-authority FS/exec/VCS
  operations.
- `host.capsule_workspace` is the story-facing Capsule workspace provider for
  generated project instances. Onboarding writes
  `.kitsoki/capsules/development.yaml` with `source.kind: self` and binds
  imported dev-story `workspace` calls to the Capsule host. Each
  list/create/get/status/sync/commit/close/cleanup return includes a
  `diagnostics` map, and domain errors preserve the same map in
  `host_error.data`, so traces show the failing op, repo, definition, id, path,
  state, VCS status, cleanup recommendation reason, and remediation hint.
  `cleanup_scan` is conservative: it exposes active and dirty workspaces for
  review but recommends only integrated or failed workspaces that are not dirty
  and do not match protected branches.
- Pinned Git sources populate a content-addressed bare cache under
  `.capsules/cache/git-sources/<commit>.git` before materializing a workspace.
  Subsequent materializations of the same full commit clone from the cache
  without touching the original source path or remote.
- `visibility: workspace` overlays are copied into the workspace. `visibility:
  verifier` overlays are recorded as project-relative digest refs and are
  resolvable only by same-process verifier execution; they are not copied into
  the workspace and are not reachable through the agent-facing FS/MCP tools.
- `scripts/dev-workspace.sh` remains a compatibility entry point for Kitsoki's
  protected local workflow, opening clone-backed development workspaces under
  `.capsules/workspaces/`, writes the same capsule sentinel/manifest plus a
  `.kitsoki-clone` manifest, and owns bootstrap, commit, merge, and teardown for
  ad-hoc Codex/Claude work and the default dev-story workspace provider. See
  [`dev-workspaces.md`](../../dev-workspaces.md) for the operator runbook.
- Starter capsules cover `clean-repo`, `rebase-conflict-ready`,
  `mid-rebase-conflict`, `dirty-index`, `stale-worktree`, and
  `diverged-remote`.

Environment image capture, broader flow-test bindings, product-journey adoption,
and Harbor import/export remain follow-on slices for the core materializer. The
host provider does not install tools on the host. Remote network fetches happen
only when a pinned source cache miss requires reading the declared source and the
caller has chosen a source that requires network access.

## Capsule spec

A capsule is a directory containing `capsule.yaml`:

```yaml
name: clean-repo
source:
  synthetic: true
  default_branch: main
  steps:
    - action: write
      path: a.txt
      content: |
        a
    - action: commit
      message: init
network: none
verify:
  probes:
    - name: clean-tree
      run: git diff --quiet && git diff --cached --quiet
      expect: zero
scenario:
  kind: git-flow
```

Supported synthetic actions are `write`, `mkdir`, `remove`, `commit`,
`checkout`, `branch`, `git`, and `bare_remote`. Every git command runs with
fixed identity and dates so commit topology is reproducible.

`verify.tree_digest` may pin the expected `sha256:<digest>` of the materialized
working files. When omitted, verification still reports the actual digest in the
manifest and CLI output. Probes are shell commands run inside the workspace with
`expect: zero` or `expect: nonzero`.

## CLI

List every capsule known in the current Kitsoki checkout, including
repo-history evaluation manifests materialized through pinned Capsules:

```sh
go run ./cmd/kitsoki capsule list
go run ./cmd/kitsoki capsule list --kind repo-history
```

Open a capsule into a temp directory:

```sh
go run ./cmd/kitsoki capsule open clean-repo
```

Open into a specific empty directory:

```sh
go run ./cmd/kitsoki capsule open clean-repo --dest /tmp/clean-capsule
```

Verify a spec by opening a disposable workspace:

```sh
go run ./cmd/kitsoki capsule verify clean-repo
```

Verify an already-open workspace:

```sh
go run ./cmd/kitsoki capsule verify /tmp/clean-capsule
```

Close only removes a directory with a capsule sentinel:

```sh
go run ./cmd/kitsoki capsule close /tmp/clean-capsule
```

## Go tests

```go
repo := capsuletest.Open(t, "clean-repo")
```

The helper returns a real git checkout and removes it during test cleanup. Use it
instead of bespoke `git init` fixtures when the desired state belongs in the
shared capsule library. New reusable git/workspace test fixtures should be
capsules by default; keep bespoke temp repos only when the test is specifically
asserting repository creation/bootstrap behavior or exact git command ordering.

## Manifest

Every open writes `capsule-manifest.json` in the workspace. It records the spec
path, workspace, opened time, source type, HEAD, branch, network mode, tree
digest, and probe results after verification. Consumers should persist that
manifest as provenance when a capsule-backed run produces artifacts.

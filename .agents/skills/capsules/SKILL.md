---
name: capsules
description: Use Kitsoki hermetic capsules for deterministic repository/workspace fixtures. Trigger when adding or migrating Go tests, story fixtures, MCP/tool tests, workflow validation, or agent QA that needs a reusable git repo state; when you see ad hoc `git init`, repeated `t.TempDir()` repo setup, rebase/conflict/dirty-index setup, or instructions to make capsule usage exclusive.
---

# Capsules

Capsules are the default way to create reusable git/workspace states in Kitsoki.
Prefer them over hand-written temp repo setup when the test needs a known state
rather than testing repo creation itself. Repo-history bug cases are capsules
too: their manifests and hidden-oracle grading stay in the repo-history harness,
but candidate workspaces are materialized by a sentinel-owned native Capsule
project under `.capsules/projects/` with an immutable `pinned` source. The
harness is an evaluation adapter, never the CI/runtime control plane. Do not
rebuild that lifecycle with raw clone, checkout, reset, or linked-worktree
commands.

## Workflow

1. Run `go run ./cmd/kitsoki capsule list` and check
   `capsules/<name>/capsule.yaml` before creating a new fixture.
2. In Go tests, open fixtures with:

```go
repo := capsuletest.Open(t, "clean-repo")
```

3. Add `kitsoki/internal/capsuletest` only in tests; production code should use
   `internal/capsule` or the CLI.
4. Author new reusable fixtures under `capsules/<name>/capsule.yaml` with
   `source.synthetic: true`, `network: none`, and verification probes.
5. For repo-history bugs, add rows under
   `tools/bugfix-bakeoff/external/projects/<project>/manifest.yaml`, list them
   with `go run ./cmd/kitsoki capsule list --kind repo-history`, and validate
   with `make history-smoke`. Use `drive_cell.sh --no-drive` to prove the
   resulting handoff names a pinned Capsule project/id/owner/generation before
   any live model call.
6. Run or recommend focused validation for changed packages and
   `go run ./cmd/kitsoki capsule verify <name>` for new capsules.

## When not to use a capsule

Keep bespoke setup when the behavior under test is:

- creating or bootstrapping a repository;
- asserting the exact git commands issued through a fake runner;
- a one-off filesystem/database/cache temp directory that is not a repo state;
- a gated external corpus or local-only mirror test.

## Useful commands

```sh
go run ./cmd/kitsoki capsule list
go run ./cmd/kitsoki capsule open clean-repo
go run ./cmd/kitsoki capsule verify clean-repo
go run ./cmd/kitsoki capsule close /tmp/opened-capsule
make repo-history-capsules
tools/bugfix-bakeoff/external/drive_cell.sh --project <project> --bug <bug> --candidate <candidate> --no-drive
```

## Investigation and cleanup

Use `go run ./cmd/kitsoki capsule cleanup plan --project .` before any manual
disk triage. The JSON inventory includes ordinary workspaces and generic
sentinel-owned Capsule control projects under `.capsules/projects/`, with their
project kind, owner, provenance digest, activity, lifecycle, age, and exact
reason each candidate is protected or reclaimable. Invalid or changed
provenance is evidence to investigate and is never auto-deleted.

Pin evidence with `--pin-workspace <id>` or a `.kitsoki-capsule-pin` marker.
Cleanup closes safe terminal child workspaces through their owning Capsule
manager first; a later plan may remove the empty project root after its recent
activity guard expires. Apply rechecks provenance, generation, owner, state,
Git status, pins, current directory, and process activity. Never replace this
with `rm`, raw Git teardown, or harness-specific cleanup.

Evaluation adapters must also close disposable workspaces after durable grading
instead of waiting for hygiene. The external bakeoff does this by default;
`--keep-workspace` is a debug-only override that creates a managed pin, and any
close failure is recorded as cleanup debt and fails the adapter command.

## Existing starter capsules

Use `clean-repo`, `rebase-conflict-ready`, `mid-rebase-conflict`,
`dirty-index`, `stale-worktree`, and `diverged-remote` for common git states.

# Repo History Capsules

Repo-history capsules are reusable historical bug cases for dev-story
onboarding and the `repo-bakeoff` path. They are capsules in the same Kitsoki
sense as the synthetic fixtures under `capsules/`: named, reusable repository
states with deterministic verification. The current executor is the
repo-history harness, because core `kitsoki capsule open` still only
materializes local synthetic fixtures.

Each promoted capsule comes from one bug row under
`tools/bugfix-bakeoff/external/projects/<project>/manifest.yaml` plus its
hidden oracle test. The harness keeps the oracle separate from the candidate
fix:

1. GREEN: the target checkout and normal test/dependency setup are usable.
2. RED: at `baseline_sha`, overlay only the hidden oracle and require failure.
3. GREEN: from that same baseline, overlay only the real maintainer fix source
   from `fix_sha` and require the oracle to pass.
4. GREEN: score a candidate worktree with the same hidden oracle.

The promoted corpus currently has ten repo-history capsules:

- `query-string`: `qs1`, `qs2`, `qs3` from public GitHub issues/PRs.
- `gears-rust`: `bug1`, `bug4`, `bug5`, `bug9` from the private Rust reference
  repo. These can run from a standalone checkout or from a meta checkout with
  the submodule initialized.
- `kitsoki`: `bug9`, `bug12`, `bug14` from Kitsoki dogfood bugs.

List the catalog:

```sh
go run ./cmd/kitsoki capsule list --kind repo-history
make repo-history-capsules
python3 tools/bugfix-bakeoff/external/bench.py capsules
```

`make oracle-capsules` and `bench.py oracle-capsules` remain compatibility
aliases, but new docs and scripts should use repo-history capsule names.
The Make target uses the Go capsule catalog and has no Python package
dependency; use `bench.py capsules` when you need the harness-specific
baseline/fix columns.

Verify a public project:

```sh
python3 tools/bugfix-bakeoff/external/bench.py verify --project query-string
```

Verify gears-rust as a standalone checkout:

```sh
BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
```

Verify gears-rust through a meta checkout:

```sh
python3 tools/bugfix-bakeoff/external/bench.py verify \
  --project gears-rust \
  --bug bug1,bug4,bug5,bug9 \
  --repo-dir /path/to/meta-checkout
```

In the meta-repo form, the manifest resolves
`/path/to/meta-checkout/src/cyberfabric/gears-rust`. If that submodule is not
initialized, or if the checkout does not have the benchmark baseline/fix commits
reachable, preflight fails before any model spend. A standalone gears-rust
checkout with the benchmark history works through the same manifest.

The onboarding starter pack includes `repo-bakeoff`, so a newly onboarded
project sees the repo-history capsule workflow alongside setup, bugfix, PR
refinement, and git operations.

# Corpus Proofing

Corpus Forge admits a candidate only after `internal/corpusproof` independently
records its explicit oracle as RED at `baseline_ref` and GREEN at `fix_ref`.
This makes a corpus a measurement instrument rather than a collection of task
descriptions or worker assertions.

## Contract

The intake boundary is JSON `corpus-case.v1`:

```json
{
  "kind": "corpus-case.v1",
  "id": "example-1",
  "repo": "owner/project",
  "source": "local-git-history",
  "baseline_ref": "abc",
  "fix_ref": "def",
  "ticket": "observable bug report",
  "archetype": "bugfix",
  "oracle": {"command": ["go", "test", "./..."]},
  "provenance": {"adapter": "history", "locator": "commit:abc", "source_digest": "sha256:..."}
}
```

`oracle` is canonicalized and SHA-256 hashed before execution. Candidate input
contains no `verified_red` or `verified_green` fields: admission proof is a
separate receipt with baseline/fix command, exit code, output, and an environment
fingerprint.

## Isolation and rejections

The proof executor accepts injected fixture and oracle-runner dependencies. A
fixture adapter may bridge a synthetic [capsule](capsules.md) or a repo-history
manifest; the executor never performs a network fetch or asks a model. Both
legs must expose the same non-empty environment fingerprint.

## Local repository runtime

`corpusproof.NewRuntime` is the reusable production construction seam for a
story or host that deliberately opts into proofing a local repository. It needs
all of the following; there is no global or fake default:

- a `RepositoryRoot` and, optionally, the expected `RepositoryIdentity`;
- a `FixtureCommands` executor for local Git only; and
- an `OracleSandbox` that reports `NetworkDisabled` and returns the stable
  environment fingerprint for the exact sandbox it executes in. The runtime
  reads that fingerprint itself and checks it again for each oracle leg.

The runtime resolves each declared ref in that configured root, locally clones
it into an owned temporary directory, checks out the resolved commit detached,
then releases both directories after proof. Candidate `repo`, locator, and
oracle data cannot cause a fetch, clone from a URL, or shell evaluation.

Oracle JSON is deliberately small and strict:

```json
{"command":["go","test","./..."],"env":{"GOWORK":"off"}}
```

`command` is direct argv, not shell syntax; unknown JSON fields and malformed
environment keys are rejected. A host connects it explicitly, for example by
passing the constructed executor to `host.CorpusProofHandler(runtime)`. The
host remains fail-closed until that deliberate configuration happens.

The following typed rejections are durable exclusion evidence:

- `missing_oracle` — the candidate cannot be evaluated.
- `already_green` — the baseline oracle exits zero, so it is not a regression.
- `non_reproducible_environment` — fixture opening, execution, command capture,
  or environment equality cannot be established; a failing fix is also rejected.
- `invalid_candidate` — required corpus-case identity or provenance is absent.

Adapters may discover many candidates, but only the separate proof receipt can
admit one to calibration or heldout selection.

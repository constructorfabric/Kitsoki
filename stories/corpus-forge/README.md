# Corpus Forge

Corpus Forge is the reusable, source-neutral admission story for an evaluation
corpus. It receives canonical `corpus-case.v1` candidates, inspects and
selects them deterministically, independently proves RED at `baseline_ref` and
GREEN at `fix_ref`, and freezes a `corpus-receipt.v1` record. It is intended for
any Kitsoki story developer—not only the Kitsoki repository.

Its story logic has no Python, shell, LLM, or network execution path. A
production runtime executes only the declared argv oracle through its explicit
network-denying sandbox; the only execution seam is an injected
`host.corpus.prove` handler backed by a `corpusproof.Executor`, and a missing
executor fails closed. Candidate supplied
`verified_red` and `verified_green` fields are ignored and cannot admit a task.

## Lifecycle

1. **Import proposals.** Use `internal/corpusintake.LoadArenaYAML` for an Arena
   manifest, `LoadExternalBakeoffYAML` for an external bakeoff manifest, or
   `DiscoverLocal(ctx, source, corpusintake.DiscoveryOptions{AllowLive:true})`
   for explicitly opted-in local history. The local adapter deliberately never
   scans without `AllowLive:true`.
2. **Inspect and reject.** Start the story with the canonical candidate objects
   in `source_candidates`; malformed candidates and too-small selections stop
   in **rejected** before proof.
3. **Prove.** Configure `host.CorpusProofHandler(executor)` with a fixture
   opener and oracle runner. The executor records typed RED/GREEN evidence and
   an environment fingerprint. It rejects already-green baselines,
   non-reproducible environments, and malformed cases.
4. **Freeze calibration.** Run with `corpus_role: calibration`, a stable
   `selection_id`, and a reviewable `source_manifest_ref`. Save the receipt.
5. **Freeze heldout.** Run separately with `corpus_role: heldout` and a
   distinct source-manifest reference. Configure a shared receipt registry for
   both sessions; it rejects candidate-ID overlap with calibration *before* a
   heldout receipt can freeze. Do not tune a story or prompt on this receipt;
   use it only for promotion measurements.

The receipt records the role, source reference, selected canonical candidates,
and proof evidence. `internal/corpusreceipt.Registry` accepts only this
contract with complete RED/GREEN evidence. Give it a `FileStore` rooted at an
operator-selected directory to make the registry durable across Studio
sessions. Its default is fail-closed: an unconfigured Studio runtime refuses
`host.corpus.freeze_receipt`; `MemoryStore` is explicit and only appropriate
for deterministic tests.

## Studio route

Studio already exposes the smallest real product surface; no bespoke UI or mock
form is required. In a Studio MCP client, first use `story.validate` and
`story.test` against `stories/corpus-forge`. For an interactive deterministic
drive, open `session.new` with `harness: replay`, seed the same world keys used
by the flow fixture, and provide a host cassette/handler for
`host.corpus.prove`. Then use `session.submit` with `start`, followed by
`accept` through load → select → prove → receipt, and inspect the final receipt
using `session.inspect` or a targeted `session.world` read.

`session.new.initial_world` is the product input contract:

```json
{
  "source_candidates": [{"kind":"corpus-case.v1", "id":"case-1", "...":"canonical candidate fields"}],
  "task_limit": 5,
  "selection_id": "my-story-calibration-2026-07",
  "corpus_role": "calibration",
  "source_manifest_ref": "corpus/candidates.yaml"
}
```

For a production drive, start Studio with an explicit local runtime file rather
than using cassettes. This installs both handlers for every `session.new`:

```yaml
# corpus-runtime.local.yaml — keep it out of version control
schema: corpus-runtime/v1
repository_root: /srv/mirrors/my-story-target
repository_identity: acme/my-story-target
workspace_root: /var/tmp/my-story-corpus-work
receipt_store: /srv/evaluations/my-story/receipts
sandbox:
  # Linux: bubblewrap /usr/bin/bwrap. macOS: sandbox-exec /usr/bin/sandbox-exec.
  kind: sandbox-exec
  binary: /usr/bin/sandbox-exec
```

```sh
go run ./cmd/kitsoki mcp --stories-dir ./stories \
  --corpus-runtime-config /secure/path/corpus-runtime.local.yaml
```

`repository_root` must be a local Git mirror; Corpus Forge never fetches a
candidate-controlled remote. The sandbox is mandatory: Linux `bubblewrap`
receives a fixed `--unshare-net` profile, while macOS `sandbox-exec` receives a
generated deny-by-default Seatbelt profile with no network and writes permitted
only inside each isolated checkout. A missing, non-executable, or unsupported
sandbox aborts Studio startup; there is no unrestricted fallback. The file
store is created at startup and remains shared across Studio processes. Keep it
outside `workspace_root`, which is cleaned after each proof.

A default Studio host intentionally has neither proof executor nor receipt
registry, so it rejects rather than inventing proof or global durability. Use a
flow or host cassette only for deterministic QA; it is not evidence for an
evaluation claim.

## No-LLM verification

```sh
.agents/skills/starlark/tools/validate.sh stories/corpus-forge/scripts -kitsoki
go run ./cmd/kitsoki validate stories/corpus-forge/app.yaml
go run ./cmd/kitsoki test flows stories/corpus-forge/app.yaml
```

The happy flow runs the real Starlark lifecycle with deterministic proof and
freeze stubs. `proof_rejection.yaml` proves an independent rejection cannot
produce a receipt, and `heldout_overlap_rejected.yaml` proves the story routes
a durable-registry overlap refusal to **rejected**. Package tests exercise the
real FileStore across two separate Registry instances.

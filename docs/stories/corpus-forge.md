# Corpus Forge: build a trustworthy evaluation corpus

Use the `corpus-forge` story when a Kitsoki story needs a corpus before a
measurement, comparison, or goal-seeking loop. It creates a reviewable boundary
between a source of proposed cases and a frozen corpus that can support a real
claim. It is not an agent benchmark runner and it does not call a model.

The story accepts only source-neutral `corpus-case.v1` records. The public Go
intake adapters turn Arena manifests, external bakeoff manifests, and an
explicitly opted-in local-history inspector into that contract. The story does
not know which adapter produced a case; it preserves each case's provenance for
review. Local history discovery must be explicitly enabled, so a normal
developer or CI flow never scans a repository by surprise.

Each admitted case needs independent evidence that the chosen oracle fails at
its baseline and succeeds at its fix. That evidence is supplied by an injected
`corpusproof.Executor`, not by a source manifest or worker self-report. The
default `host.corpus.prove` registration has no executor and rejects. This is a
deliberate fail-closed boundary: a Studio session cannot create a receipt merely
because it has a plausible task description.

## Freeze protocol

Start with a calibration receipt. Set a stable `selection_id`, `corpus_role:
calibration`, and `source_manifest_ref` identifying the reviewed input. Corpus
Forge ID-sorts the candidates, applies `task_limit`, proves every selected
candidate, and emits `corpus-receipt.v1`.

For the promotion corpus, start a new run with `corpus_role: heldout` and a
distinct source-manifest reference. Keep both receipts with the measurement
result. A production Studio runtime uses one explicitly configured durable
receipt registry, which rejects candidate-ID overlap across independent
calibration and heldout sessions before the heldout receipt can freeze. The
comparison runner must still consume the named frozen receipts, not mutable
session state.
Calibration may tune the story; heldout may only evaluate the frozen choice.

## Studio MCP dogfood

The developer-facing surface is the existing Studio story/session API rather
than a parallel UI. Seed `session.new.initial_world` with the six keys described
in [the story README](../../stories/corpus-forge/README.md#studio-route), then
submit the deterministic `start`/`accept` intents. A replay host cassette gives
a no-cost developer walkthrough; a real corpus freeze requires a runtime that
has registered `host.CorpusProofHandler` with the repository-specific fixture
opener and oracle runner.

Before a live measurement, run these no-LLM checks through the same engine
Studio uses:

```sh
go run ./cmd/kitsoki validate stories/corpus-forge/app.yaml
go run ./cmd/kitsoki test flows stories/corpus-forge/app.yaml
```

The flow suite exercises a calibration receipt, proof rejection, and a heldout
receipt. It intentionally does not prove a benchmark result or invoke an LLM.

The hermetic Studio/MCP dogfood test uses
`capsules/corpus-forge-red-green`: its `HEAD~1` ref makes the argv-only
`/bin/test -f fixed.txt` oracle fail, while `HEAD` makes it pass. On macOS,
where `/usr/bin/sandbox-exec` is available, run it with:

```sh
go test ./cmd/kitsoki -run TestCorpusForgeDogfoodOverMCP -count=1
```

It uses a generated `corpus-runtime/v1` config, real local-Git fixture opening,
the platform network-denying sandbox, and FileStore. It does not use a flow
stub, network, or LLM. The second MCP session deliberately attempts to freeze
the calibration candidate as heldout and must be rejected from the durable
receipt registry.

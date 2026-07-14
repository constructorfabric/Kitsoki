# Roadmap ledger workflow

Use `.artifacts/roadmap/progress.yaml` as the local, YAML source of truth while
roadmap work is moving. It is not a committed product artifact; it is the
machine-readable work log that keeps ad-hoc agents and Kitsoki stories aligned
until the durable docs, proposal queue, feature catalog, product site, demos,
and roadmap deck are updated.

The schema is `kitsoki-roadmap-ledger/v1`. A ledger has two parts:

- `items`: the current horizon/status for work we know about.
- `events`: idempotent facts that changed an item. Event ids are derived from
  the event payload, so replaying the same story turn or re-running the same
  ad-hoc command does not duplicate the log.

## Required loop

1. When a proposal is introduced, write a `proposal_published` event.
2. When implementation starts, write an `implementation_started` or equivalent
   event and keep the same `item_id`.
3. Before calling work `done`, update all coverage checks:
   `proposal`, `docs`, `feature_yaml`, `product_site`, and `rrweb_demo`.
   Each must be `done`, `blocked`, or `not_applicable`; unresolved states are
   `pending`, `missing`, or `stale`.
4. Run `kitsoki roadmap ledger check`.
5. Update the durable surfaces: proposal status or deletion, narrative docs,
   feature YAML/product-site media, rrweb demo bindings, and the roadmap deck.

The key discipline is that "done" is not just code merged. It means the
implementation, docs, feature catalog/product-site surfaces, and demo evidence
have an explicit state.

## Ad-hoc agents

Ad-hoc Codex/Claude agents should write the same ledger the stories write:

```sh
go run ./cmd/kitsoki roadmap ledger event \
  --ledger .artifacts/roadmap/progress.yaml \
  --event proposal_published \
  --item-id proposal/roadmap-ledger \
  --kind proposal \
  --title "Roadmap ledger" \
  --status proposed \
  --horizon next \
  --proposal docs/proposals/roadmap-portfolio-work.md \
  --check proposal=done \
  --check docs=pending \
  --check feature_yaml=pending \
  --check product_site=pending \
  --check rrweb_demo=pending \
  --source ad-hoc
```

For a completed item:

```sh
go run ./cmd/kitsoki roadmap ledger event \
  --ledger .artifacts/roadmap/progress.yaml \
  --event shipped \
  --item-id proposal/roadmap-ledger \
  --kind proposal \
  --title "Roadmap ledger" \
  --status done \
  --horizon done \
  --proposal docs/proposals/roadmap-portfolio-work.md \
  --doc docs/workflows/roadmap-ledger.md \
  --feature-yaml features/<id>.yaml \
  --product-site tools/site/src/guide/<page>.md \
  --rrweb-demo docs/decks/assets/<deck>/<clip>.rrweb.json \
  --check proposal=done \
  --check docs=done \
  --check feature_yaml=done \
  --check product_site=done \
  --check rrweb_demo=done \
  --source ad-hoc
```

Use `not_applicable` instead of omitting a check when the work genuinely should
not have that surface. Use `blocked` when a human decision, spend gate, external
system, or missing substrate prevents the update.

## Story integration

`stories/dev-story` writes the ledger automatically at the boundaries it owns:

- proposal publish writes `proposal_published`;
- entering the implementation import writes `implementation_started`;
- returning from the implementation import writes `implementation_completed`.

The publish event records the proposal path, feature-ticket path when one was
filed, `proposal=done`, and the durable-surface checks as `pending`. The
implementation-start event moves the item to `in_progress`. The implementation
completion event records `partial`, not `done`, and includes the child story's
latest implementation progress artifact as a ref. That is deliberate: code can
be merged while docs, feature YAML, product-site copy, rrweb media, or roadmap
deck coverage are still pending.

This is not perfect dev-story coverage. PRD publication, docs publishing,
deploy/observability loops, demo-video production, deliver fan-out completion,
and ad-hoc workbench plans still need their own ledger producers when they
change the roadmap/product surface. The invariant is narrower and deterministic:
proposal authoring and the direct implementation path cannot happen through
dev-story without leaving a ledger event trail.

Artifact-driven restart is connected at the boundary rather than replacing
story-local artifacts. Implementation keeps writing its per-phase progress YAML
for checkpoint/restart purposes; dev-story projects the last progress artifact
out when the import exits and records it on the global ledger. The global ledger
is the cross-story rollup, while each story's own progress YAML remains the
room-level restart checkpoint.

## Validation

```sh
go run ./cmd/kitsoki roadmap ledger check \
  --ledger .artifacts/roadmap/progress.yaml \
  --repo-root . \
  --strict
```

The checker validates schema, ids, statuses, event references, and completion
coverage. A `done` item fails if any required check is still unresolved or if a
`done` docs/feature/product-site/rrweb path is missing.

Use `--strict` in gates. Strict mode still accepts an empty local ledger, but
once items exist it requires event history, the standard coverage checks for
`in_progress`, `partial`, and `done` items, and at least one proposal/doc/surface
path or ref.

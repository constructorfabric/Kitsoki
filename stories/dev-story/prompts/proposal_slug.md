# Name the proposal — a short kebab-case slug

You are naming a new kitsoki proposal. Turn the idea below into a **short,
meaningful slug** — the kind of name a maintainer would give the file.

The idea:

> {{ args.idea }}

## What makes a good slug

- **2–5 words**, kebab-case, lowercase (`local-model-oracle`,
  `per-session-workdir`, `jsonl-trace-export`).
- Captures the **essence** — the thing being proposed — not the whole
  sentence. A raw sentence makes a terrible slug.
- Matches the house style of the existing proposals.

## Avoid collisions

Look at the existing names (use `Glob`/`Read`) and pick one that is **not
already taken**:

- `docs/proposals/*.md` — accepted proposals.
- `docs/proposals/.workspace/*/` — proposals being drafted right now.

If your first choice is taken, pick a distinct, still-meaningful
alternative. (Uniqueness is also enforced downstream, but a clean,
non-colliding name is better than a `-2` suffix.)

## Output

Submit a `slug` object (see `schemas/slug.json`): `{ slug, rationale }`.

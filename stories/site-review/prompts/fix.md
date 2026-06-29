# Close the catalog / site gaps

The site-review agent decided the feature catalog / product site is out
of sync with what shipped at HEAD `{{ args.commit_sha }}`. Your job is
to **close the gaps** — concrete, minimal, surgical changes — so the
catalog and site once again advertise what the code does.

## How to read the inputs

You receive the full verdict below. Treat `gaps[]` as your worklist:
one entry per `(kind, target)`. Each entry has 1+ `evidence[]` records
that explain *what shipped* and *why the target is a gap*. Some entries
also list a `recommended_actions` hint in the parallel array of the same
name (same order as `gaps`).

### Verdict summary

> {{ args.verdict_summary }}

### Gap listing (worklist)

{% for g in args.gaps %}**[{{ g.kind }}] `{{ g.target }}`**{% if g.detail %} — {{ g.detail }}{% endif %}

{% for e in g.evidence %}- [{{ e.commit }}{% if e.source %} · `{{ e.source }}`{% endif %}] **{{ e.change }}** → {{ e.reason }}
{% endfor %}
{% empty %}(no gaps — something is wrong; abort and report){% endfor %}

### Recommended actions (hints, same order as gaps[])

{% for a in args.recommended_actions %}- {{ a }}
{% empty %}(none){% endfor %}

### Full verdict JSON (for reference)

```json
{{ args.verdict_json }}
```

## What to do

1. For each `gaps[]` entry, by `kind`:
   - **missing-catalog-entry / missing-demo**: author a
     `features/<target>.yaml` entry. Follow `features/AGENTS.md` and copy
     the shape of an existing entry (e.g. `features/operator-ask.yaml`).
     Required: `id` (== filename stem), a valid `kind`, `title`,
     `tagline`, `summary`. For a promoted feature add `promo:` and a
     `demo:` binding with a `posterStep`. **Do NOT fabricate a demo
     recording** — if no deterministic flow/cassette + Playwright spec
     exists yet, point `demo.spec` at the intended path and record the
     entry in `unresolved[]` (the demo still needs authoring) rather than
     claiming it is done.
   - **site-inconsistency / stale-doc**: `Read` the cited file (and the
     `source:` / commit it drifted from), then apply the minimal edit so
     the site matches reality. For guide copy under `tools/site/src/guide/**`,
     prefer editing the `docs/**` source it is generated from (the site
     copies it via `make site`); note that in the file's `change:`.
2. Do NOT broaden scope. Touch only what the verdict cited.
3. Do NOT commit. Leave changes uncommitted in the working tree. The
   operator will run `make features` (to regenerate manifests) and
   review before staging.
4. If an entry is genuinely ambiguous or needs an artifact you cannot
   produce (e.g. a recorded demo video), skip the edit and record it in
   `unresolved[]` with a one-sentence reason.
5. When done, call `submit` with the `site_fix_artifact` shape:
   - `applied`: `true` iff every gap entry was addressed.
   - `summary`: one paragraph naming each touched file.
   - `files_changed[]`: one row per file you created/edited, each with a
     one-sentence `change:`.
   - `unresolved[]`: rows for skipped entries (empty when applied=true).
   - `blockers[]`: only when applied=false (environment problem).

The submit call comes LAST, after all edits are in the worktree.

## Example submit payload (shape only)

```json
{
  "applied": true,
  "summary": "Authored features/github-agent.yaml (promoted feature, demo spec stubbed) and dropped the transient docs/proposals link from features/dynamic-workflows.yaml.",
  "files_changed": [
    { "path": "features/github-agent.yaml", "change": "New catalog entry for the GitHub agent; promoted, demo spec points at the intended tests/playwright path." },
    { "path": "features/dynamic-workflows.yaml", "change": "Removed docs/proposals/process-design.md link (not in the site allowlist)." }
  ],
  "unresolved": [
    { "target": "github-agent", "reason": "demo recording (flow + cassette + Playwright spec) still needs authoring before the card shows a video." }
  ],
  "blockers": []
}
```

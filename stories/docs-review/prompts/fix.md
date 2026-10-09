# Apply the documentation fixes

The docs-review agent decided this repo's docs are out of sync with
the code at HEAD `{{ args.commit_sha }}`. Your job is to **apply** the
fixes — concrete, minimal, surgical edits — so the docs once again
reflect what the code does.

## How to read the inputs

You receive the full verdict below. Treat `stale_docs[]` as your
worklist: one entry per `(path, lines)` to fix. Each entry has 1+
`invalidations[]` that explain *what changed in the code* and *why the
doc section is now wrong*. Some entries also list a `recommended_actions`
hint in the parallel array of the same name (same order as `stale_docs`).

### Verdict summary

> {{ args.verdict_summary }}

### Stale doc listing (worklist)

{% for d in args.stale_docs %}**`{{ d.path }}` : lines `{{ d.lines }}`**{% if d.anchor %} — {{ d.anchor }}{% endif %}

{% for inv in d.invalidations %}- [{{ inv.commit }}{% if inv.source %} · `{{ inv.source }}`{% endif %}] **{{ inv.change }}** → {{ inv.reason }}
{% endfor %}
{% empty %}(no stale rows — something is wrong; abort and report){% endfor %}

### Recommended actions (hints, same order as stale_docs[])

{% for a in args.recommended_actions %}- {{ a }}
{% empty %}(none){% endfor %}

### Full verdict JSON (for reference)

```json
{{ args.verdict_json }}
```

## What to do

1. For each `stale_docs[]` entry:
   - `Read` the cited doc at the cited line range to see the current text.
   - `Read` the cited `source:` file (or run `git show <commit>`) to
     confirm what the code actually does now.
   - Decide the minimal edit that brings the doc back in sync with the
     code — usually a few lines, occasionally a section rewrite.
   - Apply the edit with `Edit` (preferred — preserves surrounding text)
     or `Write` (only if creating a new doc / rewriting from scratch).
2. Do NOT broaden scope. If an entry says "§4.3 lines 110-117", don't
   rewrite §4.4 too. Touch only what the verdict cited; leave the rest.
3. Do NOT commit. Leave changes uncommitted in the working tree.
4. If an entry is genuinely ambiguous (the verdict contradicts itself,
   the cited line range no longer exists, the source file is gone),
   skip it and record it in `unresolved[]` with a one-sentence reason.
5. When done, call `submit` with the `docs_fix_artifact` shape:
   - `applied`: `true` iff every stale_docs entry was addressed.
   - Create `.artifacts/docs-review/{{ args.commit_sha }}-fix-report.md` after
     the first useful diagnosis and update it during each edit. It contains the
     detailed per-document rationale and evidence; `report_path` returns it.
   - `summary`: one compact checkpoint sentence naming the result and report.
   - `files_changed[]`: one row per file you edited, each with a
     one-sentence `change:`.
   - `unresolved[]`: rows for skipped entries (empty when applied=true).
   - `blockers[]`: only when applied=false (environment problem).

The submit call comes LAST, after all edits are in the worktree.

## Example submit payload (shape only)

```json
{
  "applied": true,
  "summary": "Updated two stale docs; detailed evidence is in the repair report.",
  "report_path": ".artifacts/docs-review/abc123-fix-report.md",
  "files_changed": [
    { "path": "docs/embedded/llm-guide.md", "change": "Replaced lines 110-117: --trace is no longer required; auto-trace landing path documented." },
    { "path": "CLAUDE.md", "change": "Updated tracing one-liner: 'pass --trace' → 'every run writes a trace by default'." }
  ],
  "unresolved": [],
  "blockers": []
}
```

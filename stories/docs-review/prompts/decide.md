# Docs review — is the documentation up to date with HEAD?

You are auditing whether this repository's documentation accurately
describes the code at HEAD. The output is rendered as a **listing**:
one row per `(doc path, line range)`, joined to one or more
**invalidation records** that each cite a specific code/doc change.
Write with that shape in mind — concise rows, not narrative.

## Context

- **Commit (HEAD)**: `{{ args.commit_sha }}`
- **Total commits in history**: `{{ args.commit_count }}`
- **Review mode**: `{{ args.review_mode }}`

{% if args.review_mode == "baseline" %}
You are running a **baseline audit**: there is no recent-commit window
or working-tree diff to focus on. Sweep the *whole repo* and find docs
that contradict, omit, or describe code that no longer exists. This
mode is for seeding a fresh project audit, catching long-accumulated
drift, or auditing a freshly-cloned tree where commit history is not
informative.

### Doc inventory

Files under the authoritative doc roots that exist right now:

```
{{ args.doc_inventory }}
```

### Code surface inventory

Top-level directories under `cmd/` and `internal/` — read these to
sample the actual surface the docs are supposed to describe:

```
{{ args.code_inventory }}
```

There is no `commits[]` lookup table to populate from a diff. Cite
invalidations with `commit: "structural"`. Sample several authoritative
docs against their corresponding code surfaces; do not enumerate
everything.
{% else %}
### Recent commits

```
{{ args.recent_log }}
```

### Recent diffstat

This is `git diff --stat HEAD~N..HEAD` where N is min(20, total
commits − 1). For a single-commit repo it is `git show --stat HEAD`
(the contents of the initial commit).

```
{{ args.recent_changes }}
```

### Uncommitted working-tree changes (vs HEAD)

```
{{ args.working_tree_diff }}
```
{% endif %}

## How to audit

Read-only tools: `Read`, `Grep`, `Glob`, and `Bash` under the
read-only profile. Use `git show <sha>` / `git log -p <path>` to
pull the actual change for any commit you want to cite. Use `Read`
with line ranges to lock down exact `lines:` for each stale section.

1. **Pick the in-scope commits.** Walk the recent log + diffstat and
   keep the commits that *change observable behavior or surfaces*
   (CLI flags, defaults, host calls, file layout, schemas, prompts).
   Skip pure refactors / formatting / dependency bumps unless they
   alter a documented contract. Treat working-tree changes as if they
   were the next commit; cite them as `commit: "working-tree"`.
2. **For each in-scope commit**, write one `commits[]` row: `sha`,
   `subject`, and a one-sentence `change` describing the actual
   behavior change (not a restatement of the subject).
3. **Find the doc sections that are now wrong.** For each one, open
   the doc, identify the exact line range that is stale (e.g.
   `lines: "110-117"`, or `"650"` for a single line), and write one
   `stale_docs[]` row keyed by `(path, lines)`. Add the optional
   `anchor:` when a section heading helps the reader (e.g.
   `"§4.3 Iterate on an app with full tracing"`).
4. **List every change that invalidates that section.** Inside the
   row, `invalidations[]` is a list — one record per change.
   Multiple commits can each contribute to the same section going
   stale; record them separately. Each record needs:
     - `commit`: sha from `commits[]`, or `"working-tree"`, or
       `"structural"` (no specific commit; structural-audit case).
     - `source`: optional file that drives the drift (code path or
       another doc) — cite when it sharpens the link.
     - `change`: what changed at the behavior/contract level.
     - `reason`: why the doc section is now wrong, quoting or
       paraphrasing its current claim.
5. **Empty diffstat / shallow repo / dependency-only?** Do a
   structural audit instead: compare `stories/*/README.md` against
   each story's `app.yaml`, walk `docs/architecture/hosts.md` against
   `internal/host/handlers.go`, skim `README.md` / `CLAUDE.md` for
   outdated paths or removed features. `commits[]` may be empty;
   cite `commit: "structural"` on the invalidation records.

Authoritative doc roots: `README.md`, `CLAUDE.md`, `docs/**`,
`stories/*/README.md`.

## Output

**Call the `submit` tool** with a JSON object matching the
`docs_review_verdict` schema. Do not paste the verdict as YAML or as a
fenced code block in your response — the host only reads the
`submit` tool payload. Free-text in your final assistant message is
discarded.

### Example submit payload (shape only — fill with real findings)

```json
{
  "decision": "needs_update",
  "summary": "Commit a0a814d made --trace opt-out; docs/embedded/llm-guide.md §4.3 still implies it is opt-in.",
  "confidence": 0.7,
  "commits": [
    { "sha": "a0a814d", "subject": "Auto-write JSONL trace…", "change": "kitsoki run now writes a JSONL trace to .kitsoki/sessions/ by default; --trace overrides." }
  ],
  "stale_docs": [
    {
      "path": "docs/embedded/llm-guide.md",
      "lines": "110-117",
      "anchor": "§4.3 Iterate on an app with full tracing",
      "invalidations": [
        {
          "commit": "a0a814d",
          "source": "cmd/kitsoki/trace.go",
          "change": "auto-trace default added; --trace is no longer required",
          "reason": "the section still tells the reader to pass --trace explicitly to get a JSONL file"
        }
      ]
    }
  ],
  "recommended_actions": ["rewrite §4.3 to lead with the auto-trace default"]
}
```

### Required fields

- `decision`: `"needs_update"` or `"up_to_date"`.
- `summary`: one paragraph; cites file paths, line ranges, and
  commit short-shas.
- `confidence`: 0..1. Prefer 0.5–0.6 with `needs_update` when in
  doubt so a human takes a second look.
- `commits`: every in-scope commit. **Required even when
  `up_to_date`** — it shows your work. Empty only for structural
  audits.
- `stale_docs`: `[]` when up-to-date. Otherwise one row per
  `(path, lines)` with `invalidations[]` (≥ 1 record).
- `recommended_actions`: short directive strings, ideally one per
  stale row in the same order.

Do not narrate findings only in `summary` — anything actionable must
appear as a `stale_docs[]` row so it shows up in the listing.

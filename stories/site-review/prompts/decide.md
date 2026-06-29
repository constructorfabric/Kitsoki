# Site review — does the catalog + product site advertise what shipped?

You are auditing whether this repository's **feature catalog**
(`features/*.yaml`) and **product site** (`tools/site/`) still reflect
the capabilities that have actually shipped. The output is rendered as
a **listing**: one row per gap, joined to one or more **evidence
records** that each cite a specific commit / source. Write with that
shape in mind — concise rows, not narrative.

## Context

- **Commit (HEAD)**: `{{ args.commit_sha }}`
- **Total commits in history**: `{{ args.commit_count }}`
- **Review mode**: `{{ args.review_mode }}`

### Current feature catalog (ids — one per `features/<id>.yaml`)

```
{{ args.feature_catalog }}
```

### Product-site pages (`tools/site/src/**.md`)

```
{{ args.site_pages }}
```

{% if args.review_mode == "baseline" %}
You are running a **baseline audit**: there is no recent-commit window
or working-tree diff to focus on. Sweep the *whole* catalog + site and
find capabilities that exist in the repo (stories, host calls, CLI
surfaces) with no catalog entry, cataloged features missing required
pieces, and site copy that contradicts the code. This mode is for
catching long-accumulated drift.

There is no `commits[]` lookup table to populate from a diff. Cite
evidence with `commit: "structural"`.
{% else %}
### Recent commits

```
{{ args.recent_log }}
```

### Recent diffstat

This is `git diff --stat HEAD~N..HEAD` where N is min(20, total
commits − 1). For a single-commit repo it is `git show --stat HEAD`.

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
read-only profile. Use `git show <sha>` / `git log -p <path>` to pull
the actual change for any commit you want to cite. Authoritative
surfaces:

- **Feature catalog**: `features/*.yaml` (one entry per feature; see
  `features/AGENTS.md` for the required shape and the completeness
  invariants enforced by `pnpm features:check`).
- **Product site**: `tools/site/src/index.md` (landing), `src/features/`
  (the grid + per-feature pages), `src/guide/**` (copied from `docs/**`),
  and the docs allowlist `tools/site/docs-manifest.json`.

1. **Pick the in-scope commits.** Walk the recent log + diffstat and
   keep the commits that *delivered a user-visible capability* — a new
   story, a new host call, a new CLI/web surface, a behavior default.
   Skip pure refactors / formatting / dependency bumps and pure
   deck/media tweaks. Treat working-tree changes as the next commit;
   cite them as `commit: "working-tree"`.
2. **For each in-scope commit**, write one `commits[]` row: `sha`,
   `subject`, and a one-sentence `capability` describing what a user can
   now do (not a restatement of the subject).
3. **Find the gaps.** For each shipped capability, check whether the
   catalog/site advertises it, and emit a `gaps[]` row keyed by
   `(kind, target)`:
   - `missing-catalog-entry` — the capability has no `features/<id>.yaml`.
     `target` is the feature id you'd create.
   - `missing-demo` — a cataloged feature has no recordable demo binding.
   - `site-inconsistency` — catalog/site metadata disagrees with reality
     (a `docs:` link outside the allowlist, a promoted feature missing
     required pieces, dead copy on the landing/index page). `target` is
     the file or feature id.
   - `stale-doc` — published guide copy under `tools/site/src/**` (or the
     `docs/**` it is generated from) contradicts the code. `target` is the
     doc path; add `lines` when localized.
4. **List the evidence.** Inside each row, `evidence[]` is a list — one
   record per change. Each needs `commit` (sha / `working-tree` /
   `structural`), optional `source`, `change` (what shipped), and
   `reason` (why the target is a gap — quote the catalog/site's current
   state).
5. **Nothing stale?** Emit `decision: "up_to_date"` with `gaps: []` and
   still populate `commits[]` to show your work.

## Output

**Call the `submit` tool** with a JSON object matching the
`site_review_verdict` schema. Do not paste the verdict as YAML or as a
fenced code block in your response — the host only reads the `submit`
tool payload.

### Example submit payload (shape only — fill with real findings)

```json
{
  "decision": "needs_update",
  "summary": "Commit b6937f1 shipped the GitHub agent (report-bug → GitHub, evidence as release assets) but there is no features/github-agent.yaml, so the capability never reaches the promo site.",
  "confidence": 0.7,
  "commits": [
    { "sha": "b6937f1", "subject": "feat(host): upload bug evidence to GitHub as release assets", "capability": "an agent can file a bug to GitHub and attach captured evidence as a release asset." }
  ],
  "gaps": [
    {
      "kind": "missing-catalog-entry",
      "target": "github-agent",
      "detail": "GitHub agent capability is uncataloged",
      "evidence": [
        {
          "commit": "b6937f1",
          "source": "internal/host/github.go",
          "change": "added GitHub issue filing + release-asset upload",
          "reason": "no features/github-agent.yaml exists, so the promo grid has no card for it"
        }
      ]
    }
  ],
  "recommended_actions": ["author features/github-agent.yaml (kind: feature, promoted) with a no-LLM flow + cassette + video spec"]
}
```

### Required fields

- `decision`: `"needs_update"` or `"up_to_date"`.
- `summary`: one paragraph; cites feature ids, file paths, and commit
  short-shas.
- `confidence`: 0..1. Prefer 0.5–0.6 with `needs_update` when in doubt.
- `commits`: every in-scope commit. **Required even when
  `up_to_date`** — it shows your work. Empty only for structural audits.
- `gaps`: `[]` when up-to-date. Otherwise one row per `(kind, target)`
  with `evidence[]` (≥ 1 record).
- `recommended_actions`: short directive strings, ideally one per gap row
  in the same order.

Do not narrate findings only in `summary` — anything actionable must
appear as a `gaps[]` row so it shows up in the listing.

# Apply Minimal Fix — Group: {{ args.group_title }}

You are a front-end engineer fixing a UI accessibility/layout issue in the kitsoki web UI.

## Strict constraints

1. **Edit only files under `{{ args.src_root }}`** — the SPA source directory.
   Do NOT touch tests, build config, `*.spec.*`, `*.test.*`, `package.json`, `vite.config.*`,
   or any file outside `{{ args.src_root }}`.
2. **Fix exactly this group.** If you notice an adjacent issue that belongs to a different group,
   record it in `notes` but do NOT apply it now.
3. **Minimal change.** Target the root cause; do not refactor surrounding code or add features.
4. After your edits, the acceptance command `vue-tsc --noEmit` (run in `tools/runstatus`) must pass.

## Group

**ID:** {{ args.group_id }}
**Title:** {{ args.group_title }}
**Pattern:** {{ args.pattern }}
**Root cause:** {{ args.root_cause }}
**Recommendation:** {{ args.recommendation }}
**Member finding IDs:** {{ args.member_ids }}

{% if args.refine_feedback %}
## Operator refinement feedback (cycle {{ args.group_cycle }})

{{ args.refine_feedback }}

Apply this feedback strictly. It is a binding directive. Before submitting, confirm each point
in the feedback has been addressed.
{% endif %}

## Before frames

The following frames were captured before any fix. Use them to confirm the problem location.
Frames are in `{{ args.frames_dir }}`:

{% for frame in args.before_frames %}
- `{{ frame }}`
{% endfor %}

## Task

1. Read the implicated component(s) and style(s) in `{{ args.src_root }}`.
2. Apply the minimal fix for this group's root cause.
3. Confirm the fix is syntactically correct (TypeScript/Vue — no obvious errors).
4. Submit a `fix_verdict` JSON with:
   - `applied`: true if the fix was applied
   - `files`: list of modified file paths (relative to repo root)
   - `summary`: one paragraph describing what was changed and why it clears the findings
   - `diff_excerpt`: key lines from the diff (not the full diff — 5–15 lines)
   - `notes`: any adjacent issues noticed but NOT fixed (empty string if none)

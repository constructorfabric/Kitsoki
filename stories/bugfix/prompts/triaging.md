# Triage a bug — produce a standardized verdict (no fix)

You are triaging bug **{{ args.ticket_id }}** — *{{ args.ticket_title }}* against
the current working tree at `{{ args.workdir }}`.

Ticket source mode: **{{ args.ticket_source_mode }}**
{% if args.ticket_source_ref %}Ticket source ref: `{{ args.ticket_source_ref }}`{% endif %}
{% if args.ticket_url %}Ticket URL: {{ args.ticket_url }}{% endif %}
{% if args.ticket_repo %}Ticket repo: `{{ args.ticket_repo }}`{% endif %}
{% if args.thread %}Thread: `{{ args.thread }}`{% endif %}

This is **triage only**. Do NOT fix anything, do NOT write files, do NOT create
a branch. Your single job: determine whether the reported defect **still exists
in the code right now**, and emit a standardized verdict with concrete code
evidence.

## Ticket source contract

The caller must choose exactly one source mode. Do not silently switch modes.

- **local** — the bug report is a local markdown ticket. Read the concrete local
  file named by `ticket_source_ref` when present; otherwise read `thread` when it
  is a markdown/path value; otherwise read `issues/bugs/{{ args.ticket_id }}.md`.
- **remote** — the bug report is a remote tracker issue. Use the supplied
  `ticket_body` below plus `ticket_title`, `ticket_url`, and `ticket_repo`.
  Do **not** search `issues/bugs/`, `.context`, traces, or unrelated local notes
  to reconstruct the report. If `ticket_body` is empty, fall back to
  `ticket_title` and `ticket_url` only; return `UNCLEAR` if those are
  insufficient and say the remote issue body could not be fetched.
- **freeform** — the bug report is an inline operator complaint, not a filed
  issue. Use the supplied `ticket_body` below plus `ticket_title`. Do **not**
  require `issues/bugs/{{ args.ticket_id }}.md` to exist. If the complaint is
  too vague to map to specific code, return `UNCLEAR` and say what guidance is
  missing.
- Any other mode is invalid. Return `UNCLEAR` and cite the invalid mode.

{% if args.ticket_source_mode == "remote" || args.ticket_source_mode == "freeform" %}
## Ticket body

```markdown
{{ args.ticket_body }}
```
{% endif %}

## How to triage

1. Read the bug report from the selected source above — especially the "Steps to
   reproduce", "Files involved", and any "Proposed/Suggested fix" section. Note
   the specific `file:line`, function names, and behaviors it cites.
2. Open those exact files/functions in the CURRENT tree. Has the cited buggy
   line/behavior changed? Does the proposed fix already appear to be applied?
3. Search for a regression test asserting the fixed behavior (`grep` for the
   function name, the bug id, or a `Test…` that pins it). A green regression
   test is strong evidence of ALREADY-FIXED.
4. If the bug frontmatter says `status: fixed|resolved`, still VERIFY in code —
   do not trust the flag alone.

{% if args.refine_feedback %}## ⚠ Operator refinement directive

The previous verdict was rejected. This feedback is a **binding directive** that
overrides defaults where they conflict:

> {{ args.refine_feedback }}

Address each point before submitting.

---
{% endif %}

## Verdict vocabulary (pick exactly one)

- **ALREADY-FIXED** — the cited defect no longer exists; the fix (or a
  regression test) is in the tree. Cite the function/test that proves it.
- **STILL-LIVE** — the buggy code/behavior is still present. Cite the line that
  still reads the buggy way.
- **PARTIAL** — some sites fixed, others remain. Cite both.
- **UNCLEAR** — you cannot determine from the code alone (needs a human or a
  live repro). Say what you'd need.

## Output

Submit a `triage_verdict` (see `schemas/triage_verdict.json`):

- `verdict` — one of the four above.
- `confidence` — 0..1, honest given the evidence you actually examined.
- `summary_title` — one-line headline, e.g. `ALREADY-FIXED — severity sort landed in localfiles_ticket.go`.
- `evidence` — **the load-bearing field.** Cite specific code: the
  `file:line` / function / regression test you opened and what you found there
  (the buggy line still reads X; function Y now does Z; `TestW` asserts the fix).
  Prose without a code citation is a failed triage.
- `summary_markdown` — the reasoning trail a human reviewer reads.
- `suggested_action` — e.g. `close as fixed`, `drive the full bugfix pipeline`,
  `needs human repro`, `fix remaining site in <file>`.
- `fixed_in_ref` — when ALREADY-FIXED, the commit/function/test that proves it (or null).
- `involved_components` — the files/modules you inspected with what you found.

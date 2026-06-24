# Identify UI Fix Patterns

You are a senior front-end engineer reviewing a UI audit of the kitsoki web application.
You have a set of de-duplicated findings from a DOM-geometry + axe accessibility + vision audit.
Your task: cluster these findings into a **ranked list of groups**, where each group is exactly
**one code change** (one root cause, one fix).

## Why grouping matters

The raw audit has {{ args.total_deduped }} de-duped findings but most are symptoms of the same
3–5 root causes. Fixing the root cause once clears all its symptoms. Do not propose 371 separate
fixes — propose the dozen-ish patterns.

## Inputs

De-duplicated findings ({{ args.total_deduped }} items, severity ≥ {{ args.severity_floor }}):

```json
{{ args.findings_json }}
```

{% if args.review_feedback %}
## Operator guidance for this review pass

{{ args.review_feedback }}

Apply this guidance strictly. If told to split a group, produce separate groups. If told a
pattern is wrong, revise it.
{% endif %}

## Clustering rules

1. **One group = one root cause.** If fixing file X.vue line N would clear multiple findings
   (e.g. a `select` missing `aria-label` across every viewport and step), that is ONE group.
2. **Rank by leverage.** A finding that blocks the most visible surface at the smallest viewport
   ranks highest. The mobile tour-popover overlap (blocks whole page) is priority 1.
3. **Keep genuinely different problems separate.** A font-size floor on trace chips and a missing
   label on a filter control are two different root causes even if both are on `iv-*` components.
4. **Use the check and selector to identify patterns.** Findings sharing `(source, check,
   selector)` or `(source, check)` with the same component context are strong candidates to group.
5. **Severity of the group = worst severity of its members.**

{% block spec_project_context %}{% endblock %}

## Output

Emit a `group_set` JSON object matching the required schema.

Each group needs:
- `id`: short slug (e.g. `tour-popover-mobile-overlap`, `tap-target-trace-chips`)
- `title`: concise human title (≤ 60 chars)
- `pattern`: one sentence describing the repeating problem
- `root_cause`: the single code-level root cause — name the file/component/style property
- `severity`: worst severity of member findings
- `member_ids`: the `id` field values from the input findings that belong to this group
- `surfaces`: union of surfaces across members
- `viewports`: union of viewports across members
- `before_frames`: union of `frames` across members
- `recommendation`: concrete, actionable fix — name the CSS property, aria attribute, or Vue
  component that needs changing

Rank groups from most-leveraged (biggest visible win, smallest viewport) to least.

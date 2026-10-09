{% comment %}
Read-only review gate. Rendered for the `reviewer` agent after the deterministic
test command is green. The reviewer has Read/Grep/Glob/Bash in read-only mode.
{% endcomment %}
# Review the green test-fix diff

The deterministic command is now green:

```sh
{{ args.test_cmd }}
```

Review the repository state in read-only mode and decide whether this green is
acceptable. Inspect the current diff and the files it touches. Use read-only
commands such as `git status --short`, `git diff --stat`, and `git diff`.

## Context

- Fix cycle: **{{ args.cycle }}** of **{{ args.max_cycles }}**
- Test command output:

```
{{ args.test_stdout }}
```

- Fixer artifact:

```json
{{ args.fix_artifact }}
```

{% if args.review_feedback %}
- Prior review feedback the fixer was asked to address:

```
{{ args.review_feedback }}
```
{% endif %}

## Pass criteria

Set `pass: true` only when all of these are true:

1. The command is green.
2. The diff preserves existing functionality; it does not remove, narrow, or
   regress behavior that already existed unless the tests and surrounding code
   clearly prove that behavior was wrong.
3. The diff preserves intended new work already present in the tree; it does not
   delete or bypass in-progress functionality just to get green.
4. Tests were not weakened to get green. Fail if tests were deleted, skipped,
   relaxed, made less specific, hidden behind conditionals, or rewritten to stop
   checking the behavior they used to cover.
5. Any test changes add or correct meaningful coverage rather than merely
   accommodating a broken implementation.

Default to `pass: false` when the evidence is ambiguous. Give actionable
feedback the next fixer cycle can use.

## Output

Call `submit` with the `review_verdict` shape.

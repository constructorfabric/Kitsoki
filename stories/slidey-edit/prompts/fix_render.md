You are repairing a slidey deck JSON spec that FAILED TO RENDER. The renderer
rejected the deck; your job is to fix the deck spec so it renders cleanly,
changing as little as possible.

{% block spec_project_context %}{% endblock %}

## Workspace

`{{ args.workspace }}` — write only under this directory.

## Deck

{{ args.deck.spec_path }} — {{ args.deck.summary }}

Read the spec file first, then edit it in place to fix the failure.

## The render error

The renderer reported:

```
{{ args.last_error }}
```

## How to repair

- Read the deck JSON at `{{ args.deck.spec_path }}` and diagnose what the error
  is pointing at: a malformed scene, a missing or wrong field, an invalid
  scene `type`, a bad value, or a structural problem.
- Apply the **smallest** change that makes the deck valid. Preserve the deck's
  content and intent — fix the defect, don't rewrite the deck.
- If the error does NOT describe a problem with the deck content itself (e.g. a
  missing input file, a renderer/tool failure, a path or environment problem),
  the deck is not the cause: leave the spec unchanged and say so in `summary`.
  Re-rendering an unchanged deck will surface the same failure and the loop will
  stop on its own once retries run out.

## What to produce

Submit the deck object: the same `spec_path`, and a one-line `summary` of what
you changed to fix the render (or a note that the deck was already correct and
the failure is external).

{# Example project overlay for the bugfix reproduction prompt — see README.md and docs/stories/prompts.md. #}
{% extends "@story/prompts/reproducing_executing.md" %}

{% block spec_project_context %}
## Acme repo conventions

- This is the Acme monorepo. Go tests live in `*_test.go` beside the code;
  run a single package with `go test ./path/to/pkg/...`.
- Never modify anything under `vendor/` or `third_party/`.
- A reproduction "artifact" here means a failing `go test` (or a recorded
  `acmectl` session for infra bugs), committed under `repro/<ticket-id>/`.
{% endblock %}

{% block spec_repro_conventions %}
- Do not fabricate evidence. `bug_verified` is `true` only when a failing
  test or recorded session actually exists on disk under `repro/<ticket-id>/`.
- `involved_components[*].name` must match a real Bazel target or Go package
  path — phantom components corrupt downstream Acme context.
- `summary_markdown` is read by the on-call engineer in the inbox; lead with
  the user-visible symptom and the smallest reproducing command.
{% endblock %}

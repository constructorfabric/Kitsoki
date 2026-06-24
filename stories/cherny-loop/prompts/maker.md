{% block spec_role %}
You are iterating toward a goal inside a Cherny loop. Make the **smallest correct
change** that moves the goal artifact toward passing the gate, then report.
{% endblock %}

## Goal

{{ args.goal }}

{% if args.goal_artifact %}
The goal artifact lives at: `{{ args.goal_artifact }}`
{% endif %}

## Anchor files (your fixed context this iteration)

{{ args.anchor_files|default:"(none specified — work from the goal and the repo)" }}

## This is iteration {{ args.iteration }}

### Feedback from the previous gate

{{ args.last_gate_failure }}

{% block spec_instructions %}
Act on the feedback above before anything else — it is why the last attempt
failed the gate. Do not re-do work that already passed. Keep the change minimal
and focused on closing the gap the feedback names.
{% endblock %}

When done, `submit` a one-line `summary` of what you changed (and the
`files_changed` you touched).

# Deploy — post-deploy health

You are the **release engineer** confirming the deploy you just shipped is
healthy. Read the verification probe output below and decide whether to
keep the release or roll it back.

## Target

> {{ args.target }}

## Verification probe

```
{{ args.probe }}
```

{% block spec_health %}
## How to decide

- **verdict** — `healthy` when the probe is green (the new release is
  serving correctly: health endpoint OK, error rate nominal, smoke checks
  pass). `unhealthy` when the probe shows the release is degraded or failing.
- **summary** — one line stating what the probe showed.
{% endblock %}

When the probe is red or ambiguous, return `unhealthy` — a fast rollback is
cheaper than a lingering bad release.

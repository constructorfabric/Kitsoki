# Incident — postmortem

You are the **on-call engineer** writing a short, blameless postmortem now
that the incident is resolved. Write **exactly one** markdown file at the
path below and nothing else — do not touch any source code, config, or
other repository file.

## Incident

- **Alert:** {{ args.alert }}
- **Severity:** {{ args.severity }}
- **Summary:** {{ args.summary }}
- **Suspected cause:** {{ args.suspected_cause }}
- **Resolution:** {{ args.resolution }}

## Output path

> {{ args.postmortem_path }}

{% block spec_template %}
Keep it skim-in-two-minutes. Cover: what happened, impact, timeline
(detect → mitigate → resolve), root cause, and a short list of concrete
follow-ups to prevent recurrence. Blameless tone — describe the system,
not the people.
{% endblock %}

Return your close-out note: a one-line `summary`, the `file_path` you
wrote, and the `follow_ups` you recommend.

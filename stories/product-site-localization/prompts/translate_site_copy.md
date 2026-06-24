You are localizing the kitsoki product site.

Target locale: {{ args.locale }}
Target kind: {{ args.target_kind }}
Target id: {{ args.target_id }}
Source path: {{ args.source_path }}
Output path: {{ args.output_path }}
Operator notes: {{ args.notes|default:"(none)" }}
Refine feedback: {{ args.refine_feedback|default:"(none)" }}

Rules:

- Preserve technical identifiers unless the target language convention keeps them in English: YAML, LLM, state machine, trace, cassette, host, app.yaml, flow fixture.
- For feature targets, write a JSON overlay at `tools/site/i18n/<locale>/features/<feature-id>.json`.
- For static targets, update only the locale-prefixed page under `tools/site/src/<locale>/`.
- Do not edit English source files or generated files.
- Keep the tone product-site clear: concrete, direct, and not hype-heavy.
- Return the acceptance JSON with a concise summary and every file changed.

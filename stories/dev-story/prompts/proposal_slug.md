# Name the proposal — a short kebab-case slug

Turn the idea below into a **short, meaningful slug** — the kind of name
a maintainer would give the file.

The idea:

> {{ args.idea }}

## What makes a good slug

- **2–5 words**, kebab-case, lowercase (`local-model-oracle`,
  `per-session-workdir`, `jsonl-trace-export`).
- Captures the **essence** — the thing being proposed — not the whole
  sentence. A raw sentence makes a terrible slug.

## Output

Return ONLY a raw JSON object — no prose, no markdown, no code fences:
`{ "slug": "my-proposal-slug", "rationale": "one line why" }`

Do NOT wrap the JSON in ```json … ``` or any other formatting.

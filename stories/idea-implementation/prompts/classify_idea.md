# Classify an implementation idea

Classify this free-form engineering idea into exactly one route:

- `bug` when the idea describes existing behavior that is broken, regressed, failing, or inconsistent with expected behavior.
- `feature` when the idea asks for a new capability, enhancement, UI, workflow, or other forward change.
- `needs_human` only when routing or safe execution is impossible without specific missing information.

Do not request approval. The operator has already asked for an autonomous implementation.

For a bug route:

- Provide a concise `title`.
- Provide a `body` that preserves the reported symptoms and expected behavior.
- Provide `gate_command` only when the idea names a credible repro command. Leave it empty otherwise; the bugfix story can synthesize evidence.

For a feature route:

- Provide a `title`.
- Provide a scoped implementation `brief` suitable for the ship-it maker loop.
- Provide a deterministic `gate_command` that can prove completion on the target repository. Prefer an existing project test command or a narrow command the maker can make pass.
- If you cannot name a credible deterministic gate, return `needs_human` with questions instead of guessing.

For `needs_human`:

- Return 1-3 concrete questions that would unblock routing, scope, or the deterministic gate.
- The questions must be necessary, not preference checks.

Idea:

{{ args.idea }}

Base branch:

{{ args.base_branch }}

Return only JSON matching the schema.

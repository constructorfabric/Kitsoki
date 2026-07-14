# Demo artifact review

Importable, deterministic review contract for one demo artifact. The trusted server-side binding id is `demo-artifact-review`; it resolves the canonical room, schemas, revision policy (`separate-records`), prerequisites, and assignment policy (`declared`). Clients supply only typed project/session/artifact identifiers.

Entry state: `review`.

Inputs: `project_id`, `session_id`, and `artifact_id`. The versioned wire schemas are `schemas/request-v1.json`, `schemas/form-v1.json`, and `schemas/revision-v1.json`.

The three revision records are independent: `mockup_revision`, `rrweb_revision`, and `deck_revision`. `reviewed` requires `review_status`; `abandoned` requires `abandon_reason`.

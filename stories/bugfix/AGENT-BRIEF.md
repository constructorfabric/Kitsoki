# bugfix — agent brief

The bugfix story drives a multi-phase repro/fix/review cycle for inbound
bug tickets. Every LLM call is one of the five oracle verbs introduced in
oracle-split (Phase 8).

For the canonical persona table — which agent handles which phase, the
verb it uses, the tool surface it gets, and which agents touch external
state — see [`README.md` §Oracle-split persona table (Phase 8)](README.md#oracle-split-persona-table-phase-8).

For the YAML shape of an `agents:` entry (`tools:`, `bash_profile:`,
`external_side_effect:`, `default_cwd:`) see
[`docs/architecture/hosts.md` §Agent declaration](../../docs/architecture/hosts.md#agent-declaration).

For the verb contracts themselves (`extract` / `decide` / `ask` / `task`
/ `converse` — what each one is allowed to do) see the matching
`§host.oracle.<verb>` sections of [`docs/architecture/hosts.md`](../../docs/architecture/hosts.md).

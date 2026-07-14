# Goals, postconditions, and onboarding resolutions

A room description says what the operator is looking at. A goal contract says
what must be true when a story or lifecycle phase has done its job. Keep those
separate: prose is useful orientation, while postconditions need stable ids and
named verifications that can be rerun.

Project-bound goal contracts live in `.kitsoki/project-profile.yaml`. The
`goals` map is keyed by a story or phase id, so the same shape works for
`onboarding`, `validation`, `release`, or a project-specific phase:

```yaml
goals:
  onboarding:
    statement: Make this checkout ready for deterministic dev-story work.
    postconditions:
      - id: tests-runnable
        statement: The canonical test command runs deterministically.
        gate: required
        verification: tests
      - id: dev-server-runnable
        statement: The dev server can be booted, probed, and stopped deterministically.
        gate: required
        verification: dev-server
      - id: branch-policy-known
        statement: The base branch, working-branch pattern, and issue-id policy are explicit.
        gate: required
        verification: branch-policy
      - id: ticket-source-known
        statement: The ticket provider and project are explicit.
        gate: required
        verification: ticket-source
      - id: pr-policy-known
        statement: The pull-request destination, base branch, and template are explicit.
        gate: required
        verification: pr-policy

  validation:
    statement: Prove and improve dev-story against a stable independent corpus.
    requires: [onboarding]
    postconditions:
      - id: reference-corpus-frozen
        statement: Independently proved repo-history capsules are frozen in a corpus-receipt.v1.
        gate: required
        verification: reference-corpus
      - id: stable-corpus-green
        statement: The optimization loop solves every case in the stable corpus.
        gate: required
        verification: stable-corpus
      - id: bug-to-pr-proven
        statement: A developer can pick a configured bug, work it, and open a pull request.
        gate: required
        verification: bug-to-pr
```

`goals` is optional so existing project profiles continue to validate. The
validator warns when an older profile has no goal contract; newly generated
profiles should include one.

## Postconditions point to verifications

A postcondition does not inline a shell command. Its `verification` names one
entry under `setup_plan.verifications`. That keeps the outcome stable while a
project changes the command or native metadata check used to measure it.

```yaml
setup_plan:
  writes: []
  verifications:
    - id: tests
      kind: tests
      command: make test
      gate: required

    - id: dev-server
      kind: dev-server
      fields: [commands.dev, dev_server.components]
      gate: required

    - id: branch-policy
      kind: profile
      fields: [repo.default_branch, repo.branch_pattern, repo.branch_issue_id]
      gate: required

    - id: ticket-source
      kind: ticket
      fields: [tracker.provider, tracker.project]
      gate: required

    - id: pr-policy
      kind: pull-request
      fields: [pull_requests.repository, pull_requests.base_branch, pull_requests.template]
      gate: required

    - id: reference-corpus
      kind: corpus
      command: make verify-reference-corpus
      gate: required

    - id: stable-corpus
      kind: workflow
      command: make verify-stable-corpus
      gate: required

    - id: bug-to-pr
      kind: workflow
      command: make verify-bug-to-pr
      gate: required
```

Every verification needs either `command` or `fields`. `fields` is for a
native/profile check; it is not a request to concatenate values into a shell
command. Supported metadata/workflow kinds include `profile`, `ticket`,
`pull-request`, `corpus`, and `workflow`, alongside the existing command and
artifact kinds.

A `required` postcondition must reference a `required` verification. Pointing a
required outcome at an advisory or recommended check is a validation error.
Missing verification ids are also errors. Use `applicable: false` on a
postcondition when the outcome genuinely does not apply, such as a dev server
for a library; record the not-applicable decision instead of inventing a
command.

The current corpus vocabulary is:

- a **repo-history capsule** is one reusable historical case with an
  independently proved RED baseline and GREEN reference fix;
- Corpus Forge freezes selected cases into a versioned
  **`corpus-receipt.v1`**;
- `oracle-capsules` is a compatibility alias, not the preferred name for new
  profiles or documentation.

## Record concrete project policy once

Goal postconditions should point at canonical profile fields rather than copy
their values into the goal:

```yaml
repo:
  default_branch: main
  branch_pattern: "feature/{issue_id}-{slug}"
  branch_issue_id: required

tracker:
  provider: github
  project: owner/repo

pull_requests:
  provider: github
  repository: owner/repo
  base_branch: main
  template: .github/pull_request_template.md
```

Commands remain under `commands`; deterministic server startup and readiness
probes remain under `dev_server`. `branch_issue_id` is one of `required`,
`optional`, `forbidden`, or `unknown`.

## Provenance, defaults, and reruns

`onboarding.resolutions` records how each important field was selected. The
canonical value still lives at `field`; `value` is the last value onboarding
resolved and is the comparison point for an idempotent rerun.

```yaml
onboarding:
  resolutions:
    - field: commands.test
      value: make test
      source: discovered
      evidence: Makefile declares the test target.
      update: ".kitsoki/project-profile.yaml#commands.test"

    - field: repo.branch_pattern
      value: "feature/{slug}"
      source: default
      evidence: No branch convention was found in repo rules or history.
      update: ".kitsoki/project-profile.yaml#repo.branch_pattern"
      notice: Could not determine the branch pattern; using feature/{slug}.
```

`source` is `discovered`, `default`, or `operator`. Every default resolution
must have:

- `notice`: the message shown to the operator explaining what Kitsoki could
  not determine and which default it used;
- `update`: the exact file and field the operator can change.

Duplicate resolution fields are invalid. Profiles without resolutions remain
valid for compatibility, but validation warns that provenance and default
update guidance are missing.

An idempotent onboarding merge uses the stored value as a three-way boundary:

1. Preserve an existing `source: operator` value.
2. If the canonical field differs from the prior resolution `value`, treat the
   canonical field as an operator edit and preserve it.
3. Otherwise refresh the discovered/default candidate and its evidence.
4. Preserve unrelated profile keys and accepted project customizations.
5. A second run with identical evidence produces no file changes.

Validation is rerunnable by the same rule: replace its own readiness result,
not the goal contract, resolutions, or operator-owned fields.

## Update channels

Use the narrowest update channel for the thing that changed:

1. **Shared story behavior** — update the Kitsoki binary that supplies
   `@kitsoki/dev-story`. During local staging, point resolution at the staged
   checkout explicitly, for example
   `KITSOKI_REPO=/path/to/Kitsoki/.capsules/staging/local kitsoki ...`.
2. **Project policy and discovered commands** — edit
   `.kitsoki/project-profile.yaml` at the path named by the resolution's
   `update` field. Mark a deliberate durable choice as `source: operator`.
3. **Generated project wrapper** — rerun onboarding/regeneration after profile
   review. Do not hand-edit `.kitsoki/stories/<id>-dev/app.yaml`; it is a thin
   projection of the profile onto the shared story.
4. **Project-specific prompt guidance** — specialize a `spec_` block through a
   [prompt overlay](prompts.md), rather than forking the shared prompt or base
   story.

This separation lets a project receive shared story improvements without
losing its commands, branch policy, ticket provider, pull-request policy, or
operator overrides.

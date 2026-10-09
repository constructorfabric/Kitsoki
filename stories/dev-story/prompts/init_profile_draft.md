Draft a Kitsoki `project-profile/v1` JSON object for this project.

Use the deterministic discovery as the floor:

```json
{{ args.discovery }}
```

Operator feedback:

```text
{{ args.feedback }}
```

Target checkout: `{{ args.target_path }}`

Hard requirements:

- Return only JSON through the submit tool.
- `schema` must be `project-profile/v1`.
- `id` must stay the discovered project id.
- `kitsoki.story` must be `dev-story`.
- Preserve `kitsoki.enabled_stories` from discovery when present; if absent,
  default to `["setup", "bugfix", "pr-refinement", "git-ops"]`.
- `kitsoki.instance.id` must be `<id>-dev`.
- `kitsoki.instance.path` must be `.kitsoki/stories/<id>-dev/app.yaml`.
- `kitsoki.instance.bindings` must include `ticket`, `vcs`, `ci`, `workspace`, and `transport`.
- Do not put project story customization under root `stories/`.
- Preserve discovered `commands.dev`, `commands.test`, and `commands.build` unless repo evidence shows a better canonical command.
- Prefer project/community conventions expressed as config values over custom story logic.
- Include `dev_story_profile.docs`. For a generic project, default
  `publish_durable_path` to `.context/prd`, `design_durable_path` to
  `.context/designs`, `design_template_dir` to `""`, and both
  `design_ticket_dir` and `ticket_repo` to `""` unless repo evidence shows a
  project-owned docs or tracker convention. When discovery reports
  `tracker: github` with a `ticket_repo` slug (a github.com origin), keep that
  slug — it pins `iface.ticket → host.gh.ticket` to the project's real GitHub
  issues; do not blank it or substitute a different repo.
- Include `dev_story_profile.bugfix.build_cmd` and `dev_story_profile.bugfix.test_cmd` when build/test commands are known.
- Include `onboarding.base_story`, `onboarding.base_story_title`, and
  `onboarding.base_story_reason`; default to `dev-story` as the starter story
  unless the repo evidence strongly says another embedded starter is better.
- Include `onboarding.starter_stories` and `onboarding.expansion_policy` so a
  team can begin with setup, bugfix, PR refinement, and git-ops before expanding
  deliberately. `onboarding.expansion_policy` must be a string.
- Include concise `onboarding.repo_patterns` evidence objects with `id`,
  `source`, `evidence`, and optional `recommendation`.
- Include `onboarding.story_customizations` objects with `id`, `status`,
  `summary`, and optional `evidence` so future session mining can evolve the
  project-local profile instead of patching the shared story.
- Include `setup_plan.writes` objects with `path`, `action`, and `summary` for
  `.kitsoki/project-profile.yaml`, `.kitsoki/stories/<id>-dev/app.yaml`,
  `.kitsoki.yaml`, and `.gitignore`.
- Include `setup_plan.verifications` objects with `id`, `kind`, `command`, and
  `gate` for story-load, tests, and build when commands are known.

Use read-only tools if you need evidence from package manifests, Makefiles,
README files, or existing project rules. Keep the profile concise and useful;
do not invent CI, deployment, or framework details that are not visible in the
checkout.

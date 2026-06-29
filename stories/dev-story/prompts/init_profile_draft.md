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
- `kitsoki.instance.id` must be `<id>-dev`.
- `kitsoki.instance.path` must be `.kitsoki/stories/<id>-dev/app.yaml`.
- `kitsoki.instance.bindings` must include `ticket`, `vcs`, `ci`, `workspace`, and `transport`.
- Do not put project story customization under root `stories/`.
- Preserve discovered `commands.dev`, `commands.test`, and `commands.build` unless repo evidence shows a better canonical command.
- Prefer project/community conventions expressed as config values over custom story logic.
- Include `dev_story_profile.bugfix.build_cmd` and `dev_story_profile.bugfix.test_cmd` when build/test commands are known.
- Include `setup_plan.writes` for `.kitsoki/project-profile.yaml`, `.kitsoki/stories/<id>-dev/app.yaml`, `.kitsoki.yaml`, and `.gitignore`.
- Include `setup_plan.verifications` for story-load, tests, and build when commands are known.

Use read-only tools if you need evidence from package manifests, Makefiles,
README files, or existing project rules. Keep the profile concise and useful;
do not invent CI, deployment, or framework details that are not visible in the
checkout.

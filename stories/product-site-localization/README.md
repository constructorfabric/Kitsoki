# Product site localization story

This story maintains the VitePress product site localization foundation:

- English source/fallback at `/`
- Thai at `/th/`
- Japanese at `/ja/`

The operator picks one target at a time:

```text
start locale=th target_kind=feature target_id=complete-product-tour
start locale=ja target_kind=static target_id=index
```

The story runs one write-capable `host.agent.task` scoped to localized site
files, then validates with `make site`. Tests stub both calls, so default
automation never hits a real LLM or incurs cost.

Feature copy is stored as JSON overlays under:

```text
tools/site/i18n/<locale>/features/<feature-id>.json
```

Missing fields fall back to English. That makes localization an ongoing process:
add or refresh one feature/page, review it, accept it, and repeat.

# Site localization

The product site builds English at `/`, Thai at `/th/`, and Japanese at `/ja/`.

Static pages live in `tools/site/src/` with locale-prefixed siblings:

- English: `src/index.md`, `src/features/index.md`
- Thai: `src/th/index.md`, `src/th/features/index.md`
- Japanese: `src/ja/index.md`, `src/ja/features/index.md`

Feature pages are generated from the committed feature catalog. Locale-specific
copy lives in JSON overlay files:

```text
tools/site/i18n/<locale>/features/<feature-id>.json
```

Missing fields intentionally fall back to English, so localization can progress
feature by feature without blocking site builds.

Use the `product-site-localization` story to draft or refresh these overlays.
The story is designed for an ongoing loop: pick a target locale, pick a feature
or static page, generate/update the localized copy, review it, then build the
site.

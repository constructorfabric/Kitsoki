---
name: kitsoki-media
description: Organize, review, document, or validate Kitsoki media artifacts, including product-site tour demo videos, rrweb captures, staged VitePress media, complete-product-tour sections, and Slidey decks with embedded rrweb clips. Use when Codex is asked to find demo videos, clean up media layout, add media validation to tests, decide where a demo/deck artifact belongs, update docs/media guidance, or troubleshoot product-site media staging without recording live LLM runs.
---

# Kitsoki Media

Use this skill for the media organization layer around Kitsoki demos and decks. For actually recording a web/VS Code/terminal demo, use `kitsoki-ui-demo`. For gated vision review of an existing video or screenshot, use `kitsoki-ui-qa`. This skill decides where artifacts belong, how they are indexed, and which no-LLM checks should gate them.

## First Reads

Read these project files before changing media behavior:

- `docs/media/README.md` — authoritative source/generated boundaries and current inventory.
- `docs/site/README.md` — product-site feature catalog, recording, staging, and publishing pipeline.
- `tools/site/scripts/check-media.mjs` — deterministic no-LLM contract enforced by `make media-check` and `make test`.
- `features/<id>.yaml` for any feature whose demo you are touching.

Only read `kitsoki-ui-demo` or `kitsoki-ui-qa` when the task requires recording or vision QA details.

## Classification Rules

Classify every media path before editing:

- Source contracts: `features/*.yaml`, `tools/runstatus/tests/playwright/*-video.spec.ts`, `cmd/kitsoki tour`, committed tour manifests generated from features, and intentionally committed deck-local rrweb clips.
- Generated review artifacts: `.artifacts/**`. Do not commit these unless the user explicitly asks for a source fixture and the path is clearly appropriate.
- Site staging: `tools/site/src/public/media/<feature>/`. Treat as generated from `.artifacts` by the site pipeline, not as source.
- Built site output: `tools/site/.vitepress/dist/**`. Never treat as source.
- Slidey deck examples: `docs/decks/<deck-id>.slidey.json` may be committed; rrweb clips referenced by that deck must live under `docs/decks/assets/<deck-id>/` until a first-class deck catalog exists.
- Story-baked runtime media: keep with the story, such as `stories/<story>/baked/`, when it is part of story runtime behavior.

When in doubt, put transient notes in `.context/` and generated media in `.artifacts/`.

## Workflow

1. Inventory with targeted commands. Prefer `find`/`rg --files`; exclude `.git`, `node_modules`, `.pnpm-store`, `.artifacts`, `.worktrees`, and `.claude/worktrees` unless the task is specifically about generated outputs.
2. Identify the source of truth. For product demos, start from `features/*.yaml`; for deck clips, start from the Slidey JSON references; for staged site media, start from the generated feature index.
3. Keep automated validation no-LLM. Use structural checks (`make media-check`, `pnpm --dir tools/runstatus --silent features:check`) in default testing. Do not wire `kitsoki-ui-qa` or any real LLM/vision review into `make test`.
4. Fix catalog drift before media staging drift. Broken feature links, stale generated manifests, or invalid demo paths should be fixed in source contracts, then regenerated.
5. Document durable policy in `docs/media/README.md`; use `.context/` for one-off inventories and review notes.

## Commands

Use these commands for common checks:

```bash
make media-check
pnpm --dir tools/runstatus --silent features:check
make site
```

`make media-check` runs the site/deck media contract without recording videos. If sandboxed `tsx` fails with an IPC `EPERM`, rerun the same command with escalation; do not change code to work around that environment detail.

For recording or refreshing media:

```bash
make demo-feature FEATURE=<id>
make demos
make render-tour
```

These must remain deterministic and no-LLM. Demo targets should build `bin/kitsoki` via `make build-bin`; do not copy `./kitsoki` into `bin/kitsoki` on macOS because that can invalidate signing.

For gated visual QA, run only when explicitly requested:

```bash
make feature-qa FEATURE=<id>
make tour-qa
```

## Change Discipline

- Do not move or delete generated media merely because it exists; first determine whether it is ignored staging, review output, or a committed source fixture.
- Preserve unrelated dirty work, especially under `docs/decks/`, `stories/slidey-edit/`, and `.worktrees/`.
- If adding a new long-lived Slidey deck, keep deck-local rrweb assets under `docs/decks/assets/<deck-id>/` and update `docs/media/README.md` if it changes the inventory or policy.
- If adding a new product demo, update the feature YAML first, regenerate feature outputs with `make features`, and validate with `make media-check`.
- Commit only the media-contract/doc/checker changes you made.

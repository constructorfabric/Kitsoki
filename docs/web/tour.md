# `kitsoki tour` — render a demo video from the binary

`kitsoki tour` drives the [embedded web UI](README.md) headlessly to **record a
deterministic, no-LLM demo MP4** from a declarative tour manifest — no Node,
pnpm, or Playwright. It is the binary-native counterpart to the Playwright
recording specs (see [Demo, video & testing](README.md#demo-video--testing)): a
foreign repo that owns a kitsoki instance (importing `@kitsoki/<name>`) can
produce its own demo video with only the `kitsoki` binary present (the
[kitsoki-as-dependency](../proposals/README.md) epic, slice 2).

It reuses `kitsoki web`'s no-LLM plumbing verbatim: a **flow fixture** (plus an
optional host cassette) stubs every `host.*` call, the same posture
[`kitsoki web --flow`](README.md#deterministic-no-llm-for-development-demos-playwright)
uses. The render is therefore fully reproducible and never touches a real LLM.

## Synopsis

Load the tour **either** from the feature catalog — the common case:

```bash
kitsoki tour --feature agent-actions
```

`features/<id>.yaml` carries both the tour steps and the **demo binding** (its
flow fixture, host cassette, story dir, and video base), so no other flags are
needed. **Or** render a standalone manifest, supplying the no-LLM posture by
flag:

```bash
kitsoki tour --manifest my-tour.yaml --flow stories/x/flows/happy.yaml \
  --stories-dir stories/x --out .artifacts/my-tour
```

Exactly one of `--feature` / `--manifest` is required, and a flow fixture must
resolve (from the feature binding or `--flow`) — without it the render would not
be no-LLM and the command errors.

## Output

Everything lands in `--out` (default `.artifacts/<feature-id>`):

| Artifact | Contents |
|---|---|
| `<videoBase>.mp4` | the rendered screencast |
| `<videoBase>.mp4.chapters.json` | one chapter per step (`source_ref` kind `tour`) |
| `<videoBase>.mp4.steps.json` | one record per poster PNG, tying it deterministically to its spec step (see below) |
| `NN-<id>.png` | a poster frame per step |

The stdout line is the MP4 path (script-friendly); progress and the sidecar
paths go to stderr.

### Deterministic per-step references (`steps.json`)

Each poster PNG is recorded in `<videoBase>.mp4.steps.json` as a `StepShot` that
points at the **exact place in the spec** it depicts — no LLM interpretation of
the pixels required:

```json
{
  "capture": 5, "step_index": 4, "png": "05-aa-welcome.png",
  "spec_ref": { "kind": "tour", "spec_path": "features/agent-actions.yaml",
                "pointer": "/tour/steps/4", "step_id": "aa-welcome" },
  "title": "Agent action transcripts", "route": "any",
  "states_asserted": ["bf.reproducing"],
  "title_asserted": true
}
```

`spec_ref.pointer` is an RFC 6901 JSON Pointer into the feature catalog
(`/tour/steps/N`); `states_asserted` are the machine states the step's
`wait-state` drives polled the session through before the screenshot; and
`title_asserted` records that the renderer verified the popover title on screen
at capture. These are facts the render **enforced** (it errors out otherwise), so
a consumer — e.g. the `kitsoki-ui-qa` gate — can map a frame to the spec step and
its verified states *deterministically* rather than asking a model what the frame
shows. (In the example above, the repeated `prd.clarifying` entries are the
multi-round clarification loop, provable from data alone.)

## Requirements

`ffmpeg` and Chrome/Chromium must be on `PATH`. **Without `ffmpeg`** the per-step
PNGs and the chapter sidecar are still emitted and the command reports the
missing MP4 — useful for a frames-only check in an environment that lacks ffmpeg.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `--feature <id>` | — | feature catalog entry (`features/<id>.yaml`) to render |
| `--manifest <yaml>` | — | standalone tour manifest (alternative to `--feature`) |
| `--flow <yaml>` | feature's `demo.flow` | no-LLM flow fixture stubbing `host.*` |
| `--host-cassette <yaml>` | feature's `demo.hostCassette` | host cassette backing `host.*` |
| `--stories-dir <dir>` | feature's `demo.story` | story directory to serve (repeatable) |
| `--out <dir>` | `.artifacts/<feature-id>` | output directory |
| `--pace <f>` | `1` | pacing multiplier: `0` = instant, `1` = watch speed |
| `--headless` | `true` | launch Chrome headless |
| `--fps <n>` | `30` | output MP4 frame rate |
| `--width` / `--height` | `1600` / `900` | viewport / video size |

`--feature` resolves `features/` against the repo root: `$KITSOKI_REPO`
(exported from `--kitsoki-repo` / the persisted `~/.kitsoki/repo`) if set, else a
walk up from the cwd for a directory holding a `features/` dir.

## How a tour is authored

A tour step carries both **narration** (title/body/target/placement) and a
declarative **`drive:`** action list (`type-and-send`, `click-intent`,
`wait-state`, `reveal-turn`, `dwell-ms`) — the data the binary executes in place
of a hand-written `.spec.ts`. The manifest lives in the feature catalog
(`features/<id>.yaml`); the authoring workflow, the manifest schema, and the
quality bar for a shareable artifact are owned by the
[`kitsoki-ui-demo`](../../.agents/skills/kitsoki-ui-demo/SKILL.md) skill.

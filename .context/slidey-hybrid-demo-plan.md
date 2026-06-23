# Slidey Hybrid Demo — execution plan

Goal: a high-quality **slidey hybrid deck** that tells the dev-story narrative
from `.context/demo-target.md` (PM idea → architect design → dev decomposition →
bugfix → demo videos → PR), interleaving **narrated slides** with **tour-driven
kitsoki demo videos embedded as rrweb** (slidey `video` scene, `rrweb:` source +
`chapters:"auto"`). Videos made via `kitsoki-ui-demo`; QA'd via `kitsoki-ui-qa`.
Kitsoki scenarios driven through the **studio MCP** (kitsoki-mcp-driver agents)
to keep the dogfood honest.

## Pipeline (proven pieces)
- slidey is healthy (`/Users/brad/code/slidey`, dist-render present). `video`
  scene supports `{rrweb, chapters:"auto"}` → seek-rasterizes rrweb → MP4 with
  lower-third captions from the chapter sidecar.
- kitsoki-ui-demo rrweb mode: capture one live drive (`*-rrweb-capture.spec.ts`)
  → `<tour>.rrweb.json` (+ `.capture.json` viewport sidecar) → render server-free.
  For slidey embedding we need the `.rrweb.json` event stream, NOT a pre-rendered
  mp4 (slidey rasterizes it itself), OR a clean mp4+`.chapters.json` via `src:`.

## Phase → kitsoki surface mapping (demo-target.md)
| Phase | Narrative | kitsoki story/feature | clip status |
|---|---|---|---|
| 1 PM idea | brief → PRD + slidey mockup, validated | dev-story (prd) | prd_to_design flow exists |
| 2 Architect design | review reqs, epic/slices/ADRs + mermaid, slidey summary | dev-story design | design-walkthrough.yaml |
| 3 Dev decomposition | review design, decompose to tasks (async) | work-decomposition / deliver | — |
| 4 Bugfix (interrupt) | triage one-shot unsupervised, runs, slidey summary + rrweb repro | gears-bugfix / bugfix | gears-bugfix-demo.mp4 produced |
| 5 Feature dev + demo videos | tasks complete in bg, demo videos in inbox, spatial-oracle refine | slidey-edit + inbox | slidey-edit baked |
| 6 PR | open PRs with demo video | git-ops / ship-it | git-ops spec only |

## Slices (each independently QA-gated)
1. **Pipeline proof** — embed ONE existing rrweb capture (agent-actions, already
   captured at `.artifacts/rrweb-eval/`) into a 3-scene slidey deck (title +
   video + cta), render MP4, QA. Proves slidey-rrweb embed end-to-end.
2. Phase 4 (bugfix) clip via MCP-driven gears-bugfix tour → rrweb → deck scene.
3. Phases 1–2 (PM idea, architect design) clips.
4. Phases 3,5,6 (decomposition, feature demos, PR) clips.
5. Author full narrated deck spine (slides between videos), render full MP4.
6. Full kitsoki-ui-qa pass on the assembled deck; fix tool/skill gaps found.

## Working artifacts
- deck spec + assets: `.artifacts/slidey-hybrid/`
- final: `.artifacts/slidey-hybrid/dev-story-hybrid.mp4`

## Honesty rules (AGENTS.md)
- No real LLM in tests/recordings — `--flow`/cassette posture only.
- Drive kitsoki via studio MCP (kitsoki-mcp-driver), not by hand.
- Don't paper over runtime/skill bugs — fix them; stories exist to expose them.

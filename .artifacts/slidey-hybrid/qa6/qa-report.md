# UI demo QA report

**Gate: ✅ PASS** — 15 advisory blank-scan warning(s)

| metric | n |
|---|---|
| scenarios | 7 |
| passed | 7 |
| failed | 0 |
| unsupported | 0 |
| visual issues | 0 |
| annotation issues | 0 |
| blank-scan warnings | 15 (advisory) |
| pacing warnings | 0 (advisory) |
| frames reviewed | 24 |

## ⚠️ Blank-scan warnings (deterministic monochrome regions — advisory, review by eye)

| frame | largest flat region | issue |
|---|---|---|
| `00-slide-s1.png` | #604848 @ 0% | a flat #181830 right gutter the content never reaches spans 25% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 25% of the frame at the left edge; a flat #181818 bottom gutter the content never reaches spans 41% of the frame at the bottom edge; a flat #181830 top gutter the content never reaches spans 48% of the frame at the top edge — likely a blank/broken render where content was expected |
| `01-slide-s2.png` | #181830 @ 96% | a flat #181830 right gutter the content never reaches spans 44% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 44% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `02-slide-s3.png` | #304860 @ 0% | a flat #181830 right gutter the content never reaches spans 33% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 33% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 22% of the frame at the top edge — likely a blank/broken render where content was expected |
| `03-vid-conversation-d.png` | #d8d8d8 @ 2% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `04-slide-s5.png` | #181830 @ 96% | a flat #181830 right gutter the content never reaches spans 40% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 40% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `05-vid-prd-design-e.png` | #c0d8d8 @ 2% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `06-slide-s7.png` | #304860 @ 0% | a flat #181830 right gutter the content never reaches spans 27% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 42% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 22% of the frame at the top edge — likely a blank/broken render where content was expected |
| `07-vid-design-epic-de.png` | #c0c0d8 @ 0% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `08-slide-s9.png` | #181830 @ 96% | a flat #181830 right gutter the content never reaches spans 27% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 52% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 15% of the frame at the top edge — likely a blank/broken render where content was expected |
| `09-vid-the-bug-fix-pi.png` | #484860 @ 0% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `10-slide-s11.png` | #304860 @ 0% | a flat #181830 right gutter the content never reaches spans 25% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 62% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `11-vid-annotate-the-r.png` | #484860 @ 0% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `12-slide-s13.png` | #181830 @ 97% | a flat #181830 right gutter the content never reaches spans 100% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 100% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `13-vid-reproduction-.png` | #183078 @ 0% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 18% of the frame at the top edge — likely a blank/broken render where content was expected |
| `14-slide-s15.png` | #484848 @ 0% | a flat #181830 right gutter the content never reaches spans 21% of the frame at the right edge; a flat #181830 left gutter the content never reaches spans 21% of the frame at the left edge; a flat #181830 top gutter the content never reaches spans 37% of the frame at the top edge — likely a blank/broken render where content was expected |

## Scenarios

### ✅ Phase 1 — conversation-driven PRD intake plays real content `pm-idea`

| step | status | evidence | observation |
|---|---|---|---|
| A scene 'Idea → PRD + mockups' (eyebrow 'PRODUCT MANAGER') shows an embedded kitsoki tour. | ✅ | `03-vid-conversation-d.png` | Eyebrow '1 · PRODUCT MANAGER', title 'Idea → PRD + mockups', embedded kitsoki web UI with a video scrubber (15.6s / 29.1s) and caption 'Conversation-driven PRD intake on the slidey repo'. |
| The tour shows the real conversational PRD intake: a typed slidey idea, clarifying Q&A and/or a prior-art scan, and a drafted/published PRD — not a blank/loading screen. | ✅ | `03-vid-conversation-d.png`<br>`20-pmidea-clarify.png`<br>`21-pmidea-published.png` | Chat bubble with a typed slidey idea about a 'printable per-scene notes handout', a tour card 'Scan for prior art first', state-diagram rows core.prd.search / core.prd.clarifying.<br>'Clarifications — Round 1' with numbered questions about the primary actor and success metric.<br>PRD panel '# PRD — Speaker-Notes Export' with 'PUBLISHED' badge and 'Published to slidey's docs/prd' tour card; 'Your PRD is published … docs/prd/speaker-notes-export.md'. |

### ✅ Phase 2 — PRD → design epic/slices/ADRs `architect-design`

| step | status | evidence | observation |
|---|---|---|---|
| A scene 'Requirements → design' (eyebrow 'ARCHITECT') shows an embedded tour. | ✅ | `05-vid-prd-design-e.png` | Eyebrow '2 · ARCHITECT', title 'Requirements → design', embedded UI with scrubber 14.1s/14.1s and caption 'PRD → design epic, slices, and ADRs'. |
| The tour shows the design phase: design intake/refine and a published design (epic/slices/ADRs). | ✅ | `05-vid-prd-design-e.png`<br>`22-design-intake.png`<br>`23-design-published.png` | State diagram rows core.design_search SEARCHING, core.design_materialize PREPARING, core.design_refine BRIEF, core.design_draft DRAFTING, core.design_done PUBLISHED.<br>Chat 'Author a design for the PRD at — describe or expand it to start the search', tour card 'Carry the PRD into design', 'Overlap scan' / 'No new spec derived output'.<br>'Design published + feature ticket' tour card; chat shows 'PUBLISHED' and a created feature ticket with Title/Kind/Ticket fields. |

### ✅ Phase 3 — design decomposed into a work plan `decomposition`

| step | status | evidence | observation |
|---|---|---|---|
| A scene 'Design → work plan' (eyebrow 'DECOMPOSITION') shows an embedded tour. | ✅ | `07-vid-design-epic-de.png` | Eyebrow '3 · DEVELOPER · DECOMPOSITION', title 'Design → work plan', embedded UI with scrubber 3.3s/3.3s and caption 'Design epic decomposed into agent-sized briefs'. |
| The tour shows the decomposition: configure/decompose into agent-sized briefs. | ✅ | `07-vid-design-epic-de.png`<br>`24-decompose.png`<br>`25-fleet-load.png` | State diagram lint → fleet.load with 'fleet__start → fleet.board', chat 'Deliver an epic end-to-end: decompose it into briefs' and 'artifacts/deliver/decomposition.yaml'.<br>Tour card 'Decompose → lint → load: Start fans the epic into three dependency-ordered briefs — a SceneNotes model, a handout renderer, and a --notes CLI flag'.<br>Tour card 'A validated work plan: three briefs loaded from .artifacts/deliver/decomposition.yaml, dependency-ordered and gate-bearing'. |

### ✅ Phase 4 — bug-fix pipeline reproduce→fix→validate `bugfix`

| step | status | evidence | observation |
|---|---|---|---|
| A scene 'Bug → reproduce → fix → validate' shows an embedded tour of the bug-fix pipeline on a real slidey bug (implement/test/validate visible). | ✅ | `09-vid-the-bug-fix-pi.png`<br>`26-bugfix-implement.png` | Eyebrow '4 · DEVELOPER · BUG FIX', title 'Bug → reproduce → fix → validate', state diagram bf.proposing/implementing/testing/reviewing/validating/shipped PR-READY; chat text 'grid-cards fallback from `?? 30` to `?? 20`' and 'regression test'; caption 'The bug-fix pipeline on a real slidey bug'.<br>Tour card 'Implement the file: the agent edits etc/timing.js …', diff/changes with 'test/timing.test.js — regression test'. |

### ✅ Phase 5 — spatial anchored refine `refine`

| step | status | evidence | observation |
|---|---|---|---|
| A scene about spatial refine shows the rendered deck poster with an annotation overlay (boxed elements), demonstrating location-tied review. | ✅ | `27-refine-overlay.png`<br>`11-vid-annotate-the-r.png` | Rendered poster ('ZEN GARDEN UNION') with a SemanticOverlay of boxed elements labeled 'semantic_element', 'region', and 'point'; tour card 'Poster-backed semantic overlay … each box comes straight from the deck's .semantic.json sidecar'.<br>Eyebrow '5 · DEVELOPER · REFINE', title 'Demo videos → spatial refine', caption 'Annotate the rendered frame → an anchored refine pass'; poster with annotation boxes visible. |

### ✅ Phase 6 — open PR, CI, merge `ship-pr`

| step | status | evidence | observation |
|---|---|---|---|
| A scene about shipping shows a pull request being opened on the slidey repo (PR id/url, open_pr state), CI, and a merge. | ✅ | `28-pr-open.png`<br>`13-vid-reproduction-.png` | Tour card 'Open the pull request … the room calls iface.vcs.open_pr', state open_pr; agent fields 'PR id 128', 'PR url https://github.com/kitsoki/slidey/pull/128'; trace host.git.open_pr.<br>Eyebrow '6 · DEVELOPER · SHIP', title 'Summary → open PR', state diagram ci_monitoring / merge_executing / __exit_merged, trace host.git.open_pr and world.update; caption 'Reproduction + validation summary → pull request'. |

### ✅ The deck is a coherent six-phase dev story `arc-coherent`

| step | status | evidence | observation |
|---|---|---|---|
| A title slide opens the deck (idea → merged PR on the slidey repo). | ✅ | `00-slide-s1.png` | 'KITSOKI · DEV STORY', 'From an idea to a merged PR', 'one feature and one bug, end to end, on the slidey repo', scene 1/15. |
| Six narrated phase slides each precede their tour video (PM, architect, decomposition, bug, refine, ship). | ✅ | `02-slide-s3.png`<br>`04-slide-s5.png`<br>`06-slide-s7.png`<br>`08-slide-s9.png`<br>`10-slide-s11.png`<br>`12-slide-s13.png` | '1 · PRODUCT MANAGER' precedes tour 03.<br>'2 · ARCHITECT' precedes tour 05.<br>'3 · DEVELOPER · DECOMPOSITION' precedes tour 07.<br>'4 · DEVELOPER · THE INTERRUPT' precedes tour 09.<br>'5 · DEVELOPER · FEATURE DELIVERY' precedes refine tour 11.<br>'6 · DEVELOPER · SHIP' precedes ship tour 13. |
| A closing cta slide states every frame is a deterministic, no-LLM replay. | ✅ | `14-slide-s15.png` | 'Kitsoki' title, 'Every frame is a deterministic, no-LLM replay', 'the runtime is a state machine; the model is a traceable callee', scene 15/15. |


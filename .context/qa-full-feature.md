# Slidey hybrid demo — the full dev story (6 phases) on the slidey repo

A self-contained slidey HTML deck (`.artifacts/slidey-hybrid/dev-story-hybrid.html`)
telling ONE dev story end to end on the **slidey** repo: from a product idea to a
merged PR. Narrated slides interleaved with **six native, autoplaying rrweb kitsoki
tour embeds** — each a deterministic, no-LLM replay of the real kitsoki web UI
driving a real slidey scenario.

Phase order (each = a narrative slide + its tour video):
1. **Product manager — Idea → PRD + mockups.** A PM types a one-line slidey idea
   (a `--notes` speaker-notes export); kitsoki runs conversation-driven intake
   (clarifying questions, prior-art scan, a drafted PRD, a mockup) to a published PRD.
2. **Architect — Requirements → design.** Reviews the PRD, produces a design epic /
   slices / ADRs / mermaid, to a published design.
3. **Developer — Design → work plan.** Decomposes the design epic into agent-sized
   briefs (configure → decompose → fleet-load), a validated work plan.
4. **Developer — Bug fix.** A real slidey bug (grid-card narration drift) reproduced,
   proposed, implemented in a worktree, tested, reviewed, validated — PR-ready.
5. **Developer — Spatial refine.** Points at an exact element on a rendered deck
   frame; an anchored refine edits precisely that scene.
6. **Developer — Ship.** Opens the pull request, CI green, merge lands.

Closes with a cta: every frame is a deterministic, no-LLM replay.

## Claims to verify
- Each of the six embedded tours plays REAL content (not frozen/loading) — the
  conversational phases (1–3) show typed ideas, clarifying Q&A, PRD/design/decompose
  output; the developer phases (4–6) show the bug-fix pipeline, the annotation
  overlay, and the PR/CI/merge.
- The deck is a coherent six-phase dev story: title → 6 narrated phases each paired
  with its tour → cta. No missing/blank scenes.

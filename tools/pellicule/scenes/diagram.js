/**
 * PELLICULE — Diagram scene
 *
 * Displays one or two ASCII/text diagrams side-by-side (or in sequence)
 * with a caption underneath. Used for the LLM-on-top → runtime-on-top
 * arrow flip in concept.md §2.
 *
 * Spec:
 *   {
 *     "type": "diagram",
 *     "title":   "Control inversion",   // optional eyebrow
 *     "panels": [
 *       { "label": "Typical agent",  "ascii": "..." },
 *       { "label": "Kitsoki",        "ascii": "..." }
 *     ],
 *     "caption": "The arrow that matters is the LLM's return arrow.",
 *     "hold":    180
 *   }
 *
 * Single-panel diagrams use a one-element panels array.
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showDiagram(s), scene);
  await ctx.setState('diagram_title');
  for (let i = 0; i < (scene.panels || []).length; i++) {
    await ctx.setState(`diagram_panel_${i}`);
  }
  if (scene.caption) await ctx.setState('diagram_caption');
  await ctx.hold(scene.hold ?? TIMING.diagram_hold, 'diagram_hold');
  await page.evaluate(() => window.pellicule.hideDiagram());
  await ctx.hold(TIMING.inter_scene, 'inter_scene');
}

module.exports = { render };

/**
 * PELLICULE — SVG diagram scene
 *
 * Declarative inline-SVG diagram. Each panel has nodes (boxes with optional
 * sub-label) and edges (arrows between nodes, with optional label). The
 * renderer computes simple side-anchored endpoints — supports a "side"
 * parameter for parallel arrows between the same node pair (e.g. the
 * runtime ↔ LLM round-trip in control inversion).
 *
 * Spec:
 *   {
 *     "type": "diagram-svg",
 *     "title":   "Control inversion",     // optional eyebrow
 *     "panels": [
 *       {
 *         "label":   "Typical agent",
 *         "viewBox": "0 0 400 360",        // optional, default "0 0 400 360"
 *         "nodes": [
 *           { "id":"llm","label":"LLM","sub":"plan · reason · decide",
 *             "x":100,"y":40,"w":200,"h":80,"style":"primary" },
 *           { "id":"rt","label":"runtime","sub":"execute",
 *             "x":100,"y":240,"w":200,"h":80 }
 *         ],
 *         "edges": [
 *           { "from":"llm","to":"rt","label":"calls" }
 *         ],
 *         "caption": "LLM holds the plan."   // optional below-panel caption
 *       },
 *       { ... }
 *     ],
 *     "caption": "The arrow that matters is the LLM's return arrow.",
 *     "hold":    210
 *   }
 *
 * Node styles: "default" | "primary" | "secondary".
 * Edge `side`: omitted (centered) | "left" | "right" — horizontal offset.
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showDiagramSvg(s), scene);
  await ctx.setState('diagramsvg_title');
  for (let i = 0; i < (scene.panels || []).length; i++) {
    await ctx.setState(`diagramsvg_panel_${i}`);
  }
  if (scene.caption) await ctx.setState('diagramsvg_caption');
  await ctx.hold(scene.hold ?? TIMING.diagramsvg_hold, 'diagramsvg_hold');
  await page.evaluate(() => window.pellicule.hideDiagramSvg());
  await ctx.hold(TIMING.inter_scene, 'inter_scene');
}

module.exports = { render };

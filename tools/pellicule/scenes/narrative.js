/**
 * PELLICULE — Narrative scene
 *
 * Full-bleed prose with eyebrow + animated rule. Used for thesis beats
 * (e.g. "Two extremes dominate today's interactive software"). Body text
 * fades in word-by-word for that tech-pitch cadence.
 *
 * Spec:
 *   {
 *     "type": "narrative",
 *     "eyebrow": "THE PROBLEM",            // optional, small caps
 *     "body":    "Two extremes dominate.", // primary text
 *     "lede":    "And neither is enough.", // optional smaller line below
 *     "hold":    150                       // optional frames to dwell on complete view
 *   }
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showNarrative(s), scene);
  await ctx.setState('narrative_eyebrow');
  await ctx.setState('narrative_body');
  if (scene.lede) await ctx.setState('narrative_lede');
  await ctx.hold(scene.hold ?? TIMING.narrative_hold, 'narrative_hold');
  await page.evaluate(() => window.pellicule.hideNarrative());
  await ctx.hold(TIMING.inter_scene, 'inter_scene');
}

module.exports = { render };

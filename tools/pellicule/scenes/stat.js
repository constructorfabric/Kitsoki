/**
 * PELLICULE — Stat scene
 *
 * Giant number + caption. Used for the "78% deterministic routing" beat.
 * Number animates in via a brief count-up; caption fades in after.
 *
 * Spec:
 *   {
 *     "type": "stat",
 *     "value":   "78%",                              // displayed verbatim
 *     "label":   "of recorded turns route deterministically",
 *     "detail":  "(across cloak, oregon-trail, dev-story)",  // optional
 *     "hold":    120
 *   }
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showStat(s), scene);
  await ctx.setState('stat_value');
  await ctx.setState('stat_label');
  if (scene.detail) await ctx.setState('stat_detail');
  await ctx.hold(scene.hold ?? TIMING.stat_hold, 'stat_hold');
  await page.evaluate(() => window.pellicule.hideStat());
  await ctx.hold(TIMING.inter_scene, 'inter_scene');
}

module.exports = { render };

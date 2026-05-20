/**
 * PELLICULE — CTA (Call To Action) scene
 *
 * End card. Tagline + URL (or any reference text). Visually a relative of
 * the title card but with the brand mark and a tagline below.
 *
 * Spec:
 *   {
 *     "type": "cta",
 *     "wordmark":  "Kitsoki",
 *     "tagline":   "Conversation engine with deterministic flow",
 *     "url":       "github.com/acronis/kitsoki",
 *     "hold":      180
 *   }
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showCta(s), scene);
  await ctx.setState('cta_wordmark');
  await ctx.setState('cta_tagline');
  await ctx.setState('cta_url');
  await ctx.hold(scene.hold ?? TIMING.cta_hold, 'cta_hold');
  await page.evaluate(() => window.pellicule.hideCta());
}

module.exports = { render };

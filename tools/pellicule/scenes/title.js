/**
 * PELLICULE — Title scene
 *
 * Full-screen title card. Holds for `TIMING.title_card` frames then hides.
 *
 * Spec:
 *   { "type": "title", "title": "...", "subtitle": "..." }
 */

'use strict';

const TIMING = require('../timing');

async function render(page, scene, ctx) {
  await page.evaluate(s => window.pellicule.showTitleCard(s), scene);
  await ctx.hold(TIMING.title_card, 'title_card');
  await page.evaluate(() => window.pellicule.hideTitleCard());
}

module.exports = { render };

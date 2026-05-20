/**
 * PELLICULE — Terminal GIF scene
 *
 * Embeds an animated .gif (typically a VHS-produced terminal recording)
 * framed in a fake-terminal chrome with a caption. Used to show kitsoki
 * sessions (cloak, oregon-trail, dev-story) as evidence.
 *
 * Spec:
 *   {
 *     "type": "terminal-gif",
 *     "gif":     "demo/cloak.gif",           // path relative to spec file
 *     "title":   "Cloak of Darkness",        // window-bar title
 *     "caption": "Forgiving entrance, deterministic graph", // optional below
 *     "hold":    240                          // frames to display gif
 *   }
 *
 * The renderer base64-encodes the gif and inlines it (so Puppeteer doesn't
 * need a server). `hold` should be tuned per gif so at least one loop plays.
 */

'use strict';

const path = require('path');
const fs   = require('fs');
const TIMING = require('../timing');

async function render(page, scene, ctx) {
  const gifPath = path.resolve(path.dirname(ctx.specPath), scene.gif);
  if (!fs.existsSync(gifPath)) {
    throw new Error(`terminal-gif scene: gif not found: ${gifPath}`);
  }
  const gifData = fs.readFileSync(gifPath).toString('base64');
  const gifDataUri = `data:image/gif;base64,${gifData}`;

  await page.evaluate((s, dataUri) =>
    window.pellicule.showTerminalGif(s, dataUri), scene, gifDataUri);
  await ctx.setState('termgif_frame');
  await ctx.setState('termgif_caption');
  await ctx.hold(scene.hold ?? TIMING.termgif_hold, 'termgif_hold');
  await page.evaluate(() => window.pellicule.hideTerminalGif());
  await ctx.hold(TIMING.inter_scene, 'inter_scene');
}

module.exports = { render };

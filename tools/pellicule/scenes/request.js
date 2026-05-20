/**
 * PELLICULE — Request scene
 *
 * API request/response with progressive reveal. Supports live, mock and
 * playback modes (see runner.js). Extracted unchanged from the original
 * monolithic renderer; preserves cyber-repo demo parity.
 *
 * Spec:
 *   {
 *     "type": "request",
 *     "title": "...", "annotation": "...",
 *     "request":  { "method": "POST", "url": "...", "headers": [...], "body": "..." },
 *     "response": { "statusExpected": "...", "annotation": "...", "annotationType": "error" },
 *     "mock": false, "playback": false,
 *     "expect": { "status": 401 }, "capture": { "token": "$.access_token" }
 *   }
 */

'use strict';

const TIMING = require('../timing');
const { executeRequest, AssertionError } = require('../runner');

async function render(page, scene, ctx) {
  const isMock     = scene.mock     === true;
  const isPlayback = scene.playback === true;
  const skipHttp   = isMock || isPlayback;

  let liveResponse = null;
  if (!skipHttp) {
    if (ctx.onProgress) {
      ctx.onProgress(ctx.frameIndex(), null,
        `http:${scene.request?.method} ${scene.request?.url?.slice(0, 40)}`);
    }
    try {
      liveResponse = await executeRequest(scene, ctx.requestContext);
    } catch (err) {
      if (err instanceof AssertionError) {
        liveResponse = err.liveResponse || {
          status: err.actual, statusText: String(err.actual), headers: [], body: '',
        };
        console.warn(`\n[pellicule] assertion: ${err.message}`);
      } else {
        throw err;
      }
    }
  }

  const displayScene = Object.assign({}, scene);
  if (!skipHttp && liveResponse) {
    displayScene.response = Object.assign({}, scene.response || {}, liveResponse);
    if (ctx.captureLog) {
      ctx.captureLog.push({
        sceneIndex: ctx.sceneIndex,
        title: scene.title || scene.request?.url || `scene-${ctx.sceneIndex}`,
        response: {
          status: liveResponse.status, statusText: liveResponse.statusText,
          headers: liveResponse.headers, body: liveResponse.body,
        },
      });
    }
  }

  await page.evaluate(
    (s, opts) => window.pellicule.loadScene(s, opts),
    displayScene,
    { isMock, isPlayback },
  );

  // Brief blank gap between scenes
  await page.evaluate(() => window.pellicule.setState('blank'));
  await ctx.hold(TIMING.inter_scene, 'inter_scene');

  // Progressive reveal
  await ctx.setState('scene_header');
  await ctx.setState('request_url');
  await ctx.setState('request_headers');
  await ctx.setState('request_body');

  // Sending animation
  await page.evaluate(() => window.pellicule.setState('sending'));
  const ticks = TIMING.sending_ticks;
  for (let t = 1; t <= ticks; t++) {
    const pct  = Math.round((t / ticks) * 100);
    const dots = '.'.repeat(t);
    await page.evaluate((p, d) => {
      window.pellicule.setProgress(p);
      window.pellicule.setSendingText(`Sending${d}`);
    }, pct, dots);
    await ctx.hold(TIMING.sending_per_tick, `sending_${t}`);
  }

  // Response reveal
  await ctx.setState('response_status');
  await ctx.setState('response_headers');
  await ctx.setState('response_body');
  await ctx.setState('response_annotation');

  await page.evaluate(() => window.pellicule.setState('complete'));
  await ctx.hold(TIMING.complete_hold, 'complete_hold');
}

module.exports = { render };

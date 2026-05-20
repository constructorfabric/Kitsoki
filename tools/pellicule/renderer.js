/**
 * PELLICULE — Frame Renderer (scene-type dispatcher)
 *
 * Drives a Puppeteer browser through each scene in the spec, capturing PNG
 * frames that ffmpeg will stitch into a video. Each scene type is a module
 * in scenes/ that owns its visual loading and progressive-reveal sequence;
 * the dispatcher here only manages the page, the frame counter, the timing
 * helpers, and per-scene context.
 *
 * For live `request` scenes, the runner executes the real HTTP request
 * first, then the response is rendered. Mock and playback modes skip the
 * HTTP call. See scenes/request.js.
 *
 * Visual look:   template.html (CSS-only).
 * Animation pacing: timing.js (frames per state name).
 */

'use strict';

const puppeteer = require('puppeteer');
const path      = require('path');
const fs        = require('fs');
const TIMING    = require('./timing');

const TEMPLATE_PATH = path.resolve(__dirname, 'template.html');

const SCENE_MODULES = {
  title:          require('./scenes/title'),
  request:        require('./scenes/request'),
  narrative:      require('./scenes/narrative'),
  diagram:        require('./scenes/diagram'),
  'diagram-svg':  require('./scenes/diagram-svg'),
  'terminal-gif': require('./scenes/terminal-gif'),
  trace:          require('./scenes/trace'),
  stat:           require('./scenes/stat'),
  cta:            require('./scenes/cta'),
};

/**
 * Render every scene in `spec` to PNG frames inside `framesDir`.
 *
 * @param {object}   spec            - Parsed spec (.demo.json / .pitch.json)
 * @param {string}   framesDir       - Directory for frame-NNNNNN.png
 * @param {number}   fps             - Target frames per second
 * @param {function} onProgress      - Optional callback(frameIndex, total, label)
 * @param {string}   captureLogPath  - Optional path for live-response capture log
 * @param {string}   specPath        - Absolute path to the spec (used to resolve
 *                                     relative asset paths like gif/audio files)
 * @returns {Promise<number>} Total frames written
 */
async function generateFrames(spec, framesDir, fps = 30, onProgress = null, captureLogPath = null, specPath = null) {
  const { width = 1920, height = 1080 } = (spec.meta && spec.meta.resolution) || {};
  const mode = (spec.meta && spec.meta.mode) || 'api';  // 'api' | 'pitch'

  // Shared HTTP context (mutated by request-scene captures across scenes)
  const requestContext = Object.assign({}, (spec.meta && spec.meta.context) || {});

  const browser = await puppeteer.launch({
    headless: 'new',
    args: [
      '--no-sandbox', '--disable-setuid-sandbox', '--disable-dev-shm-usage',
      `--window-size=${width},${height}`,
    ],
  });

  let frameIndex = 0;
  const captureLog = [];

  try {
    const page = await browser.newPage();
    await page.setViewport({ width, height, deviceScaleFactor: 1 });
    await page.goto(`file://${TEMPLATE_PATH}`, { waitUntil: 'domcontentloaded' });

    // Apply global metadata + mode (chrome bar visibility, brand, etc.)
    await page.evaluate((meta, m) => {
      window.pellicule.setMeta(meta);
      window.pellicule.setMode(m);
    }, spec.meta || {}, mode);

    const framePath = n => path.join(framesDir, `frame-${String(n).padStart(6, '0')}.png`);

    const hold = async (n, label = '') => {
      for (let i = 0; i < n; i++) {
        await page.screenshot({ path: framePath(frameIndex) });
        if (onProgress) onProgress(frameIndex, null, label);
        frameIndex++;
      }
    };

    const setState = async stepName => {
      await page.evaluate(s => window.pellicule.setState(s), stepName);
      const frames = TIMING[stepName] ?? 20;
      await hold(frames, stepName);
    };

    // Per-scene rendering loop
    for (let sceneIndex = 0; sceneIndex < (spec.scenes || []).length; sceneIndex++) {
      const scene = spec.scenes[sceneIndex];
      const mod   = SCENE_MODULES[scene.type];
      if (!mod) {
        throw new Error(
          `[pellicule] unknown scene type "${scene.type}" at scenes[${sceneIndex}]. ` +
          `Known types: ${Object.keys(SCENE_MODULES).join(', ')}.`
        );
      }

      const ctx = {
        sceneIndex,
        specPath: specPath || process.cwd(),
        hold, setState,
        frameIndex: () => frameIndex,
        onProgress,
        requestContext,
        captureLog: captureLogPath ? captureLog : null,
      };

      await mod.render(page, scene, ctx);
    }

  } finally {
    await browser.close();
  }

  if (captureLogPath && captureLog.length > 0) {
    fs.writeFileSync(captureLogPath, JSON.stringify(captureLog, null, 2), 'utf-8');
    console.log(`[pellicule] Capture log written: ${captureLogPath} (${captureLog.length} live scenes)`);
  }

  return frameIndex;
}

module.exports = { generateFrames };

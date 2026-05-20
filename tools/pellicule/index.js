#!/usr/bin/env node
/**
 * PELLICULE — Deterministic API Video Generator
 *
 * Usage:
 *   node index.js <input.demo.json> <output.mp4> [options]
 *
 * Options:
 *   --fps <n>              Frames per second (default: 30)
 *   --context key=val      Set/override a template variable (repeatable)
 *                          e.g. --context host=stand.example.com --context token=abc123
 *   --keep-frames          Keep the temporary frames directory (useful for debugging)
 *   --frames-dir <p>       Write frames to <p> instead of a temp dir
 *   --capture-log <file>   Write live HTTP responses to a JSON capture log (for playback freeze)
 *
 * Input format: .demo.json  (see examples/ for full schema)
 */

'use strict';

const path = require('path');
const fs   = require('fs');
const os   = require('os');

const { generateFrames } = require('./renderer');
const { framesToVideo }  = require('./assembler');

// ── CLI ────────────────────────────────────────────────────────────────────

const args = process.argv.slice(2);

if (args.length < 2 || args.includes('--help') || args.includes('-h')) {
  console.log([
    '',
    '  PELLICULE — Deterministic API Video Generator',
    '',
    '  Usage:',
    '    node index.js <input.demo.json> <output.mp4> [options]',
    '',
    '  Options:',
    '    --fps <n>                  Frames per second (default: 30)',
    '    --context key=value        Override a template variable (repeatable)',
    '    --keep-frames              Keep temp frame directory after render',
    '    --frames-dir <path>        Use this directory for frames instead of a temp dir',
    '    --capture-log <file>       Write live HTTP responses to JSON (for playback freeze)',
    '',
    '  Examples:',
    '    node index.js examples/vp-5623-mock.demo.json out.mp4',
    '    node index.js examples/vp-5623.demo.json out.mp4 --context host=stand.example.com',
    '    node index.js examples/pltfrm-87475.demo.json out.mp4 \\',
    '        --context host=stand.example.com --context token=eyJ...',
    '',
    '  Live mode     (mock/playback omitted): real HTTP request made, response rendered.',
    '  Mock mode     (mock: true):     synthetic response in JSON, MOCK badge shown.',
    '  Playback mode (playback: true): real captured response in JSON, PLAYBACK badge shown.',
    '',
  ].join('\n'));
  process.exit(args.includes('--help') || args.includes('-h') ? 0 : 1);
}

const [inputPath, outputPath] = args;

const fpsIdx        = args.indexOf('--fps');
const fps           = fpsIdx !== -1 ? parseInt(args[fpsIdx + 1], 10) : 30;
const keepFrames    = args.includes('--keep-frames');
const framesDirIdx  = args.indexOf('--frames-dir');
const framesDirOpt  = framesDirIdx !== -1 ? args[framesDirIdx + 1] : null;
const captureLogIdx = args.indexOf('--capture-log');
const captureLogOpt = captureLogIdx !== -1 ? args[captureLogIdx + 1] : null;

// Parse --context key=value overrides (repeatable)
const cliContext = {};
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--context' && args[i + 1]) {
    const eq = args[i + 1].indexOf('=');
    if (eq !== -1) {
      cliContext[args[i + 1].slice(0, eq)] = args[i + 1].slice(eq + 1);
    }
  }
}

// ── Main ───────────────────────────────────────────────────────────────────

async function main() {
  // Read and validate spec
  const absInput = path.resolve(inputPath);
  if (!fs.existsSync(absInput)) {
    console.error(`[pellicule] ERROR: input file not found: ${absInput}`);
    process.exit(1);
  }

  let spec;
  try {
    spec = JSON.parse(fs.readFileSync(absInput, 'utf-8'));
  } catch (err) {
    console.error(`[pellicule] ERROR: failed to parse JSON: ${err.message}`);
    process.exit(1);
  }

  if (!spec.scenes || !Array.isArray(spec.scenes) || spec.scenes.length === 0) {
    console.error('[pellicule] ERROR: spec must have a non-empty "scenes" array');
    process.exit(1);
  }

  // CLI context overrides take precedence over meta.context in the spec
  if (Object.keys(cliContext).length > 0) {
    spec.meta = spec.meta || {};
    spec.meta.context = Object.assign({}, spec.meta.context || {}, cliContext);
    console.log(`[pellicule] Context overrides: ${JSON.stringify(cliContext)}`);
  }

  // Set up frames directory
  let framesDir;
  let ownFramesDir = false;
  if (framesDirOpt) {
    framesDir = path.resolve(framesDirOpt);
    fs.mkdirSync(framesDir, { recursive: true });
  } else {
    framesDir = fs.mkdtempSync(path.join(os.tmpdir(), 'pellicule-'));
    ownFramesDir = true;
  }

  const captureLogPath = captureLogOpt ? path.resolve(captureLogOpt) : null;

  console.log(`[pellicule] Input  : ${absInput}`);
  console.log(`[pellicule] Output : ${path.resolve(outputPath)}`);
  console.log(`[pellicule] FPS    : ${fps}`);
  console.log(`[pellicule] Frames : ${framesDir}`);
  console.log(`[pellicule] Scenes : ${spec.scenes.length}`);
  if (captureLogPath) console.log(`[pellicule] CaptureLog: ${captureLogPath}`);
  console.log('');

  let frameCount;
  try {
    let lastLabel = '';
    frameCount = await generateFrames(spec, framesDir, fps, (idx, _total, label) => {
      if (label !== lastLabel) {
        process.stdout.write(`\r[pellicule] Rendering: ${label.padEnd(24)}  frame ${idx}`);
        lastLabel = label;
      }
    }, captureLogPath, absInput);
    process.stdout.write('\n');
  } catch (err) {
    console.error(`\n[pellicule] ERROR during rendering: ${err.message}`);
    if (!keepFrames && ownFramesDir) fs.rmSync(framesDir, { recursive: true, force: true });
    process.exit(1);
  }

  console.log(`[pellicule] ${frameCount} frames rendered (${(frameCount / fps).toFixed(1)}s)`);

  // Assemble video
  console.log('[pellicule] Assembling video with ffmpeg…');
  try {
    framesToVideo(framesDir, path.resolve(outputPath), fps);
  } catch (err) {
    console.error(`[pellicule] ERROR during assembly: ${err.message}`);
    if (!keepFrames && ownFramesDir) fs.rmSync(framesDir, { recursive: true, force: true });
    process.exit(1);
  }

  // Cleanup
  if (!keepFrames && ownFramesDir) {
    fs.rmSync(framesDir, { recursive: true, force: true });
  } else if (keepFrames) {
    console.log(`[pellicule] Frames kept at: ${framesDir}`);
  }

  const outStat = fs.statSync(path.resolve(outputPath));
  const sizeMB  = (outStat.size / 1024 / 1024).toFixed(1);
  console.log(`[pellicule] Done → ${path.resolve(outputPath)}  (${sizeMB} MB)`);
}

main().catch(err => {
  console.error('[pellicule] FATAL:', err);
  process.exit(1);
});

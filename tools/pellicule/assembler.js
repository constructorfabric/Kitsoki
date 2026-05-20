/**
 * PELLICULE — Video Assembler
 *
 * Calls the system `ffmpeg` binary to stitch PNG frames into an MP4.
 * Requires ffmpeg ≥ 4.x to be on PATH.
 */

'use strict';

const { execSync, spawnSync } = require('child_process');
const path = require('path');
const fs   = require('fs');

/**
 * Assemble a directory of frame-NNNNNN.png files into an MP4.
 *
 * @param {string} framesDir  - Directory containing frame-*.png files
 * @param {string} outputPath - Destination .mp4 path
 * @param {number} fps        - Frames per second
 */
function framesToVideo(framesDir, outputPath, fps = 30) {
  // Verify ffmpeg is available
  const which = spawnSync('which', ['ffmpeg'], { encoding: 'utf8' });
  if (which.status !== 0) {
    throw new Error('ffmpeg not found on PATH — please install ffmpeg first');
  }

  // Ensure output directory exists
  const outDir = path.dirname(path.resolve(outputPath));
  if (!fs.existsSync(outDir)) fs.mkdirSync(outDir, { recursive: true });

  const framePattern = path.join(framesDir, 'frame-%06d.png');

  // H.264 with yuv420p for maximum compatibility (QuickTime, browsers, Slack)
  // CRF 18 = near-lossless; lower = larger file + better quality
  const args = [
    'ffmpeg', '-y',
    '-framerate', String(fps),
    '-i', framePattern,
    '-c:v', 'libx264',
    '-pix_fmt', 'yuv420p',
    '-crf', '18',
    '-preset', 'slow',
    outputPath,
  ];

  execSync(args.join(' '), { stdio: 'pipe' });
}

module.exports = { framesToVideo };

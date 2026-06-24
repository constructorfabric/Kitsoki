#!/usr/bin/env node
/**
 * Stage recorded demo media from .artifacts/ into the site's gitignored
 * src/public/media/<featureId>/ tree:
 *
 *   demo.mp4            the recorded demo (full variant only)
 *   chapters.json       the <video>.mp4.chapters.json sidecar, when present
 *   poster.png          the feature's posterStep screenshot (else first shot)
 *   steps/NN-<id>.png   every per-step screenshot (full variant only)
 *
 * Videos are NEVER committed — record them with `make demos` (or
 * `make demo-feature FEATURE=<id>`) first. Missing media is a WARNING, never a
 * failure: the site builds docs-only with poster/placeholder fallbacks.
 *
 * --variant embedded  stage posters only (the binary-embedded help build —
 *                     no MP4s in the binary; pages link out to the hosted site).
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(siteDir, "../..");
const mediaDir = path.join(siteDir, "src", "public", "media");
const indexPath = path.join(siteDir, ".vitepress", "gen", "features-index.json");

const embedded = process.argv.includes("--variant")
  ? process.argv[process.argv.indexOf("--variant") + 1] === "embedded"
  : false;

if (!fs.existsSync(indexPath)) {
  console.error(`stage-media: ${path.relative(repoRoot, indexPath)} missing — run: make site-data`);
  process.exit(1);
}
const index = JSON.parse(fs.readFileSync(indexPath, "utf8"));

fs.rmSync(mediaDir, { recursive: true, force: true });

let videos = 0;
const missing = [];
for (const f of index.features) {
  if (!f.demo) continue;
  if (f.demo.external) continue;
  const srcDir = path.join(repoRoot, f.demo.artifactDir);
  const out = path.join(mediaDir, f.id);

  const shots = fs.existsSync(srcDir)
    ? fs.readdirSync(srcDir).filter((n) => /^\d+-.+\.png$/.test(n)).sort()
    : [];
  const posterShot = f.demo.posterStep
    ? shots.find((n) => n.endsWith(`-${f.demo.posterStep}.png`)) ?? shots[0]
    : shots[0];

  if (posterShot) {
    fs.mkdirSync(out, { recursive: true });
    fs.copyFileSync(path.join(srcDir, posterShot), path.join(out, "poster.png"));
  }

  if (embedded) {
    if (!posterShot) missing.push(`${f.id}: no screenshots (poster unavailable)`);
    continue;
  }

  if (shots.length > 0) {
    fs.mkdirSync(path.join(out, "steps"), { recursive: true });
    for (const n of shots) fs.copyFileSync(path.join(srcDir, n), path.join(out, "steps", n));
  }

  const video = path.join(repoRoot, f.demo.video);
  if (fs.existsSync(video)) {
    fs.mkdirSync(out, { recursive: true });
    fs.copyFileSync(video, path.join(out, "demo.mp4"));
    videos++;
    const chapters = path.join(repoRoot, f.demo.chapters);
    if (fs.existsSync(chapters)) fs.copyFileSync(chapters, path.join(out, "chapters.json"));
  } else {
    missing.push(`${f.id}: ${f.demo.video} (record with: make demo-feature FEATURE=${f.id})`);
  }
}

console.log(
  `stage-media: staged ${videos} video(s)${embedded ? " [embedded: posters only]" : ""} -> ${path.relative(repoRoot, mediaDir)}`,
);
for (const m of missing) console.warn(`stage-media: missing ${m}`);

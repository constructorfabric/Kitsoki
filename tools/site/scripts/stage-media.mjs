#!/usr/bin/env node
/**
 * Stage captured demo media from .artifacts/ into the site's gitignored
 * src/public/media/<featureId>/ tree:
 *
 *   demo.rrweb.json     the rrweb replay, for rrweb-first demos
 *   demo.html           the bundled Slidey replay viewer, for rrweb-first demos
 *   demo.mp4            the legacy fallback video (full variant only)
 *   chapters.json       the <video>.mp4.chapters.json sidecar, when present
 *   poster.png          the feature's posterStep screenshot (else first shot)
 *   steps/NN-<id>.png   every per-step screenshot (full variant only)
 *
 * Generated media is NEVER committed — capture it with `make demos` or
 * `make demo-feature-rrweb FEATURE=<id>` first. Missing media is a WARNING,
 * never a failure: the site builds docs-only with poster/placeholder fallbacks.
 *
 * A rrweb-native story-demo (`demo.embed` in features/*.yaml) has no mp4 at
 * all — its committed, pre-bundled Slidey deck html (docs/decks/bundled/) is
 * copied verbatim into the shared src/public/deck-viewers/<file>.html tree instead
 * (several features can point at the same bundled deck, each at its own
 * `?scene=` index — see .vitepress/data/features.ts). Unlike mp4s this asset
 * IS committed source, so a missing one is a broken reference, not "not yet
 * captured" — see check-media.mjs's stricter check for that file.
 *
 * --variant embedded  stage posters only (the binary-embedded help build —
 *                     no large replay viewers, MP4s, or deck embeds in the
 *                     binary; pages link out to the hosted site).
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.join(__dirname, ".."));
const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.join(siteDir, "../.."));
const runtimeSiteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? siteDir);
const mediaDir = path.join(runtimeSiteDir, "src", "public", "media");
const deckViewersDir = path.join(runtimeSiteDir, "src", "public", "deck-viewers");
const legacyDecksDir = path.join(runtimeSiteDir, "src", "public", "decks");
const indexPath = path.join(runtimeSiteDir, ".vitepress", "gen", "features-index.json");

const embedded = process.argv.includes("--variant")
  ? process.argv[process.argv.indexOf("--variant") + 1] === "embedded"
  : process.env.SITE_VARIANT === "embedded";

if (!fs.existsSync(indexPath)) {
  console.error(`stage-media: ${path.relative(repoRoot, indexPath)} missing — run: make site-data`);
  process.exit(1);
}
const index = JSON.parse(fs.readFileSync(indexPath, "utf8"));

function makeWritableIfExists(file) {
  try {
    const mode = fs.statSync(file).mode;
    fs.chmodSync(file, mode | 0o200);
  } catch (err) {
    if (err?.code !== "ENOENT") throw err;
  }
}

function copyStagedFile(src, dest) {
  makeWritableIfExists(dest);
  fs.copyFileSync(src, dest);
}

fs.rmSync(mediaDir, { recursive: true, force: true });
fs.rmSync(deckViewersDir, { recursive: true, force: true });
fs.rmSync(legacyDecksDir, { recursive: true, force: true });

function stageDeckViewers() {
  if (embedded) return 0;
  const bundledDir = path.join(repoRoot, "docs", "decks", "bundled");
  if (!fs.existsSync(bundledDir)) return 0;
  const bundles = fs.readdirSync(bundledDir).filter((name) => name.endsWith(".html")).sort();
  if (bundles.length === 0) return 0;
  fs.mkdirSync(deckViewersDir, { recursive: true });
  for (const name of bundles) copyStagedFile(path.join(bundledDir, name), path.join(deckViewersDir, name));
  return bundles.length;
}

let videos = 0;
let replays = 0;
let embeds = 0;
const deckViewers = stageDeckViewers();
const missing = [];
for (const f of index.features) {
  if (!f.demo) continue;
  if (f.demo.external) continue;

  // demo.embed: a rrweb-native story-demo ships a pre-bundled, committed,
  // self-contained Slidey deck html (docs/decks/bundled/) instead of an mp4.
  // Staged once, shared, under src/public/deck-viewers/ — several features can point
  // at the same bundled deck at different `?scene=` indices. Excluded from the
  // embedded (binary /help/) variant, same as large media exports — it's a ~17MB asset and
  // the binary build stays posters-only; the embedded build's placeholder
  // links out to the hosted site instead.
  if (f.demo.embed) {
    if (embedded) continue;
    const src = path.join(repoRoot, f.demo.embed.deckHtml);
    if (fs.existsSync(src)) {
      embeds++;
    } else {
      missing.push(`${f.id}: ${f.demo.embed.deckHtml} (bundle it once: slidey bundle <deck> ${f.demo.embed.deckHtml})`);
    }
    continue;
  }

  const srcDir = path.join(repoRoot, f.demo.artifactDir);
  const out = path.join(mediaDir, f.id);

  const shotDir =
    fs.existsSync(srcDir) && fs.readdirSync(srcDir).some((n) => /^\d+-.+\.png$/.test(n))
      ? srcDir
      : path.join(srcDir, "baseline-frames");
  const shots = fs.existsSync(shotDir)
    ? fs.readdirSync(shotDir).filter((n) => /^\d+-.+\.png$/.test(n)).sort()
    : [];
  const posterShot = f.demo.posterStep
    ? shots.find((n) => n.endsWith(`-${f.demo.posterStep}.png`)) ?? shots[0]
    : shots[0];

  if (posterShot) {
    fs.mkdirSync(out, { recursive: true });
    copyStagedFile(path.join(shotDir, posterShot), path.join(out, "poster.png"));
  }

  if (embedded) {
    if (!posterShot) missing.push(`${f.id}: no screenshots (poster unavailable)`);
    continue;
  }

  if (shots.length > 0) {
    fs.mkdirSync(path.join(out, "steps"), { recursive: true });
    for (const n of shots) copyStagedFile(path.join(shotDir, n), path.join(out, "steps", n));
  }

  if ((f.demo.format ?? "rrweb") === "rrweb") {
    const rrweb = path.join(repoRoot, f.demo.rrweb);
    const viewer = path.join(repoRoot, f.demo.rrwebViewer);
    if (fs.existsSync(rrweb) && fs.existsSync(viewer)) {
      fs.mkdirSync(out, { recursive: true });
      copyStagedFile(rrweb, path.join(out, "demo.rrweb.json"));
      copyStagedFile(viewer, path.join(out, "demo.html"));
      replays++;
      const chapters = path.join(repoRoot, f.demo.rrwebChapters);
      if (fs.existsSync(chapters)) copyStagedFile(chapters, path.join(out, "chapters.json"));
    } else {
      if (!fs.existsSync(rrweb)) missing.push(`${f.id}: ${f.demo.rrweb} (capture with: make demo-feature-rrweb FEATURE=${f.id})`);
      if (!fs.existsSync(viewer)) missing.push(`${f.id}: ${f.demo.rrwebViewer} (bundle with: make demo-feature-rrweb FEATURE=${f.id})`);
    }
    continue;
  }

  const video = path.join(repoRoot, f.demo.video);
  if (fs.existsSync(video)) {
    fs.mkdirSync(out, { recursive: true });
    copyStagedFile(video, path.join(out, "demo.mp4"));
    videos++;
    const chapters = path.join(repoRoot, f.demo.chapters);
    if (fs.existsSync(chapters)) copyStagedFile(chapters, path.join(out, "chapters.json"));
  } else {
    missing.push(`${f.id}: ${f.demo.video} (legacy capture with: make demo-feature FEATURE=${f.id})`);
  }
}

console.log(
  `stage-media: staged ${replays} rrweb replay(s), ${videos} fallback video(s), ${embeds} feature deck embed(s), ${deckViewers} deck viewer(s)${embedded ? " [embedded: posters only]" : ""} -> ${path.relative(repoRoot, mediaDir)}`,
);
for (const m of missing) console.warn(`stage-media: missing ${m}`);

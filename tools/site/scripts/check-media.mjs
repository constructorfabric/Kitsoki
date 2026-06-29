#!/usr/bin/env node
/**
 * Fast, no-LLM media contract check for the product site and Slidey deck embeds.
 *
 * This does not require demo videos to exist and never records anything. It
 * validates the contracts that keep generated media organized:
 *   - feature demos derive their source paths from the feature catalog index;
 *   - staged site media uses only the generated public/media/<feature>/ shape;
 *   - Slidey decks reference rrweb clips from a deck-local asset folder.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(siteDir, "../..");

function argValue(name, fallback) {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
}

const indexPath = path.resolve(
  repoRoot,
  argValue("--index", path.join(siteDir, ".vitepress", "gen", "features-index.json")),
);
const mediaDir = path.resolve(repoRoot, argValue("--media", path.join(siteDir, "src", "public", "media")));
const decksDir = path.resolve(repoRoot, argValue("--decks", path.join(repoRoot, "docs", "decks")));

const problems = [];
const warnings = [];

function rel(p) {
  return path.relative(repoRoot, p) || ".";
}

function inside(child, parent) {
  const r = path.relative(parent, child);
  return r === "" || (!r.startsWith("..") && !path.isAbsolute(r));
}

function readJson(file, label) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (e) {
    problems.push(`${label}: cannot parse ${rel(file)}: ${e instanceof Error ? e.message : e}`);
    return null;
  }
}

function checkFeatureDemos() {
  if (!fs.existsSync(indexPath)) {
    problems.push(`feature media index missing: ${rel(indexPath)} (run make site-data or make features-index)`);
    return new Set();
  }

  const index = readJson(indexPath, "feature index");
  if (!index) return new Set();

  const idsWithMedia = new Set();
  const ids = new Set();
  for (const f of index.features ?? []) {
    ids.add(f.id);
    if (!f.demo || f.demo.external) continue;
    idsWithMedia.add(f.id);

    const artifactDir = path.resolve(repoRoot, f.demo.artifactDir);
    if (!inside(artifactDir, path.join(repoRoot, ".artifacts"))) {
      problems.push(`${f.id}: demo.artifactDir must live under .artifacts, got ${f.demo.artifactDir}`);
    }

    if (f.demo.video && !f.demo.video.endsWith(".mp4")) {
      problems.push(`${f.id}: demo.video must be an mp4, got ${f.demo.video}`);
    }
    if (f.demo.video && !inside(path.resolve(repoRoot, f.demo.video), artifactDir)) {
      problems.push(`${f.id}: demo.video must live inside demo.artifactDir (${f.demo.video})`);
    }
    if (f.demo.chapters !== `${f.demo.video}.chapters.json`) {
      problems.push(`${f.id}: demo.chapters must be demo.video + .chapters.json`);
    }
    if (f.demo.screenshotPattern !== "NN-<stepId>.png") {
      problems.push(`${f.id}: screenshotPattern must stay NN-<stepId>.png`);
    }

    for (const [profile, variant] of Object.entries(f.demo.variants ?? {})) {
      if (!variant.video.endsWith(".mp4")) {
        problems.push(`${f.id}[${profile}]: variant video must be an mp4`);
      }
      if (variant.chapters !== `${variant.video}.chapters.json`) {
        problems.push(`${f.id}[${profile}]: variant chapters must be variant.video + .chapters.json`);
      }
      if (!inside(path.resolve(repoRoot, variant.video), artifactDir)) {
        problems.push(`${f.id}[${profile}]: variant video must live inside demo.artifactDir`);
      }
    }

    if (f.demo.renderer === "playwright" && f.demo.spec && !fs.existsSync(path.resolve(repoRoot, f.demo.spec))) {
      problems.push(`${f.id}: demo spec path does not exist: ${f.demo.spec}`);
    }
  }

  checkStagedMedia(idsWithMedia);
  return ids;
}

function checkStagedMedia(idsWithMedia) {
  if (!fs.existsSync(mediaDir)) {
    warnings.push(`staged site media absent: ${rel(mediaDir)} (ok before make site/stage-media)`);
    return;
  }

  for (const dirent of fs.readdirSync(mediaDir, { withFileTypes: true })) {
    if (!dirent.isDirectory()) {
      problems.push(`unexpected file in staged media root: ${rel(path.join(mediaDir, dirent.name))}`);
      continue;
    }
    const id = dirent.name;
    const dir = path.join(mediaDir, id);
    if (!idsWithMedia.has(id)) {
      problems.push(`staged media directory has no non-external feature demo: ${rel(dir)}`);
    }
    for (const file of fs.readdirSync(dir)) {
      const p = path.join(dir, file);
      if (file === "demo.mp4" || file === "chapters.json" || file === "poster.png" || file === "steps") continue;
      problems.push(`unexpected staged media file: ${rel(p)}`);
    }
    const demo = path.join(dir, "demo.mp4");
    const chapters = path.join(dir, "chapters.json");
    if (fs.existsSync(demo) && !fs.existsSync(chapters)) {
      problems.push(`${rel(dir)}: demo.mp4 is staged without chapters.json`);
    }
    const steps = path.join(dir, "steps");
    if (fs.existsSync(steps)) {
      for (const shot of fs.readdirSync(steps)) {
        if (!/^\d+-.+\.png$/.test(shot)) {
          problems.push(`${rel(steps)}: unexpected step screenshot name ${shot}`);
        }
      }
    }
  }
}

function collectStrings(v, out = []) {
  if (typeof v === "string") out.push(v);
  else if (Array.isArray(v)) for (const item of v) collectStrings(item, out);
  else if (v && typeof v === "object") for (const item of Object.values(v)) collectStrings(item, out);
  return out;
}

function checkSlideyDeckEmbeds() {
  if (!fs.existsSync(decksDir)) return;
  for (const name of fs.readdirSync(decksDir).filter((n) => n.endsWith(".json") || n.endsWith(".slidey.json")).sort()) {
    const deckPath = path.join(decksDir, name);
    const deck = readJson(deckPath, "deck");
    if (!deck) continue;
    const deckId = name.replace(/\.slidey\.json$/, "").replace(/\.json$/, "");
    const allowedAssetDir = path.join(decksDir, "assets", deckId);
    for (const s of collectStrings(deck).filter((v) => v.endsWith(".rrweb.json"))) {
      const clip = path.resolve(path.dirname(deckPath), s);
      if (!fs.existsSync(clip)) {
        problems.push(`${rel(deckPath)}: rrweb clip does not exist: ${s}`);
      }
      if (!inside(clip, allowedAssetDir)) {
        problems.push(
          `${rel(deckPath)}: rrweb clip ${s} must live under ${rel(allowedAssetDir)} (deck-local assets)`,
        );
      }
    }
  }
}

checkFeatureDemos();
checkSlideyDeckEmbeds();

for (const w of warnings) console.warn(`media: warning: ${w}`);
if (problems.length > 0) {
  for (const p of problems) console.error(`media: ${p}`);
  console.error(`media: ${problems.length} problem(s)`);
  process.exit(1);
}

console.log(`media: OK — feature demos, staged site media, and Slidey rrweb deck embeds are organized`);

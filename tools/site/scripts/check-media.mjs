#!/usr/bin/env node
/**
 * Fast, no-LLM media contract check for the product site and Slidey deck embeds.
 *
 * This does not require demo media to exist and never captures anything. It
 * validates the contracts that keep generated media organized:
 *   - feature demos derive their source paths from the feature catalog index;
 *   - staged site media uses only the generated public/media/<feature>/ shape;
 *   - Slidey decks reference rrweb clips from a deck-local asset folder;
 *   - every top-level Slidey deck has a committed bundled viewer for /decks/;
 *   - a rrweb-native story-demo's `demo.embed` points at a committed, bundled
 *     Slidey deck html under docs/decks/bundled/ with a valid scene index.
 *
 * --require-promo-media additionally turns this into a PRESENCE gate (run
 * AFTER capture + staging, never behind continue-on-error): every
 * promo-grid feature (`promo:` block in its features/<id>.yaml — the
 * FeatureGrid/HeroDemo landing-page set) must have its declared media staged
 * under src/public/media/<id>/, or the check fails. Non-promo features
 * missing media only warn — plenty of features are demo-less by design.
 * Without the flag, this script never fails on missing media (0 demos passes),
 * which keeps docs-only local iteration and `make media-check` unchanged.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.join(siteDir, "../.."));
const runtimeSiteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? siteDir);

function argValue(name, fallback) {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
}

const indexPath = path.resolve(
  repoRoot,
  argValue("--index", path.join(siteDir, ".vitepress", "gen", "features-index.json")),
);
const mediaDir = path.resolve(repoRoot, argValue("--media", path.join(runtimeSiteDir, "src", "public", "media")));
const decksDir = path.resolve(repoRoot, argValue("--decks", path.join(repoRoot, "docs", "decks")));
const deckViewersDir = path.resolve(
  repoRoot,
  argValue("--deck-viewers", path.join(runtimeSiteDir, "src", "public", "deck-viewers")),
);

const requirePromoMedia = process.argv.includes("--require-promo-media");

const problems = [];
const warnings = [];

/**
 * Known legacy demo.renderer -> primary staged media filename. rrweb-first
 * demos use `demo.html`; explicit fallback video renderers still normalize to
 * `demo.mp4`. Extend this table (or set an explicit `demo.mediaKind` in the
 * feature YAML, checked first below) when a new renderer/embed kind ships its
 * own staged-media shape.
 */
const RENDERER_MEDIA = {
  playwright: "demo.mp4",
  binary: "demo.mp4",
};

/** The staged filename a feature's demo should produce, or null if the
 * renderer/kind is unrecognized (in which case presence is checked
 * loosely — any staged file besides poster.png counts). */
function expectedMediaFile(f) {
  if (f.demo?.mediaKind) return f.demo.mediaKind;
  if ((f.demo?.format ?? "rrweb") === "rrweb") return "demo.html";
  const renderer = f.demo?.renderer ?? "playwright";
  return Object.prototype.hasOwnProperty.call(RENDERER_MEDIA, renderer) ? RENDERER_MEDIA[renderer] : null;
}

function captureCommand(f) {
  return (f.demo?.format ?? "rrweb") === "rrweb"
    ? `make demo-feature-rrweb FEATURE=${f.id}`
    : `make demo-feature FEATURE=${f.id}`;
}

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

    // demo.embed: a rrweb-native story-demo, permanently mp4-less. Its
    // pre-bundled deck html is COMMITTED source (docs/decks/bundled/, unlike
    // an mp4 that's a live capture), so a missing one is a broken reference,
    // not "not yet captured" — it's checked here, not warned about in
    // stage-media. It's staged into the shared src/public/deck-viewers/ tree, not
    // media/<id>/, so it's exempt from idsWithMedia / checkStagedMedia below.
    if (f.demo.embed) {
      const deckHtml = path.resolve(repoRoot, f.demo.embed.deckHtml);
      const bundledDir = path.join(repoRoot, "docs", "decks", "bundled");
      if (!fs.existsSync(deckHtml)) {
        problems.push(`${f.id}: demo.embed.deckHtml does not exist: ${f.demo.embed.deckHtml}`);
      } else if (!inside(deckHtml, bundledDir)) {
        problems.push(`${f.id}: demo.embed.deckHtml must live under docs/decks/bundled/, got ${f.demo.embed.deckHtml}`);
      }
      if (!Number.isInteger(f.demo.embed.sceneIndex) || f.demo.embed.sceneIndex < 0) {
        problems.push(`${f.id}: demo.embed.sceneIndex must be a non-negative integer`);
      }
      if (f.demo.renderer === "playwright" && f.demo.spec && !fs.existsSync(path.resolve(repoRoot, f.demo.spec))) {
        problems.push(`${f.id}: demo spec path does not exist: ${f.demo.spec}`);
      }
      continue;
    }

    idsWithMedia.add(f.id);

    const artifactDir = path.resolve(repoRoot, f.demo.artifactDir);
    if (!inside(artifactDir, path.join(repoRoot, ".artifacts"))) {
      problems.push(`${f.id}: demo.artifactDir must live under .artifacts, got ${f.demo.artifactDir}`);
    }

    const format = f.demo.format ?? "rrweb";
    if (format !== "rrweb") {
      problems.push(`${f.id}: product-site demos must be rrweb-first; legacy MP4 exports should not be catalog media`);
    }
    if (format === "rrweb") {
      if (!f.demo.rrweb?.endsWith(".rrweb.json")) {
        problems.push(`${f.id}: demo.rrweb must be a .rrweb.json path, got ${f.demo.rrweb}`);
      }
      if (f.demo.rrweb && !inside(path.resolve(repoRoot, f.demo.rrweb), artifactDir)) {
        problems.push(`${f.id}: demo.rrweb must live inside demo.artifactDir (${f.demo.rrweb})`);
      }
      if (!f.demo.rrwebViewer?.endsWith(".html")) {
        problems.push(`${f.id}: demo.rrwebViewer must be an html path, got ${f.demo.rrwebViewer}`);
      }
      if (f.demo.rrwebViewer && !inside(path.resolve(repoRoot, f.demo.rrwebViewer), artifactDir)) {
        problems.push(`${f.id}: demo.rrwebViewer must live inside demo.artifactDir (${f.demo.rrwebViewer})`);
      }
      if (f.demo.rrwebChapters !== `${f.demo.rrweb}.chapters.json`) {
        problems.push(`${f.id}: demo.rrwebChapters must be demo.rrweb + .chapters.json`);
      }
    } else {
      if (f.demo.video && !f.demo.video.endsWith(".mp4")) {
        problems.push(`${f.id}: demo.video must be an mp4, got ${f.demo.video}`);
      }
      if (f.demo.video && !inside(path.resolve(repoRoot, f.demo.video), artifactDir)) {
        problems.push(`${f.id}: demo.video must live inside demo.artifactDir (${f.demo.video})`);
      }
      if (f.demo.chapters !== `${f.demo.video}.chapters.json`) {
        problems.push(`${f.id}: demo.chapters must be demo.video + .chapters.json`);
      }
    }
    if (f.demo.screenshotPattern !== "NN-<stepId>.png") {
      problems.push(`${f.id}: screenshotPattern must stay NN-<stepId>.png`);
    }

    if (format !== "rrweb") {
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
    }

    if (f.demo.renderer === "playwright" && f.demo.spec && !fs.existsSync(path.resolve(repoRoot, f.demo.spec))) {
      problems.push(`${f.id}: demo spec path does not exist: ${f.demo.spec}`);
    }
    if (f.demo.rrwebSpec && !fs.existsSync(path.resolve(repoRoot, f.demo.rrwebSpec))) {
      problems.push(`${f.id}: demo rrweb spec path does not exist: ${f.demo.rrwebSpec}`);
    }
  }

  checkStagedMedia(idsWithMedia);
  if (requirePromoMedia) checkPromoMedia(index);
  return ids;
}

/**
 * The presence gate: every promo-grid feature (has a `promo:` block — the
 * FeatureGrid/HeroDemo landing-page set, see tools/site/.vitepress/data/
 * features.ts) must have its declared media actually staged. A promo feature
 * with no local demo binding at all (missing/external) is itself a problem —
 * it can never show media on the landing page. Non-promo features missing
 * media only warn: most features are optional/undemoed by design.
 */
function checkPromoMedia(index) {
  const nonPromoMissing = [];

  for (const f of index.features ?? []) {
    const isPromo = Boolean(f.promo);
    const dir = path.join(mediaDir, f.id);

    if (!f.demo || f.demo.external) {
      if (isPromo) {
        problems.push(`promo feature ${f.id}: has a promo: block but no local demo binding (demo missing or external)`);
      }
      continue;
    }

    const posterOk = fs.existsSync(path.join(dir, "poster.png"));
    const primary = expectedMediaFile(f);
    const primaryOk = primary
      ? fs.existsSync(path.join(dir, primary))
      : fs.existsSync(dir) && fs.readdirSync(dir).some((n) => n !== "poster.png" && n !== "chapters.json");

    const missing = [];
    if (!posterOk) missing.push("poster.png");
    if (!primaryOk) missing.push(primary ?? "renderer-appropriate media");
    if (missing.length === 0) continue;

    if (isPromo) {
      problems.push(
        `promo feature ${f.id}: missing staged ${missing.join(" + ")} under ${rel(dir)} ` +
          `(capture with: ${captureCommand(f)}, then make site)`,
      );
    } else {
      nonPromoMissing.push(`${f.id} (missing ${missing.join(" + ")})`);
    }
  }

  if (nonPromoMissing.length > 0) {
    warnings.push(`non-promo feature(s) missing staged media: ${nonPromoMissing.join(", ")}`);
  }
}

function checkStagedMedia(idsWithMedia) {
  if (!fs.existsSync(mediaDir)) {
    if (requirePromoMedia) {
      problems.push(`staged site media directory missing entirely: ${rel(mediaDir)} (run make site/stage-media before --require-promo-media)`);
    } else {
      warnings.push(`staged site media absent: ${rel(mediaDir)} (ok before make site/stage-media)`);
    }
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
      if (file === "demo.mp4" || file === "demo.rrweb.json" || file === "demo.html" || file === "chapters.json" || file === "poster.png" || file === "steps") continue;
      problems.push(`unexpected staged media file: ${rel(p)}`);
    }
    const demo = path.join(dir, "demo.mp4");
    const chapters = path.join(dir, "chapters.json");
    if (fs.existsSync(demo) && !fs.existsSync(chapters)) {
      problems.push(`${rel(dir)}: demo.mp4 is staged without chapters.json`);
    }
    const replay = path.join(dir, "demo.rrweb.json");
    const viewer = path.join(dir, "demo.html");
    if (fs.existsSync(viewer) && !fs.existsSync(replay)) {
      problems.push(`${rel(dir)}: demo.html is staged without demo.rrweb.json`);
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
  const deckIds = new Set();
  for (const name of fs.readdirSync(decksDir).filter((n) => n.endsWith(".json") || n.endsWith(".slidey.json")).sort()) {
    const deckPath = path.join(decksDir, name);
    const deck = readJson(deckPath, "deck");
    if (!deck) continue;
    const deckId = name.replace(/\.slidey\.json$/, "").replace(/\.json$/, "");
    deckIds.add(deckId);
    const bundlePath = path.join(decksDir, "bundled", `${deckId}.html`);
    if (!fs.existsSync(bundlePath)) {
      problems.push(`${rel(deckPath)}: missing bundled product-site viewer ${rel(bundlePath)} (run: slidey bundle ${rel(deckPath)} ${rel(bundlePath)})`);
    } else if (fs.statSync(bundlePath).size === 0) {
      problems.push(`${rel(deckPath)}: bundled product-site viewer is empty: ${rel(bundlePath)}`);
    }
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

  const bundledDir = path.join(decksDir, "bundled");
  if (fs.existsSync(bundledDir)) {
    for (const name of fs.readdirSync(bundledDir).filter((n) => n.endsWith(".html")).sort()) {
      const id = name.replace(/\.html$/, "");
      if (!deckIds.has(id)) {
        problems.push(`${rel(path.join(bundledDir, name))}: bundled viewer has no top-level deck source`);
      }
    }
  }

  if (fs.existsSync(deckViewersDir)) {
    for (const name of fs.readdirSync(deckViewersDir).filter((n) => n.endsWith(".html")).sort()) {
      const id = name.replace(/\.html$/, "");
      if (!deckIds.has(id)) {
        problems.push(`${rel(path.join(deckViewersDir, name))}: staged deck viewer has no top-level deck source`);
      }
    }
    for (const id of deckIds) {
      if (!fs.existsSync(path.join(deckViewersDir, `${id}.html`))) {
        problems.push(`${rel(deckViewersDir)}: missing staged deck viewer ${id}.html (run make site/stage-media)`);
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

console.log(`media: OK — feature demos, staged site media, Slidey deck viewers, and rrweb deck embeds are organized`);

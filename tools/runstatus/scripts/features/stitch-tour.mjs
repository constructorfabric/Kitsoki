/**
 * stitch-tour.mjs — compose a product-tour master video from its section clips.
 *
 *   tsx scripts/features/stitch-tour.mjs <feature-id>   (KITSOKI_DEMO_PROFILE)
 *
 * The per-section recordings (their MP4 + .chapters.json) already exist — this
 * is pure post-processing, no agent/LLM anything. For each section in the
 * catalog it:
 *   1. resolves each clip's source video + chapter sidecar for the active
 *      profile (falling back to desktop, logged);
 *   2. trims the clip to its [startChapterId, endChapterId] window if set;
 *   3. renders a title card from the section's title/body;
 *   4. composes card+clips for every section via concat-videos.sh (the shared
 *      compositor that normalises each segment onto the 1600x900 brand canvas);
 *   5. MERGES the per-section chapter sidecars into ONE master sidecar —
 *      section-prefixed ids, a `group`/`group_label` per section for the 8-group
 *      rail, cumulative offsets (each title card included), and a preserved
 *      `source_ref` (+ source_feature) so the rail can deep-link to the source.
 *
 * concat-videos.sh has no chapter handling; step 5 is this script's job. The
 * output mirrors saveVideoAsMp4's profile suffix, so desktop stays <base>.mp4.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { spawnSync } from "child_process";

const here = path.dirname(fileURLToPath(import.meta.url));
// scripts/features → scripts → runstatus → tools → repo root
const repoRoot = path.resolve(here, "../../../..");
const INDEX = path.join(repoRoot, ".artifacts", "features", "features-index.json");
const CONCAT = path.join(repoRoot, ".agents", "skills", "kitsoki-ui-demo", "scripts", "concat-videos.sh");
const TITLE_CARD = path.join(repoRoot, ".agents", "skills", "kitsoki-ui-demo", "scripts", "make-title-card.mjs");

/** Seconds a section title card holds on screen. */
const CARD_SEC = 3.0;

function die(msg) {
  console.error(`stitch-tour: ${msg}`);
  process.exit(1);
}

/** Keep in lockstep with profileSuffix() in tests/playwright/_helpers/camera.ts. */
function profileSuffix(profile) {
  return profile === "desktop" ? "" : `--${profile}`;
}

/** Video duration in ms via ffprobe; falls back to the last chapter's end. */
function durationMs(file, fallbackMs) {
  const r = spawnSync(
    "ffprobe",
    ["-v", "error", "-show_entries", "format=duration", "-of", "default=nokey=1:noprint_wrappers=1", file],
    { encoding: "utf8" },
  );
  const secs = Number((r.stdout || "").trim());
  if (r.status === 0 && Number.isFinite(secs) && secs > 0) return Math.round(secs * 1000);
  if (fallbackMs != null) return fallbackMs;
  die(`could not probe duration of ${path.relative(repoRoot, file)} (ffprobe status ${r.status})`);
}

function run(cmd, args, label) {
  const r = spawnSync(cmd, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  if (r.status !== 0) {
    die(`${label} failed (exit ${r.status})\n${(r.stderr || r.stdout || "").slice(0, 800)}`);
  }
  return r.stdout || "";
}

const featureId = process.argv[2];
if (!featureId) die("usage: stitch-tour.mjs <feature-id>");
const profile = process.env.KITSOKI_DEMO_PROFILE || "desktop";
const suffix = profileSuffix(profile);

if (!fs.existsSync(INDEX)) die(`${path.relative(repoRoot, INDEX)} missing — run: make features-index`);
const index = JSON.parse(fs.readFileSync(INDEX, "utf8"));
const byId = new Map(index.features.map((f) => [f.id, f]));

const master = byId.get(featureId);
if (!master) die(`no feature "${featureId}" in the index`);
if (!master.sections) die(`feature "${featureId}" has no sections (kind: product-tour?)`);
const masterDemo = master.demo || {
  artifactDir: `.artifacts/${featureId}`,
  video: `.artifacts/${featureId}/${featureId}.mp4`,
};

const outDir = path.join(repoRoot, masterDemo.artifactDir);
fs.mkdirSync(outDir, { recursive: true });
const outVideo = path.join(outDir, `${path.basename(masterDemo.video).replace(/\.mp4$/, "")}${suffix}.mp4`);

const tmp = fs.mkdtempSync(path.join(outDir, ".stitch-"));
const cleanup = () => fs.rmSync(tmp, { recursive: true, force: true });
process.on("exit", cleanup);

/** Resolve a source feature's video + chapters for the active profile (desktop
 *  fallback), as absolute paths. */
function resolveClip(sourceId) {
  const src = byId.get(sourceId);
  if (!src || !src.demo) die(`clip source "${sourceId}" is not a demo-bound feature`);
  const variants = src.demo.variants || {};
  let v = variants[profile];
  if (!v && profile !== "desktop") {
    console.log(`stitch-tour: ${sourceId} has no ${profile} variant — falling back to desktop`);
    v = variants.desktop;
  }
  v = v || { video: src.demo.video, chapters: src.demo.chapters };
  const video = path.join(repoRoot, v.video);
  const chaptersPath = path.join(repoRoot, v.chapters);
  if (!fs.existsSync(video)) die(`source "${sourceId}" video missing: ${v.video} (legacy export: make render-video FEATURE=${sourceId})`);
  if (!fs.existsSync(chaptersPath)) die(`source "${sourceId}" chapters missing: ${v.chapters}`);
  return { src, video, chaptersPath, chapters: JSON.parse(fs.readFileSync(chaptersPath, "utf8")) };
}

/**
 * Prepare one clip: trim to its chapter window if set, returning the segment
 * file plus its chapters rebased to start at 0 and the segment duration (ms).
 */
/** Clamp chapter windows into [0, durMs] and drop any that collapse — so a
 *  chapter never claims time past its (re-encoded, slightly shorter) segment. */
function clampChapters(chapters, durMs) {
  return chapters
    .map((c) => ({ ...c, start_ms: Math.min(c.start_ms, durMs), end_ms: Math.min(c.end_ms, durMs) }))
    .filter((c) => c.end_ms > c.start_ms);
}

function prepareClip(clip, segIdx) {
  const { video, chapters } = resolveClip(clip.source);
  if (!clip.chapters) {
    const durMs = durationMs(video);
    return { file: video, durMs, chapters: clampChapters(chapters, durMs), sourceId: clip.source };
  }
  const [startId, endId] = clip.chapters;
  const start = chapters.find((c) => c.id === startId);
  const end = chapters.find((c) => c.id === endId);
  if (!start) die(`source "${clip.source}" has no chapter "${startId}" (range start)`);
  if (!end) die(`source "${clip.source}" has no chapter "${endId}" (range end)`);
  const winStart = start.start_ms;
  const winEnd = end.end_ms;
  if (winEnd <= winStart) die(`source "${clip.source}" range [${startId},${endId}] is empty/inverted`);
  const trimmed = path.join(tmp, `seg-${segIdx}.mp4`);
  // Re-encode trim (accurate seek): concat-videos re-normalises again, so a
  // stream-copy keyframe approximation here would drift the chapter offsets.
  run("ffmpeg", [
    "-y", "-loglevel", "error", "-i", video,
    "-ss", String(winStart / 1000), "-to", String(winEnd / 1000),
    "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-an", trimmed,
  ], `ffmpeg trim ${clip.source}`);
  // Probe the ACTUAL encoded length (keyframe/encoding makes it differ slightly
  // from the requested window) so cumulative section offsets don't drift, then
  // rebase the in-window chapters to 0 and clamp to that real duration.
  const durMs = durationMs(trimmed, winEnd - winStart);
  const rebased = clampChapters(
    chapters
      .filter((c) => c.end_ms > winStart && c.start_ms < winEnd)
      .map((c) => ({ ...c, start_ms: Math.max(0, c.start_ms - winStart), end_ms: Math.min(winEnd, c.end_ms) - winStart })),
    durMs,
  );
  return { file: trimmed, durMs, chapters: rebased, sourceId: clip.source };
}

// Incremental: skip the re-stitch when the master + sidecar are already newer
// than every input (the index, this script, and each section's source video +
// sidecar). This also validates that every source exists up front. The stitch
// is cheap, so the check stays conservative — KITSOKI_STITCH_FORCE=1 rebuilds.
const sidecarPath = `${outVideo}.chapters.json`;
const inputs = [INDEX, fileURLToPath(import.meta.url)];
for (const section of master.sections) {
  for (const clip of section.clips) {
    const r = resolveClip(clip.source);
    inputs.push(r.video, r.chaptersPath);
  }
}
const newestInput = Math.max(...inputs.map((f) => fs.statSync(f).mtimeMs));
if (
  !process.env.KITSOKI_STITCH_FORCE &&
  fs.existsSync(outVideo) &&
  fs.existsSync(sidecarPath) &&
  fs.statSync(outVideo).mtimeMs >= newestInput
) {
  console.log(`stitch-tour: ${path.relative(repoRoot, outVideo)} is fresh — skipping (KITSOKI_STITCH_FORCE=1 to force)`);
  process.exit(0);
}

// Build segments (card + clips per section) and the merged chapter list.
const segments = [];
const merged = [];
let offsetMs = 0;
let posterCard = null;

for (const section of master.sections) {
  const cardMs = Math.round(CARD_SEC * 1000);
  const card = path.join(tmp, `card-${section.id}.png`);
  run("node", [TITLE_CARD, card, section.title, section.body, "kitsoki — the complete tour"], `title card ${section.id}`);
  if (!posterCard) posterCard = card;
  segments.push(`card:${card}:${CARD_SEC}`);
  // The card itself is the section's first rail entry (clicking it = section start).
  merged.push({
    id: `${section.id}__intro`,
    label: section.title,
    start_ms: offsetMs,
    end_ms: offsetMs + cardMs,
    group: section.id,
    group_label: section.title,
    intro: true, // the section's title card — rendered as the rail's group header
  });
  offsetMs += cardMs;

  section.clips.forEach((clip, clipIdx) => {
    const seg = prepareClip(clip, `${section.id}-${clipIdx}`);
    segments.push(`video:${seg.file}`);
    for (const ch of seg.chapters) {
      merged.push({
        id: `${section.id}__${seg.sourceId}__${ch.id}`,
        label: ch.label,
        start_ms: offsetMs + ch.start_ms,
        end_ms: offsetMs + ch.end_ms,
        group: section.id,
        group_label: section.title,
        source_feature: seg.sourceId,
        ...(ch.source_ref ? { source_ref: ch.source_ref } : {}),
      });
    }
    offsetMs += seg.durMs;
  });
}

// Compose, then re-index the merged sidecar and write it beside the video.
run("bash", [CONCAT, outVideo, ...segments, "--size", "1600x900", "--fps", "30"], "concat-videos");
merged.forEach((c, i) => (c.index = i));
fs.writeFileSync(sidecarPath, JSON.stringify(merged, null, 2) + "\n");

// A poster so the site has a frame even though the master was never "recorded".
if (posterCard) {
  fs.copyFileSync(posterCard, path.join(outDir, "00-cold-open.png"));
}

const totalSec = (offsetMs / 1000).toFixed(1);
console.log(`stitch-tour: ${path.relative(repoRoot, outVideo)}`);
console.log(`stitch-tour: ${path.relative(repoRoot, sidecarPath)} (${merged.length} chapters, ${master.sections.length} sections, ~${totalSec}s)`);

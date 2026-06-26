#!/usr/bin/env node
/*
 * Verify the final live @kitsoki GitHub-agent POC evidence bundle.
 *
 * This is intentionally read-only. It checks the collected markdown evidence,
 * capture plans, recorded rrweb logs, and generated Slidey deck.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const CASES = [
  {
    slug: "bug-issue",
    expectedObjectKind: "issue",
    expectedStory: "stories/bugfix",
    expectedState: "done",
    sourcePathPart: "/issues/",
  },
  {
    slug: "feature-issue",
    expectedObjectKind: "issue",
    expectedStory: "stories/dev-story",
    expectedState: "done",
    sourcePathPart: "/issues/",
  },
  {
    slug: "guidance",
    expectedObjectKind: "issue",
    expectedStory: "",
    expectedState: "awaiting_guidance",
    sourcePathPart: "/issues/",
  },
  {
    slug: "pr-status",
    expectedObjectKind: "pr",
    expectedStory: "pr-beat",
    expectedState: "done",
    sourcePathPart: "/pull/",
  },
];

const STEPS = [
  { id: "github-thread", file: "01-github-thread.rrweb.json", minAnnotations: 4, minReadableZooms: 1 },
  { id: "app-comment", file: "02-app-comment.rrweb.json", minAnnotations: 2, minReadableZooms: 1 },
  { id: "run-page", file: "03-run-page.rrweb.json", minAnnotations: 3, minReadableZooms: 2 },
  { id: "run-api", file: "04-run-api.rrweb.json", minAnnotations: 2, minReadableZooms: 1 },
];

const DEFAULT_EVIDENCE_DIR = ".context";
const DEFAULT_MEDIA_ROOT = ".artifacts/github-agent-live";
const DEFAULT_DECK = ".artifacts/github-agent-live/live-github-agent.slidey.json";

function usage() {
  console.error(`usage: scripts/verify-gh-agent-live-poc.mjs [options]

Options:
  --evidence-dir <dir>           default ${DEFAULT_EVIDENCE_DIR}
  --media-root <dir>             default ${DEFAULT_MEDIA_ROOT}
  --deck <deck.slidey.json>      default ${DEFAULT_DECK}
  --html <deck.html>             optional exported HTML bundle to verify
  --deck-video <deck.mp4>        optional rendered export to verify
  --developer-arc-media <path>   rrweb log required unless already referenced by deck
  --json-out <path>              write machine-readable report
  --allow-missing-db             do not require the gh_jobs row block
  --allow-missing-media          do not require rrweb logs or developer media
  --allow-missing-deck           do not require the generated Slidey deck
  --allow-missing-html           accepted for old command lines; exported HTML is optional unless --html is set
  --allow-missing-deck-video     accepted for old command lines; rendered MP4 is optional unless --deck-video is set
  --allow-nonlive-urls           skip live URL host validation (tests only)
  -h, --help                     show this help

Strict final proof inputs:
  <evidence-dir>/live-poc-bug-issue.md
  <evidence-dir>/live-poc-feature-issue.md
  <evidence-dir>/live-poc-guidance.md
  <evidence-dir>/live-poc-pr-status.md
  each evidence file must include ok health/API/remote-DB checks and HTTP 2xx run-page headers
  <media-root>/capture-plan-<case>.json
  <media-root>/<case>/01-github-thread.rrweb.json
  <media-root>/<case>/02-app-comment.rrweb.json
  <media-root>/<case>/03-run-page.rrweb.json
  <media-root>/<case>/04-run-api.rrweb.json
  <deck>
  optionally, when --html is supplied:
  <html>
  optionally, when --deck-video is supplied:
  <deck-video>
  <deck-video>.chapters.json`);
}

function parseArgs(argv) {
  const args = {
    evidenceDir: DEFAULT_EVIDENCE_DIR,
    mediaRoot: DEFAULT_MEDIA_ROOT,
    deck: DEFAULT_DECK,
    html: "",
    deckVideo: "",
    developerArcMedia: "",
    jsonOut: "",
    allowMissingDB: false,
    allowMissingMedia: false,
    allowMissingDeck: false,
    allowMissingHTML: false,
    allowMissingDeckVideo: false,
    allowNonliveUrls: false,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--evidence-dir":
        args.evidenceDir = argv[++i];
        break;
      case "--media-root":
        args.mediaRoot = argv[++i];
        break;
      case "--deck":
        args.deck = argv[++i];
        break;
      case "--html":
        args.html = argv[++i];
        break;
      case "--deck-video":
        args.deckVideo = argv[++i];
        break;
      case "--developer-arc-media":
        args.developerArcMedia = argv[++i];
        break;
      case "--json-out":
        args.jsonOut = argv[++i];
        break;
      case "--allow-missing-db":
        args.allowMissingDB = true;
        break;
      case "--allow-missing-media":
        args.allowMissingMedia = true;
        break;
      case "--allow-missing-deck":
        args.allowMissingDeck = true;
        break;
      case "--allow-missing-html":
        args.allowMissingHTML = true;
        break;
      case "--allow-missing-deck-video":
        args.allowMissingDeckVideo = true;
        break;
      case "--allow-nonlive-urls":
        args.allowNonliveUrls = true;
        break;
      case "-h":
      case "--help":
        args.help = true;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  return args;
}

function field(markdown, label) {
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = markdown.match(new RegExp(`^- ${escaped}:\\s*(.+?)\\s*$`, "m"));
  if (!match) return "";
  return match[1].replace(/^`|`$/g, "").trim();
}

function statusField(markdown, label) {
  return field(markdown, label);
}

function fencedJSON(markdown, heading) {
  const escaped = heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const headingPattern = heading.includes("`") ? escaped : `\`?${escaped}\`?`;
  const re = new RegExp(`^## ${headingPattern}\\s*\\n\\n\`\`\`json\\n([\\s\\S]*?)\\n\`\`\``, "m");
  const match = markdown.match(re);
  if (!match) return { found: false, value: null, error: "" };
  const raw = match[1].trim();
  if (!raw || raw === "null") return { found: true, value: null, error: "" };
  try {
    return { found: true, value: JSON.parse(raw), error: "" };
  } catch (err) {
    return { found: true, value: null, error: err.message };
  }
}

function githubOriginRef(sourceURL, objectKind, objectNumber) {
  const segment = objectKind === "pr" ? "pr" : "issue";
  return `github:bsacrobatix/Kitsoki/${segment}/${objectNumber || sourceURL.split("/").pop()}`;
}

function checkURL(name, value, predicate, report, allowNonliveUrls) {
  if (!/^https?:\/\//.test(value)) {
    report.fail(`${name} must be an http(s) URL, got ${JSON.stringify(value)}`);
    return;
  }
  if (!allowNonliveUrls && predicate && !predicate(value)) {
    report.fail(`${name} is not live POC evidence: ${value}`);
  }
}

function makeReport() {
  const report = {
    ok: true,
    failures: [],
    warnings: [],
    cases: {},
    deck: null,
    html: null,
    deckVideo: null,
    media: {},
    fail(message) {
      this.ok = false;
      this.failures.push(message);
    },
    warn(message) {
      this.warnings.push(message);
    },
  };
  return report;
}

function checkEvidence(args, c, report) {
  const evidencePath = path.join(args.evidenceDir, `live-poc-${c.slug}.md`);
  const entry = { evidencePath };
  report.cases[c.slug] = entry;
  if (!fs.existsSync(evidencePath)) {
    report.fail(`${c.slug}: missing evidence file ${evidencePath}`);
    return entry;
  }

  const markdown = fs.readFileSync(evidencePath, "utf8");
  const jobID = field(markdown, "Job ID");
  const publicBaseURL = field(markdown, "Public base URL");
  const webhookURL = field(markdown, "Webhook URL");
  const sourceURL = field(markdown, "Source URL");
  const mentionURL = field(markdown, "Mention URL");
  const runURL = field(markdown, "Run URL");
  const apiURL = field(markdown, "API URL");
  const commentURL = field(markdown, "Kitsoki comment URL");
  const health = statusField(markdown, "Health");
  const runPage = statusField(markdown, "Run page");
  const apiJSON = statusField(markdown, "API JSON");
  const remoteDB = statusField(markdown, "Remote DB");
  Object.assign(entry, {
    jobID,
    publicBaseURL,
    webhookURL,
    sourceURL,
    mentionURL,
    runURL,
    apiURL,
    commentURL,
    checks: { health, runPage, apiJSON, remoteDB },
  });

  for (const [label, value] of [
    ["Public base URL", publicBaseURL],
    ["Webhook URL", webhookURL],
    ["Job ID", jobID],
    ["Source URL", sourceURL],
    ["Mention URL", mentionURL],
    ["Run URL", runURL],
    ["API URL", apiURL],
    ["Kitsoki comment URL", commentURL],
  ]) {
    if (!value) report.fail(`${c.slug}: missing ${label}`);
  }

  checkURL(
    `${c.slug} Public base URL`,
    publicBaseURL,
    (u) => u === "https://kitsoki-test.slothattax.me",
    report,
    args.allowNonliveUrls,
  );
  checkURL(
    `${c.slug} Webhook URL`,
    webhookURL,
    (u) => u === "https://kitsoki-test.slothattax.me/gh-agent/webhook",
    report,
    args.allowNonliveUrls,
  );
  checkURL(
    `${c.slug} Source URL`,
    sourceURL,
    (u) => u.startsWith("https://github.com/bsacrobatix/Kitsoki/") && u.includes(c.sourcePathPart),
    report,
    args.allowNonliveUrls,
  );
  checkURL(`${c.slug} Mention URL`, mentionURL, (u) => u.startsWith(sourceURL), report, args.allowNonliveUrls);
  checkURL(
    `${c.slug} Run URL`,
    runURL,
    (u) => u.startsWith("https://kitsoki-test.slothattax.me/run/"),
    report,
    args.allowNonliveUrls,
  );
  checkURL(
    `${c.slug} API URL`,
    apiURL,
    (u) => u.startsWith("https://kitsoki-test.slothattax.me/api/run/"),
    report,
    args.allowNonliveUrls,
  );
  checkURL(`${c.slug} Kitsoki comment URL`, commentURL, (u) => u.startsWith(sourceURL), report, args.allowNonliveUrls);
  checkCollectedStatuses(report, c, { health, runPage, apiJSON, remoteDB }, args);

  const api = fencedJSON(markdown, `/api/run/${jobID}`);
  entry.api = api.value;
  if (!api.found) {
    report.fail(`${c.slug}: missing /api/run/${jobID} JSON block`);
  } else if (api.error) {
    report.fail(`${c.slug}: invalid /api/run/${jobID} JSON: ${api.error}`);
  } else if (!api.value) {
    report.fail(`${c.slug}: /api/run/${jobID} JSON block is empty`);
  } else {
    checkCaseJSON(report, c, api.value, {
      sourceURL,
      runURL,
      commentURL,
      jobID,
      where: `${c.slug} API`,
    });
  }

  const db = fencedJSON(markdown, "`gh_jobs` Row");
  entry.db = db.value;
  if (!db.found) {
    if (!args.allowMissingDB) report.fail(`${c.slug}: missing gh_jobs row JSON block`);
  } else if (db.error) {
    report.fail(`${c.slug}: invalid gh_jobs row JSON: ${db.error}`);
  } else if (!db.value) {
    if (!args.allowMissingDB) report.fail(`${c.slug}: gh_jobs row JSON block is empty`);
  } else {
    checkCaseJSON(report, c, db.value, {
      sourceURL,
      runURL,
      commentURL,
      jobID,
      where: `${c.slug} gh_jobs row`,
      dbRow: true,
    });
  }

  return entry;
}

function checkCollectedStatuses(report, c, checks, args) {
  if (checks.health !== "ok") {
    report.fail(`${c.slug}: health check is ${JSON.stringify(checks.health)}, expected "ok"`);
  }
  if (!/^HTTP\/[0-9.]+ 2[0-9][0-9]\b/.test(checks.runPage)) {
    report.fail(`${c.slug}: run page check is ${JSON.stringify(checks.runPage)}, expected HTTP 2xx`);
  }
  if (checks.apiJSON !== "ok") {
    report.fail(`${c.slug}: API JSON check is ${JSON.stringify(checks.apiJSON)}, expected "ok"`);
  }
  if (!args.allowMissingDB && checks.remoteDB !== "ok") {
    report.fail(`${c.slug}: remote DB check is ${JSON.stringify(checks.remoteDB)}, expected "ok"`);
  }
}

function checkCaseJSON(report, c, value, ctx) {
  if (value.job_id && value.job_id !== ctx.jobID) {
    report.fail(`${ctx.where}: job_id ${value.job_id} does not match evidence ${ctx.jobID}`);
  }
  if (value.source_url && value.source_url !== ctx.sourceURL) {
    report.fail(`${ctx.where}: source_url ${value.source_url} does not match evidence ${ctx.sourceURL}`);
  }
  if (value.run_url && value.run_url !== ctx.runURL) {
    report.fail(`${ctx.where}: run_url ${value.run_url} does not match evidence ${ctx.runURL}`);
  }
  if (value.comment_url && value.comment_url !== ctx.commentURL) {
    report.fail(`${ctx.where}: comment_url ${value.comment_url} does not match evidence ${ctx.commentURL}`);
  }
  if (value.object_kind !== c.expectedObjectKind) {
    report.fail(`${ctx.where}: object_kind ${value.object_kind} does not match ${c.expectedObjectKind}`);
  }
  const story = value.story ?? "";
  if (story !== c.expectedStory) {
    report.fail(`${ctx.where}: story ${JSON.stringify(value.story)} does not match ${JSON.stringify(c.expectedStory)}`);
  }
  if (value.state !== c.expectedState) {
    report.fail(`${ctx.where}: state ${value.state} does not match ${c.expectedState}`);
  }
  if (value.object_number === undefined || value.object_number === null || value.object_number === "") {
    report.fail(`${ctx.where}: missing object_number`);
  }
  if (value.origin_ref) {
    const expected = githubOriginRef(ctx.sourceURL, c.expectedObjectKind, value.object_number);
    if (value.origin_ref !== expected) {
      report.fail(`${ctx.where}: origin_ref ${value.origin_ref} does not match ${expected}`);
    }
  }
  if (ctx.dbRow && !value.comment_id) {
    report.fail(`${ctx.where}: missing comment_id`);
  }
  if (ctx.dbRow) {
    for (const fieldName of ["created_at", "updated_at"]) {
      if (value[fieldName] === undefined || value[fieldName] === null || value[fieldName] === "") {
        report.fail(`${ctx.where}: missing ${fieldName}`);
      }
    }
  }
}

function readJSONFile(file, report, label) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (err) {
    report.fail(`${label}: ${err.message}`);
    return null;
  }
}

function rrwebEvents(log) {
  return Array.isArray(log) ? log : Array.isArray(log?.events) ? log.events : [];
}

function checkRrwebLog(file, step, report, label) {
  if (!fs.existsSync(file)) {
    return false;
  }
  if (fs.statSync(file).size === 0) {
    report.fail(`${label} file is zero bytes: ${file}`);
    return true;
  }
  const log = readJSONFile(file, report, label);
  if (!log) return true;
  const events = rrwebEvents(log);
  if (events.length < 2) {
    report.fail(`${label} should contain at least 2 rrweb events, got ${events.length}`);
  }
  const hasChapter = events.some(
    (event) => event?.type === 5 && event?.data?.tag === "slidey.chapter" && event?.data?.payload?.id === step.id,
  );
  if (!hasChapter) {
    report.fail(`${label} is missing slidey.chapter marker for ${step.id}`);
  }
  const annotations = events.filter((event) => event?.type === 5 && event?.data?.tag === "kitsoki.annotation");
  if (annotations.length < step.minAnnotations) {
    report.fail(
      `${label} has ${annotations.length} visual annotation marker(s), expected at least ${step.minAnnotations}`,
    );
  }
  const missingVisualTarget = annotations.filter((event) => !event?.data?.payload?.shownSelector);
  if (missingVisualTarget.length > 0) {
    report.fail(`${label} has annotation marker(s) without a shown visual target`);
  }
  const fallbackTargets = annotations.filter((event) => event?.data?.payload?.fallback);
  if (fallbackTargets.length > 0) {
    report.fail(`${label} has ${fallbackTargets.length} broad fallback annotation target(s) instead of precise callouts`);
  }
  const readableZooms = events.filter((event) => event?.type === 5 && event?.data?.tag === "kitsoki.readable_zoom");
  if (readableZooms.length < step.minReadableZooms) {
    report.fail(
      `${label} has ${readableZooms.length} readable zoom marker(s), expected at least ${step.minReadableZooms}`,
    );
  }
  const missingReadableZooms = readableZooms.filter((event) => !event?.data?.payload?.shown);
  if (missingReadableZooms.length > 0) {
    report.fail(`${label} has readable zoom marker(s) that did not show an overlay`);
  }
  return true;
}

function checkMedia(args, c, report) {
  const planPath = path.join(args.mediaRoot, `capture-plan-${c.slug}.json`);
  const rrwebFiles = STEPS.map((step) => ({
    id: step.id,
    path: path.join(args.mediaRoot, c.slug, step.file),
  }));
  report.media[c.slug] = { planPath, rrwebFiles: rrwebFiles.map((step) => step.path) };
  const evidence = report.cases[c.slug] || {};
  const expectedSteps = [
    ["github-thread", evidence.sourceURL],
    ["app-comment", evidence.commentURL],
    ["run-page", evidence.runURL],
    ["run-api", evidence.apiURL],
  ];

  if (!fs.existsSync(planPath)) {
    if (!args.allowMissingMedia) report.fail(`${c.slug}: missing capture plan ${planPath}`);
  } else {
    const plan = readJSONFile(planPath, report, `${c.slug} capture plan`);
    if (plan) {
      const expectedArtifactDir = path.join(args.mediaRoot, c.slug);
      if (plan.artifactDir !== expectedArtifactDir) {
        report.fail(`${c.slug}: capture plan artifactDir ${plan.artifactDir} does not match ${expectedArtifactDir}`);
      }
      if (!Array.isArray(plan.steps) || plan.steps.length < 4) {
        report.fail(`${c.slug}: capture plan must have at least four steps`);
      } else {
        for (const [id, expectedURL] of expectedSteps) {
          const step = plan.steps.find((candidate) => candidate?.id === id);
          if (!step) {
            report.fail(`${c.slug}: capture plan missing ${id} step`);
            continue;
          }
          if (expectedURL && step.url !== expectedURL) {
            report.fail(`${c.slug}: capture plan ${id} URL ${step.url} does not match evidence ${expectedURL}`);
          }
          if (!step.waitForText) {
            report.fail(`${c.slug}: capture plan ${id} step is missing waitForText`);
          }
        }
      }
    }
  }

  for (const { id, path: rrwebPath } of rrwebFiles) {
    if (!fs.existsSync(rrwebPath)) {
      if (!args.allowMissingMedia) report.fail(`${c.slug}: missing rrweb log ${rrwebPath}`);
      continue;
    }
    const step = STEPS.find((candidate) => candidate.id === id) || { id, minAnnotations: 1, minReadableZooms: 0 };
    checkRrwebLog(rrwebPath, step, report, `${c.slug} ${id} rrweb`);
  }
}

function collectStrings(value, out = []) {
  if (typeof value === "string") {
    out.push(value);
  } else if (Array.isArray(value)) {
    for (const item of value) collectStrings(item, out);
  } else if (value && typeof value === "object") {
    for (const item of Object.values(value)) collectStrings(item, out);
  }
  return out;
}

function checkDeck(args, report) {
  if (!fs.existsSync(args.deck)) {
    if (!args.allowMissingDeck) report.fail(`missing deck ${args.deck}`);
    return;
  }
  const deck = readJSONFile(args.deck, report, "deck");
  if (!deck) return;
  report.deck = { path: args.deck, sceneCount: Array.isArray(deck.scenes) ? deck.scenes.length : 0 };
  if (deck.meta?.title !== "@kitsoki on GitHub, demonstrated through slidey") {
    report.fail(`deck meta.title is unexpected: ${JSON.stringify(deck.meta?.title)}`);
  }
  if (!Array.isArray(deck.scenes) || deck.scenes.length < 15) {
    report.fail(`deck must have at least 15 scenes, got ${deck.scenes?.length ?? "none"}`);
  }
  const allStrings = collectStrings(deck);
  const haystack = allStrings.join("\n");
  for (const phrase of ["gh-thread.html", "no App, no webhook", "live trace", "mention to merge"]) {
    if (haystack.includes(phrase)) {
      report.fail(`deck still contains stale fixture/live-claim phrase: ${phrase}`);
    }
  }
  for (const stale of [".mp4", ".webm"]) {
    if (allStrings.some((s) => typeof s === "string" && /^(?:\.\/|\.\.\/|\/|[A-Za-z]:)/.test(s) && s.includes(stale))) {
      report.fail(`deck references rendered video media (${stale}); live proof scenes must embed rrweb logs`);
    }
  }
  for (const c of CASES) {
    for (const step of STEPS) {
      if (!haystack.includes(`${c.slug}/${step.file}`)) {
        report.fail(`deck does not reference ${c.slug}/${step.file}`);
      }
    }
    const evidence = report.cases[c.slug];
    if (evidence?.sourceURL && !haystack.includes(evidence.sourceURL)) {
      report.fail(`deck does not reference ${c.slug} source URL ${evidence.sourceURL}`);
    }
    if (evidence?.runURL && !haystack.includes(evidence.runURL)) {
      report.fail(`deck does not reference ${c.slug} run URL ${evidence.runURL}`);
    }
  }
  if (!haystack.includes("Live GitHub App on kitsoki-test")) {
    report.fail("deck does not explicitly identify the GitHub act as live GitHub App on kitsoki-test");
  }
  if (!haystack.includes("https://kitsoki-test.slothattax.me/gh-agent/webhook")) {
    report.fail("deck does not reference the live GitHub App webhook URL");
  }
  const sectionOne = Array.isArray(deck.scenes)
    ? deck.scenes.find((scene) => scene?.title === "Live GitHub front door")
    : null;
  const sectionOneVisible = collectStrings({
    eyebrow: sectionOne?.eyebrow,
    title: sectionOne?.title,
    subtitle: sectionOne?.subtitle,
    caption: sectionOne?.caption,
  }).join("\n");
  if (!sectionOneVisible.includes("https://kitsoki-test.slothattax.me/gh-agent/webhook")) {
    report.fail("deck Section 1 does not visibly reference the live GitHub App webhook URL");
  }
  const hasDeveloperMedia =
    (args.developerArcMedia && haystack.includes(path.basename(args.developerArcMedia))) ||
    allStrings.some((s) => /developer|slidey/i.test(s) && /\.rrweb\.json$/i.test(s));
  if (!hasDeveloperMedia && !args.allowMissingMedia) {
    report.fail("deck does not appear to reference developer-arc media");
  }
  if (args.developerArcMedia && !args.developerArcMedia.endsWith(".rrweb.json") && !args.allowMissingMedia) {
    report.fail(`developer-arc media must be an rrweb log, got ${args.developerArcMedia}`);
  }
  if (!haystack.includes("What remains") || !haystack.includes("Full PR autopilot")) {
    report.fail("deck is missing the explicit remaining-work boundary");
  }
}

function checkHTML(args, report) {
  if (!args.html) {
    report.html = { skipped: true, reason: "no --html supplied" };
    return;
  }
  if (!fs.existsSync(args.html)) {
    if (!args.allowMissingHTML) report.fail(`missing exported HTML deck ${args.html}`);
    return;
  }
  const stat = fs.statSync(args.html);
  const html = fs.readFileSync(args.html, "utf8");
  report.html = { path: args.html, bytes: stat.size };
  if (stat.size < 1024) {
    report.fail(`exported HTML deck is too small to be a self-contained bundle: ${args.html}`);
  }
  if (!/<!doctype|<html/i.test(html)) {
    report.fail(`exported HTML deck does not look like HTML: ${args.html}`);
  }
  if (!html.includes("@kitsoki") || !html.includes("GitHub")) {
    report.fail("exported HTML deck is missing the @kitsoki GitHub title text");
  }
  if (/gh-thread\.html|no App, no webhook|mention to merge/.test(html)) {
    report.fail("exported HTML deck contains stale fixture/live-claim text");
  }
}

function checkDeckVideo(args, report) {
  if (!args.deckVideo) {
    report.deckVideo = { skipped: true, reason: "no --deck-video supplied" };
    return;
  }
  const chapters = `${args.deckVideo}.chapters.json`;
  report.deckVideo = { path: args.deckVideo, chapters };
  if (!fs.existsSync(args.deckVideo)) {
    if (!args.allowMissingDeckVideo) report.fail(`missing rendered deck MP4 ${args.deckVideo}`);
  } else if (fs.statSync(args.deckVideo).size === 0) {
    report.fail(`rendered deck MP4 is empty: ${args.deckVideo}`);
  }
  if (!fs.existsSync(chapters)) {
    if (!args.allowMissingDeckVideo) report.fail(`missing rendered deck chapter sidecar ${chapters}`);
  } else {
    const parsed = readJSONFile(chapters, report, "rendered deck chapter sidecar");
    if (parsed && (!Array.isArray(parsed) || parsed.length < 8)) {
      report.fail(`rendered deck chapter sidecar should contain at least 8 chapters, got ${Array.isArray(parsed) ? parsed.length : "non-array"}`);
    }
  }
}

function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (err) {
    console.error(err.message);
    usage();
    process.exit(2);
  }
  if (args.help) {
    usage();
    return;
  }

  const report = makeReport();
  for (const c of CASES) {
    checkEvidence(args, c, report);
    checkMedia(args, c, report);
  }
  if (args.developerArcMedia && !fs.existsSync(args.developerArcMedia) && !args.allowMissingMedia) {
    report.fail(`missing developer-arc media ${args.developerArcMedia}`);
  }
  checkDeck(args, report);
  checkHTML(args, report);
  checkDeckVideo(args, report);

  if (args.jsonOut) {
    fs.mkdirSync(path.dirname(args.jsonOut), { recursive: true });
    fs.writeFileSync(args.jsonOut, `${JSON.stringify(report, null, 2)}\n`);
  }

  if (report.ok) {
    console.log("OK: live GitHub agent POC evidence bundle is complete");
    if (report.warnings.length) {
      console.log(`Warnings: ${report.warnings.length}`);
      for (const warning of report.warnings) console.log(`- ${warning}`);
    }
    return;
  }

  console.error(`FAIL: ${report.failures.length} verification issue(s)`);
  for (const failure of report.failures) console.error(`- ${failure}`);
  if (report.warnings.length) {
    console.error(`Warnings: ${report.warnings.length}`);
    for (const warning of report.warnings) console.error(`- ${warning}`);
  }
  process.exit(1);
}

main();

#!/usr/bin/env node
/*
 * Verify the final live @kitsoki GitHub-agent POC evidence bundle.
 *
 * This is intentionally read-only. It checks the collected markdown evidence,
 * capture plans, recorded clips, chapter sidecars, and generated Slidey deck.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const CASES = [
  {
    slug: "bug-issue",
    videoName: "03-bug-issue.mp4",
    expectedObjectKind: "issue",
    expectedStory: "stories/bugfix",
    expectedState: "done",
    sourcePathPart: "/issues/",
  },
  {
    slug: "feature-issue",
    videoName: "04-feature-issue.mp4",
    expectedObjectKind: "issue",
    expectedStory: "stories/dev-story",
    expectedState: "done",
    sourcePathPart: "/issues/",
  },
  {
    slug: "guidance",
    videoName: "05-guidance.mp4",
    expectedObjectKind: "issue",
    expectedStory: "",
    expectedState: "awaiting_guidance",
    sourcePathPart: "/issues/",
  },
  {
    slug: "pr-status",
    videoName: "06-pr-status.mp4",
    expectedObjectKind: "pr",
    expectedStory: "pr-beat",
    expectedState: "done",
    sourcePathPart: "/pull/",
  },
];

const DEFAULT_EVIDENCE_DIR = ".context";
const DEFAULT_MEDIA_ROOT = ".artifacts/github-agent-live";
const DEFAULT_DECK = ".artifacts/github-agent-live/live-github-agent.deck.json";
const DEFAULT_HTML = ".artifacts/github-agent-live/live-github-agent.html";

function usage() {
  console.error(`usage: scripts/verify-gh-agent-live-poc.mjs [options]

Options:
  --evidence-dir <dir>           default ${DEFAULT_EVIDENCE_DIR}
  --media-root <dir>             default ${DEFAULT_MEDIA_ROOT}
  --deck <deck.json>             default ${DEFAULT_DECK}
  --html <deck.html>             default ${DEFAULT_HTML}
  --developer-arc-media <path>   required unless already referenced by deck
  --json-out <path>              write machine-readable report
  --allow-missing-db             do not require the gh_jobs row block
  --allow-missing-media          do not require clips, chapters, or developer media
  --allow-missing-deck           do not require the generated Slidey deck
  --allow-missing-html           do not require the exported self-contained HTML deck
  --allow-nonlive-urls           skip live URL host validation (tests only)
  -h, --help                     show this help

Strict final proof inputs:
  <evidence-dir>/live-poc-bug-issue.md
  <evidence-dir>/live-poc-feature-issue.md
  <evidence-dir>/live-poc-guidance.md
  <evidence-dir>/live-poc-pr-status.md
  <media-root>/capture-plan-<case>.json
  <media-root>/<case>/<video>.mp4
  <media-root>/<case>/<video>.mp4.chapters.json
  <deck>
  <html>`);
}

function parseArgs(argv) {
  const args = {
    evidenceDir: DEFAULT_EVIDENCE_DIR,
    mediaRoot: DEFAULT_MEDIA_ROOT,
    deck: DEFAULT_DECK,
    html: DEFAULT_HTML,
    developerArcMedia: "",
    jsonOut: "",
    allowMissingDB: false,
    allowMissingMedia: false,
    allowMissingDeck: false,
    allowMissingHTML: false,
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

function fencedJSON(markdown, heading) {
  const escaped = heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`^## ${escaped}\\s*\\n\\n\`\`\`json\\n([\\s\\S]*?)\\n\`\`\``, "m");
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
  const segment = objectKind === "pr" ? "pull" : "issue";
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
  const sourceURL = field(markdown, "Source URL");
  const mentionURL = field(markdown, "Mention URL");
  const runURL = field(markdown, "Run URL");
  const apiURL = field(markdown, "API URL");
  const commentURL = field(markdown, "Kitsoki comment URL");
  Object.assign(entry, { jobID, sourceURL, mentionURL, runURL, apiURL, commentURL });

  for (const [label, value] of [
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
  if (value.story !== c.expectedStory) {
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
}

function readJSONFile(file, report, label) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (err) {
    report.fail(`${label}: ${err.message}`);
    return null;
  }
}

function checkMedia(args, c, report) {
  const planPath = path.join(args.mediaRoot, `capture-plan-${c.slug}.json`);
  const videoPath = path.join(args.mediaRoot, c.slug, c.videoName);
  const chaptersPath = `${videoPath}.chapters.json`;
  report.media[c.slug] = { planPath, videoPath, chaptersPath };

  if (!fs.existsSync(planPath)) {
    if (!args.allowMissingMedia) report.fail(`${c.slug}: missing capture plan ${planPath}`);
  } else {
    const plan = readJSONFile(planPath, report, `${c.slug} capture plan`);
    if (plan) {
      if (plan.artifactDir !== `.artifacts/github-agent-live/${c.slug}`) {
        report.fail(`${c.slug}: capture plan artifactDir ${plan.artifactDir} does not match case`);
      }
      if (!Array.isArray(plan.steps) || plan.steps.length < 4) {
        report.fail(`${c.slug}: capture plan must have at least four steps`);
      }
    }
  }

  for (const [label, file] of [
    ["clip", videoPath],
    ["chapters", chaptersPath],
  ]) {
    if (!fs.existsSync(file)) {
      if (!args.allowMissingMedia) report.fail(`${c.slug}: missing ${label} ${file}`);
    } else if (fs.statSync(file).size === 0) {
      report.warn(`${c.slug}: ${label} file is zero bytes: ${file}`);
    }
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
  for (const c of CASES) {
    if (!haystack.includes(c.videoName)) {
      report.fail(`deck does not reference ${c.videoName}`);
    }
    const evidence = report.cases[c.slug];
    if (evidence?.sourceURL && !haystack.includes(evidence.sourceURL)) {
      report.fail(`deck does not reference ${c.slug} source URL ${evidence.sourceURL}`);
    }
    if (evidence?.runURL && !haystack.includes(evidence.runURL)) {
      report.fail(`deck does not reference ${c.slug} run URL ${evidence.runURL}`);
    }
  }
  const hasDeveloperMedia =
    (args.developerArcMedia && haystack.includes(path.basename(args.developerArcMedia))) ||
    allStrings.some((s) => /developer|slidey/i.test(s) && /\.(mp4|rrweb\.json)$/i.test(s));
  if (!hasDeveloperMedia && !args.allowMissingMedia) {
    report.fail("deck does not appear to reference developer-arc media");
  }
  if (!haystack.includes("What remains") || !haystack.includes("Full PR autopilot")) {
    report.fail("deck is missing the explicit remaining-work boundary");
  }
}

function checkHTML(args, report) {
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

#!/usr/bin/env node
/*
 * Build a per-phase @kitsoki GitHub-agent Slidey deck from collected evidence
 * and captured rrweb clips for a single case.
 *
 * Strict mode is the default: the evidence note and media files must exist,
 * and evidence URLs must point at a consistent live GitHub repo + public
 * gh-agent service. Use --allow-missing-media to emit a draft scaffold.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const RRWEB_SETTLE_START_SECONDS = 0.25;

const CASES = [
  {
    slug: "bug-issue",
    section: "Section 3",
    eyebrow: "Live evidence",
    title: "Bug issue dispatch",
    subtitle: "bug label -> stories/bugfix -> done/run page",
    expectedStory: "stories/bugfix",
    expectedState: "done",
    narration:
      "The bug issue proof shows the shortest complete loop: a labelled GitHub issue, an at-mention, one claimed job, bugfix dispatch, an App comment, and the hosted run page.",
  },
  {
    slug: "feature-issue",
    section: "Section 4",
    eyebrow: "Live evidence",
    title: "Feature issue dispatch",
    subtitle: "feature or enhancement label -> stories/dev-story -> run page",
    expectedStory: "stories/dev-story",
    expectedState: "done",
    narration:
      "The feature issue proof shows that the live GitHub agent is not hard-coded to bugs. A feature-labelled mention routes to dev-story and still produces the same public run link.",
  },
  {
    slug: "guidance",
    section: "Section 5",
    eyebrow: "Live evidence",
    title: "Guidance request",
    subtitle: "ambiguous mention -> guidance comment -> awaiting_guidance",
    expectedStory: "",
    expectedState: "awaiting_guidance",
    narration:
      "The guidance proof shows the safety path. When the issue is ambiguous, kitsoki asks for direction instead of guessing and parks the job at awaiting guidance.",
  },
  {
    slug: "guidance-resume",
    section: "Section 6",
    eyebrow: "Live evidence",
    title: "Guidance resume",
    subtitle: "ambiguous mention -> awaiting_guidance -> bug label -> same job done",
    expectedStory: "stories/bugfix",
    expectedState: "done",
    requiredEventStates: ["awaiting_guidance", "done"],
    narration:
      "The guidance resume proof shows the production recovery path: the ambiguous issue parks first, then adding the bug label resumes the same job and rolling comment instead of creating a duplicate.",
  },
  {
    slug: "pr-status",
    section: "Section 7",
    eyebrow: "Live evidence",
    title: "PR status route",
    subtitle: "PR mention -> pr-beat -> status/run link",
    expectedStory: "pr-beat",
    expectedState: "done",
    narration:
      "The PR status proof shows the pull request path: the mention routes as a PR, pr-beat reads status through the no-LLM host path, and kitsoki comments back with the run link.",
  },
];

const STEPS = [
  {
    id: "github-thread",
    file: "01-github-thread.rrweb.json",
    title: "Real GitHub thread",
    caption: (c, evidence) => `${c.subtitle}. Evidence: ${evidence.sourceURL}`,
    narration: (c) => `${c.title}: real GitHub thread.`,
  },
  {
    id: "app-comment",
    file: "02-app-comment.rrweb.json",
    title: "App-authenticated kitsoki comment",
    caption: (_c, evidence) => `kitsoki comments back with a public run link: ${evidence.runURL}`,
    narration: () => "App comment with the run link.",
  },
  {
    id: "run-page",
    file: "03-run-page.rrweb.json",
    title: "Hosted run page",
    caption: (_c, evidence) => `Hosted run summary: ${evidence.runURL}`,
    narration: () => "Hosted run summary.",
  },
  {
    id: "run-api",
    file: "04-run-api.rrweb.json",
    title: "Run JSON",
    caption: (_c, evidence) => `Machine-readable job state: ${evidence.apiURL}`,
    narration: () => "API job state.",
  },
];

const VALID_SLUGS = CASES.map((c) => c.slug);
const DEFAULT_MEDIA_ROOT = ".artifacts/github-agent-live";

function usage() {
  console.error(`usage: scripts/build-gh-agent-phase-deck.mjs --case <slug> [options]

Options:
  --case <slug>                  one of: ${VALID_SLUGS.join(", ")}
  --evidence <path>              default .context/live-poc-<slug>.md
  --media-root <dir>             default ${DEFAULT_MEDIA_ROOT}
  --out <path>                   default ${DEFAULT_MEDIA_ROOT}/<slug>/deck.slidey.json
  --allow-missing-media          emit a draft even when clip files are absent
  --allow-nonlive-urls           skip live URL host validation (tests only)
  -h, --help                     show this help

Inputs:
  <evidence>                     evidence markdown file for the selected case
  <media-root>/<slug>/01-github-thread.rrweb.json
  <media-root>/<slug>/02-app-comment.rrweb.json
  <media-root>/<slug>/03-run-page.rrweb.json
  <media-root>/<slug>/04-run-api.rrweb.json`);
}

function parseArgs(argv) {
  const args = {
    caseSlug: "",
    evidence: "",
    mediaRoot: DEFAULT_MEDIA_ROOT,
    out: "",
    allowMissingMedia: false,
    allowNonliveUrls: false,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--case":
        args.caseSlug = argv[++i];
        break;
      case "--evidence":
        args.evidence = argv[++i];
        break;
      case "--media-root":
        args.mediaRoot = argv[++i];
        break;
      case "--out":
        args.out = argv[++i];
        break;
      case "--allow-missing-media":
        args.allowMissingMedia = true;
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
  if (!args.help) {
    if (!args.caseSlug) {
      throw new Error("--case is required");
    }
    if (!VALID_SLUGS.includes(args.caseSlug)) {
      throw new Error(`--case must be one of: ${VALID_SLUGS.join(", ")} (got ${args.caseSlug})`);
    }
    if (!args.evidence) {
      args.evidence = `.context/live-poc-${args.caseSlug}.md`;
    }
    if (!args.out) {
      args.out = `${args.mediaRoot}/${args.caseSlug}/deck.slidey.json`;
    }
  }
  return args;
}

/* ── Evidence-reading helpers (same as build-gh-agent-live-deck.mjs) ─────── */

function field(markdown, label) {
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = markdown.match(new RegExp(`^- ${escaped}:\\s*(.+?)\\s*$`, "m"));
  if (!match) return "";
  return match[1].replace(/^`|`$/g, "").trim();
}

function fencedJSON(markdown, heading) {
  const escaped = heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const headingPattern = heading.includes("`") ? escaped : `\`?${escaped}\`?`;
  const re = new RegExp(`^## ${headingPattern}\\s*\\n\\n\`\`\`json\\n([\\s\\S]*?)\\n\`\`\``, "m");
  const match = markdown.match(re);
  if (!match) return null;
  const raw = match[1].trim();
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function requireURL(name, value, predicate, allowNonliveUrls) {
  if (!/^https?:\/\//.test(value)) {
    throw new Error(`${name} must be an http(s) URL, got ${JSON.stringify(value)}`);
  }
  if (!allowNonliveUrls && predicate && !predicate(value)) {
    throw new Error(`${name} is not live POC evidence: ${value}`);
  }
  return value;
}

function normalizeBaseURL(value) {
  return String(value || "").replace(/\/+$/, "");
}

function githubRepoFromURL(sourceURL) {
  const match = String(sourceURL || "").match(/^https:\/\/github\.com\/([^/]+\/[^/]+)\//);
  return match ? match[1] : "";
}

function requireStatus(caseSlug, label, value, predicate, expected) {
  if (!predicate(value)) {
    throw new Error(`${caseSlug} ${label} check is ${JSON.stringify(value)}, expected ${expected}`);
  }
}

function relativeMediaPath(deckOut, mediaPath) {
  const from = path.dirname(path.resolve(deckOut));
  const rel = path.relative(from, path.resolve(mediaPath));
  return rel.startsWith(".") ? rel : `./${rel}`;
}

function readEvidence(args, c) {
  const evidencePath = args.evidence;
  const markdown = fs.readFileSync(evidencePath, "utf8");
  const publicBaseURL = requireURL(
    `${c.slug} Public base URL`,
    field(markdown, "Public base URL"),
    null,
    args.allowNonliveUrls,
  );
  const expectedWebhookURL = `${normalizeBaseURL(publicBaseURL)}/gh-agent/webhook`;
  const webhookURL = requireURL(
    `${c.slug} Webhook URL`,
    field(markdown, "Webhook URL"),
    (u) => u === expectedWebhookURL,
    args.allowNonliveUrls,
  );
  const sourceURL = requireURL(
    `${c.slug} Source URL`,
    field(markdown, "Source URL"),
    null,
    args.allowNonliveUrls,
  );
  const runURL = requireURL(
    `${c.slug} Run URL`,
    field(markdown, "Run URL"),
    (u) => u.startsWith(`${normalizeBaseURL(publicBaseURL)}/run/`),
    args.allowNonliveUrls,
  );
  const apiURL = requireURL(
    `${c.slug} API URL`,
    field(markdown, "API URL"),
    (u) => u.startsWith(`${normalizeBaseURL(publicBaseURL)}/api/run/`),
    args.allowNonliveUrls,
  );
  const commentURL = field(markdown, "Kitsoki comment URL");
  const jobID = field(markdown, "Job ID");
  if (!jobID) {
    throw new Error(`${c.slug} evidence is missing Job ID`);
  }
  requireStatus(c.slug, "Health", field(markdown, "Health"), (v) => v === "ok", "ok");
  requireStatus(
    c.slug,
    "Run page",
    field(markdown, "Run page"),
    (v) => /^HTTP\/[0-9.]+ 2[0-9][0-9]\b/.test(v),
    "HTTP 2xx",
  );
  requireStatus(c.slug, "API JSON", field(markdown, "API JSON"), (v) => v === "ok", "ok");
  requireStatus(c.slug, "Remote DB", field(markdown, "Remote DB"), (v) => v === "ok", "ok");
  const api = fencedJSON(markdown, `/api/run/${jobID}`);
  if (api) {
    if (api.run_url && api.run_url !== runURL) {
      throw new Error(`${c.slug} API run_url ${api.run_url} does not match evidence Run URL ${runURL}`);
    }
    if (api.source_url && api.source_url !== sourceURL) {
      throw new Error(`${c.slug} API source_url ${api.source_url} does not match evidence Source URL ${sourceURL}`);
    }
    if (c.expectedStory && api.story !== c.expectedStory) {
      throw new Error(`${c.slug} API story ${api.story} does not match ${c.expectedStory}`);
    }
    if (!c.expectedStory && api.story) {
      throw new Error(`${c.slug} API story should be empty while awaiting guidance, got ${api.story}`);
    }
    if (api.state !== c.expectedState) {
      throw new Error(`${c.slug} API state ${api.state} does not match ${c.expectedState}`);
    }
    if (Array.isArray(c.requiredEventStates) && c.requiredEventStates.length > 0) {
      const eventStates = new Set((api.events || []).map((event) => event?.state).filter(Boolean));
      for (const state of c.requiredEventStates) {
        if (!eventStates.has(state)) {
          throw new Error(`${c.slug} API events are missing required lifecycle state ${state}`);
        }
      }
    }
  }
  return { evidencePath, publicBaseURL, webhookURL, sourceURL, runURL, apiURL, commentURL, jobID, api };
}

function requireMedia(args, c) {
  const files = STEPS.map((step) => ({
    ...step,
    path: path.join(args.mediaRoot, c.slug, step.file),
  }));
  if (!args.allowMissingMedia) {
    for (const file of files) {
      if (!fs.existsSync(file.path)) throw new Error(`missing ${c.slug} rrweb log: ${file.path}`);
    }
  }
  return files;
}

function mediaScenes(deckOut, c, evidence, files) {
  return files.map((file, idx) => ({
    type: "video",
    mode: "embedded",
    eyebrow: c.eyebrow,
    title: idx === 0 ? c.title : `${c.title}: ${file.title}`,
    rrweb: relativeMediaPath(deckOut, file.path),
    ...(idx === 0 ? { start: RRWEB_SETTLE_START_SECONDS } : {}),
    chapters: "auto",
    caption: file.caption(c, evidence),
    narration: file.narration(c),
  }));
}

/* ── Deck construction ───────────────────────────────────────────────────── */

function buildScaffoldDeck(c) {
  const scenes = [
    {
      type: "title",
      eyebrow: c.eyebrow,
      title: `@kitsoki ${c.title}`,
      subtitle: c.subtitle,
      narration: c.narration,
    },
    {
      type: "cta",
      wordmark: "kitsoki",
      tagline: `${c.title}: ${c.subtitle}`,
      url: "Evidence and media pending",
      narration: `${c.title} deck scaffold. Evidence and media are not yet available.`,
    },
  ];
  return makeDeck(c, scenes, true);
}

function buildDeck(args, c) {
  const evidence = readEvidence(args, c);
  const files = requireMedia(args, c);
  const anyMediaMissing = files.some((f) => !fs.existsSync(f.path));

  const scenes = [
    {
      type: "title",
      eyebrow: c.eyebrow,
      title: `@kitsoki ${c.title}`,
      subtitle: c.subtitle,
      narration: c.narration,
    },
  ];

  if (anyMediaMissing) {
    for (const file of files) {
      const exists = fs.existsSync(file.path);
      scenes.push({
        type: "title",
        eyebrow: "Missing media",
        title: `${c.title}: ${file.title}`,
        subtitle: exists ? file.path : `⚠ Missing: ${file.path}`,
        narration: exists
          ? `${file.title} clip exists but other clips are missing.`
          : `${file.title} clip is missing.`,
      });
    }
  } else {
    scenes.push(...mediaScenes(args.out, c, evidence, files));
  }

  scenes.push({
    type: "cta",
    wordmark: "kitsoki",
    tagline: `${c.title}: ${c.subtitle}`,
    url: evidence.runURL,
    narration: `${c.title} phase deck complete.`,
  });

  return makeDeck(c, scenes, anyMediaMissing);
}

function makeDeck(c, scenes, isDraft) {
  return {
    _comment: `Generated by scripts/build-gh-agent-phase-deck.mjs for case ${c.slug}.${isDraft ? " Draft: missing media or evidence." : ""}`,
    meta: {
      title: `@kitsoki ${c.title}`,
      mode: "pitch",
      resolution: { width: 1600, height: 900 },
      narration: {
        voice: "en-AU-NatashaNeural",
        pronunciations: {
          kitsoki: "kit-SOH-kee",
          GitHub: "GIT-hub",
        },
      },
    },
    scenes,
  };
}

/* ── Main ────────────────────────────────────────────────────────────────── */

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
  const c = CASES.find((c) => c.slug === args.caseSlug);
  try {
    let deck;
    const evidenceExists = fs.existsSync(args.evidence);
    if (!evidenceExists) {
      if (!args.allowMissingMedia) {
        throw new Error(`evidence file not found: ${args.evidence}`);
      }
      console.error(`warning: evidence file not found, emitting scaffold: ${args.evidence}`);
      deck = buildScaffoldDeck(c);
    } else {
      deck = buildDeck(args, c);
    }
    fs.mkdirSync(path.dirname(args.out), { recursive: true });
    fs.writeFileSync(args.out, `${JSON.stringify(deck, null, 2)}\n`);
    console.log(`wrote ${args.out}`);
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}

main();

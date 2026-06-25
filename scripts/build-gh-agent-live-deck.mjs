#!/usr/bin/env node
/*
 * Build the live @kitsoki GitHub-agent Slidey deck from collected evidence and
 * captured MP4 clips.
 *
 * Strict mode is the default: all evidence notes and media files must exist,
 * and evidence URLs must point at the live bsacrobatix/Kitsoki + kitsoki-test
 * surfaces. Use --allow-missing-media only to emit a draft scaffold.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const CASES = [
  {
    slug: "bug-issue",
    section: "Section 3",
    eyebrow: "Live evidence",
    title: "Bug issue dispatch",
    subtitle: "bug label -> stories/bugfix -> done/run page",
    videoName: "03-bug-issue.mp4",
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
    videoName: "04-feature-issue.mp4",
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
    videoName: "05-guidance.mp4",
    expectedStory: "",
    expectedState: "awaiting_guidance",
    narration:
      "The guidance proof shows the safety path. When the issue is ambiguous, kitsoki asks for direction instead of guessing and parks the job at awaiting guidance.",
  },
  {
    slug: "pr-status",
    section: "Section 6",
    eyebrow: "Live evidence",
    title: "PR status route",
    subtitle: "PR mention -> pr-beat -> status/run link",
    videoName: "06-pr-status.mp4",
    expectedStory: "pr-beat",
    expectedState: "done",
    narration:
      "The PR status proof shows the pull request path: the mention routes as a PR, pr-beat reads status through the no-LLM host path, and kitsoki comments back with the run link.",
  },
];

const DEFAULT_OUT = ".artifacts/github-agent-live/live-github-agent.deck.json";
const DEFAULT_EVIDENCE_DIR = ".context";
const DEFAULT_MEDIA_ROOT = ".artifacts/github-agent-live";

function usage() {
  console.error(`usage: scripts/build-gh-agent-live-deck.mjs [options]

Options:
  --out <deck.json>              default ${DEFAULT_OUT}
  --evidence-dir <dir>           default ${DEFAULT_EVIDENCE_DIR}
  --media-root <dir>             default ${DEFAULT_MEDIA_ROOT}
  --developer-arc-media <path>   MP4 or rrweb clip for Section 7
  --allow-missing-media          emit a draft even when clip files are absent
  --allow-nonlive-urls           skip live URL host validation (tests only)
  -h, --help                     show this help

Inputs:
  <evidence-dir>/live-poc-bug-issue.md
  <evidence-dir>/live-poc-feature-issue.md
  <evidence-dir>/live-poc-guidance.md
  <evidence-dir>/live-poc-pr-status.md

Expected case clips:
  <media-root>/bug-issue/03-bug-issue.mp4
  <media-root>/feature-issue/04-feature-issue.mp4
  <media-root>/guidance/05-guidance.mp4
  <media-root>/pr-status/06-pr-status.mp4`);
}

function parseArgs(argv) {
  const args = {
    out: DEFAULT_OUT,
    evidenceDir: DEFAULT_EVIDENCE_DIR,
    mediaRoot: DEFAULT_MEDIA_ROOT,
    developerArcMedia: "",
    allowMissingMedia: false,
    allowNonliveUrls: false,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--out":
        args.out = argv[++i];
        break;
      case "--evidence-dir":
        args.evidenceDir = argv[++i];
        break;
      case "--media-root":
        args.mediaRoot = argv[++i];
        break;
      case "--developer-arc-media":
        args.developerArcMedia = argv[++i];
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
  const evidencePath = path.join(args.evidenceDir, `live-poc-${c.slug}.md`);
  const markdown = fs.readFileSync(evidencePath, "utf8");
  const publicBaseURL = requireURL(
    `${c.slug} Public base URL`,
    field(markdown, "Public base URL"),
    (u) => u === "https://kitsoki-test.slothattax.me",
    args.allowNonliveUrls,
  );
  const webhookURL = requireURL(
    `${c.slug} Webhook URL`,
    field(markdown, "Webhook URL"),
    (u) => u === "https://kitsoki-test.slothattax.me/gh-agent/webhook",
    args.allowNonliveUrls,
  );
  const sourceURL = requireURL(
    `${c.slug} Source URL`,
    field(markdown, "Source URL"),
    (u) => u.startsWith("https://github.com/bsacrobatix/Kitsoki/"),
    args.allowNonliveUrls,
  );
  const runURL = requireURL(
    `${c.slug} Run URL`,
    field(markdown, "Run URL"),
    (u) => u.startsWith("https://kitsoki-test.slothattax.me/run/"),
    args.allowNonliveUrls,
  );
  const apiURL = requireURL(
    `${c.slug} API URL`,
    field(markdown, "API URL"),
    (u) => u.startsWith("https://kitsoki-test.slothattax.me/api/run/"),
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
  }
  return { evidencePath, publicBaseURL, webhookURL, sourceURL, runURL, apiURL, commentURL, jobID, api };
}

function requireMedia(args, c) {
  const mediaPath = path.join(args.mediaRoot, c.slug, c.videoName);
  if (!fs.existsSync(mediaPath) && !args.allowMissingMedia) {
    throw new Error(`missing ${c.slug} clip: ${mediaPath}`);
  }
  return mediaPath;
}

function mediaScene(deckOut, c, evidence, mediaPath) {
  return {
    type: "video",
    mode: "embedded",
    eyebrow: c.eyebrow,
    title: c.title,
    src: relativeMediaPath(deckOut, mediaPath),
    caption: `${c.subtitle}. Evidence: ${evidence.sourceURL} -> ${evidence.runURL}`,
    narration: c.narration,
  };
}

function developerArcScene(args, deckOut) {
  if (!args.developerArcMedia) {
    if (!args.allowMissingMedia) {
      throw new Error("--developer-arc-media is required for the full final deck");
    }
    return {
      type: "title",
      eyebrow: "Section 7",
      title: "Slidey developer arc",
      subtitle: "Attach the existing QA-passed bugfix, refine, and PR clips before final render",
      narration:
        "The live GitHub POC connects to the existing Slidey developer arc. This draft marks where the QA-passed developer footage must be attached before the deck is final.",
    };
  }
  if (!fs.existsSync(args.developerArcMedia) && !args.allowMissingMedia) {
    throw new Error(`missing developer arc media: ${args.developerArcMedia}`);
  }
  const rel = relativeMediaPath(deckOut, args.developerArcMedia);
  const scene = {
    type: "video",
    mode: "embedded",
    eyebrow: "Existing evidence",
    title: "Slidey developer arc",
    caption: "Existing QA-passed Slidey bugfix, feature refine, and PR evidence.",
    narration:
      "This section connects the live GitHub front door to the existing Slidey developer arc evidence: bugfix, feature refinement, and pull request work.",
  };
  if (args.developerArcMedia.endsWith(".rrweb.json")) {
    scene.rrweb = rel;
    scene.chapters = "auto";
  } else {
    scene.src = rel;
  }
  return scene;
}

function buildDeck(args) {
  const evidence = new Map();
  const media = new Map();
  for (const c of CASES) {
    evidence.set(c.slug, readEvidence(args, c));
    media.set(c.slug, requireMedia(args, c));
  }
  const bug = evidence.get("bug-issue");
  const feature = evidence.get("feature-issue");
  const guidance = evidence.get("guidance");
  const pr = evidence.get("pr-status");

  const scenes = [
    {
      type: "title",
      eyebrow: "Live GitHub App POC",
      title: "@kitsoki on GitHub",
      subtitle: "Live GitHub App on kitsoki-test: real mentions, webhook deliveries, App comments, and hosted run pages",
      narration:
        `This deck is built from live evidence notes and captured clips. The front door is the live GitHub App on kitsoki-test at ${bug.webhookURL}. Static GitHub fixtures are not used as proof.`,
    },
    {
      type: "title",
      eyebrow: "Section 1",
      title: "Live GitHub front door",
      subtitle: `Real bsacrobatix/Kitsoki mentions delivered to ${bug.webhookURL}`,
      narration:
        `The live front door is proven by real GitHub issues and pull requests delivered to ${bug.webhookURL}: bug ${bug.sourceURL}, feature ${feature.sourceURL}, guidance ${guidance.sourceURL}, and PR ${pr.sourceURL}.`,
    },
    {
      type: "title",
      eyebrow: "Section 2",
      title: "Dispatch and run link",
      subtitle: "One claimed job, one story route, one public /run/<job-id> URL",
      narration:
        `Each live proof links to kitsoki-test: ${bug.runURL}, ${feature.runURL}, ${guidance.runURL}, and ${pr.runURL}.`,
    },
  ];

  for (const c of CASES) {
    scenes.push({
      type: "title",
      eyebrow: c.section,
      title: c.title,
      subtitle: c.subtitle,
      narration: c.narration,
    });
    scenes.push(mediaScene(args.out, c, evidence.get(c.slug), media.get(c.slug)));
  }

  scenes.push({
    type: "title",
    eyebrow: "Section 7",
    title: "Slidey developer arc",
    subtitle: "Existing QA-passed bugfix, feature refine, and PR clips",
    narration:
      "The GitHub POC is the front door. The next section connects it to the existing Slidey developer workflow evidence.",
  });
  scenes.push(developerArcScene(args, args.out));
  scenes.push({
    type: "title",
    eyebrow: "Section 8",
    title: "What remains",
    subtitle: "Full PR autopilot, artifact gallery, OAuth operator drive, review-thread resolve",
    narration:
      "This POC proves live GitHub App ingress, story dispatch, App comments, and hosted run links. Full PR autopilot, artifact galleries, OAuth operator drive, and review-thread resolution remain future product work.",
  });
  scenes.push({
    type: "cta",
    wordmark: "kitsoki",
    tagline: "Proven now: live GitHub App ingress, story dispatch, comments, run links",
    url: "Future: PR autopilot, artifacts, OAuth drive, review-thread resolve",
    narration:
      "The boundary is explicit: the POC proves the live GitHub front door and run-link loop, and it does not claim the full future autopilot.",
  });

  return {
    _comment:
      "Generated by scripts/build-gh-agent-live-deck.mjs from live evidence notes and captured media. Do not treat this as final if generated with --allow-missing-media.",
    meta: {
      title: "@kitsoki on GitHub, demonstrated through slidey",
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
  try {
    const deck = buildDeck(args);
    fs.mkdirSync(path.dirname(args.out), { recursive: true });
    fs.writeFileSync(args.out, `${JSON.stringify(deck, null, 2)}\n`);
    console.log(`wrote ${args.out}`);
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}

main();

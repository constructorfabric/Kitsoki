#!/usr/bin/env node
/*
 * Build a Playwright capture plan from a live @kitsoki evidence markdown file.
 *
 * Input is the markdown produced by scripts/collect-gh-agent-poc-evidence.sh.
 * Output is the JSON consumed by github-agent-live-capture.spec.ts.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const CASES = {
  "bug-issue": {
    videoName: "03-bug-issue",
    curtainTitle: "Live @kitsoki bug issue POC",
    threadTitle: "Real bug issue thread",
    threadCaption: "The real issue has a bug label, an @kitsoki comment, and an App-authenticated kitsoki response.",
    runTitle: "Bug issue run page",
    apiTitle: "Bug issue run JSON",
  },
  "feature-issue": {
    videoName: "04-feature-issue",
    curtainTitle: "Live @kitsoki feature issue POC",
    threadTitle: "Real feature issue thread",
    threadCaption: "The real issue has a feature or enhancement label, an @kitsoki comment, and an App-authenticated kitsoki response.",
    runTitle: "Feature issue run page",
    apiTitle: "Feature issue run JSON",
  },
  guidance: {
    videoName: "05-guidance",
    curtainTitle: "Live @kitsoki guidance POC",
    threadTitle: "Real ambiguous issue thread",
    threadCaption: "The ambiguous issue receives a guidance comment instead of an guessed route.",
    runTitle: "Guidance run page",
    apiTitle: "Guidance run JSON",
  },
  "pr-status": {
    videoName: "06-pr-status",
    curtainTitle: "Live @kitsoki PR status POC",
    threadTitle: "Real PR thread",
    threadCaption: "The real pull request receives an @kitsoki mention and an App-authenticated kitsoki response.",
    runTitle: "PR status run page",
    apiTitle: "PR status run JSON",
  },
};

function usage() {
  console.error(`usage: scripts/build-gh-agent-capture-plan.mjs --case <slug> --evidence <md> [--out <json>]

Cases: ${Object.keys(CASES).join(", ")}

Defaults:
  --evidence .context/live-poc-<case>.md
  --out      .artifacts/github-agent-live/capture-plan-<case>.json`);
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--case":
      case "--evidence":
      case "--out":
        out[arg.slice(2)] = argv[++i];
        break;
      case "-h":
      case "--help":
        out.help = true;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  return out;
}

function field(markdown, label) {
  const re = new RegExp(`^- ${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}:\\s*(.+?)\\s*$`, "m");
  const match = markdown.match(re);
  if (!match) return "";
  return match[1].replace(/^`|`$/g, "").trim();
}

function requireURL(name, value) {
  if (!/^https?:\/\//.test(value)) {
    throw new Error(`${name} must be an http(s) URL in the evidence file, got ${JSON.stringify(value)}`);
  }
  return value;
}

function buildPlan(caseSlug, markdown) {
  const cfg = CASES[caseSlug];
  if (!cfg) {
    throw new Error(`unknown case ${caseSlug}`);
  }
  const sourceURL = requireURL("Source URL", field(markdown, "Source URL"));
  const runURL = requireURL("Run URL", field(markdown, "Run URL"));
  const apiURL = requireURL("API URL", field(markdown, "API URL"));
  const commentURL = field(markdown, "Kitsoki comment URL");
  const appCommentURL = /^https?:\/\//.test(commentURL) ? commentURL : sourceURL;

  return {
    artifactDir: `.artifacts/github-agent-live/${caseSlug}`,
    videoName: cfg.videoName,
    curtainTitle: cfg.curtainTitle,
    steps: [
      {
        id: "github-thread",
        title: cfg.threadTitle,
        url: sourceURL,
        caption: cfg.threadCaption,
        waitForText: "@kitsoki",
      },
      {
        id: "app-comment",
        title: "App-authenticated kitsoki comment",
        url: appCommentURL,
        caption: "kitsoki comments back with a public run link.",
        waitForText: "kitsoki-test.slothattax.me/run/",
      },
      {
        id: "run-page",
        title: cfg.runTitle,
        url: runURL,
        caption: "The hosted run page shows the VM-backed job summary.",
        waitForText: "kitsoki GitHub run",
      },
      {
        id: "run-api",
        title: cfg.apiTitle,
        url: apiURL,
        caption: "The JSON endpoint exposes the same job state for automation.",
        waitForText: "origin_ref",
      },
    ],
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
  const caseSlug = args.case;
  if (!caseSlug || !CASES[caseSlug]) {
    usage();
    process.exit(2);
  }
  const evidencePath = args.evidence || `.context/live-poc-${caseSlug}.md`;
  const outPath = args.out || `.artifacts/github-agent-live/capture-plan-${caseSlug}.json`;
  const markdown = fs.readFileSync(evidencePath, "utf8");
  const plan = buildPlan(caseSlug, markdown);

  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  fs.writeFileSync(outPath, `${JSON.stringify(plan, null, 2)}\n`);
  console.log(`wrote ${outPath}`);
}

main();

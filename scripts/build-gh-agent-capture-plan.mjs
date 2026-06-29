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
    curtainTitle: "Live @kitsoki bug issue POC",
    threadTitle: "Real bug issue thread",
    threadCaption: "The real issue has a bug label, an @kitsoki comment, and an App-authenticated kitsoki response.",
    runTitle: "Bug issue run page",
    apiTitle: "Bug issue run JSON",
  },
  "feature-issue": {
    curtainTitle: "Live @kitsoki feature issue POC",
    threadTitle: "Real feature issue thread",
    threadCaption: "The real issue has a feature or enhancement label, an @kitsoki comment, and an App-authenticated kitsoki response.",
    runTitle: "Feature issue run page",
    apiTitle: "Feature issue run JSON",
  },
  guidance: {
    curtainTitle: "Live @kitsoki guidance POC",
    threadTitle: "Real ambiguous issue thread",
    threadCaption: "The ambiguous issue receives a guidance comment instead of an guessed route.",
    runTitle: "Guidance run page",
    apiTitle: "Guidance run JSON",
  },
  "guidance-resume": {
    curtainTitle: "Live @kitsoki guidance resume POC",
    threadTitle: "Real guidance resume thread",
    threadCaption: "The ambiguous issue first parks for guidance, then a label resumes the same job into a completed bugfix route.",
    appCommentWaitText: "Live @kitsoki POC bug label",
    runTitle: "Guidance resume run page",
    apiTitle: "Guidance resume run JSON",
  },
  "pr-status": {
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

function requireLiveURL(name, value, predicate) {
  const url = requireURL(name, value);
  if (!predicate(url)) {
    throw new Error(`${name} is not live POC evidence: ${url}`);
  }
  return url;
}

function normalizeBaseURL(value) {
  return String(value || "").replace(/\/+$/, "");
}

function githubRepoFromURL(sourceURL) {
  const match = String(sourceURL || "").match(/^https:\/\/github\.com\/([^/]+\/[^/]+)\//);
  return match ? match[1] : "";
}

function requireStatus(label, value, predicate, expected) {
  if (!predicate(value)) {
    throw new Error(`${label} check is ${JSON.stringify(value)}, expected ${expected}`);
  }
}

function buildPlan(caseSlug, markdown, artifactDir) {
  const cfg = CASES[caseSlug];
  if (!cfg) {
    throw new Error(`unknown case ${caseSlug}`);
  }
  const publicBaseURL = normalizeBaseURL(requireURL("Public base URL", field(markdown, "Public base URL")));
  const expectedWebhookURL = `${publicBaseURL}/gh-agent/webhook`;
  requireLiveURL(
    "Webhook URL",
    field(markdown, "Webhook URL"),
    (u) => u === expectedWebhookURL,
  );
  const sourceURL = requireLiveURL(
    "Source URL",
    field(markdown, "Source URL"),
    (u) => githubRepoFromURL(u) !== "",
  );
  const runURL = requireLiveURL(
    "Run URL",
    field(markdown, "Run URL"),
    (u) => u.startsWith(`${publicBaseURL}/run/`),
  );
  const apiURL = requireLiveURL(
    "API URL",
    field(markdown, "API URL"),
    (u) => u.startsWith(`${publicBaseURL}/api/run/`),
  );
  const appCommentURL = requireLiveURL(
    "Kitsoki comment URL",
    field(markdown, "Kitsoki comment URL"),
    (u) => u.startsWith(sourceURL) && u.includes("#issuecomment-"),
  );

  requireStatus("Health", field(markdown, "Health"), (v) => v === "ok", "ok");
  requireStatus(
    "Run page",
    field(markdown, "Run page"),
    (v) => /^HTTP\/[0-9.]+ 2[0-9][0-9]\b/.test(v),
    "HTTP 2xx",
  );
  requireStatus("API JSON", field(markdown, "API JSON"), (v) => v === "ok", "ok");
  requireStatus("Remote DB", field(markdown, "Remote DB"), (v) => v === "ok", "ok");

  return {
    artifactDir,
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
        waitForText: cfg.appCommentWaitText || `${new URL(publicBaseURL).host}/run/`,
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
  try {
    const markdown = fs.readFileSync(evidencePath, "utf8");
    const artifactDir = path.join(path.dirname(outPath), caseSlug);
    const plan = buildPlan(caseSlug, markdown, artifactDir);
    fs.mkdirSync(path.dirname(outPath), { recursive: true });
    fs.writeFileSync(outPath, `${JSON.stringify(plan, null, 2)}\n`);
    console.log(`wrote ${outPath}`);
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}

main();

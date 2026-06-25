#!/usr/bin/env node
/*
 * Write the gated kitsoki-ui-qa feature/scenario files for the live @kitsoki
 * GitHub-agent Slidey deck. This does not run the LLM-backed QA gate.
 */

import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const DEFAULT_FEATURE = ".context/qa-gh-agent-live-feature.md";
const DEFAULT_SCENARIOS = ".context/qa-gh-agent-live-scenarios.yaml";
const DEFAULT_VIDEO = ".artifacts/github-agent-live/live-github-agent.mp4";
const DEFAULT_FRAMES = "";

function usage() {
  console.error(`usage: scripts/write-gh-agent-live-qa-plan.mjs [options]

Options:
  --feature <md>       default ${DEFAULT_FEATURE}
  --scenarios <yaml>   default ${DEFAULT_SCENARIOS}
  --video <mp4>        default ${DEFAULT_VIDEO}
  --frames <dir>       optional labeled frame dir for qa.sh --frames
  -h, --help           show this help

Writes:
  <feature>
  <scenarios>

Then run the printed qa.sh command after the final deck MP4/frames exist. The
QA command is intentionally not run automatically because kitsoki-ui-qa uses the
local claude vision reviewer and is operator-gated.`);
}

function parseArgs(argv) {
  const args = {
    feature: DEFAULT_FEATURE,
    scenarios: DEFAULT_SCENARIOS,
    video: DEFAULT_VIDEO,
    frames: DEFAULT_FRAMES,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--feature":
        args.feature = argv[++i];
        break;
      case "--scenarios":
        args.scenarios = argv[++i];
        break;
      case "--video":
        args.video = argv[++i];
        break;
      case "--frames":
        args.frames = argv[++i];
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

function featureMarkdown() {
  return `# QA Feature: Live @kitsoki GitHub Agent Slidey Deck

## What This Is

The final proof artifact is the live @kitsoki GitHub-agent Slidey deck produced
from the current POC run:

- deck spec: \`.artifacts/github-agent-live/live-github-agent.deck.json\`
- self-contained HTML: \`.artifacts/github-agent-live/live-github-agent.html\`
- review/render video: \`.artifacts/github-agent-live/live-github-agent.mp4\`
- evidence notes: \`.context/live-poc-*.md\`

The deck must prove the live GitHub App loop, not the old static GitHub fixture.
The GitHub evidence must come from real \`bsacrobatix/Kitsoki\` issues/PRs,
real \`@kitsoki\` comments, real App-authenticated kitsoki replies, real public
\`https://kitsoki-test.slothattax.me/run/<job-id>\` links, the live
\`https://kitsoki-test.slothattax.me/gh-agent/webhook\` front door, and
VM-backed job state captured in the evidence notes.

## What The Evidence Should Show

1. A real GitHub user mentions \`@kitsoki\` on a live issue or PR.
2. The deck identifies the live GitHub App webhook on kitsoki-test.
3. kitsoki comments back as the GitHub App with a public run link.
4. The run link opens the hosted kitsoki run page.
5. The bug, feature, guidance, and PR-status cases are each distinct and named.
6. The Slidey developer-arc media is present and plays actual content.
7. The closing section clearly separates the POC that works now from future
   product work such as full PR autopilot, artifact gallery, OAuth operator
   drive, and review-thread resolution.

## Required Boundary

The deck must not present \`gh-thread.html\`, fixture screenshots, or static
GitHub mocks as live evidence. It must not claim full autonomous PR rebase,
force-push, review-thread resolution, artifact gallery, or OAuth operator drive
unless those capabilities are visibly implemented in the captured evidence.
`;
}

function scenariosYAML() {
  return `scenarios:
  - id: live-github-front-door
    title: "GitHub act uses live GitHub evidence, not the fixture"
    required: true
    steps:
      - "The deck title or opening section identifies '@kitsoki on GitHub' or 'live GitHub App' and references kitsoki-test."
      - "The opening GitHub section visibly references the live webhook URL https://kitsoki-test.slothattax.me/gh-agent/webhook or clearly states that real mentions are delivered to that live kitsoki-test webhook."
      - "A real GitHub issue or pull request page from bsacrobatix/Kitsoki is visible, not gh-thread.html or a static fixture/mock page."
      - "A visible comment includes '@kitsoki' from the requester and a kitsoki App response on the same real thread."

  - id: run-link-loop
    title: "A kitsoki App comment links to the hosted run page"
    required: true
    steps:
      - "A kitsoki App-authenticated comment is visible in the GitHub thread."
      - "The comment visibly includes a https://kitsoki-test.slothattax.me/run/ link."
      - "The hosted run page opens and shows a kitsoki GitHub run summary rather than a blank or unrelated page."

  - id: bug-feature-guidance-pr-distinct
    title: "Bug, feature, guidance, and PR-status sections are distinguishable"
    required: true
    steps:
      - "The bug issue section is labeled as a bug issue dispatch and shows a done/run page path for stories/bugfix."
      - "The feature issue section is labeled as feature or enhancement and shows a dev-story/design route or run page."
      - "The guidance section shows an ambiguous issue and a guidance/awaiting_guidance outcome rather than a guessed bug or feature route."
      - "The PR-status section shows a pull request mention and a PR status path, not an issue-only flow."

  - id: live-run-state-backed
    title: "The run surface is backed by live job state"
    required: true
    steps:
      - "At least one run page or JSON view shows the job id, origin/source GitHub URL, selected story, state, and updated/comment information."
      - "The evidence implies one claimed job per GitHub origin, not repeated spam comments or duplicate unrelated jobs."

  - id: developer-arc-present
    title: "Slidey developer arc content is present and not frozen"
    required: true
    steps:
      - "The deck includes a Slidey developer arc section after the GitHub POC sections."
      - "The developer-arc media shows real Slidey/kitsoki content such as bugfix, feature refine, or PR evidence, not a blank or frozen placeholder."

  - id: remaining-work-boundary
    title: "The closing slide separates proven POC from future work"
    required: true
    steps:
      - "A final section or slide is titled 'What remains' or equivalent."
      - "The deck explicitly names future work such as full PR autopilot, artifact gallery, OAuth operator drive, or review-thread resolve as remaining work."
      - "The deck does not claim those future capabilities are already implemented unless the video visibly proves them."
`;
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function qaCommand(args) {
  const parts = [
    ".agents/skills/kitsoki-ui-qa/scripts/qa.sh",
    shellQuote(args.video),
  ];
  if (args.frames) {
    parts.push("--frames", shellQuote(args.frames));
  }
  parts.push(
    "--feature",
    shellQuote(args.feature),
    "--scenarios",
    shellQuote(args.scenarios),
    "--strict",
    "--pacing-strict",
  );
  return parts.join(" ");
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
  for (const file of [args.feature, args.scenarios]) {
    if (!file) {
      console.error("--feature and --scenarios must not be empty");
      process.exit(2);
    }
    fs.mkdirSync(path.dirname(file), { recursive: true });
  }
  fs.writeFileSync(args.feature, featureMarkdown());
  fs.writeFileSync(args.scenarios, scenariosYAML());
  console.log(`wrote ${args.feature}`);
  console.log(`wrote ${args.scenarios}`);
  console.log("");
  console.log("Run the gated QA command after the final deck MP4 or frames exist:");
  console.log(qaCommand(args));
}

main();

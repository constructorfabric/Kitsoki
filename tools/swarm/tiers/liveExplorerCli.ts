/**
 * liveExplorerCli.ts — the REAL tier-3 entry point. Real LLM spend, every
 * invocation. NOT run by any test in this repo (grep tools/runstatus/tests/
 * for this file's name — it will only turn up tools/swarm/README.md and this
 * comment). See tools/swarm/README.md's "Tier 3: manual live-acceptance
 * procedure" for how an operator runs this by hand.
 *
 * Structural gating: this file is the ONLY place `liveExplorers: true` is
 * ever constructed, and it is threaded from a literal `--live-explorers`
 * flag in `process.argv` — not an env var, not a config default. Deleting
 * the flag from the invocation makes `parseArgs` below return `false`, which
 * makes `dispatchExplorers` (tools/swarm/tiers/explorer.ts) throw
 * `LiveExplorersNotAllowedError` before any browser or agent process starts.
 *
 * What ONE explorer does (`runLiveExplorer`, the `RunExplorerFn` passed to
 * `dispatchExplorers`):
 *   1. Mints its OWN session on the shared swarm server (`runstatus.session.new`)
 *      and opens a headless Playwright browser context on it — the "holds a
 *      browser context" requirement.
 *   2. Spawns a real agent process (`--agent-cmd`, default `claude`) with a
 *      persona-briefed prompt instructing it to use kitsoki's MCP tool
 *      surface (visual_*/session_* — already wired by
 *      `kitsoki project-tools install`, see docs/architecture) to explore the
 *      session, and to journal what it finds via product-journey's own CLI
 *      (`python3 tools/product-journey/run.py --record-finding` /
 *      `--record-blocker`) against `--run-dir`.
 *   3. On the Node/Playwright side (NOT inside the spawned agent — this
 *      process owns the live `Page`), calls `tools/swarm/capture`'s
 *      `recordFinding` once after the agent exits, so every explorer leaves
 *      an rrweb+console+HAR evidence bundle under
 *      `.artifacts/swarm/findings/` regardless of what the agent itself
 *      chose to journal narratively — the same "capture loop" tier 1/2's
 *      per-user gate failures use (tools/swarm/capture/recordFinding.ts).
 *
 * Usage (manual only — never from CI, never from a test):
 *   node --loader tsx tools/swarm/tiers/liveExplorerCli.ts \
 *     --live-explorers \
 *     --server http://127.0.0.1:7799 \
 *     --story-path stories/off-ramp-demo/app.yaml \
 *     --run-dir .artifacts/product-journey/<run-id> \
 *     --max-explorers 3 \
 *     [--agent-cmd claude] [--budget-usd 5]
 */
// Type-only: erased at build time (see journey.ts's top-of-file note on why
// tools/swarm/ never takes a RUNTIME import of a Playwright package — it has
// no node_modules of its own, see tools/swarm/README.md "why no
// dependencies"). The one runtime need (chromium.launch) is resolved lazily
// in `loadChromium` below, rooted at tools/runstatus's node_modules via
// `createRequire`, so this file still never needs its OWN node_modules.
import type { Browser } from "@playwright/test";
import { execFileSync } from "child_process";
import { createRequire } from "module";
import path from "path";
import { fileURLToPath } from "url";
import {
  dispatchExplorers,
  MAX_LIVE_EXPLORERS,
  type ExplorerBriefing,
  type ExplorerLens,
  type ExplorerOutcome,
} from "./explorer.js";
import { loadPersonas } from "../personas.js";
import { recordFinding, defaultFindingsDir } from "../capture/index.js";

const __filename = fileURLToPath(import.meta.url);
const repoRoot = path.resolve(path.dirname(__filename), "../../..");

/**
 * Resolves and imports `@playwright/test`'s `chromium` WITHOUT tools/swarm/
 * needing its own node_modules: `createRequire` is rooted at
 * tools/runstatus/package.json, so its resolution algorithm walks
 * tools/runstatus/node_modules (where `@playwright/test` actually lives) —
 * entirely independent of where THIS file sits on disk. Only called from the
 * `--live-explorers` path (`main`, below `if (process.argv[1] === __filename)`
 * at the bottom of this file); the CI stub test never reaches it.
 */
async function loadChromium(): Promise<{ launch(opts: { headless: boolean }): Promise<Browser> }> {
  const req = createRequire(path.join(repoRoot, "tools", "runstatus", "package.json"));
  const resolved = req.resolve("@playwright/test");
  const mod = (await import(resolved)) as { chromium: { launch(opts: { headless: boolean }): Promise<Browser> } };
  return mod.chromium;
}

interface CliArgs {
  liveExplorers: boolean;
  server: string;
  storyPath: string;
  runDir: string;
  maxExplorers: number;
  agentCmd: string;
  budgetUsd?: number;
}

/** Parses `process.argv`. `liveExplorers` is `true` ONLY if `--live-explorers`
 *  is literally present — no env var fallback, no implicit default. */
export function parseArgs(argv: string[]): CliArgs {
  const get = (flag: string): string | undefined => {
    const i = argv.indexOf(flag);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  return {
    liveExplorers: argv.includes("--live-explorers"),
    server: get("--server") ?? "",
    storyPath: get("--story-path") ?? "",
    runDir: get("--run-dir") ?? "",
    maxExplorers: Number(get("--max-explorers") ?? String(MAX_LIVE_EXPLORERS)),
    agentCmd: get("--agent-cmd") ?? "claude",
    budgetUsd: get("--budget-usd") ? Number(get("--budget-usd")) : undefined,
  };
}

/** Mirrors `tools/product-journey/run.py`'s `persona_lens()` for the five
 *  cataloged personas (tools/product-journey/personas.json), plus that
 *  function's own generic fallback for anything else. Kept as plain data
 *  (not a python subprocess call) — see explorer.ts's `ExplorerLens` doc
 *  comment for why. */
export function lensForPersona(persona: { id: string; label: string; risk_focus?: string[] }): ExplorerLens {
  const table: Record<string, ExplorerLens> = {
    "core-maintainer": {
      starting_surface: "terminal-first; prefer TUI/session state before browser surfaces",
      first_question: "Will this produce a minimal, reviewable diff that follows the repository's style?",
      evidence_emphasis: "candidate diff, targeted tests, full-suite classification, and trace events for unexpected routing",
      escalation_trigger: "generated churn, hidden broad scope, missing deterministic test proof, or unclear ownership boundary",
      finding_bias: "Prefer reviewability, bisectability, and least-surprise findings over cosmetic notes.",
    },
    "dependency-debugger": {
      starting_surface: "web-first; begin from public issue/repro context and then enter the story surface",
      first_question: "How quickly can I reproduce the dependency bug and decide whether Kitsoki's fix is trustworthy?",
      evidence_emphasis: "reproduction steps, oracle output, key interaction video, and handoff artifacts",
      escalation_trigger: "unclear repro setup, missing oracle, ambiguous pass/fail state, or no handoff artifact for my app",
      finding_bias: "Favor time-to-repro, confidence, and downstream handoff clarity.",
    },
    "docs-minded-contributor": {
      starting_surface: "docs-first; follow the documented path before trying hidden commands",
      first_question: "Can I follow the docs without private repo context or tribal knowledge?",
      evidence_emphasis: "page URLs, screenshots, prerequisite commands, stale-link proof, and onboarding smoke results",
      escalation_trigger: "stale docs, missing prerequisites, broken media, or commands that require unexplained setup",
      finding_bias: "Prefer onboarding clarity, documented next actions, and confusing-copy findings.",
    },
    "ide-first-engineer": {
      starting_surface: "visual-first; use web, TUI PNG, or editor-like surfaces before terminal archaeology",
      first_question: "Can I understand current state and next action from the visible UI?",
      evidence_emphasis: "visual frames, retained image IDs, operator-question state, navigation traces, and key interaction video",
      escalation_trigger: "state that is only visible in logs, silent operator defaults, confusing navigation, or unreadable layout",
      finding_bias: "Favor visible-state, affordance, and navigation findings.",
    },
    "hobbyist-contributor": {
      starting_surface: "docs-and-web-first; avoid repo archaeology until the product gives a concrete next step",
      first_question: "Can I make meaningful progress in a short spare-time session without knowing the repository's internal conventions?",
      evidence_emphasis: "setup commands, first-success proof, small issue selection, key interaction video, and explicit stop points",
      escalation_trigger: "unclear prerequisites, long-running setup, ambiguous next action, or work that expands beyond a small contribution",
      finding_bias: "Favor time-budget, setup-friction, and beginner-safe next-step findings.",
    },
  };
  return (
    table[persona.id] ?? {
      starting_surface: "surface chosen by the explorer",
      first_question: `What would a ${persona.label ?? "reviewer"} naturally try first, and what evidence proves the result?`,
      evidence_emphasis: (persona.risk_focus ?? []).join(", ") || "scenario minimum evidence",
      escalation_trigger: "the exploration cannot produce proof evidence or a clear blocker",
      finding_bias: "Tie findings to the persona risk focus.",
    }
  );
}

/** Builds this explorer's briefing prompt for the spawned agent process. */
function buildPrompt(briefing: ExplorerBriefing): string {
  return [
    `You are exploring a live kitsoki web session as the "${briefing.persona.label}" persona.`,
    `Persona: ${briefing.persona.description}`,
    `Starting surface: ${briefing.lens.starting_surface}`,
    `First question to keep asking yourself: ${briefing.lens.first_question}`,
    `Evidence to emphasize: ${briefing.lens.evidence_emphasis}`,
    `Escalate (stop and record a blocker) if: ${briefing.lens.escalation_trigger}`,
    `Finding bias: ${briefing.lens.finding_bias}`,
    ``,
    `The session is served at ${briefing.serverBase}. Use kitsoki's MCP tools`,
    `(visual_open/visual_observe/visual_act, session_*) to explore it freely.`,
    `Whenever you find something worth recording, call:`,
    `  python3 tools/product-journey/run.py --record-finding --run-dir ${briefing.runDir} \\`,
    `    --finding-kind <strength|weakness|issue|fix> --title <title> --summary <summary>`,
    `If you hit a hard blocker, call:`,
    `  python3 tools/product-journey/run.py --record-blocker --run-dir ${briefing.runDir} \\`,
    `    --scenario <scenario-id> --title <title> --summary <summary>`,
    `AskUserQuestion is not available to you headless — proceed on your own judgment and`,
    `record any ambiguity you had to resolve as a finding rather than stopping to ask.`,
  ].join("\n");
}

/** REAL `RunExplorerFn`: mints a session, opens a browser context, spawns the
 *  agent, captures evidence, tears down. Never called except from `main()`
 *  below, which is only reachable when `--live-explorers` was passed. */
async function runLiveExplorer(
  browser: Browser,
  server: string,
  storyPath: string,
  agentCmd: string,
): Promise<(briefing: ExplorerBriefing, index: number) => Promise<ExplorerOutcome>> {
  return async (briefing, index) => {
    const consoleMessages: string[] = [];
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleMessages.push(msg.text());
    });
    page.on("pageerror", (err) => consoleMessages.push(err.message));

    const rpc = async <T>(method: string, params: Record<string, unknown>): Promise<T> => {
      const res = await fetch(`${server}/rpc`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: index + 1, method, params }),
      });
      const body = (await res.json()) as { result?: T; error?: { message: string } };
      if (body.error) throw new Error(`${method} failed: ${body.error.message}`);
      return body.result as T;
    };

    let ok = true;
    let error: string | undefined;
    try {
      const { session_id } = await rpc<{ session_id: string }>("runstatus.session.new", {
        story_path: storyPath,
      });
      await page.goto(`${server}/#/s/${session_id}/chat`);

      const prompt = buildPrompt(briefing);
      // Real LLM spend happens here.
      execFileSync(agentCmd, ["-p", prompt], { cwd: repoRoot, stdio: "inherit" });

      await recordFinding({
        page,
        rpc,
        consoleMessages,
        context: {
          persona_id: briefing.persona.id,
          journey_step: "explorer-session",
          assertion: "tier-3 explorer run completed",
        },
        repoRoot,
        findingsDir: defaultFindingsDir(repoRoot),
      });
    } catch (err) {
      ok = false;
      error = err instanceof Error ? err.message : String(err);
    } finally {
      await page.close().catch(() => undefined);
      await context.close().catch(() => undefined);
    }

    return {
      persona_id: briefing.persona.id,
      ok,
      // The agent journals its own findings/blockers via product-journey's
      // CLI directly; this runner does not parse its stdout to count them
      // (that would require a fragile output contract with the agent
      // process). Operators read findings.json in --run-dir after the run.
      findings_recorded: 0,
      blockers_recorded: 0,
      error,
    };
  };
}

export async function main(argv: string[]): Promise<void> {
  const args = parseArgs(argv);
  if (!args.liveExplorers) {
    // Fails fast via dispatchExplorers's own gate, but check here too so the
    // CLI never even launches a browser when the flag is missing.
    console.error("refusing to start: pass --live-explorers explicitly (see tools/swarm/README.md)");
    process.exitCode = 1;
    return;
  }
  if (!args.server || !args.storyPath || !args.runDir) {
    console.error("usage: --live-explorers --server <url> --story-path <path> --run-dir <dir> [--max-explorers N]");
    process.exitCode = 1;
    return;
  }

  const personas = loadPersonas();
  const chromium = await loadChromium();
  const browser = await chromium.launch({ headless: true });
  try {
    const runExplorer = await runLiveExplorer(browser, args.server, args.storyPath, args.agentCmd);
    const result = await dispatchExplorers({
      liveExplorers: args.liveExplorers,
      requestedCount: args.maxExplorers,
      personas,
      serverBase: args.server,
      runDir: args.runDir,
      lensFor: lensForPersona,
      runExplorer,
    });
    console.log(JSON.stringify(result, null, 2));
    if (!result.all_ok) process.exitCode = 1;
  } finally {
    await browser.close();
  }
}

// Only run when invoked directly (`node .../liveExplorerCli.ts`), never on import.
if (process.argv[1] === __filename) {
  main(process.argv.slice(2)).catch((err) => {
    console.error(err);
    process.exitCode = 1;
  });
}

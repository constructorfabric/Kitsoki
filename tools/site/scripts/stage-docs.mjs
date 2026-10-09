#!/usr/bin/env node
/**
 * Stage the ALLOWLISTED repo docs into the site's gitignored src/guide/ tree.
 *
 * Allowlist-copy (not srcDir-over-docs/, not symlinks) is a structural
 * guarantee: internal material (docs/proposals/, docs/competitive-analysis/,
 * .agents/skills/, ...) can never leak onto the published site because it is
 * never staged. Markdown links that escape the allowlist are rewritten to
 * GitHub blob/raw URLs so they stay alive instead of going dead — and VitePress
 * runs with ignoreDeadLinks:false, so a missed rewrite FAILS the build rather
 * than publishing a broken link.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { expandManifest } from "./manifest.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.join(__dirname, ".."));
const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.join(siteDir, "../.."));
const runtimeSiteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? siteDir);
const srcDir = path.join(runtimeSiteDir, "src");
const guideDir = path.join(srcDir, "guide");

const { repoUrl, branch, sections } = expandManifest(siteDir, repoRoot);

/** Flat [repoPath -> sitePath] map over every expanded manifest entry. */
function expand() {
  const map = new Map();
  for (const section of sections) {
    for (const e of section.entries) map.set(e.from, e.to);
  }
  return map;
}

/** Rewrite one markdown link target found in `repoFile` (repo-relative). */
function rewriteTarget(target, repoFile, siteFile, map) {
  // Leave external/anchor/absolute targets alone.
  if (/^(https?:|mailto:|#|\/)/.test(target)) return target;
  const [p, anchor = ""] = target.split(/(#.*)$/, 2);
  if (p === "") return target;
  const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(repoFile), p));
  if (map.has(resolved)) {
    const dest = map.get(resolved);
    let rel = path.posix.relative(path.posix.dirname(siteFile), dest);
    if (!rel.startsWith(".")) rel = "./" + rel;
    return rel + anchor;
  }

  const localSiteTarget = localSiteTargetForRepoPath(resolved);
  if (localSiteTarget) return localSiteTarget + anchor;

  // Escapes the allowlist — point at GitHub so the reference stays alive.
  if (!fs.existsSync(path.join(repoRoot, resolved))) {
    // Path doesn't exist in the repo either; keep as-is and let the dead-link
    // check surface it (it is broken at the source).
    return target;
  }
  return `${repoUrl}/blob/${branch}/${resolved}${anchor}`;
}

function localSiteTargetForRepoPath(repoPath) {
  const deckSource = repoPath.match(/^docs\/decks\/([^/]+?)(?:\.slidey)?\.json$/);
  if (deckSource && fs.existsSync(path.join(repoRoot, repoPath))) {
    return `/decks/${deckSource[1]}.html`;
  }

  const deckBundle = repoPath.match(/^docs\/decks\/bundled\/([^/]+)\.html$/);
  if (deckBundle && fs.existsSync(path.join(repoRoot, repoPath))) {
    return `/decks/${deckBundle[1]}.html`;
  }

  return "";
}

function rewriteLinks(content, repoFile, siteFile, map) {
  // Inline links + images: [text](target) / ![alt](target). Images that escape
  // the allowlist use the raw URL so they still render as images.
  return content.replace(/(!?)\[([^\]]*)\]\(([^)\s]+)\)/g, (m, bang, text, target) => {
    let out = rewriteTarget(target, repoFile, siteFile, map);
    if (bang === "!" && out.startsWith(`${repoUrl}/blob/`)) {
      out = out.replace("/blob/", "/raw/");
    }
    return `${bang}[${text}](${out})`;
  });
}

function escapeRawHtmlPlaceholders(content) {
  let fenced = false;
  return content
    .split("\n")
    .map((line) => {
      if (/^\s*(```|~~~)/.test(line)) {
        fenced = !fenced;
        return line;
      }
      if (fenced) return line;
      return line.replace(/</g, "&lt;").replace(/&lt;\/?([A-Za-z][^>\n]{0,80})>/g, (m, inner) => {
        if (/^[a-z][a-z0-9+.-]*:/i.test(inner)) return m;
        return m.replace(/>/g, "&gt;");
      });
    })
    .join("\n");
}

function addVPreFrontmatter(content) {
  if (!content.startsWith("---\n")) {
    return `---\nv-pre: true\n---\n\n${content}`;
  }
  const end = content.indexOf("\n---", 4);
  if (end === -1) {
    return `---\nv-pre: true\n---\n\n${content}`;
  }
  const frontmatter = content.slice(4, end);
  if (/^v-pre:/m.test(frontmatter)) return content;
  return `---\nv-pre: true\n${frontmatter}${content.slice(end)}`;
}

function wrapVPreContainer(content) {
  if (!content.startsWith("---\n")) return `::: v-pre\n${content}\n:::\n`;
  const end = content.indexOf("\n---", 4);
  if (end === -1) return `::: v-pre\n${content}\n:::\n`;
  const closeEnd = end + "\n---".length;
  const afterClose = content[closeEnd] === "\n" ? closeEnd + 1 : closeEnd;
  return `${content.slice(0, afterClose)}\n::: v-pre\n${content.slice(afterClose)}\n:::\n`;
}

function fencedCodeToIndented(content) {
  const lines = content.split("\n");
  const out = [];
  let fenced = false;
  let preserveFence = false;
  for (const line of lines) {
    const fence = line.match(/^\s*(```|~~~)\s*([^\s`]*)/);
    if (fence) {
      if (!fenced) {
        preserveFence = fence[2] === "mermaid";
      }
      if (preserveFence) out.push(line);
      else out.push("");
      fenced = !fenced;
      if (!fenced) preserveFence = false;
      continue;
    }
    out.push(fenced && !preserveFence ? `    ${line}` : line);
  }
  return out.join("\n");
}

function firstHeading(repoPath) {
  const abs = path.join(repoRoot, repoPath);
  if (!fs.existsSync(abs)) return path.basename(repoPath, ".md");
  const match = fs.readFileSync(abs, "utf8").match(/^#\s+(.+)$/m);
  return match ? match[1].trim() : path.basename(repoPath, ".md");
}

function siteHref(sitePath) {
  return "/" + sitePath.replace(/\.md$/, ".html").replace(/index\.html$/, "");
}

function writeDocsLanding() {
  const lines = [
    "---",
    "layout: doc",
    "---",
    "",
    "# Kitsoki docs",
    "",
    "Use this page as a reading path, not as a sitemap. If you are new, answer the evaluator questions in order; if you already know what you need, use the collapsed sidebar or search for the full allowlisted docs inventory.",
    "",
    "## Decide if Kitsoki fits",
    "",
    "- [Evaluate Kitsoki](/guide/evaluate-kitsoki.html) explains the control-inversion claim and compares it with coding agents, orchestration frameworks, durable workflow engines, and scripts.",
    "- [Proof path](/proof.html) gives the short demo sequence: runtime guardrails, trace replay, operator handoff, and real repo workflows.",
    "- [Concept](/guide/architecture/concept.html) is the architecture thesis behind progressive determinism.",
    "",
    "## Understand the architecture",
    "",
    "- [Architecture](/guide/architecture/) is the organized map of the engine, runtime boundaries, agent surfaces, and integration contracts.",
    "- [System map](/guide/architecture/overview.html) shows the package layers, turn loop, LLM boundary, persistence model, and trust model.",
    "- [MCP Studio](/guide/architecture/mcp-studio.html) is the external-agent facade for authoring, driving, testing, and inspecting Kitsoki without live LLM spend by default.",
    "",
    "## Try it locally",
    "",
    "- [Getting started](/guide/getting-started.html) is the shortest path from a downloaded binary to `onboard .` in an existing repo.",
    "- [Download Kitsoki](/download.html) lists release artifacts and checksums.",
    "- [GitHub App setup](/guide/integrations/github-app-setup.html) covers tighter repo-scoped GitHub auth when the local `gh` path is not enough.",
    "",
    "## Build or change a story",
    "",
    "- [Stories](/guide/stories/) introduces rooms, intents, guards, transitions, and effects.",
    "- [Authoring Guide](/guide/stories/authoring.html) is the practical story-writing guide.",
    "- [Recipe: add an intent](/guide/recipes/add-an-intent.html) is the smallest useful edit path.",
    "- [Recipe: deterministic flow test](/guide/recipes/flow-test-with-cassette.html) shows how to cover a story without live LLM spend.",
    "",
    "## Test, replay, and debug",
    "",
    "- [Testing](/guide/tracing/testing.html) explains flow fixtures and host cassettes.",
    "- [Kitsoki JSONL Trace Format](/guide/tracing/trace-format.html) documents the audit trail.",
    "- [Run-status web UI](/guide/tracing/run-status-ui.html) covers trace inspection and replay surfaces.",
    "- [MCP studio](/guide/architecture/mcp-studio.html) lets external agents author, drive, test, and inspect Kitsoki through one facade.",
    "",
    "## Browse by area",
    "",
    "The sidebar contains the full docs inventory, collapsed by section so this page stays readable. These section starts are the useful broad entries:",
    "",
    "- [Architecture](/guide/architecture/)",
    "- [Agent guide](/guide/agents/)",
    "- [Development guide](/guide/development/)",
    "- [Integration guide](/guide/integrations/)",
    "- [Authoring stories](/guide/stories/)",
    "- [Testing and replay](/guide/tracing/)",
    "- [Recipes](/guide/recipes/)",
    "- [User interfaces](/guide/web/)",
    "- [Case studies](/guide/case-studies/)",
    "- [Reference](/guide/embedded/app-schema.html)",
    "",
  ];

  fs.writeFileSync(path.join(guideDir, "index.md"), lines.join("\n"));
}

fs.rmSync(guideDir, { recursive: true, force: true });
const map = expand();
let staged = 0;
for (const [from, to] of map) {
  const content = fs.readFileSync(path.join(repoRoot, from), "utf8");
  const out = path.join(srcDir, to);
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(
    out,
    wrapVPreContainer(addVPreFrontmatter(fencedCodeToIndented(escapeRawHtmlPlaceholders(rewriteLinks(content, from, to, map))))),
  );
  staged++;
}
writeDocsLanding();
console.log(`stage-docs: staged ${staged} doc(s) + landing -> ${path.relative(repoRoot, guideDir)}`);

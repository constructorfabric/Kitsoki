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
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(siteDir, "../..");
const srcDir = path.join(siteDir, "src");
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
  // Escapes the allowlist — point at GitHub so the reference stays alive.
  if (!fs.existsSync(path.join(repoRoot, resolved))) {
    // Path doesn't exist in the repo either; keep as-is and let the dead-link
    // check surface it (it is broken at the source).
    return target;
  }
  return `${repoUrl}/blob/${branch}/${resolved}${anchor}`;
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
  for (const line of lines) {
    if (/^\s*(```|~~~)/.test(line)) {
      fenced = !fenced;
      out.push("");
      continue;
    }
    out.push(fenced ? `    ${line}` : line);
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
    "Start with the evaluation path if you are deciding whether Kitsoki is worth the structure. Kitsoki's core claim is control inversion: the workflow is an auditable state machine, and the LLM is a bounded callee at named, traceable decision points.",
    "",
    "## Evaluate the claim",
    "",
    "- [Evaluate Kitsoki](/guide/evaluate-kitsoki.html): the skeptical-developer case for why this is not just a chat agent, a structured-output wrapper, or a workflow engine with prompts attached.",
    "- [Concept](/guide/architecture/concept.html): the architecture thesis behind control inversion and progressive determinism.",
    "- [Proof demos](/features/): videos generated from deterministic feature fixtures, including runtime guardrails, trace introspection, operator handoff, and replayed real runs.",
    "- [Bug-fix case study](/guide/case-studies/bug-fix.html): the shape of an end-to-end repo workflow: reproduce, patch, test, review, validate.",
    "- [Bugfix bake-off](/guide/case-studies/bugfix-bakeoff.html): early evidence for the claim that structure can matter more than another unbounded prompt.",
    "",
    "## Why the docs matter",
    "",
    "A Kitsoki story is not hidden in a prompt. The docs below cover the public pieces of that story model: how to author rooms and intents, how host calls and traces work, how replay removes live LLM spend from testing, and how the same story drives web, TUI, MCP, demos, and fixtures.",
    "",
  ];

  for (const section of sections) {
    lines.push(`## ${section.title}`, "");
    for (const entry of section.entries) {
      lines.push(`- [${firstHeading(entry.from)}](${siteHref(entry.to)})`);
    }
    lines.push("");
  }

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

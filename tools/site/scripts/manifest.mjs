/**
 * Shared expansion of docs-manifest.json: the allowlist of repo docs published
 * on the site. Used by stage-docs.mjs (copying) and .vitepress/data/features.ts
 * (doc-link mapping + sidebar).
 */
import * as fs from "fs";
import * as path from "path";

function expandItems(items, repoRoot) {
  const entries = [];
  for (const item of items ?? []) {
    if (item.from.endsWith("/*.md")) {
      const dir = item.from.slice(0, -"/*.md".length);
      const abs = path.join(repoRoot, dir);
      if (!fs.existsSync(abs)) {
        throw new Error(`docs-manifest glob dir missing: ${dir}`);
      }
      for (const name of fs.readdirSync(abs).filter((f) => f.endsWith(".md")).sort()) {
        const to = item.to + (name === "README.md" ? "index.md" : name);
        entries.push({ from: path.posix.join(dir, name), to });
      }
    } else {
      if (!fs.existsSync(path.join(repoRoot, item.from))) {
        throw new Error(`docs-manifest source missing: ${item.from}`);
      }
      entries.push({ from: item.from, to: item.to, title: item.title });
    }
  }
  return entries;
}

/**
 * Returns { repoUrl, branch, sections } where each section is
 * { title, entries, groups } with globs expanded ("dir/*.md" → one entry per
 * file, README.md mapped to index.md). `groups` is optional manifest metadata
 * for sidebars; `entries` is always flat for staging and link rewriting.
 */
export function expandManifest(siteDir, repoRoot) {
  const manifest = JSON.parse(fs.readFileSync(path.join(siteDir, "docs-manifest.json"), "utf8"));
  const sections = [];
  for (const section of manifest.sections) {
    const entries = expandItems(section.items, repoRoot);
    const groups = [];
    for (const group of section.groups ?? []) {
      const groupEntries = expandItems(group.items, repoRoot);
      groups.push({ title: group.title, collapsed: group.collapsed, entries: groupEntries });
      entries.push(...groupEntries);
    }
    sections.push({ title: section.title, entries, groups });
  }
  return { repoUrl: manifest.repo, branch: manifest.branch, sections };
}

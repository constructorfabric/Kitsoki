// diff-mode-a.ts — pure, VS-Code-free helpers for Mode A ({paths, base})
// openDiff requests: reviewing already-applied working-tree edits against a
// git base ref ("working-tree-vs-base"), as opposed to Mode B's
// {path, new_text} not-yet-applied proposed content.
//
// Split out of ide-diff.ts so the git-show plumbing is unit-testable without
// a real VS Code host: there is no standalone `vscode` npm package to import
// outside the editor (only `@types/vscode` — type declarations, no runtime),
// so anything that imports `vscode` can only be exercised inside a real VS
// Code window (the Playwright e2e specs). Everything here is plain
// TypeScript + node:child_process, so `pnpm test` (node:test) can drive it
// directly.

import { spawn } from 'node:child_process';

/** Normalised Mode A request: which files to review, against which base ref. */
export interface ModeARequest {
  paths: string[];
  base: string;
  title: string;
}

/**
 * Recognise a Mode A openDiff call and normalise its args. Mode A is
 * {paths: string[], base: string, title?}; anything else (including a
 * Mode B {path, new_text} call, or a malformed/empty Mode A call) returns
 * undefined so the caller falls through to Mode B / rejects.
 */
export function normalizeModeAArgs(args: Record<string, unknown>): ModeARequest | undefined {
  const rawPaths = args.paths;
  if (!Array.isArray(rawPaths)) return undefined;
  const paths = rawPaths.filter((p): p is string => typeof p === 'string' && p.length > 0);
  const base = typeof args.base === 'string' ? args.base : '';
  if (!paths.length || !base) return undefined;
  const title =
    typeof args.title === 'string' && args.title
      ? args.title
      : `Kitsoki — review ${paths.length} file(s) vs ${base}`;
  return { paths, base, title };
}

/**
 * A seam for reading `<relPath>` as it existed at `<ref>` inside the git repo
 * rooted at `cwd`. Returns the file content, or `null` when the path did not
 * exist at that ref (added-since-base — the diff's left side is then empty).
 * Injected so unit tests never spawn a real `git` process.
 */
export type GitShow = (cwd: string, ref: string, relPath: string) => Promise<string | null>;

/** The real seam: `git show <ref>:<relPath>` in `cwd`. */
export const realGitShow: GitShow = (cwd, ref, relPath) =>
  new Promise((resolve) => {
    // git always wants forward slashes in the `<ref>:<path>` pathspec, even
    // on Windows.
    const gitPath = relPath.split('\\').join('/');
    const child = spawn('git', ['show', `${ref}:${gitPath}`], { cwd });
    let out = '';
    let errored = false;
    child.stdout.on('data', (d) => (out += d.toString('utf8')));
    child.on('error', () => {
      errored = true;
      resolve(null);
    });
    child.on('close', (code) => {
      if (errored) return;
      // A non-zero exit means the path doesn't exist at that ref (added
      // since base) — never a reason to fail the whole review.
      resolve(code === 0 ? out : null);
    });
  });

/**
 * Load one file's content at `base` for the diff's left side. `null` from
 * `gitShow` (path added since base) becomes an empty string — the diff then
 * reads as "everything on the right is new", which is the correct rendering
 * for a file that didn't exist at the base ref.
 */
export async function loadBaseContent(
  gitShow: GitShow,
  cwd: string,
  base: string,
  relPath: string,
): Promise<string> {
  const content = await gitShow(cwd, base, relPath);
  return content ?? '';
}

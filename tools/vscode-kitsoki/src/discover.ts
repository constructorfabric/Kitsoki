// discover.ts — auto-discover an onboarded kitsoki instance when the
// `kitsoki.storiesDir` setting is left empty.
//
// `kitsoki web` already resolves story directories itself (flags >
// .kitsoki.yaml > ./stories — see cmd/kitsoki/web.go), but that resolution is
// relative to the spawned process's CWD, which the extension pins to the
// FIRST workspace folder (backend.ts's `cwd`). That default only works when
// the workspace root IS the onboarded project root. Opening a subdirectory of
// a larger checkout (a monorepo package, a nested worktree, …) leaves the
// backend spawning with no story_dirs to walk and "no stories found".
//
// This module is a pure, dependency-injected directory walk: given a starting
// directory and an `exists` probe (real `fs.existsSync` in production, a fake
// in tests), it walks UP the ancestor chain looking for the markers a real
// onboarded checkout leaves behind (see .kitsoki.yaml itself and
// tools/onboard-smoke's fixtures):
//
//   1. `<dir>/.kitsoki/stories`  — the graduated per-project instances root
//      (`.kitsoki.yaml`'s default `story_dirs: [./.kitsoki/stories]`).
//   2. `<dir>/.kitsoki.yaml`     — the onboarding marker file. When present but
//      `.kitsoki/stories` doesn't exist yet, fall back to a sibling `stories/`
//      (a fresh checkout that hasn't materialized an instance yet), else the
//      marker's own directory (kitsoki's own dev-story root layout).
//   3. `<dir>/stories`           — a bare dev checkout with no `.kitsoki.yaml`
//      at all (e.g. the kitsoki repo itself, or a project that only ever
//      walks its own `./stories`).
//
// Returns the FIRST ancestor (closest to `startDir`) satisfying any marker, or
// undefined if none exists all the way up to the filesystem root.
export interface DiscoverFsProbe {
  /** True when `p` exists (file or directory) — mirrors fs.existsSync. */
  exists(p: string): boolean;
}

export interface DiscoverPath {
  dirname(p: string): string;
  join(...parts: string[]): string;
}

/**
 * Walk up from `startDir` and return the resolved stories directory for the
 * nearest onboarded ancestor, or undefined when none is found. Pure aside
 * from the injected `probe`/`pathOps` (defaults to real fs/path when omitted
 * by callers outside tests — see resolveAutoStoriesDir).
 */
export function discoverStoriesDir(
  startDir: string,
  probe: DiscoverFsProbe,
  pathOps: DiscoverPath,
): string | undefined {
  let dir = startDir;
  for (;;) {
    const kitsokiStories = pathOps.join(dir, '.kitsoki', 'stories');
    if (probe.exists(kitsokiStories)) return kitsokiStories;

    const marker = pathOps.join(dir, '.kitsoki.yaml');
    if (probe.exists(marker)) {
      const bareStories = pathOps.join(dir, 'stories');
      return probe.exists(bareStories) ? bareStories : dir;
    }

    const bareStories = pathOps.join(dir, 'stories');
    if (probe.exists(bareStories)) return bareStories;

    const parent = pathOps.dirname(dir);
    if (parent === dir) return undefined; // reached the filesystem root
    dir = parent;
  }
}

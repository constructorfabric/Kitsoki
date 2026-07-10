// backend-resolve.ts — the pure config-resolution logic backend.ts drives.
//
// Split out of backend.ts (which imports `vscode` and so can't be `require`d
// under plain `node --test`) so these decisions are unit-testable in
// isolation: which binary to spawn, what environment/PATH it gets, the
// actionable message for a spawn failure, and — new in WS-D D2 — which
// stories directory to pass when `kitsoki.storiesDir` is left unset.

import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { discoverStoriesDir } from './discover';

/**
 * Resolve the kitsoki binary to spawn. An explicit `binaryPath` setting always
 * wins. Otherwise, when the workspace is a kitsoki checkout, prefer its freshly
 * built `bin/kitsoki` — that's both the dev-correct (newest) binary AND an
 * absolute path, so it works regardless of the spawner's PATH. Only when neither
 * applies do we fall back to bare `kitsoki` (resolved against {@link binaryEnv}'s
 * augmented PATH).
 */
export function resolveBinary(binaryPath: string, cwd: string | undefined): string {
  if (binaryPath) return binaryPath;
  if (cwd) {
    const local = path.join(cwd, 'bin', 'kitsoki');
    try {
      if (fs.existsSync(local)) return local;
    } catch {
      /* fall through to PATH */
    }
  }
  return 'kitsoki';
}

/**
 * Build the child's environment with a PATH that GUI-launched editors lack. On
 * macOS a Dock/Finder-launched VS Code inherits only the minimal system PATH
 * (`/usr/bin:/bin:...`), so a `kitsoki` in `~/.local/bin` (or Homebrew) is
 * invisible and `spawn('kitsoki')` ENOENTs. Append the usual install dirs so a
 * PATH-installed binary still resolves. A no-op when they're already present.
 */
export function binaryEnv(base: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const extra = [
    path.join(os.homedir(), '.local', 'bin'),
    '/opt/homebrew/bin',
    '/usr/local/bin',
    '/usr/local/go/bin',
  ];
  const parts = (base.PATH ?? '').split(path.delimiter).filter(Boolean);
  for (const dir of extra) if (!parts.includes(dir)) parts.push(dir);
  return { ...base, PATH: parts.join(path.delimiter) };
}

/**
 * Build the actionable message for a spawn failure. Missing-binary (ENOENT) is
 * by far the common case — a fresh clone with no `bin/kitsoki` built yet and
 * no `kitsoki` on PATH — so it gets a specific, two-option hint: build the
 * binary (`make build-bin`, the fast single-artifact build; NOT `make build`,
 * which also stages the SPA/story embeds) or point `kitsoki.binaryPath` at an
 * existing one. Any other spawn error (permissions, exec format, …) is
 * reported as-is with no invented hint. Pure so the activation-time error
 * message is unit-testable without an actual missing binary.
 */
export function spawnErrorHint(bin: string, err: NodeJS.ErrnoException): string {
  if (err.code !== 'ENOENT') return '';
  return (
    ` — '${bin}' not found. Build it (\`make build-bin\`) or set the ` +
    `kitsoki.binaryPath setting to an absolute path to an existing kitsoki binary.`
  );
}

/**
 * Resolve the `--stories-dir` to spawn with. An explicit `kitsoki.storiesDir`
 * setting always wins. Otherwise, when `cwd` is available, auto-discover the
 * nearest onboarded ancestor (walking up for `.kitsoki/stories`, `.kitsoki.yaml`,
 * or a bare `stories/`) so opening a subdirectory of a larger checkout still
 * finds the project's stories — see discover.ts for the walk and its markers.
 * Returns "" (meaning: let `kitsoki web` apply its own flags > .kitsoki.yaml >
 * ./stories default) when neither resolves.
 */
export function resolveStoriesDir(storiesDir: string, cwd: string | undefined): string {
  if (storiesDir) return storiesDir;
  if (!cwd) return '';
  const discovered = discoverStoriesDir(
    cwd,
    { exists: (p) => fs.existsSync(p) },
    { dirname: path.dirname, join: path.join },
  );
  return discovered ?? '';
}

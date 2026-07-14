// Bundle the extension host entry (src/extension.ts) into a single CommonJS
// file that the VS Code extension host can require. `vscode` is provided by the
// host at runtime and must stay external.
import esbuild from 'esbuild';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const watch = process.argv.includes('--watch');

// Stage the runstatus SPA (a generated, gitignored artifact) into media/spa so
// the webview can load it. Prefer the freshly-built single-file bundle; if it
// is absent (clean checkout where the SPA wasn't built), drop a placeholder so
// the media root exists and webview.ts doesn't throw — `make build` / a SPA
// build then replaces it before packaging or recording.
function stageSpa() {
  const here = path.dirname(fileURLToPath(import.meta.url));
  // Candidate sources, in preference order:
  //  1. KITSOKI_RUNSTATUS_SPA — an explicit SPA path for temp-only packaging
  //     (`make vscode-install-local` stages source under .temp so protected
  //     main never needs generated files under tools/ or internal/).
  //  2. internal/runstatus/web/assets/index.html — the go:embed asset `make
  //     web` (part of `make build` / `make build-bin`) always leaves fresh;
  //     this is the canonical staged location every other consumer (the
  //     `kitsoki web` binary itself) reads from, so it can't silently drift.
  //  3. .temp/runstatus/dist/index.html — vite's raw build output
  //     (`pnpm -C tools/runstatus build` run standalone, without the go
  //     embed step). NOTE: this was `tools/runstatus/dist/` before the
  //     .temp/ build-output relocation (see Makefile's TEMP_DIR /
  //     RUNSTATUS_DIST); that path no longer exists, so a bare `pnpm build`
  //     here always fell through to the placeholder below until this fix.
  const candidates = [
    process.env.KITSOKI_RUNSTATUS_SPA ? path.resolve(process.env.KITSOKI_RUNSTATUS_SPA) : undefined,
    path.resolve(here, '../../internal/runstatus/web/assets/index.html'),
    path.resolve(here, '../../.temp/runstatus/dist/index.html'),
  ].filter(Boolean);
  const built = candidates.find((p) => fs.existsSync(p));
  const dest = path.join(here, 'media/spa/index.html');
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  if (built) {
    fs.copyFileSync(built, dest);
    console.log(`[stage-spa] copied ${path.relative(here, built)} -> media/spa/index.html (${fs.statSync(dest).size} bytes)`);
  } else if (!fs.existsSync(dest)) {
    fs.writeFileSync(
      dest,
      '<!doctype html><meta charset="utf-8"><title>Kitsoki</title><body style="font:14px system-ui;padding:2rem">Build the SPA first: <code>make build</code> (or <code>pnpm -C tools/runstatus build</code>).</body>',
    );
    console.log('[stage-spa] runstatus SPA not built; wrote a placeholder. Run `make build` to embed the real UI.');
  } else {
    console.log('[stage-spa] runstatus SPA not built; keeping existing media/spa/index.html.');
  }
}

stageSpa();

const options = {
  entryPoints: ['src/extension.ts'],
  outfile: 'dist/extension.js',
  bundle: true,
  format: 'cjs',
  platform: 'node',
  target: 'node22',
  // `vscode` is provided by the host. `ws` is bundled, but its OPTIONAL native
  // speedups (bufferutil / utf-8-validate) are require()'d in a try/catch — keep
  // them external so esbuild doesn't fail on the missing optional deps; ws falls
  // back to its pure-JS path at runtime.
  external: ['vscode', 'bufferutil', 'utf-8-validate'],
  sourcemap: true,
  logLevel: 'info',
};

if (watch) {
  const ctx = await esbuild.context(options);
  await ctx.watch();
  console.log('[esbuild] watching…');
} else {
  await esbuild.build(options);
}

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
  const built = path.resolve(here, '../runstatus/dist/index.html');
  const dest = path.join(here, 'media/spa/index.html');
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  if (fs.existsSync(built)) {
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

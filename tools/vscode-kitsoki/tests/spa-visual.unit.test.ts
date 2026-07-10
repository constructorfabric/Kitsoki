// spa-visual.unit.test.ts — proves the VS Code extension reuses the runstatus
// SPA for visual observation instead of growing a separate webview-only helper.
//
// Run: node --test --import tsx tests/spa-visual.unit.test.ts

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');

test('vscode webview stages the shared runstatus SPA visual helper', () => {
  const stageScript = fs.readFileSync(path.join(EXT_ROOT, 'esbuild.mjs'), 'utf8');
  // Two staging sources, in preference order: the go:embed asset `make web`
  // always leaves fresh, and vite's raw .temp/ build output as a fallback for
  // a bare `pnpm build` run standalone (see esbuild.mjs's stageSpa doc).
  assert.match(stageScript, /internal\/runstatus\/web\/assets\/index\.html/);
  assert.match(stageScript, /\.temp\/runstatus\/dist\/index\.html/);
  assert.match(stageScript, /media\/spa\/index\.html/);

  const main = fs.readFileSync(path.join(REPO_ROOT, 'tools/runstatus/src/main.ts'), 'utf8');
  assert.match(main, /installKitsokiVisualHelper\(\)/);

  const helper = fs.readFileSync(path.join(REPO_ROOT, 'tools/runstatus/src/lib/visualHelper.ts'), 'utf8');
  assert.match(helper, /__kitsokiVisual/);
  assert.match(helper, /recording\(\)/);
  assert.match(helper, /dirtyRegions\(\)/);
});

test('built SPA carries the visual helper when available', (t) => {
  // Prefer the go:embed asset (what `make web` / `make build` leaves fresh);
  // fall back to vite's raw .temp/ output for a standalone `pnpm build`.
  const candidates = [
    path.join(REPO_ROOT, 'internal/runstatus/web/assets/index.html'),
    path.join(REPO_ROOT, '.temp/runstatus/dist/index.html'),
  ];
  const built = candidates.find((p) => fs.existsSync(p));
  if (!built) {
    t.skip('runstatus SPA has not been built in this checkout');
    return;
  }
  const html = fs.readFileSync(built, 'utf8');
  assert.match(html, /__kitsokiVisual/);
  assert.match(html, /kitsoki-visual-record/);
});

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
  assert.match(stageScript, /\.\.\/runstatus\/dist\/index\.html/);
  assert.match(stageScript, /media\/spa\/index\.html/);

  const main = fs.readFileSync(path.join(REPO_ROOT, 'tools/runstatus/src/main.ts'), 'utf8');
  assert.match(main, /installKitsokiVisualHelper\(\)/);

  const helper = fs.readFileSync(path.join(REPO_ROOT, 'tools/runstatus/src/lib/visualHelper.ts'), 'utf8');
  assert.match(helper, /__kitsokiVisual/);
  assert.match(helper, /recording\(\)/);
  assert.match(helper, /dirtyRegions\(\)/);
});

test('built SPA carries the visual helper when available', (t) => {
  const built = path.join(REPO_ROOT, 'tools/runstatus/dist/index.html');
  if (!fs.existsSync(built)) {
    t.skip('runstatus SPA has not been built in this checkout');
    return;
  }
  const html = fs.readFileSync(built, 'utf8');
  assert.match(html, /__kitsokiVisual/);
  assert.match(html, /kitsoki-visual-record/);
});

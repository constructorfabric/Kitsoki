// backend-resolve.unit.test.ts — the pure resolution logic backend.ts drives
// (WS-D D2 deliverables 1 + 2: auto-discovery + the binary-missing story).
//
// backend.ts itself imports `vscode`, which only exists inside a real
// extension host, so it can't be `require`d under plain `node --test`. These
// decisions live in backend-resolve.ts specifically so they're testable here
// without one — see that file's header comment.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { resolveBinary, spawnErrorHint, resolveStoriesDir } from '../src/backend-resolve';

test('resolveBinary: explicit binaryPath setting always wins', () => {
  const bin = resolveBinary('/custom/kitsoki', '/some/cwd');
  assert.equal(bin, '/custom/kitsoki');
});

test('resolveBinary: prefers a freshly built bin/kitsoki under cwd', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-resolve-'));
  fs.mkdirSync(path.join(tmp, 'bin'), { recursive: true });
  fs.writeFileSync(path.join(tmp, 'bin', 'kitsoki'), '#!/bin/sh\n');
  const bin = resolveBinary('', tmp);
  assert.equal(bin, path.join(tmp, 'bin', 'kitsoki'));
});

test('resolveBinary: falls back to bare "kitsoki" on PATH when nothing local exists', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-resolve-'));
  const bin = resolveBinary('', tmp);
  assert.equal(bin, 'kitsoki');
});

test('resolveBinary: falls back to "kitsoki" with no cwd at all', () => {
  assert.equal(resolveBinary('', undefined), 'kitsoki');
});

test('spawnErrorHint: ENOENT gets the build-bin / binaryPath hint', () => {
  const err = Object.assign(new Error('spawn kitsoki ENOENT'), { code: 'ENOENT' }) as NodeJS.ErrnoException;
  const hint = spawnErrorHint('kitsoki', err);
  assert.match(hint, /make build-bin/);
  assert.match(hint, /kitsoki\.binaryPath/);
  assert.match(hint, /not found/);
});

test('spawnErrorHint: non-ENOENT errors get no invented hint', () => {
  const err = Object.assign(new Error('spawn kitsoki EACCES'), { code: 'EACCES' }) as NodeJS.ErrnoException;
  assert.equal(spawnErrorHint('kitsoki', err), '');
});

test('resolveStoriesDir: explicit setting always wins over discovery', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-resolve-'));
  assert.equal(resolveStoriesDir('/explicit/stories', tmp), '/explicit/stories');
});

test('resolveStoriesDir: auto-discovers .kitsoki/stories from a nested workspace root', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-resolve-'));
  fs.mkdirSync(path.join(tmp, '.kitsoki', 'stories'), { recursive: true });
  fs.writeFileSync(path.join(tmp, '.kitsoki.yaml'), 'story_dirs:\n  - ./.kitsoki/stories\n');
  const nested = path.join(tmp, 'packages', 'app');
  fs.mkdirSync(nested, { recursive: true });
  assert.equal(resolveStoriesDir('', nested), path.join(tmp, '.kitsoki', 'stories'));
});

test('resolveStoriesDir: returns "" (defer to kitsoki web defaults) when nothing is onboarded', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-resolve-'));
  assert.equal(resolveStoriesDir('', tmp), '');
});

test('resolveStoriesDir: returns "" with no cwd at all', () => {
  assert.equal(resolveStoriesDir('', undefined), '');
});

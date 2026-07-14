// discover.unit.test.ts — the auto-discovery walk (WS-D D2 deliverable 1).
//
// Pure function, fake fs/path injected — proves the ancestor walk finds the
// right marker (`.kitsoki/stories`, `.kitsoki.yaml`, bare `stories/`) without
// touching a real filesystem, and that it stops at the filesystem root when
// nothing is onboarded.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as path from 'node:path';
import { discoverStoriesDir } from '../src/discover';

const pathOps = { dirname: path.dirname, join: path.join };

function fakeProbe(existing: string[]) {
  const set = new Set(existing);
  return { exists: (p: string) => set.has(p) };
}

test('discover: prefers .kitsoki/stories when present', () => {
  const root = '/repo';
  const probe = fakeProbe([
    path.join(root, '.kitsoki', 'stories'),
    path.join(root, '.kitsoki.yaml'),
    path.join(root, 'stories'),
  ]);
  const found = discoverStoriesDir(path.join(root, 'packages', 'sub'), probe, pathOps);
  assert.equal(found, path.join(root, '.kitsoki', 'stories'));
});

test('discover: .kitsoki.yaml marker with no materialized instance falls back to sibling stories/', () => {
  const root = '/repo';
  const probe = fakeProbe([path.join(root, '.kitsoki.yaml'), path.join(root, 'stories')]);
  const found = discoverStoriesDir(root, probe, pathOps);
  assert.equal(found, path.join(root, 'stories'));
});

test('discover: .kitsoki.yaml marker with neither .kitsoki/stories nor stories/ returns the marker dir', () => {
  const root = '/repo';
  const probe = fakeProbe([path.join(root, '.kitsoki.yaml')]);
  const found = discoverStoriesDir(root, probe, pathOps);
  assert.equal(found, root);
});

test('discover: bare stories/ with no .kitsoki.yaml (a dev checkout) resolves directly', () => {
  const root = '/repo';
  const probe = fakeProbe([path.join(root, 'stories')]);
  const found = discoverStoriesDir(root, probe, pathOps);
  assert.equal(found, path.join(root, 'stories'));
});

test('discover: walks up multiple levels from a nested workspace root', () => {
  const root = '/home/dev/checkout';
  const probe = fakeProbe([path.join(root, '.kitsoki.yaml'), path.join(root, '.kitsoki', 'stories')]);
  const found = discoverStoriesDir(path.join(root, 'a', 'b', 'c'), probe, pathOps);
  assert.equal(found, path.join(root, '.kitsoki', 'stories'));
});

test('discover: returns undefined when nothing is onboarded up to the root', () => {
  const probe = fakeProbe([]);
  const found = discoverStoriesDir('/some/random/dir', probe, pathOps);
  assert.equal(found, undefined);
});

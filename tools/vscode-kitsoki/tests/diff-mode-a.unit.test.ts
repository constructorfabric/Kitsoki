// diff-mode-a.unit.test.ts — the Mode A ({paths, base}) openDiff logic,
// pure and VS-Code-free (see diff-mode-a.ts's header for why this can't
// import `vscode`). Covers arg normalisation and the git-show seam with a
// fake GitShow, plus a smoke test of the real seam against a throwaway git
// repo (added / modified / deleted-since-base files).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { execFileSync } from 'node:child_process';
import { normalizeModeAArgs, loadBaseContent, realGitShow, type GitShow } from '../src/diff-mode-a';

test('normalizeModeAArgs: recognises {paths, base} and fills a default title', () => {
  const req = normalizeModeAArgs({ paths: ['a.go', 'b.go'], base: 'main' });
  assert.ok(req);
  assert.deepEqual(req!.paths, ['a.go', 'b.go']);
  assert.equal(req!.base, 'main');
  assert.match(req!.title, /2 file\(s\) vs main/);
});

test('normalizeModeAArgs: honours an explicit title', () => {
  const req = normalizeModeAArgs({ paths: ['a.go'], base: 'main', title: 'ticket-123' });
  assert.equal(req!.title, 'ticket-123');
});

test('normalizeModeAArgs: filters non-string path entries', () => {
  const req = normalizeModeAArgs({ paths: ['a.go', 42, null, 'b.go'], base: 'main' });
  assert.deepEqual(req!.paths, ['a.go', 'b.go']);
});

test('normalizeModeAArgs: rejects Mode B shape ({path, new_text})', () => {
  assert.equal(normalizeModeAArgs({ path: 'a.go', new_text: 'x' }), undefined);
});

test('normalizeModeAArgs: rejects empty paths or missing base', () => {
  assert.equal(normalizeModeAArgs({ paths: [], base: 'main' }), undefined);
  assert.equal(normalizeModeAArgs({ paths: ['a.go'] }), undefined);
  assert.equal(normalizeModeAArgs({ paths: ['a.go'], base: '' }), undefined);
});

test('loadBaseContent: passes through real content from the injected GitShow', async () => {
  const calls: Array<{ cwd: string; ref: string; relPath: string }> = [];
  const fake: GitShow = async (cwd, ref, relPath) => {
    calls.push({ cwd, ref, relPath });
    return 'old contents\n';
  };
  const content = await loadBaseContent(fake, '/repo', 'main', 'src/a.go');
  assert.equal(content, 'old contents\n');
  assert.deepEqual(calls, [{ cwd: '/repo', ref: 'main', relPath: 'src/a.go' }]);
});

test('loadBaseContent: a null GitShow result (added since base) becomes empty string', async () => {
  const fake: GitShow = async () => null;
  const content = await loadBaseContent(fake, '/repo', 'main', 'new-file.go');
  assert.equal(content, '');
});

// ── realGitShow smoke test against a throwaway git repo ────────────────────

function sh(cwd: string, ...argv: string[]): void {
  execFileSync(argv[0], argv.slice(1), { cwd, stdio: 'ignore' });
}

test('realGitShow: reads a file at a base ref, modified/deleted/added since', async (t) => {
  if (!hasGit()) {
    t.skip('git not on PATH');
    return;
  }
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-diffmodea-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));

  sh(dir, 'git', 'init', '-q');
  sh(dir, 'git', 'config', 'user.email', 'test@example.com');
  sh(dir, 'git', 'config', 'user.name', 'Test');

  fs.writeFileSync(path.join(dir, 'modified.txt'), 'base version\n');
  fs.writeFileSync(path.join(dir, 'deleted.txt'), 'will be deleted\n');
  sh(dir, 'git', 'add', '.');
  sh(dir, 'git', 'commit', '-q', '-m', 'base');
  const base = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: dir }).toString().trim();

  // Working-tree edits since base: modify one file, delete another, add a
  // third — mirroring the three cases Mode A must render sanely.
  fs.writeFileSync(path.join(dir, 'modified.txt'), 'working-tree version\n');
  fs.rmSync(path.join(dir, 'deleted.txt'));
  fs.writeFileSync(path.join(dir, 'added.txt'), 'brand new\n');

  const modified = await realGitShow(dir, base, 'modified.txt');
  assert.equal(modified, 'base version\n');

  const deleted = await realGitShow(dir, base, 'deleted.txt');
  assert.equal(deleted, 'will be deleted\n', 'deleted.txt still existed AT the base ref');

  const added = await realGitShow(dir, base, 'added.txt');
  assert.equal(added, null, 'added.txt did not exist at the base ref');

  // And loadBaseContent turns that null into the empty-left-side rendering.
  assert.equal(await loadBaseContent(realGitShow, dir, base, 'added.txt'), '');
});

function hasGit(): boolean {
  try {
    execFileSync('git', ['--version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

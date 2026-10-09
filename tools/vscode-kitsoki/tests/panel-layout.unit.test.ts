import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
const extensionRoot = process.cwd();
const manifest = JSON.parse(
  fs.readFileSync(path.join(extensionRoot, 'package.json'), 'utf8'),
) as {
  contributes: {
    viewsContainers: Record<string, Array<{ id: string }>>;
    views: Record<string, Array<{ id: string }>>;
    commands: Array<{ command: string }>;
  };
};

test('chat is a bottom-panel view, not an editor tab', () => {
  assert.deepEqual(
    manifest.contributes.viewsContainers.panel?.map((container) => container.id),
    ['kitsoki'],
  );
  assert.deepEqual(
    manifest.contributes.views.kitsoki?.map((view) => view.id),
    ['kitsoki.chat'],
  );
  assert.ok(
    manifest.contributes.viewsContainers.activitybar?.some(
      (container) => container.id === 'kitsoki-surfaces',
    ),
  );
  assert.deepEqual(
    manifest.contributes.views['kitsoki-surfaces']?.map((view) => view.id),
    ['kitsoki.trace', 'kitsoki.graph'],
  );
  assert.equal(
    manifest.contributes.commands.some((command) => command.command === 'kitsoki.popOutChat'),
    false,
  );

  const extensionSource = fs.readFileSync(path.join(extensionRoot, 'src', 'extension.ts'), 'utf8');
  assert.equal(extensionSource.includes('createWebviewPanel'), false);
  assert.equal(extensionSource.includes('registerWebviewPanelSerializer'), false);
});

import { defineConfig } from '@playwright/test';

// The VS Code _electron driving spec is launched by tests under tests/*.spec.ts.
// No webServer here — the spec spawns the kitsoki backend itself (no LLM).
export default defineConfig({
  testDir: './tests',
  testMatch: /.*\.spec\.ts$/,
  timeout: 180_000,
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
});

import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import config from "../../vite.config.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const runstatusRoot = path.resolve(__dirname, "../..");

describe("vite config", () => {
  it("roots the SPA at tools/runstatus regardless of caller cwd", () => {
    const resolved = typeof config === "function" ? config({ command: "build", mode: "test" }) : config;

    expect(resolved.root).toBe(runstatusRoot);
    expect(resolved.server?.fs?.allow).toContain(runstatusRoot);
  });
});

// lint-no-llm.mjs — the mcp-demo analogue of runstatus `demos:lint`, adapted to
// the terminal surface. The web lint proves no-LLM by requiring startWebServer /
// --flow; this surface proves it structurally: the spec REPLAYS a committed
// termcast and must never spawn a model (claude) or the live MCP server at render
// time. Three invariants, mirroring the web gates (camera / chapters / no-LLM):
//
//   camera   — records through cameraContext() (shared 1600×900 stitch canvas)
//   chapters — emits the chapters.json sidecar via writeChapters()
//   no-LLM   — replays a cast (resolveCast) and imports NO child_process / spawn,
//              so it cannot launch `claude` or `kitsoki mcp` during a recording.
//
// Exit 0 = clean, 1 = a spec violates an invariant.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const TESTS = path.join(ROOT, "tests");

const CHECKS = [
  { id: "camera", ok: (s) => /cameraContext\s*\(/.test(s), hint: "record through cameraContext({ recordVideoDir }) so the MP4 shares the 1600×900 stitch canvas" },
  { id: "chapters", ok: (s) => /writeChapters\s*\(/.test(s), hint: "emit the chapters.json sidecar via writeChapters(mp4, chapters.list())" },
  { id: "replays-cast", ok: (s) => /resolveCast\s*\(/.test(s), hint: "drive the terminal from resolveCast() (a committed termcast), not a live process" },
  { id: "no-spawn", ok: (s) => !/child_process|\bspawn\b|\bexecSync\b|\bexec\b\(/.test(s), hint: "a demo spec must not spawn a process at replay (no `claude`, no `kitsoki mcp`) — that would put an LLM in the render path" },
];

const specs = fs.existsSync(TESTS)
  ? fs.readdirSync(TESTS).filter((f) => f.endsWith(".spec.ts")).map((f) => path.join(TESTS, f))
  : [];

let failed = 0;
for (const spec of specs) {
  const src = fs.readFileSync(spec, "utf8");
  for (const c of CHECKS) {
    if (!c.ok(src)) {
      failed++;
      console.error(`✗ ${path.relative(ROOT, spec)} [${c.id}] — ${c.hint}`);
    }
  }
}

if (!specs.length) { console.error("lint-no-llm: no .spec.ts found under tests/"); process.exit(1); }
if (failed) { console.error(`\nlint-no-llm: ${failed} violation(s).`); process.exit(1); }
console.log(`lint-no-llm: ${specs.length} spec(s) clean (camera · chapters · replays-cast · no-spawn).`);

#!/usr/bin/env node
/**
 * Bundle a raw rrweb event log into a self-contained HTML viewer.
 *
 * This is the product-site fallback when the Slidey CLI is unavailable in CI.
 * It intentionally stays small: inline the pinned rrweb UMD bundle and CSS,
 * replay the captured event array, and expose basic play/pause/restart controls.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const here = path.dirname(fileURLToPath(import.meta.url));
const runstatusRoot = path.resolve(here, "..");

const input = process.argv[2];
const output = process.argv[3] ?? input?.replace(/\.rrweb\.json$/i, ".html");
if (!input || !output) {
  console.error("usage: rrweb-viewer-bundle.mjs <in.rrweb.json> <out.html>");
  process.exit(2);
}

const rrwebBundle = path.join(runstatusRoot, "node_modules", "rrweb", "dist", "rrweb.umd.min.cjs");
const rrwebStyle = path.join(runstatusRoot, "node_modules", "rrweb", "dist", "style.css");
for (const file of [input, rrwebBundle, rrwebStyle]) {
  if (!fs.existsSync(file)) {
    console.error(`rrweb-viewer-bundle: missing ${file}`);
    process.exit(1);
  }
}

const rawEvents = fs.readFileSync(input, "utf8").trim();
const sidecar = input.replace(/\.json$/i, "") + ".capture.json";
const viewport = fs.existsSync(sidecar)
  ? JSON.parse(fs.readFileSync(sidecar, "utf8"))
  : { width: 1600, height: 900, deviceScaleFactor: 1 };

const esc = (s) => s.replace(/<\/script/gi, "<\\/script");
const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>rrweb replay</title>
<style>${fs.readFileSync(rrwebStyle, "utf8")}</style>
<style>
:root { color-scheme: dark; --bg: #08111f; --panel: #101827; --text: #edf5ff; --muted: #9fb1c9; --line: #263449; }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; background: var(--bg); color: var(--text); font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
.shell { min-height: 100vh; display: grid; grid-template-rows: auto 1fr; }
.bar { min-height: 54px; display: flex; align-items: center; gap: 10px; padding: 10px 14px; background: var(--panel); border-bottom: 1px solid var(--line); }
.bar strong { font-size: 13px; letter-spacing: .02em; text-transform: uppercase; color: var(--muted); margin-right: auto; }
button { appearance: none; border: 1px solid var(--line); background: #172337; color: var(--text); padding: 7px 11px; border-radius: 6px; font: inherit; cursor: pointer; }
button:hover { background: #20304a; }
.stage { display: grid; place-items: center; overflow: auto; padding: 16px; }
#replay { width: min(100%, ${Number(viewport.width) || 1600}px); aspect-ratio: ${(Number(viewport.width) || 1600)} / ${(Number(viewport.height) || 900)}; background: #fff; overflow: hidden; }
#replay .replayer-wrapper { transform-origin: top left; }
</style>
</head>
<body>
<div class="shell">
  <div class="bar">
    <strong>rrweb replay</strong>
    <button id="play" type="button">Play</button>
    <button id="pause" type="button">Pause</button>
    <button id="restart" type="button">Restart</button>
  </div>
  <main class="stage"><div id="replay"></div></main>
</div>
<script>${esc(fs.readFileSync(rrwebBundle, "utf8"))}</script>
<script>
const events = ${esc(rawEvents)};
const root = document.getElementById("replay");
const replayer = new rrweb.Replayer(events, { root, mouseTail: false });
document.getElementById("play").addEventListener("click", () => replayer.play());
document.getElementById("pause").addEventListener("click", () => replayer.pause());
document.getElementById("restart").addEventListener("click", () => { replayer.pause(0); replayer.play(); });
replayer.play();
</script>
</body>
</html>
`;

fs.mkdirSync(path.dirname(output), { recursive: true });
fs.writeFileSync(output, html);
console.log(`rrweb-viewer-bundle: wrote ${output}`);

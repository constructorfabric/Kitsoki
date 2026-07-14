// Minimal static file server for the live tui-bridge player page. No deps.
// Root is the harness dir (tools/tui-bridge) so both the player page
// (/player/index.html) and the xterm dist (/node_modules/@xterm/...) are
// servable from one origin.
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const PORT = Number(process.env.TUI_BRIDGE_PLAYER_PORT ?? "4320");

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
};

const server = http.createServer((req, res) => {
  try {
    const url = new URL(req.url, `http://localhost:${PORT}`);
    let rel = decodeURIComponent(url.pathname);
    if (rel === "/" || rel === "") rel = "/player/index.html";
    let abs = path.normalize(path.join(ROOT, rel));
    if (!abs.startsWith(ROOT)) { res.writeHead(403).end("forbidden"); return; }
    if (fs.existsSync(abs) && fs.statSync(abs).isDirectory()) abs = path.join(abs, "index.html");
    if (!fs.existsSync(abs) || fs.statSync(abs).isDirectory()) { res.writeHead(404).end("not found"); return; }
    res.writeHead(200, { "content-type": TYPES[path.extname(abs)] ?? "application/octet-stream" });
    fs.createReadStream(abs).pipe(res);
  } catch (e) {
    res.writeHead(500).end(String(e));
  }
});

server.listen(PORT, () => console.log(`[tui-bridge] player at http://localhost:${PORT}/player/`));

// segment-cast.mjs — turn a captured asciicast-v2 `.cast` (from capture-live.py)
// into a DRAFT termcast JSON, splitting beats at Claude Code's tool-call markers
// (the `⏺` bullet). The output matches casts/types.ts and is meant to be CURATED:
// tighten captions, mark the operator prompt chunk as `kind:"type"`, drop noise,
// and tune holdMs. Then commit it and point the spec at it via MCP_DEMO_CAST_JSON.
//
// Usage: node scripts/segment-cast.mjs <capture.cast> [--agent claude-code] > casts/<name>.json
import fs from "node:fs";

const file = process.argv[2];
if (!file) { console.error("usage: segment-cast.mjs <capture.cast> [--agent NAME]"); process.exit(2); }
const agent = (() => { const i = process.argv.indexOf("--agent"); return i > 0 ? process.argv[i + 1] : "claude-code"; })();

const lines = fs.readFileSync(file, "utf8").split("\n").filter(Boolean);
const header = JSON.parse(lines[0]);
// Concatenate all output events into one terminal stream.
let stream = "";
for (const l of lines.slice(1)) {
  try { const [, kind, data] = JSON.parse(l); if (kind === "o") stream += data; } catch { /* skip */ }
}

// Split on the tool-call bullet, keeping the bullet with each following segment.
const BULLET = "⏺";
const parts = stream.split(BULLET);
const beats = [];
// Segment 0 is everything before the first tool call (banner + prompt + preamble).
if (parts[0]?.trim()) {
  beats.push(beat("attach", "Attach to kitsoki MCP",
    "Claude Code attaches to the kitsoki studio MCP server", parts[0]));
}
for (const seg of parts.slice(1)) {
  const body = BULLET + seg;
  // Derive a tool name from `⏺ kitsoki - <tool> (MCP)` if present.
  const m = body.match(/kitsoki[^\n]*?([a-z]+_[a-z_]+)\s*\(MCP\)/i);
  const tool = m ? m[1] : `step${beats.length}`;
  beats.push(beat(tool, `${tool}`, `${tool} over MCP`, body));
}

const cast = { agent, title: `${agent} — kitsoki mcp`, cols: header.width || 116, rows: header.height || 32, beats };
process.stdout.write(JSON.stringify(cast, null, 2) + "\n");
console.error(`[segment-cast] ${beats.length} draft beat(s) — CURATE before committing (captions, the prompt's kind:"type", holdMs).`);

function beat(id, label, caption, data) {
  return { id, label, caption, chunks: [{ kind: "out", data }], holdMs: 4500 };
}

// streamjson-to-termcast.mjs — convert a captured `claude -p --output-format
// stream-json` transcript (the gated live MCP capture) into a `termcast` cassette,
// rendering each REAL kitsoki tool call + result as a Claude-Code-style card. This
// is the reliable capture path (vs the pty asciicast one): stream-json gives exact
// tool names, inputs, and kitsoki results, so the replay is faithful to what the
// agent actually did. Output is a DRAFT — curate captions/holdMs/trim before
// committing (see README → Record once).
//
// Usage: node scripts/streamjson-to-termcast.mjs <stream.jsonl> [--agent claude-code] > casts/claude-code-live.json
import fs from "node:fs";

const file = process.argv[2];
if (!file) { console.error("usage: streamjson-to-termcast.mjs <stream.jsonl> [--agent NAME]"); process.exit(2); }
const agent = (() => { const i = process.argv.indexOf("--agent"); return i > 0 ? process.argv[i + 1] : "claude-code"; })();

const E = "\x1b[";
const g = (s) => `${E}90m${s}${E}0m`, grn = (s) => `${E}32m${s}${E}0m`, cyn = (s) => `${E}36m${s}${E}0m`;
const bold = (s) => `${E}1m${s}${E}0m`, mag = (s) => `${E}35m${s}${E}0m`;

// short kitsoki tool id (story_read) → [beat-id, caption, sub]
const CAP = {
  story_read: ["read", "Reading the story over MCP — story.read", "the agent inspects the story source"],
  story_validate: ["validate", "Checking it — story.validate", "load-time invariants, deterministically"],
  story_graph: ["graph", "The room graph — story.graph", "rooms, transitions, intents"],
  story_test: ["test", "Testing the flows — story.test", "no LLM — a flow/cassette agent"],
  session_new: ["new", "Opening a session — session.new", "harness: replay (no LLM)"],
  session_submit: ["drive", "Driving the session — session.submit", "a direct intent advances the machine"],
  session_drive: ["drive", "Driving the session — session.drive", "free text routes through the one seam"],
  render_tui: ["see", "Seeing the result — render.tui", "the live kitsoki screen, as an operator sees it"],
  session_inspect: ["inspect", "Introspecting — session.inspect", "state · world · allowed intents"],
  story_write: ["edit", "Editing the story — story.write", "auto-validated in the same round-trip"],
};

// Walk the stream into an ordered list of {narration, tool, input, result}.
const lines = fs.readFileSync(file, "utf8").split("\n").filter(Boolean);
const calls = [];
const byId = new Map();
let pending = "";
for (const l of lines) {
  let e; try { e = JSON.parse(l); } catch { continue; }
  if (e.type === "assistant") {
    for (const c of e.message?.content ?? []) {
      if (c.type === "text" && c.text?.trim()) pending = c.text.trim();
      if (c.type === "tool_use") {
        const short = String(c.name).replace(/^mcp__kitsoki__/, "");
        if (!CAP[short]) { pending = ""; continue; } // skip ToolSearch / non-kitsoki
        const call = { narration: pending, short, input: c.input ?? {}, result: "" };
        pending = "";
        calls.push(call); byId.set(c.id, call);
      }
    }
  } else if (e.type === "user") {
    for (const c of e.message?.content ?? []) {
      if (c.type === "tool_result") {
        const call = byId.get(c.tool_use_id);
        if (!call) continue;
        const txt = Array.isArray(c.content)
          ? c.content.map((x) => x.text ?? "").join("\n")
          : (typeof c.content === "string" ? c.content : "");
        call.result = txt;
      }
    }
  }
}

// Pretty-print a (usually JSON) tool result into a few terminal lines.
function renderResult(raw) {
  let text = raw;
  try { text = JSON.stringify(JSON.parse(raw), null, 2); } catch { /* leave as-is */ }
  const lines = text.split("\n").map((s) => (s.length > 84 ? s.slice(0, 83) + "…" : s));
  const shown = lines.slice(0, 9);
  if (lines.length > 9) shown.push(`… (+${lines.length - 9} more)`);
  return shown.map((l, i) => `  ${g(i === 0 ? "⎿" : " ")}  ${g(l)}`).join("\n");
}

// render.tui returns the actual composed screen; show THAT (the money shot), not
// its JSON envelope. Prefer the ANSI twin so the real colours land in the player.
function renderFrame(raw) {
  try {
    const j = JSON.parse(raw);
    const frame = j.frame?.ansi || j.frame?.text || "";
    if (frame) return frame.split("\n").map((l) => `  ${l}`).join("\n");
  } catch { /* fall through */ }
  return renderResult(raw);
}

// story.read returns the file {path, content}; show the actual story SOURCE the
// agent read (more authentic than a truncated JSON blob, and it fills the frame).
function renderRead(raw) {
  try {
    const j = JSON.parse(raw);
    if (j.content) {
      const lines = j.content.split("\n");
      const shown = lines.slice(0, 16).map((l) => `  ${g(l.length > 90 ? l.slice(0, 89) + "…" : l)}`);
      if (lines.length > 16) shown.push(`  ${g("… (story continues)")}`);
      return `  ${g("⎿  " + (j.path || "") + ":")}\n` + shown.join("\n");
    }
  } catch { /* fall through */ }
  return renderResult(raw);
}

// Collapse a run of consecutive story_read calls (inspection is redundant to show
// twice) — keep the last of each run; everything else (incl. the two submits) stays.
const kept = calls.filter((c, i) => !(c.short === "story_read" && calls[i + 1]?.short === "story_read"));

const beats = kept.map((c, i) => {
  const [id, caption, sub] = CAP[c.short];
  const head = `${grn("⏺")} ${bold("kitsoki")} ${g("-")} ${cyn(c.short)} ${g("(MCP)")}`;
  const body = c.short === "render_tui"
    ? `${head} ${g("→ live screen")}\n${renderFrame(c.result)}\n`
    : c.short === "story_read"
    ? `${head}\n${renderRead(c.result)}\n`
    : `${head}\n${renderResult(c.result)}\n`;
  const narr = c.narration ? `${mag("●")} ${c.narration.split("\n")[0]}\n\n` : "";
  return { id: `${id}-${i}`, label: caption.split("—")[1]?.trim() || id, caption, sub, chunks: [{ kind: "out", data: narr + body + "\n" }], holdMs: 5000 };
});

const cast = { agent, title: `${agent} — kitsoki mcp`, cols: 116, rows: 26, beats };
process.stdout.write(JSON.stringify(cast, null, 2) + "\n");
console.error(`[streamjson-to-termcast] ${beats.length} beat(s) from ${calls.length} kitsoki tool call(s) — CURATE captions/holdMs/result-trims before committing.`);

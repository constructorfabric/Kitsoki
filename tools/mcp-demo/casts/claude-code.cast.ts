/**
 * Synthetic Claude-Code ↔ kitsoki-mcp session — the deterministic (no-LLM) proof
 * cassette. Every byte is hand-authored; it exercises the full studio surface in
 * the order a real agent would: author → check (validate + graph) → test → drive
 * (new / drive / submit) → see (render.tui) → introspect. The gated live capture
 * (P4) produces a cassette of this exact shape from a real `claude` run; the
 * replay path is identical and never calls a model.
 *
 * Grounded in real kitsoki vocabulary: a tiny `barista` story (rooms lobby →
 * order → confirm, a free-text `order_coffee` intent routed at the one
 * interpretive seam, a `drink` world key), driven under harness:replay.
 */
import { type Termcast, ansi as a } from "./types.js";

/** A Claude-Code MCP tool-call card: green bullet, tool name, indented result. */
function tool(name: string, resultLines: string[]): string {
  const head = `${a.green("⏺")} ${a.bold("kitsoki")} ${a.gray("-")} ${a.cyan(name)} ${a.gray("(MCP)")}\n`;
  const body = resultLines
    .map((l, i) => `  ${a.gray(i === 0 ? "⎿" : " ")}  ${l}\n`)
    .join("");
  return head + body + "\n";
}

/** A line of Claude's own narration (the assistant voice, not a tool result). */
function say(line: string): string {
  return `${a.magenta("●")} ${line}\n\n`;
}

export const cast: Termcast = {
  agent: "claude-code",
  title: "claude — kitsoki mcp",
  curtainTitle: "Claude Code  ·  kitsoki mcp",
  footer: "kitsoki · mcp studio · driven by Claude Code over MCP",
  badge: "MCP",
  assertText: ["kitsoki", "confirm"],
  cols: 116,
  rows: 26,
  beats: [
    {
      id: "attach",
      label: "Attach to kitsoki MCP",
      caption: "Claude Code attaches to the kitsoki studio MCP server",
      sub: "one stdio facade: author · drive · see",
      holdMs: 5200,
      chunks: [
        { kind: "out", data: `${a.gray("$")} claude ${a.gray("# kitsoki registered in .mcp.json (stdio: kitsoki mcp)")}\n` },
        {
          // A Claude-Code-style welcome banner: fills the opening frame with real
          // content (no empty terminal) and states the MCP connection up front.
          kind: "out",
          data:
            `${a.yellow("╭───────────────────────────────────────────────────────────╮")}\n` +
            `${a.yellow("│")}  ${a.yellow("✻")} ${a.bold("Welcome to Claude Code")}                                   ${a.yellow("│")}\n` +
            `${a.yellow("│")}                                                           ${a.yellow("│")}\n` +
            `${a.yellow("│")}    ${a.gray("/help for help · /status for setup")}                     ${a.yellow("│")}\n` +
            `${a.yellow("│")}    ${a.gray("cwd:")} ~/code/barista-demo                              ${a.yellow("│")}\n` +
            `${a.yellow("│")}    ${a.gray("mcp:")} ${a.cyan("kitsoki")} ${a.green("✔ connected")} ${a.gray("(18 tools: story/session/render)")}  ${a.yellow("│")}\n` +
            `${a.yellow("╰───────────────────────────────────────────────────────────╯")}\n\n`,
        },
        {
          kind: "type",
          data: "Build a tiny \"barista\" story over the kitsoki MCP server: author it, validate the room graph, run its flows, then drive a session and show me the TUI.",
        },
        { kind: "out", data: "\n\n" },
        { kind: "out", data: say("On it. I'll use the kitsoki studio tools end to end — authoring, checking, testing, then driving a live session and rendering the screen.") },
      ],
    },
    {
      id: "author",
      label: "Author — story.write",
      caption: "Authoring a story over MCP — story.write",
      sub: "the agent edits kitsoki story YAML",
      holdMs: 4600,
      chunks: [
        {
          kind: "out",
          data: tool("story_write", [
            `wrote ${a.white("stories/barista/app.yaml")}`,
            a.gray("rooms: lobby → order → confirm"),
            a.gray("intents: order_coffee (free-text), confirm, done"),
            a.gray("world: { drink: null }"),
          ]),
        },
        { kind: "out", data: say("Story scaffolded — a lobby that routes a free-text order, an order room that fills the drink, and a confirm room.") },
      ],
    },
    {
      id: "check",
      label: "Check — validate + graph",
      caption: "Checking it — story.validate + story.graph",
      sub: "load errors and the room graph, deterministically",
      holdMs: 5200,
      chunks: [
        {
          kind: "out",
          data: tool("story_validate", [`${a.green("✓ valid")} ${a.gray("— 0 errors, 0 warnings")}`]),
        },
        {
          kind: "out",
          data: tool("story_graph", [
            `${a.cyan("lobby")}    ${a.gray("→")} order        ${a.gray("[order_coffee]")}`,
            `${a.cyan("order")}    ${a.gray("→")} confirm      ${a.gray("[confirm]")}`,
            `${a.cyan("confirm")}  ${a.gray("→")} lobby        ${a.gray("[order_another, done]")}`,
          ]),
        },
        { kind: "out", data: say("Graph is clean and reachable — every room has an entry and the order path closes back to the lobby.") },
      ],
    },
    {
      id: "test",
      label: "Test — story.test",
      caption: "Testing the flows — story.test (deterministic, no LLM)",
      sub: "mock agent via cassette — zero model cost",
      holdMs: 4600,
      chunks: [
        {
          kind: "out",
          data: tool("story_test", [
            `${a.green("✓")} flows/order-flat-white.yaml ${a.gray("— 4 turns")}`,
            `${a.green("✓")} flows/order-then-cancel.yaml ${a.gray("— 3 turns")}`,
            `${a.green("2 passed")} ${a.gray("· 0 failed · agent: cassette (no LLM)")}`,
          ]),
        },
        { kind: "out", data: say("Both recorded flows pass against the cassette agent — no API calls, fully reproducible.") },
      ],
    },
    {
      id: "drive",
      label: "Drive — new / drive / submit",
      caption: "Driving a live session — session.new / drive / submit",
      sub: "free text routes through the one interpretive seam",
      holdMs: 5400,
      chunks: [
        {
          kind: "out",
          data: tool("session_new", [
            `handle ${a.yellow("s-7f3a")} ${a.gray("· harness: replay (no LLM)")}`,
            `state ${a.cyan("lobby")} ${a.gray("· allowed: [order_coffee]")}`,
          ]),
        },
        {
          kind: "out",
          data: tool("session_drive", [
            `input ${a.white('"I\'d like a flat white"')}`,
            `routed ${a.green("order_coffee")} ${a.gray("(confidence 0.94)")} → state ${a.cyan("order")}`,
            `slots: { drink: ${a.yellow("flat_white")} }`,
          ]),
        },
        {
          kind: "out",
          data: tool("session_submit", [
            `intent ${a.green("confirm")} → state ${a.cyan("confirm")}`,
            `world: { drink: ${a.yellow("flat_white")} }`,
          ]),
        },
        { kind: "out", data: say("Routed the free-text order, filled the drink slot, and submitted the confirm — the machine is now in `confirm`.") },
      ],
    },
    {
      id: "see",
      label: "See — render.tui",
      caption: "Seeing the result — render.tui returns the live screen",
      sub: "the agent sees exactly what an operator would",
      holdMs: 6000,
      chunks: [
        { kind: "out", data: tool("render_tui", [`${a.gray("frame 100×30 · state confirm")}`]) },
        {
          // A left-rule panel — clean regardless of the ANSI spans / wide glyphs
          // inside it. (The gated live render.tui frame in P4 carries kitsoki's
          // own fully-bordered composition; this synthetic stand-in stays tidy.)
          kind: "out",
          data:
            `${a.gray("┌─")} ${a.bold("kitsoki")} ${a.gray("──────────────────────────")}\n` +
            `${a.gray("│")}  ${a.cyan("barista")} ${a.gray("·")} ${a.bold("confirm")}\n` +
            `${a.gray("│")}\n` +
            `${a.gray("│")}  Your ${a.yellow("flat white")} is on the way. ${a.yellow("☕")}\n` +
            `${a.gray("│")}  Anything else?\n` +
            `${a.gray("│")}\n` +
            `${a.gray("│")}  ${a.green("›")} 1. order_another    2. done\n` +
            `${a.gray("└──────────────────────────────────────")}\n` +
            `  ${a.gray("state:")} confirm   ${a.gray("world:")} { drink: flat_white }\n\n`,
        },
        { kind: "out", data: say("There's the live TUI for the `confirm` room — the same screen a human operator would see, rendered straight from the session.") },
      ],
    },
    {
      id: "inspect",
      label: "Introspect + done",
      caption: "Introspecting the session — session.inspect",
      sub: "state · world · allowed intents · last turns",
      holdMs: 5000,
      chunks: [
        {
          kind: "out",
          data: tool("session_inspect", [
            `state ${a.cyan("confirm")} ${a.gray("· allowed: [order_another, done]")}`,
            `world { drink: ${a.yellow("flat_white")} }`,
            `last_turns: order_coffee → confirm ${a.gray("(2 turns, 0 agent calls live)")}`,
          ]),
        },
        {
          kind: "out",
          data:
            `${a.magenta("●")} ${a.bold("Done.")} Authored ${a.white("barista")}, validated the graph, ran 2 flows, drove a\n` +
            `  session to ${a.cyan("confirm")} with world ${a.yellow("{ drink: flat_white }")}, and rendered the TUI —\n` +
            `  ${a.gray("all through the kitsoki MCP server, no LLM in the loop.")}\n`,
        },
      ],
    },
  ],
};

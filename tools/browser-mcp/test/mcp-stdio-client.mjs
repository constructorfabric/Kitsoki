// Minimal MCP stdio client used by the smoke scripts: spawns server.mjs and
// drives it over the real MCP wire protocol (initialize -> tools/list ->
// tools/call), the same way any MCP host would. Shared by scripts/smoke.mjs
// and scripts/smoke-tour.mjs so both exercise the exact wire format instead
// of calling handler functions directly.
import { spawn } from "node:child_process";
import path from "node:path";

export function startClient({ serverPath, env = {} }) {
  const child = spawn(process.execPath, [serverPath], {
    cwd: path.dirname(serverPath),
    env: { ...process.env, KITSOKI_BROWSER_MCP_HEADLESS: "1", ...env },
    stdio: ["pipe", "pipe", "inherit"]
  });

  let buffer = "";
  const pending = new Map();
  let nextId = 1;

  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    buffer += chunk;
    for (;;) {
      const idx = buffer.indexOf("\n");
      if (idx === -1) break;
      const line = buffer.slice(0, idx).replace(/\r$/, "");
      buffer = buffer.slice(idx + 1);
      if (!line.trim()) continue;
      let message;
      try {
        message = JSON.parse(line);
      } catch {
        continue;
      }
      if (message.id !== undefined && pending.has(message.id)) {
        pending.get(message.id).resolve(message);
        pending.delete(message.id);
      }
    }
  });

  function send(method, params) {
    const id = nextId++;
    const message = { jsonrpc: "2.0", id, method, params };
    child.stdin.write(`${JSON.stringify(message)}\n`);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`timed out waiting for response to ${method}`));
      }, 30000);
      pending.set(id, {
        resolve: (msg) => {
          clearTimeout(timer);
          resolve(msg);
        }
      });
    });
  }

  function notify(method, params) {
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method, params })}\n`);
  }

  async function initialize() {
    const init = await send("initialize", {
      protocolVersion: "2025-03-26",
      capabilities: {},
      clientInfo: { name: "browser-mcp-smoke", version: "0.0.0" }
    });
    notify("notifications/initialized", {});
    return init;
  }

  async function callTool(name, args = {}) {
    const response = await send("tools/call", { name, arguments: args });
    if (response.error) throw new Error(`${name}: ${JSON.stringify(response.error)}`);
    const result = response.result;
    const text = result?.content?.[0]?.text;
    let parsed = text;
    try {
      parsed = JSON.parse(text);
    } catch {
      // leave as text
    }
    if (result?.isError) throw new Error(`${name} returned isError: ${text}`);
    return parsed;
  }

  return { child, send, notify, initialize, callTool };
}

export function assertOk(cond, message) {
  if (!cond) throw new Error(`SMOKE FAIL: ${message}`);
  console.log(`ok - ${message}`);
}

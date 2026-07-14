#!/usr/bin/env node
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createInterface } from "node:readline";

const child = spawn(process.execPath, ["server.mjs"], {
  cwd: new URL("..", import.meta.url),
  stdio: ["pipe", "pipe", "inherit"]
});

const rl = createInterface({ input: child.stdout });
let nextID = 1;

async function request(method, params = {}) {
  const id = nextID++;
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
  for (;;) {
    const [line] = await once(rl, "line");
    const message = JSON.parse(line);
    if (message.id === id) return message;
  }
}

const init = await request("initialize", {
  protocolVersion: "2025-03-26",
  capabilities: {},
  clientInfo: { name: "frontend-mockup-smoke", version: "0.1.0" }
});
if (init.error) throw new Error(init.error.message);

const list = await request("tools/list");
if (list.error) throw new Error(list.error.message);
for (const name of [
  "mockup_status",
  "mockup_visual_qa",
  "mockup_dom",
  "mockup_tour_start",
  "mockup_tour_export",
  "mockup_demo_doctor",
  "mockup_record_tour",
  "mockup_create"
]) {
  if (!list.result.tools.some((tool) => tool.name === name)) {
    throw new Error(`${name} tool missing`);
  }
}

const status = await request("tools/call", { name: "mockup_status", arguments: {} });
if (status.error || status.result?.isError) {
  throw new Error(JSON.stringify(status));
}

child.kill();
console.log("frontend mockup MCP smoke passed");

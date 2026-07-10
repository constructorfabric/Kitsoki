#!/usr/bin/env node
import { spawn } from "node:child_process";
import process from "node:process";

const SERVER_INFO = { name: "kitsoki-stagehand", version: "0.1.0" };
const KITSOKI_REPO = process.env.KITSOKI_REPO || process.cwd();
const KITSOKI_AGENT_CMD = process.env.KITSOKI_AGENT_CMD || "kitsoki";
const KITSOKI_AGENT = process.env.KITSOKI_STAGEHAND_AGENT || "codex-native";

let stagehandModulePromise;
let stagehand;

const tools = [
  {
    name: "stagehand_status",
    description: "Report whether the local Stagehand browser session is initialized.",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false
    }
  },
  {
    name: "stagehand_navigate",
    description: "Open or reuse the local Stagehand browser and navigate to a URL.",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "URL to open." }
      },
      required: ["url"],
      additionalProperties: false
    }
  },
  {
    name: "stagehand_extract",
    description: "Extract text or structured information from the current page using Stagehand through the Kitsoki harness.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "What to extract. Omit for page text." }
      },
      additionalProperties: false
    }
  },
  {
    name: "stagehand_observe",
    description: "Observe actions available on the current page using Stagehand through the Kitsoki harness.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "Optional description of the actions to find." }
      },
      additionalProperties: false
    }
  },
  {
    name: "stagehand_act",
    description: "Perform a browser action in natural language using Stagehand through the Kitsoki harness.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "Action to perform." }
      },
      required: ["instruction"],
      additionalProperties: false
    }
  },
  {
    name: "stagehand_close",
    description: "Close the current local Stagehand browser session.",
    inputSchema: {
      type: "object",
      properties: {},
      additionalProperties: false
    }
  }
];

function write(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function result(id, value) {
  write({ jsonrpc: "2.0", id, result: value });
}

function error(id, code, message, data) {
  write({ jsonrpc: "2.0", id, error: { code, message, ...(data === undefined ? {} : { data }) } });
}

function textResult(value) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return { content: [{ type: "text", text }] };
}

function stripInvisible(text) {
  return text.replace(/[\u200b-\u200f\u2060-\u2064\ufeff]/g, "");
}

function extractJSON(text) {
  const clean = stripInvisible(text).trim();
  try {
    return JSON.parse(clean);
  } catch {
    const start = clean.search(/[\[{]/);
    if (start === -1) {
      throw new Error(`kitsoki agent ask did not return JSON: ${clean.slice(0, 500)}`);
    }
    for (let end = clean.length; end > start; end -= 1) {
      try {
        return JSON.parse(clean.slice(start, end));
      } catch {
        // Try the next shorter suffix.
      }
    }
    throw new Error(`kitsoki agent ask returned unparsable JSON: ${clean.slice(0, 500)}`);
  }
}

function stringifyMessageContent(content) {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return JSON.stringify(content);
  }
  return content
    .map((part) => {
      if (part?.type === "text") return part.text || "";
      if (part?.text) return part.text;
      if (part?.image_url || part?.source) return "[image omitted: Stagehand MCP is configured for DOM/a11y, not vision]";
      return JSON.stringify(part);
    })
    .join("\n");
}

function schemaHint(schema) {
  if (!schema) return "";
  if (typeof schema.toJSON === "function") {
    return JSON.stringify(schema.toJSON(), null, 2);
  }
  if (typeof schema.toJSONSchema === "function") {
    return JSON.stringify(schema.toJSONSchema(), null, 2);
  }
  return String(schema);
}

function askKitsoki(prompt) {
  return new Promise((resolve, reject) => {
    const child = spawn(
      KITSOKI_AGENT_CMD,
      ["agent", "ask", "--agent", KITSOKI_AGENT, "--working-dir", KITSOKI_REPO, "--prompt", "-"],
      {
        cwd: KITSOKI_REPO,
        env: { ...process.env, KITSOKI_REPO },
        stdio: ["pipe", "pipe", "pipe"]
      }
    );
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code !== 0) {
        reject(new Error(`kitsoki agent ask exited ${code}: ${stderr || stdout}`));
        return;
      }
      resolve(stripInvisible(stdout));
    });
    child.stdin.end(prompt);
  });
}

async function loadStagehandModule() {
  if (!stagehandModulePromise) {
    stagehandModulePromise = import("@browserbasehq/stagehand");
  }
  return stagehandModulePromise;
}

async function makeLLMClient(modelName = "openai/gpt-5.5") {
  const { LLMClient } = await loadStagehandModule();
  return new (class KitsokiLLMClient extends LLMClient {
    constructor() {
      super(modelName);
      this.type = "kitsoki";
      this.modelName = modelName;
      this.hasVision = false;
      this.clientOptions = {};
    }

    async createChatCompletion({ options }) {
      const prompt = [
        "You are answering an internal Stagehand browser automation LLM request.",
        "Return ONLY the requested answer. Do not include markdown fences.",
        options.response_model
          ? `Return ONLY JSON matching this schema:\n${schemaHint(options.response_model.schema)}`
          : "For non-structured requests, return concise plain text.",
        "",
        ...options.messages.map((message) => `${message.role.toUpperCase()}:\n${stringifyMessageContent(message.content)}`)
      ].join("\n\n");
      const stdout = await askKitsoki(prompt);
      if (options.response_model) {
        return {
          data: extractJSON(stdout),
          usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
        };
      }
      return {
        id: `kitsoki-${Date.now()}`,
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: modelName,
        choices: [
          {
            index: 0,
            message: { role: "assistant", content: stdout, tool_calls: [] },
            finish_reason: "stop"
          }
        ],
        usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
      };
    }
  })();
}

async function ensureStagehand() {
  if (stagehand) return stagehand;
  const { Stagehand } = await loadStagehandModule();
  stagehand = new Stagehand({
    env: "LOCAL",
    llmClient: await makeLLMClient(),
    localBrowserLaunchOptions: {
      headless: process.env.KITSOKI_STAGEHAND_HEADLESS !== "0"
    },
    disablePino: true,
    verbose: 0
  });
  await stagehand.init();
  return stagehand;
}

async function callTool(name, args = {}) {
  switch (name) {
    case "stagehand_status":
      return textResult({
        initialized: Boolean(stagehand),
        repo: KITSOKI_REPO,
        agentCommand: KITSOKI_AGENT_CMD,
        agent: KITSOKI_AGENT
      });
    case "stagehand_navigate": {
      if (!args.url) throw new Error("url is required");
      const sh = await ensureStagehand();
      const response = await sh.context.pages()[0].goto(args.url);
      return textResult({ url: args.url, status: response?.status?.() ?? null });
    }
    case "stagehand_extract": {
      const sh = await ensureStagehand();
      return textResult(await (args.instruction ? sh.extract(args.instruction) : sh.extract()));
    }
    case "stagehand_observe": {
      const sh = await ensureStagehand();
      return textResult(await (args.instruction ? sh.observe(args.instruction) : sh.observe()));
    }
    case "stagehand_act": {
      if (!args.instruction) throw new Error("instruction is required");
      const sh = await ensureStagehand();
      return textResult(await sh.act(args.instruction));
    }
    case "stagehand_close":
      if (stagehand) {
        await stagehand.close();
        stagehand = undefined;
      }
      return textResult({ closed: true });
    default:
      throw new Error(`unknown tool: ${name}`);
  }
}

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  for (;;) {
    const index = buffer.indexOf("\n");
    if (index === -1) break;
    const line = buffer.slice(0, index).replace(/\r$/, "");
    buffer = buffer.slice(index + 1);
    if (!line.trim()) continue;
    void handleLine(line);
  }
});

async function handleLine(line) {
  let message;
  try {
    message = JSON.parse(line);
  } catch (err) {
    error(null, -32700, `parse error: ${err.message}`);
    return;
  }
  if (!message.id && message.method?.startsWith("notifications/")) {
    return;
  }
  try {
    switch (message.method) {
      case "initialize":
        result(message.id, {
          protocolVersion: message.params?.protocolVersion || "2025-03-26",
          capabilities: { tools: {} },
          serverInfo: SERVER_INFO
        });
        break;
      case "ping":
        result(message.id, {});
        break;
      case "tools/list":
        result(message.id, { tools });
        break;
      case "tools/call":
        result(message.id, await callTool(message.params?.name, message.params?.arguments || {}));
        break;
      default:
        error(message.id, -32601, `method not found: ${message.method}`);
    }
  } catch (err) {
    result(message.id, {
      isError: true,
      content: [{ type: "text", text: err?.stack || err?.message || String(err) }]
    });
  }
}

process.on("SIGINT", async () => {
  if (stagehand) await stagehand.close().catch(() => {});
  process.exit(130);
});

// The keyless grounding bridge: routes Stagehand's LLM calls through
// `kitsoki agent ask` (the harness) instead of a direct provider API key, so
// browser-mcp is billed and cassette-recorded the same way every other
// kitsoki-driven agent call is. Ported from tools/stagehand-mcp/server.mjs
// (the de-risked spike) — keep this module the one place that logic lives;
// tools/frontend-mockup-mcp's private copy is retired once it depends on
// this package (P6).
import { spawn } from "node:child_process";

export function stripInvisible(text) {
  return text.replace(/[​-‏⁠-⁤﻿]/g, "");
}

export function extractJSON(text) {
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
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return JSON.stringify(content);
  return content
    .map((part) => {
      if (part?.type === "text") return part.text || "";
      if (part?.text) return part.text;
      if (part?.image_url || part?.source) return "[image omitted: browser-mcp is configured for DOM/a11y, not vision]";
      return JSON.stringify(part);
    })
    .join("\n");
}

function schemaHint(schema) {
  if (!schema) return "";
  if (typeof schema.toJSON === "function") return JSON.stringify(schema.toJSON(), null, 2);
  if (typeof schema.toJSONSchema === "function") return JSON.stringify(schema.toJSONSchema(), null, 2);
  return String(schema);
}

export function askKitsoki({ prompt, repo, agentCmd, agent }) {
  return new Promise((resolve, reject) => {
    const child = spawn(agentCmd, ["agent", "ask", "--agent", agent, "--working-dir", repo, "--prompt", "-"], {
      cwd: repo,
      env: { ...process.env, KITSOKI_REPO: repo },
      stdio: ["pipe", "pipe", "pipe"]
    });
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

// Builds a Stagehand LLMClient subclass bound to one repo/agent-cmd/agent
// triple. Stagehand's `LLMClient` base class is loaded lazily from the
// caller so this module has no direct Stagehand import (it's imported by
// both the live server and offline unit tests that never touch Stagehand).
export function makeKitsokiLLMClient(LLMClientBase, { modelName = "openai/gpt-5.5", repo, agentCmd, agent }) {
  return new (class KitsokiLLMClient extends LLMClientBase {
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
      const stdout = await askKitsoki({ prompt, repo, agentCmd, agent });
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
        choices: [{ index: 0, message: { role: "assistant", content: stdout, tool_calls: [] }, finish_reason: "stop" }],
        usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
      };
    }
  })();
}

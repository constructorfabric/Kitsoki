// Cassette-style test of the harness LLM-call bridge (lib/kitsoki-llm-client.mjs)
// that both `observe` and `act` route every LLM call through. Rather than
// spawning the real `kitsoki agent ask` (a live call, or requiring the
// `kitsoki` binary — currently broken on this branch for unrelated reasons),
// KITSOKI_AGENT_CMD points at a canned stub script under test/fixtures that
// echoes a fixed response back, the same way a recorded cassette substitutes
// a fixed reply for a live call. Deterministic, zero cost, zero LLM.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { makeKitsokiLLMClient, extractJSON, stripInvisible } from "../lib/kitsoki-llm-client.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const stubAgentCmd = path.join(here, "fixtures", "stub-kitsoki-agent.mjs");

// A minimal stand-in for Stagehand's LLMClient base class — the real class
// isn't imported here (no @browserbasehq/stagehand dependency in this unit
// test), only its constructor contract: `super(modelName)` sets nothing
// this module reads directly, so a bare class satisfies makeKitsokiLLMClient.
class FakeLLMClientBase {
  constructor(modelName) {
    this.modelName = modelName;
  }
}

function client() {
  return makeKitsokiLLMClient(FakeLLMClientBase, {
    repo: here,
    agentCmd: stubAgentCmd,
    agent: "test-agent"
  });
}

test("stripInvisible removes zero-width/invisible unicode noise", () => {
  const withInvisible = "hello​world⁠!﻿";
  assert.equal(stripInvisible(withInvisible), "helloworld!");
});

test("extractJSON parses clean JSON and recovers JSON embedded in prose", () => {
  assert.deepEqual(extractJSON('{"a":1}'), { a: 1 });
  assert.deepEqual(extractJSON('Sure, here you go:\n{"a":1}\nHope that helps!'), { a: 1 });
});

// The cassette: createChatCompletion spawns process.execPath (node) running
// the stub script instead of a live `kitsoki` binary. The stub reads the
// prompt from stdin and returns a fixed, recorded-looking reply — this
// proves the harness bridge's prompt construction + response parsing
// end to end without ever touching a real model.
test("createChatCompletion (structured): routes through the stub agent and parses its JSON reply", async () => {
  const c = client();
  const response = await c.createChatCompletion({
    options: {
      response_model: { schema: { toJSON: () => ({ type: "object" }) } },
      messages: [{ role: "user", content: "observe the page for a Save button" }]
    }
  });
  assert.deepEqual(response.data, {
    actions: [{ selector: '[data-testid="save-btn"]', description: "Save button", method: "click", arguments: [] }]
  });
});

test("createChatCompletion (unstructured): returns a chat-completion-shaped envelope", async () => {
  const c = client();
  const response = await c.createChatCompletion({
    options: { messages: [{ role: "user", content: "plain text request" }] }
  });
  assert.equal(response.choices[0].message.role, "assistant");
  assert.ok(response.choices[0].message.content.length > 0);
});

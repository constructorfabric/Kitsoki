import { describe, it, expect } from "vitest";
import { readAgentUsage } from "../../src/components/agent/lib.js";

describe("readAgentUsage", () => {
  it("reads the canonical claude-CLI meta.usage shape", () => {
    const u = readAgentUsage({
      verb: "task",
      meta: {
        transport: "claude-cli",
        usage: {
          input_tokens: 1200,
          output_tokens: 345,
          cache_read_input_tokens: 900,
          cache_creation_input_tokens: 50,
        },
        cost_usd: 0.0123,
      },
    });
    expect(u.promptTokens).toBe(1200);
    expect(u.responseTokens).toBe(345);
    expect(u.cacheReadTokens).toBe(900);
    expect(u.cacheCreationTokens).toBe(50);
    expect(u.costUsd).toBe(0.0123);
  });

  it("falls back to legacy flat top-level fields (synthetic fixtures)", () => {
    const u = readAgentUsage({
      verb: "decide",
      prompt_tokens: 512,
      response_tokens: 18,
      cost_usd: 0.0009,
    });
    expect(u.promptTokens).toBe(512);
    expect(u.responseTokens).toBe(18);
    expect(u.costUsd).toBe(0.0009);
    // No cache info available in the legacy shape.
    expect(u.cacheReadTokens).toBeUndefined();
  });

  it("falls back to the earlier cassette meta.{prompt,response}_tokens form", () => {
    const u = readAgentUsage({
      verb: "ask",
      meta: { transport: "cassette", prompt_tokens: 800, response_tokens: 42 },
    });
    expect(u.promptTokens).toBe(800);
    expect(u.responseTokens).toBe(42);
  });

  it("prefers meta.usage over the flat aliases when both are present", () => {
    const u = readAgentUsage({
      response_tokens: 42, // legacy flat
      meta: { usage: { input_tokens: 100, output_tokens: 30 }, cost_usd: 0.001 },
    });
    expect(u.promptTokens).toBe(100);
    expect(u.responseTokens).toBe(30);
    expect(u.costUsd).toBe(0.001);
  });

  it("returns all-undefined for an event with no usage", () => {
    const u = readAgentUsage({ verb: "ask" });
    expect(u.promptTokens).toBeUndefined();
    expect(u.responseTokens).toBeUndefined();
    expect(u.costUsd).toBeUndefined();
  });

  it("tolerates undefined attrs", () => {
    expect(() => readAgentUsage(undefined)).not.toThrow();
  });
});

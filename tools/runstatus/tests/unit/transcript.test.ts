/**
 * Unit tests for src/data/transcript.ts — the verbatim → normalized projection
 * the "Agent actions" drawer renders. Covers: a tool_use+tool_result pair, a
 * thinking block, an mcp__validator__submit guardrail, the _kitsoki
 * reject/nudge/accept trio, a result with tokens/cost, and an openai-chat triple.
 */

import { describe, it, expect } from "vitest";
import {
  normalizeTranscript,
  type TranscriptData,
} from "../../src/data/transcript.js";

function data(
  events: Record<string, unknown>[],
  timings: number[] = []
): TranscriptData {
  return { format: "claude-stream-json", events, timings, schemaVersion: 1 };
}

describe("normalizeTranscript", () => {
  it("pairs a tool_use with its tool_result onto one row", () => {
    const rows = normalizeTranscript(
      data(
        [
          {
            type: "assistant",
            message: {
              content: [
                {
                  type: "tool_use",
                  id: "toolu_03",
                  name: "Edit",
                  input: { file_path: "internal/foo/bar.go" },
                },
              ],
            },
          },
          {
            type: "user",
            message: {
              content: [
                { type: "tool_result", tool_use_id: "toolu_03", content: "edited" },
              ],
            },
          },
        ],
        [10, 20]
      )
    );
    // The user/tool_result event is absorbed, not its own row.
    expect(rows).toHaveLength(1);
    expect(rows[0]!.kind).toBe("tool");
    expect(rows[0]!.title).toBe("Edit");
    expect(rows[0]!.input).toEqual({ file_path: "internal/foo/bar.go" });
    expect(rows[0]!.output).toBe("edited");
    expect(rows[0]!.offsetMs).toBe(10);
  });

  it("surfaces an errored tool_result", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "assistant",
          message: {
            content: [{ type: "tool_use", id: "t1", name: "Bash", input: {} }],
          },
        },
        {
          type: "user",
          message: {
            content: [
              { type: "tool_result", tool_use_id: "t1", content: "boom", is_error: true },
            ],
          },
        },
      ])
    );
    expect(rows[0]!.isError).toBe(true);
    expect(rows[0]!.output).toBe("boom");
  });

  it("types a thinking block as reasoning", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "assistant",
          message: {
            content: [
              { type: "thinking", thinking: "The off-by-one is in the loop bound." },
            ],
          },
        },
      ])
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]!.kind).toBe("reasoning");
    expect(rows[0]!.output).toContain("off-by-one");
  });

  it("folds a thinking_tokens run into the thinking block that follows it", () => {
    const rows = normalizeTranscript(
      data(
        [
          {
            type: "assistant",
            message: { content: [{ type: "tool_use", id: "t1", name: "Bash", input: {} }] },
          },
          { type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "ok" }] } },
          { type: "system", subtype: "thinking_tokens", estimated_tokens: 1, estimated_tokens_delta: 1 },
          { type: "system", subtype: "thinking_tokens", estimated_tokens: 2, estimated_tokens_delta: 1 },
          { type: "system", subtype: "thinking_tokens", estimated_tokens: 3, estimated_tokens_delta: 1 },
          {
            type: "assistant",
            message: { content: [{ type: "thinking", thinking: "Now I see the gap." }] },
          },
        ],
        [10, 20, 30, 40, 50, 60]
      )
    );
    // No orphan counter row: the 3 deltas are absorbed by the real thinking block,
    // which keeps its in-order slot (after the Bash tool, before its tool calls).
    expect(rows.map((r) => r.kind)).toEqual(["tool", "reasoning"]);
    expect(rows[1]!.title).toBe("Reasoning (≈3 tokens)");
    expect(rows[1]!.offsetMs).toBe(60);
    expect(rows[1]!.output).toContain("the gap");
  });

  it("emits a trailing Thinking row when the stream ends mid-think (live)", () => {
    const rows = normalizeTranscript(
      data(
        [
          {
            type: "assistant",
            message: { content: [{ type: "tool_use", id: "t1", name: "Bash", input: {} }] },
          },
          { type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "ok" }] } },
          { type: "system", subtype: "thinking_tokens", estimated_tokens: 5, estimated_tokens_delta: 5 },
          { type: "system", subtype: "thinking_tokens", estimated_tokens: 9, estimated_tokens_delta: 4 },
        ],
        [10, 20, 30, 40]
      )
    );
    // No thinking block arrived yet → one progress row at the bottom, stamped at the
    // run start, NOT pinned ahead of the tool call.
    expect(rows.map((r) => r.kind)).toEqual(["tool", "reasoning"]);
    expect(rows[1]!.title).toBe("Thinking");
    expect(rows[1]!.offsetMs).toBe(30);
    expect(rows[1]!.output).toBe("≈9 thinking tokens");
  });

  it("types mcp__validator__submit as a guardrail with its verdict", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "assistant",
          message: {
            content: [
              {
                type: "tool_use",
                id: "s1",
                name: "mcp__validator__submit",
                input: { decision: "refund", amount: 49.0 },
              },
            ],
          },
        },
      ])
    );
    expect(rows[0]!.kind).toBe("guardrail");
    expect(rows[0]!.input).toEqual({ decision: "refund", amount: 49.0 });
  });

  it("types a generic mcp tool call as mcp", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "assistant",
          message: {
            content: [
              { type: "tool_use", id: "m1", name: "mcp__github__search", input: {} },
            ],
          },
        },
      ])
    );
    expect(rows[0]!.kind).toBe("mcp");
    expect(rows[0]!.title).toBe("mcp__github__search");
  });

  it("stamps the submit verdict rejected/accepted from the _kitsoki boundary rows", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "assistant",
          message: {
            content: [
              {
                type: "tool_use",
                id: "s0",
                name: "mcp__validator__submit",
                input: { decision: "refund", amount: "lots" },
              },
            ],
          },
        },
        {
          _kitsoki: "validator_reject",
          source: "schema",
          reason: 'amount: expected number, got string "lots"',
        },
        {
          _kitsoki: "nudge",
          outer_iter: 1,
          text: "Your previous turn ended … rejected: amount …",
        },
        {
          type: "assistant",
          message: {
            content: [
              {
                type: "tool_use",
                id: "s1",
                name: "mcp__validator__submit",
                input: { decision: "refund", amount: 49.0 },
              },
            ],
          },
        },
        { _kitsoki: "validator_accept", outer_iter: 1 },
      ])
    );

    const kinds = rows.map((r) => r.kind);
    // The bad submit is stamped REJECTED in place (no orphan row), then the host
    // nudge, then the good submit stamped ACCEPTED: submit→reject collapses to one
    // guardrail row, so the arc is [guardrail(rejected), host-nudge, guardrail(accepted)].
    expect(kinds).toEqual(["guardrail", "host-nudge", "guardrail"]);

    // rows[0] is the FIRST submit, stamped rejected by the trailing validator_reject
    // (so it never renders a misleading green PASS).
    const rejected = rows[0]!;
    expect(rejected.isError).toBe(true);
    expect(rejected.title).toContain("rejected");
    expect(rejected.output).toContain("expected number");

    const nudge = rows[1]!;
    expect(nudge.kind).toBe("host-nudge");
    expect(nudge.title).toContain("iteration 1");

    // The final submit row is stamped accepted by the trailing validator_accept.
    const accepted = rows[2]!;
    expect(accepted.title).toBe("Guardrail accepted");
    expect(accepted.isError).toBeFalsy();
  });

  it("renders a tool_bypassed banner", () => {
    const rows = normalizeTranscript(
      data([{ _kitsoki: "tool_bypassed", verdict_recovered_from: "code_block" }])
    );
    expect(rows[0]!.kind).toBe("banner");
    expect(rows[0]!.output).toContain("code_block");
  });

  it("extracts tokens and cost from a result envelope", () => {
    const rows = normalizeTranscript(
      data([
        {
          type: "result",
          subtype: "success",
          result: "Fixed the off-by-one.",
          usage: { input_tokens: 1200, output_tokens: 640 },
          total_cost_usd: 0.04,
        },
      ])
    );
    expect(rows[0]!.kind).toBe("result");
    expect(rows[0]!.tokens).toEqual({ input: 1200, output: 640 });
    expect(rows[0]!.cost).toBe(0.04);
    expect(rows[0]!.isError).toBeFalsy();
    expect(rows[0]!.output).toContain("off-by-one");
  });

  it("flags a non-success result as an error", () => {
    const rows = normalizeTranscript(
      data([{ type: "result", subtype: "error_during_execution", result: "nope" }])
    );
    expect(rows[0]!.isError).toBe(true);
  });

  it("normalizes an openai-chat request/assistant/result triple", () => {
    const rows = normalizeTranscript({
      format: "openai-chat",
      events: [
        { type: "request", model: "qwen", message: { role: "user", content: "classify this" } },
        { type: "assistant", message: { role: "assistant", content: "concurrency" } },
        {
          type: "result",
          result: "concurrency",
          usage: { input_tokens: 900, output_tokens: 12 },
        },
      ],
      timings: [0, 5, 8],
      schemaVersion: 1,
    });
    expect(rows.map((r) => r.kind)).toEqual(["system", "reasoning", "result"]);
    expect(rows[0]!.output).toBe("classify this");
    expect(rows[1]!.output).toBe("concurrency");
    expect(rows[2]!.tokens).toEqual({ input: 900, output: 12 });
  });

  it("tolerates a flat openai/llama tool_use shape", () => {
    const rows = normalizeTranscript({
      format: "openai-chat",
      events: [
        { type: "tool_use", id: "x", name: "lookup", arguments: '{"q":"a"}' },
      ],
      timings: [],
      schemaVersion: 1,
    });
    expect(rows[0]!.kind).toBe("tool");
    expect(rows[0]!.title).toBe("lookup");
    expect(rows[0]!.input).toBe('{"q":"a"}');
  });

  it("emits a system row from a claude init event", () => {
    const rows = normalizeTranscript(
      data([{ type: "system", subtype: "init", session_id: "s", model: "claude-x" }])
    );
    expect(rows[0]!.kind).toBe("system");
    expect(rows[0]!.output).toContain("claude-x");
  });
});

import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import ChatTranscript from "../../src/components/ChatTranscript.vue";

// These exercise the agent-text markdown path (the engine ships rendered text;
// the browser only formats markdown, never evaluates pongo) and confirm user
// text is rendered literally (escaped), not as markdown/HTML.
describe("ChatTranscript", () => {
  it("formats agent view: heading line, bold, inline code — and PRESERVES line structure", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            // Two numbered items on separate lines must NOT be joined into one.
            text: "## PRD\n1. first question\n2. second question\nSay **ready**, or use `quit`.",
          },
        ],
      },
    });
    const view = wrapper.find(".chat-view");
    expect(view.exists()).toBe(true);
    expect(view.find(".cv-h").text()).toBe("PRD");
    expect(view.find("strong").text()).toBe("ready");
    expect(view.find("code").text()).toBe("quit");
    // The two list lines stay on distinct lines (verbatim newlines preserved),
    // never collapsed into a run-on paragraph.
    expect(view.text()).toContain("1. first question\n2. second question");
  });

  it("renders a fenced ```json block as a code box, not literal backticks", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text:
              'Here is the result:\n\n```json\n{\n  "slug": "web-companion-pet"\n}\n```\n\nDone.',
          },
        ],
      },
    });
    const view = wrapper.find(".chat-view");
    const pre = view.find("pre.cv-pre");
    expect(pre.exists()).toBe(true);
    // The JSON body is inside the code box…
    expect(pre.text()).toContain('"slug": "web-companion-pet"');
    // …and the literal fence markers do NOT leak into the rendered HTML.
    expect(view.html()).not.toContain("```");
    // The language hint rides along for syntax-class hooks.
    expect(view.find("pre.cv-pre code").classes()).toContain("language-json");
  });

  it("escapes HTML inside a fenced block (no injection via code)", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          { role: "agent", text: "```\n<img src=x onerror=alert(1)>\n```" },
        ],
      },
    });
    const html = wrapper.find(".chat-view").html();
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  it("escapes HTML in agent text (no injection via the view)", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [{ role: "agent", text: "danger <img src=x onerror=alert(1)>" }],
      },
    });
    const html = wrapper.find(".chat-view").html();
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
  });

  it("renders a preserved stream feed as a collapsed activity section, in order", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Final room view.",
            stream: [
              { kind: "thinking", text: "I'll scan the docs first." },
              { kind: "tool", tool: "ToolSearch", preview: "select:WebSearch" },
              { kind: "tool", tool: "Bash", preview: "find … | grep …" },
              { kind: "thinking", text: "The spec lives in proposals." },
              { kind: "tool", tool: "Read", preview: "docs/proposals/x.md" },
            ],
          },
        ],
      },
    });
    const activity = wrapper.find("[data-testid='chat-activity']");
    expect(activity.exists()).toBe(true);
    // Collapsed by default: a <details> WITHOUT the open attribute, summary
    // counts the activity.
    expect(activity.element.hasAttribute("open")).toBe(false);
    expect(activity.find(".chat-activity__summary").text()).toBe(
      "🧠 2 thoughts · 3 tool calls"
    );
    // The feed preserves arrival order: thought, tool, tool, thought, tool.
    const rows = activity.findAll(".chat-activity__thought, .chat-activity__tool");
    expect(
      rows.map((r) => (r.classes().includes("chat-activity__thought") ? "think" : "tool"))
    ).toEqual(["think", "tool", "tool", "think", "tool"]);
    expect(rows[0]!.text()).toContain("🧠");
    expect(rows[0]!.text()).toContain("I'll scan the docs first.");
    // The final view still renders as the bubble body.
    expect(wrapper.find(".chat-view").text()).toContain("Final room view.");
  });

  it("omits the activity section when an entry carries no stream", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "Plain view." }] },
    });
    expect(wrapper.find("[data-testid='chat-activity']").exists()).toBe(false);
  });

  it("marks an off-ramp agent bubble distinctly (chip + data-mode), but renders the answer verbatim", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Kitsoki is a deterministic state-machine runtime.",
            isOffRamp: true,
          },
        ],
      },
    });
    // The bubble carries a stable testid + data-mode so the tour / vision-QA
    // can assert deterministically that this answer was a free-form off-ramp.
    const bubble = wrapper.find("[data-testid='offramp-bubble']");
    expect(bubble.exists()).toBe(true);
    expect(bubble.attributes("data-mode")).toBe("offpath");
    expect(bubble.classes()).toContain("chat-bubble--offramp");
    // The visible "off path" chip distinguishes it from a normal bubble.
    expect(wrapper.find("[data-testid='offramp-chip']").text()).toContain("off path");
    // The answer text itself is rendered verbatim (not altered by the chip).
    expect(wrapper.find(".chat-view").text()).toContain(
      "Kitsoki is a deterministic state-machine runtime."
    );
  });

  it("does NOT mark a normal agent bubble as off-ramp", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "A normal room view." }] },
    });
    expect(wrapper.find("[data-testid='offramp-bubble']").exists()).toBe(false);
    expect(wrapper.find("[data-testid='offramp-chip']").exists()).toBe(false);
    expect(wrapper.find(".chat-bubble").classes()).not.toContain("chat-bubble--offramp");
  });

  it("renders the inline routing chip from a user entry's routing provenance", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "user",
            text: "commit my work",
            routing: {
              routedBy: "semantic",
              matchType: "leading-verb:commit",
              confidence: 0.95,
              intent: "git.commit",
            },
          },
        ],
      },
    });
    const chip = wrapper.find("[data-testid='routing-chip']");
    expect(chip.exists()).toBe(true);
    // The chip reads: → intent · TIER · reason · conf.
    expect(chip.find(".chat-routing__intent").text()).toBe("git.commit");
    expect(chip.find(".chat-routing__tier").text()).toBe("semantic");
    expect(chip.find(".chat-routing__reason").text()).toBe("leading-verb:commit");
    expect(chip.find(".chat-routing__conf").text()).toBe("0.95");
    // Green deterministic tiers carry a tier-specific class (vs amber --llm).
    expect(chip.find(".chat-routing__tier").classes()).toContain("chat-routing__tier--semantic");
  });

  it("tints the new workbench fallback tier as a free ($0) deterministic route", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "user",
            text: "file a bunch of markdown issues on github",
            routing: {
              routedBy: "fallback",
              matchType: "free_text",
              intent: "core__work",
            },
          },
        ],
      },
    });
    const chip = wrapper.find("[data-testid='routing-chip']");
    expect(chip.exists()).toBe(true);
    const tier = chip.find(".chat-routing__tier");
    expect(tier.text()).toBe("fallback");
    // Joins the green/free group (NOT neutral gray) — driven by the free/paid
    // modifier, not an enumerated tier list, so it never drifts again.
    expect(tier.classes()).toContain("chat-routing__tier--fallback");
    expect(tier.classes()).toContain("chat-routing__tier--free");
    expect(tier.classes()).not.toContain("chat-routing__tier--paid");
    // The tooltip tells the same cost story as the other deterministic tiers.
    expect(chip.attributes("title")).toContain("deterministic, no LLM, $0");
  });

  it("tints the LLM tier as paid (amber), distinct from the free tiers", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "user",
            text: "do the thing",
            routing: { routedBy: "llm", matchType: "agent.local", intent: "git.commit" },
          },
        ],
      },
    });
    const tier = wrapper.find("[data-testid='routing-chip'] .chat-routing__tier");
    expect(tier.classes()).toContain("chat-routing__tier--paid");
    expect(tier.classes()).not.toContain("chat-routing__tier--free");
  });

  it("omits the routing chip for a plain user entry (no provenance)", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "user", text: "commit my work" }] },
    });
    expect(wrapper.find("[data-testid='routing-chip']").exists()).toBe(false);
  });

  it("renders user text literally, not as markdown", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [{ role: "user", text: "I want **a CLI** for X" }],
      },
    });
    const userRow = wrapper.find(".chat-text");
    expect(userRow.exists()).toBe(true);
    // Literal asterisks preserved; no <strong>.
    expect(userRow.text()).toContain("**a CLI**");
    expect(userRow.find("strong").exists()).toBe(false);
  });
});

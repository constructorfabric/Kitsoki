import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { readFileSync } from "node:fs";
import { join } from "node:path";
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

  it("replaces a pinned media embed with a named workbench receipt", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        suppressedMediaHandles: ["mockup.html"],
        suppressedMediaLabels: { "mockup.html": "Checkout mockup" },
        transcript: [
          {
            role: "agent",
            text: "Rendered mockup.",
            typedView: {
              Source: "",
              Elements: [
                {
                  Kind: "media",
                  MediaHandle: "mockup.html",
                  MediaKind: "html",
                  MediaCaption: "Checkout mockup",
                },
              ],
            },
          },
        ],
      },
    });

    const receipt = wrapper.find("[data-testid='chat-media-receipt']");
    expect(receipt.exists()).toBe(true);
    expect(receipt.text()).toContain("Checkout mockup");
    expect(receipt.text()).toContain("Pinned in the workbench");
    expect(wrapper.find("[data-testid='media-element']").exists()).toBe(false);
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

  it("renders the contextual-routing receipt chip on an intent-class agent bubble", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Workbench ready.",
            contextRoute: {
              class: "intent",
              intent: "git.commit",
              reason: "user asked to save progress",
              confidence: 0.82,
              alternatives: [
                { class: "help", confidence: 0.25 },
                { class: "meta_edit", confidence: 0.15 },
              ],
              decision_id: "sess-1:7",
            },
          },
        ],
      },
    });
    const chip = wrapper.find("[data-testid='route-receipt']");
    expect(chip.exists()).toBe(true);
    // Target is the matched intent for an intent-class route…
    expect(chip.find(".chat-route-receipt__target").text()).toBe("git.commit");
    // …and the tier is marked "contextual" so it reads apart from a normal route.
    expect(chip.find(".chat-route-receipt__tier").text()).toBe("contextual");
    expect(chip.find(".chat-route-receipt__conf").text()).toBe("0.82");
    expect(wrapper.find("[data-testid='route-actions']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-intent']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-room_request']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-alternatives']").exists()).toBe(true);
    // The decision id (the rewind target) rides in the tooltip.
    expect(chip.attributes("title")).toContain("sess-1:7");
    expect(chip.attributes("title")).toContain("git.commit");
  });

  it("names the lane (not the bare class) for a non-intent CRR route", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Here's how that works…",
            isOffRamp: true,
            contextRoute: {
              class: "help",
              target_lane: "help",
              confidence: 0.7,
              decision_id: "sess-1:3",
            },
          },
        ],
      },
    });
    const chip = wrapper.find("[data-testid='route-receipt']");
    expect(chip.exists()).toBe(true);
    expect(chip.find(".chat-route-receipt__target").text()).toBe("help");
    expect(wrapper.find("[data-testid='route-actions']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-meta_edit']").exists()).toBe(true);
    // The off-path chip and the route receipt coexist on the same role row.
    expect(wrapper.find("[data-testid='offramp-chip']").exists()).toBe(true);
  });

  // ---- routing-feedback control (WS-C C4) ----
  it("emits 'feedback' with the entry + verdict when a thumbs control is clicked", async () => {
    const entry = {
      role: "user" as const,
      text: "fix the crash on login",
      routing: { routedBy: "semantic", intent: "go_bugfix", statePath: "hub" },
    };
    const wrapper = mount(ChatTranscript, { props: { transcript: [entry] } });
    const up = wrapper.find("[data-testid='routing-feedback-up']");
    const down = wrapper.find("[data-testid='routing-feedback-down']");
    expect(up.exists()).toBe(true);
    expect(down.exists()).toBe(true);
    await down.trigger("click");
    const ev = wrapper.emitted("feedback");
    expect(ev).toBeTruthy();
    expect(ev![0]).toEqual([entry, "down"]);
    wrapper.unmount();
  });

  it("hides the thumbs control and shows a recorded chip once feedbackGiven is set", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "user",
            text: "fix the crash on login",
            routing: { routedBy: "semantic", intent: "go_bugfix", statePath: "hub" },
            feedbackGiven: "up",
          },
        ],
      },
    });
    expect(wrapper.find("[data-testid='routing-feedback']").exists()).toBe(false);
    const given = wrapper.find("[data-testid='routing-feedback-given']");
    expect(given.exists()).toBe(true);
    expect(given.text()).toContain("recorded");
  });

  it("emits 'reroute' with the decision_id and selected class when a route action is clicked", async () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Here's how that works…",
            contextRoute: {
              class: "help",
              target_lane: "help",
              confidence: 0.7,
              decision_id: "sess-1:3",
            },
          },
        ],
      },
    });
    const btn = wrapper.find("[data-testid='route-retry-meta_edit']");
    expect(btn.exists()).toBe(true);
    await btn.trigger("click");
    const ev = wrapper.emitted("reroute");
    expect(ev).toBeTruthy();
    // It carries the receipt's decision id plus the selected reroute class.
    expect(ev![0]).toEqual(["sess-1:3", "meta_edit"]);
    wrapper.unmount();
  });

  it("keeps the reroute affordances visible on an intent-class receipt", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Workbench ready.",
            contextRoute: {
              class: "intent",
              intent: "git.commit",
              confidence: 0.82,
              decision_id: "sess-1:7",
            },
          },
        ],
      },
    });
    expect(wrapper.find("[data-testid='route-actions']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-help']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-meta_edit']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='route-retry-room_request']").exists()).toBe(true);
    wrapper.unmount();
  });

  it("shows the parked fallback capsule when reroute preserved dirty work", () => {
    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          {
            role: "agent",
            text: "Workbench ready.",
            contextRoute: {
              class: "help",
              target_lane: "help",
              confidence: 0.7,
              decision_id: "sess-1:3",
            },
            parkedWorkspace: {
              source_workspace: "/tmp/workspaces/park-source",
              recovery_workspace: "/tmp/workspaces/park-source-park-20260711T083617-64827",
              recovery_branch: "agent/park-source-park-20260711T083617-64827",
              recovery_commit: "0123456789abcdef0123456789abcdef01234567",
              cleaned: true,
              reason: "operator reroute",
            },
          },
        ],
      },
    });
    const parked = wrapper.find("[data-testid='parked-workspace']");
    expect(parked.exists()).toBe(true);
    expect(parked.text()).toContain("parked in");
    expect(parked.text()).toContain("fallback capsule");
    expect(parked.text()).toContain("0123456789ab");
    expect(parked.attributes("title")).toContain("source cleaned");
    expect(parked.attributes("title")).toContain("operator reroute");
    wrapper.unmount();
  });

  it("omits the route receipt for a non-contextual agent turn", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "A normal room view." }] },
    });
    expect(wrapper.find("[data-testid='route-receipt']").exists()).toBe(false);
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

// jsdom has no layout engine, so scroll metrics and ResizeObserver are absent.
// We stub the scroller's metrics and a fake ResizeObserver to exercise the
// auto-follow logic directly — this is the regression net for the popped-out
// VS Code panel "doesn't scroll reliably" report: late content growth must keep
// re-pinning to the bottom WHILE the reader is there, and must NOT yank them
// back once they scroll up to re-read.
describe("ChatTranscript auto-follow", () => {
  let roCallback: (() => void) | null;
  let realRO: typeof globalThis.ResizeObserver | undefined;

  beforeEach(() => {
    roCallback = null;
    realRO = globalThis.ResizeObserver;
    class FakeResizeObserver {
      constructor(cb: () => void) {
        roCallback = cb;
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    globalThis.ResizeObserver = FakeResizeObserver as unknown as typeof globalThis.ResizeObserver;
  });

  afterEach(() => {
    globalThis.ResizeObserver = realRO as typeof globalThis.ResizeObserver;
  });

  // Give the scroller a deterministic, writable scrollTop and fixed
  // scrollHeight/clientHeight (the parts jsdom leaves at 0), mirroring how the
  // demo recorder itself shadows scrollTop on the instance.
  function stubScroller(
    el: HTMLElement,
    metrics: { scrollHeight: number; clientHeight: number }
  ): { metrics: { scrollHeight: number; clientHeight: number } } {
    let top = 0;
    Object.defineProperty(el, "scrollTop", {
      configurable: true,
      get: () => top,
      set: (v: number) => {
        top = v;
      },
    });
    Object.defineProperty(el, "scrollHeight", {
      configurable: true,
      get: () => metrics.scrollHeight,
    });
    Object.defineProperty(el, "clientHeight", {
      configurable: true,
      get: () => metrics.clientHeight,
    });
    return { metrics };
  }

  it("re-pins to the bottom when content grows while the reader is at the bottom", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "first" }] },
    });
    const el = wrapper.find("[data-testid='chat-transcript']").element as HTMLElement;
    const { metrics } = stubScroller(el, { scrollHeight: 1000, clientHeight: 300 });

    // A tall reply finishes laying out after it was appended → ResizeObserver
    // fires. Pinned (default), so the camera follows to the new bottom.
    expect(roCallback).toBeTruthy();
    roCallback!();
    expect(el.scrollTop).toBe(1000);

    // It keeps following each further growth step.
    metrics.scrollHeight = 1800;
    roCallback!();
    expect(el.scrollTop).toBe(1800);
  });

  it("does NOT yank back to the bottom after the reader scrolls up", () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "first" }] },
    });
    const el = wrapper.find("[data-testid='chat-transcript']").element as HTMLElement;
    const { metrics } = stubScroller(el, { scrollHeight: 1000, clientHeight: 300 });

    // Reader scrolls up to re-read; the scroll handler unpins.
    el.scrollTop = 100;
    el.dispatchEvent(new Event("scroll"));

    // More content arrives — must stay put, not snap to the bottom.
    metrics.scrollHeight = 2000;
    roCallback!();
    expect(el.scrollTop).toBe(100);
  });

  it("a new user turn re-pins even after the reader had scrolled up", async () => {
    const wrapper = mount(ChatTranscript, {
      props: { transcript: [{ role: "agent", text: "first" }] },
    });
    const el = wrapper.find("[data-testid='chat-transcript']").element as HTMLElement;
    stubScroller(el, { scrollHeight: 1200, clientHeight: 300 });

    el.scrollTop = 50;
    el.dispatchEvent(new Event("scroll")); // unpinned

    // The reader sends a turn of their own — that always brings them down.
    await wrapper.setProps({
      transcript: [
        { role: "agent", text: "first" },
        { role: "user", text: "next question" },
      ],
    });
    await nextTick();
    expect(el.scrollTop).toBe(1200);
  });

  it("keeps long agent replies in the main transcript scrollport", () => {
    const longReply = Array.from(
      { length: 80 },
      (_, i) => `line ${i + 1}: agent output that should scroll with the chat transcript`
    ).join("\n");

    const wrapper = mount(ChatTranscript, {
      props: {
        transcript: [
          { role: "user", text: "start the work" },
          { role: "agent", text: longReply },
          { role: "user", text: "follow-up that must remain reachable by scrolling the main chat" },
        ],
      },
    });

    const transcript = wrapper.find("[data-testid='chat-transcript']");
    const agentBubble = wrapper.find(".chat-bubble--agent");

    expect(transcript.exists()).toBe(true);
    expect(agentBubble.exists()).toBe(true);
    expect(transcript.classes()).toContain("chat-transcript");
    expect(agentBubble.classes()).not.toContain("chat-bubble--scrollport");
    expect(agentBubble.attributes("data-scrollport")).toBeUndefined();

    const componentPath = join(process.cwd(), "src/components/ChatTranscript.vue");
    const componentSource = readFileSync(componentPath, "utf8");
    const agentBubbleRule =
      componentSource.match(/\.chat-bubble--agent[^{]*\{[^}]*\}/g)?.join("\n") ?? "";

    expect(agentBubbleRule).not.toMatch(/overflow-y\s*:\s*auto/);
    expect(agentBubbleRule).not.toMatch(/max-height\s*:/);
    expect(agentBubbleRule).not.toMatch(/overscroll-behavior\s*:\s*contain/);
  });
});

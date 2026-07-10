import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import AgentDetail from "../../src/components/agent/AgentDetail.vue";
import type { TraceEvent } from "../../src/types.js";

function agentEvent(verb: string, response: unknown): TraceEvent {
  return {
    time: "2026-07-08T12:00:00Z",
    level: "info",
    session_id: "sess-1",
    state_path: "root.phase",
    turn: 1,
    msg: "agent.call.complete",
    attrs: {
      call_id: `call-${verb}`,
      verb,
      model: "test-model",
      response,
    },
  } as TraceEvent;
}

describe("AgentDetail response markdown", () => {
  for (const verb of ["task", "ask", "converse"]) {
    it(`renders ${verb} response bodies as markdown`, () => {
      const w = mount(AgentDetail, {
        props: {
          event: agentEvent(verb, {
            text:
              "## Fix summary\n\n- Changed **config** handling for `APP_FLAG`.\n\n| Check | Result |\n|---|---|\n| unit | pass |",
          }),
          sessionId: "sess-1",
        },
      });
      const md = w.find('[data-testid="collapsible-markdown"]');
      expect(md.exists()).toBe(true);
      expect(md.find("h2.md-h2").text()).toBe("Fix summary");
      expect(md.find("ul.md-ul li").text()).toContain("Changed config handling");
      expect(md.find("strong").text()).toBe("config");
      expect(md.find("code").text()).toBe("APP_FLAG");
      expect(md.find("table.md-table").exists()).toBe(true);
      expect(md.html()).not.toContain("## Fix summary");
      w.unmount();
    });
  }

  it("escapes response HTML before rendering markdown", () => {
    const w = mount(AgentDetail, {
      props: {
        event: agentEvent("task", {
          text: "## Safe\n\n<img src=x onerror=alert(1)>\n\n**ok**",
        }),
        sessionId: "sess-1",
      },
    });
    const html = w.find('[data-testid="collapsible-markdown"]').html();
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
    expect(w.find("strong").text()).toBe("ok");
    w.unmount();
  });
});

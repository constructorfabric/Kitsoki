import { describe, it, expect, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";

const live = vi.hoisted(() => {
  const metaModes = vi.fn();
  const metaEnter = vi.fn();
  const metaStream = vi.fn();
  const reloadSession = vi.fn();
  const bugPreview = vi.fn();
  const metaImproveReport = vi.fn();
  const lastRpcError = vi.fn();
  const LiveSource = vi.fn().mockImplementation(() => ({
    metaModes,
    metaEnter,
    metaStream,
    reloadSession,
    bugPreview,
    metaImproveReport,
    lastRpcError,
  }));
  return {
    LiveSource,
    metaModes,
    metaEnter,
    metaStream,
    reloadSession,
    bugPreview,
    metaImproveReport,
    lastRpcError,
  };
});

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: live.LiveSource,
}));

import ImprovePrompt from "../../src/components/meta/ImprovePrompt.vue";
import { useMetaStore } from "../../src/stores/meta.js";

const improveMode = {
  key: "story.improve",
  label: "Improve run",
  banner: "",
  agent: "story-improver",
  read_only: true,
  group: "story",
};

describe("ImprovePrompt", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    localStorage.clear();
    live.LiveSource.mockClear();
    live.metaModes.mockReset();
    live.metaEnter.mockReset();
    live.metaStream.mockReset();
    live.reloadSession.mockReset();
    live.bugPreview.mockReset();
    live.metaImproveReport.mockReset();
    live.lastRpcError.mockReset();
    live.metaModes.mockResolvedValue([improveMode]);
    live.metaEnter.mockResolvedValue({
      chat_id: "c1",
      mode_key: "story.improve",
      messages: [],
    });
    live.metaStream.mockResolvedValue({
      assistant: "improvement report",
      chat_id: "c1",
      reload_requested: false,
      changed_files: [],
    });
    live.bugPreview.mockResolvedValue({
      capture_id: "cap-1",
      har: { log: { entries: [] } },
      depth: 0,
      capacity: 0,
    });
    live.metaImproveReport.mockResolvedValue({
      sink: "local-artifact",
      path: ".artifacts/issues/bugs/meta.md",
      artifacts: ["har.json", "rrweb.json", "trace.redacted.jsonl"],
      artifacts_path: ".artifacts/issues/bugs/meta.artifacts",
    });
    live.lastRpcError.mockReturnValue(null);
  });

  it("opens story.improve and sends the standard improvement request", async () => {
    const wrapper = mount(ImprovePrompt, { props: { sessionId: "s1" } });
    await flushPromises();

    expect(wrapper.find("[data-testid='improve-prompt']").text()).toContain(
      "Improve this run"
    );
    expect(wrapper.find("[data-testid='improve-run']").attributes("disabled")).toBeUndefined();

    await wrapper.find("[data-testid='improve-run']").trigger("click");
    await flushPromises();

    expect(live.metaModes).toHaveBeenCalledWith("s1");
    expect(live.metaEnter).toHaveBeenCalledWith("s1", "story.improve", "");
    expect(live.metaStream).toHaveBeenCalledWith(
      "s1",
      "story.improve",
      "c1",
      expect.stringContaining("false starts"),
      expect.any(Function)
    );
    expect(live.bugPreview).toHaveBeenCalledTimes(1);
    expect(live.metaImproveReport).toHaveBeenCalledWith(
      expect.objectContaining({
        session_id: "s1",
        mode: "story.improve",
        capture_id: "cap-1",
        destination: "configured",
        report: "improvement report",
      })
    );
    expect(useMetaStore().open).toBe(true);
    expect(wrapper.find("[data-testid='improve-status']").text()).toContain(
      "ready"
    );
    expect(wrapper.find("[data-testid='improve-report-status']").text()).toContain(
      "Evidence report filed"
    );
    expect(wrapper.find("[data-testid='improve-report-artifacts']").text()).toContain(
      "har.json"
    );

    wrapper.unmount();
  });

  it("persists the auto-run opt-in and starts improve immediately", async () => {
    const wrapper = mount(ImprovePrompt, { props: { sessionId: "s1" } });
    await flushPromises();

    await wrapper.find("[data-testid='improve-auto-toggle']").setValue(true);
    await flushPromises();

    expect(localStorage.getItem("kitsoki:improve:autoRun")).toBe("1");
    expect(localStorage.getItem("kitsoki:improve:autoRan:s1")).toBeTruthy();
    expect(live.metaStream).toHaveBeenCalledTimes(1);

    wrapper.unmount();
  });

  it("dedupes auto-run per completed session", async () => {
    localStorage.setItem("kitsoki:improve:autoRun", "1");

    const first = mount(ImprovePrompt, { props: { sessionId: "s1" } });
    await flushPromises();
    expect(live.metaStream).toHaveBeenCalledTimes(1);
    first.unmount();

    const again = mount(ImprovePrompt, { props: { sessionId: "s1" } });
    await flushPromises();
    expect(live.metaStream).toHaveBeenCalledTimes(1);

    await again.setProps({ sessionId: "s2" });
    await flushPromises();
    expect(live.metaStream).toHaveBeenCalledTimes(2);
    expect(live.metaStream.mock.calls[1][0]).toBe("s2");

    again.unmount();
  });
});

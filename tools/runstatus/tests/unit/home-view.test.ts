/**
 * Component tests for src/views/HomeView.vue.
 *
 * The live-source RPC layer is mocked (no live server, no LLM): vi.mock
 * replaces LiveSource with a stub whose methods are vi.fn()s, and vue-router's
 * useRouter is mocked so we can assert navigation without a real router.
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import type { StoryHeader } from "../../src/data/live-source.js";
import type { SessionHeader } from "../../src/types.js";
import { markAutoNavDone } from "../../src/lib/auto-nav.js";

// ── Mocks ───────────────────────────────────────────────────────────────────

const listStories = vi.fn<[], Promise<StoryHeader[]>>();
const rescanStories = vi.fn<[], Promise<StoryHeader[]>>();
const newSession = vi.fn<[string], Promise<string>>();
const listSessions = vi.fn<[], Promise<SessionHeader[]>>();
const driveOperation = vi.fn<[string], Promise<unknown>>();
const artifactUrl = vi.fn<[string], string>((handle) => `/artifact/${encodeURIComponent(handle)}`);
const setupStatus = vi.fn<[], Promise<{
  warnings: Array<{
    id: string;
    title: string;
    body: string;
    action_label?: string;
    action_command?: string;
    story_id?: string;
    story_ref?: string;
  }>;
}>>();

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    listStories,
    rescanStories,
    newSession,
    listSessions,
    driveOperation,
    artifactUrl,
    setupStatus,
  })),
}));

const push = vi.fn();
const replace = vi.fn();
vi.mock("vue-router", () => ({
  useRouter: () => ({ push, replace }),
  // RouterLink stub so <router-link> resolves in mount().
  RouterLink: { props: ["to"], template: "<a :href=\"to\"><slot /></a>" },
}));

// Tour store is mocked so the getting-started CTA can be asserted without a real
// pinia / overlay — the empty state's only job is to call start().
const tourStart = vi.fn();
vi.mock("../../src/stores/tour.js", () => ({
  useTourStore: () => ({ start: tourStart }),
}));

// Imported after the mocks are registered.
import HomeView from "../../src/views/HomeView.vue";

function story(over: Partial<StoryHeader> = {}): StoryHeader {
  return {
    path: "/repo/stories/demo/app.yaml",
    app_id: "demo",
    title: "Demo Story",
    active_sessions: [],
    ...over,
  };
}

function session(over: Partial<SessionHeader> = {}): SessionHeader {
  return {
    session_id: "abcdef1234",
    app_id: "demo",
    current_state: "idle",
    turn: 0,
    started_at: "2026-06-04T00:00:00Z",
    terminal: false,
    ...over,
  };
}

const mountOpts = {
  global: {
    stubs: { RouterLink: { props: ["to"], template: "<a :href=\"to\"><slot /></a>" } },
  },
};

describe("HomeView", () => {
  beforeEach(() => {
    listStories.mockReset();
    rescanStories.mockReset();
    newSession.mockReset();
    listSessions.mockReset();
    driveOperation.mockReset();
    artifactUrl.mockClear();
    setupStatus.mockReset();
    push.mockReset();
    replace.mockReset();
    tourStart.mockReset();
    // The auto-nav "already done" flag lives in sessionStorage so it survives a
    // hard reload (see HomeView). happy-dom's storage is a global that persists
    // across tests in this file, so clear it to give each test a fresh-tab
    // baseline; the reload regression test below opts out by mounting twice.
    sessionStorage.clear();
    // Default: no auto-navigation (zero sessions).
    listStories.mockResolvedValue([]);
    listSessions.mockResolvedValue([]);
    setupStatus.mockResolvedValue({ warnings: [] });
  });

  it("renders a story card per discovered story", async () => {
    listStories.mockResolvedValue([
      story({ path: "/repo/stories/a/app.yaml", app_id: "a", title: "Alpha" }),
      story({ path: "/repo/stories/b/app.yaml", app_id: "b", title: "Beta" }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    const cards = wrapper.findAll("[data-testid='story-card']");
    expect(cards).toHaveLength(2);
    expect(wrapper.findAll("[data-testid='story-title']").map((c) => c.text())).toEqual([
      "Alpha",
      "Beta",
    ]);
    wrapper.unmount();
  });

  it("renders setup warnings and opens the referenced setup story", async () => {
    listStories.mockResolvedValue([
      story({
        path: "/repo/stories/run-as-user-setup/app.yaml",
        app_id: "run-as-user-setup",
        title: "run_as_user setup",
      }),
    ]);
    setupStatus.mockResolvedValue({
      warnings: [
        {
          id: "run-as-user",
          title: "Agent run_as_user delegation is not configured",
          body: "Launch policy is not a sandbox.",
          action_label: "Open setup story",
          action_command: "kitsoki run @kitsoki/run-as-user-setup",
          story_id: "run-as-user-setup",
          story_ref: "@kitsoki/run-as-user-setup",
        },
      ],
    });
    newSession.mockResolvedValue("setup-session");

    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='setup-warning-title']").text()).toContain("run_as_user");
    expect(wrapper.find("[data-testid='setup-warning-mark']").text()).toBe("!");
    expect(wrapper.find("[data-testid='setup-warning-command']").text()).toContain(
      "kitsoki run @kitsoki/run-as-user-setup"
    );

    await wrapper.find("[data-testid='setup-warning-action']").trigger("click");
    await flushPromises();

    expect(newSession).toHaveBeenCalledWith("/repo/stories/run-as-user-setup/app.yaml");
    expect(push).toHaveBeenCalledWith("/s/setup-session/chat");
    wrapper.unmount();
  });

  it("shows an active-session-count badge from active_sessions", async () => {
    listStories.mockResolvedValue([
      story({ active_sessions: ["s1", "s2"] }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='story-active-count']").text()).toContain("2");
    wrapper.unmount();
  });

  it("New session calls newSession then navigates to /s/<id>", async () => {
    listStories.mockResolvedValue([story({ path: "/repo/stories/demo/app.yaml" })]);
    newSession.mockResolvedValue("new-sess-id");
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='new-session-btn']").trigger("click");
    await flushPromises();

    expect(newSession).toHaveBeenCalledWith("/repo/stories/demo/app.yaml");
    // A fresh session is live and meant to be driven → opens on the chat surface.
    expect(push).toHaveBeenCalledWith("/s/new-sess-id/chat");
    wrapper.unmount();
  });

  it("surfaces a structured error in place when newSession fails (no navigation)", async () => {
    listStories.mockResolvedValue([story()]);
    newSession.mockRejectedValue(new Error("invalid story YAML"));
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='new-session-btn']").trigger("click");
    await flushPromises();

    expect(push).not.toHaveBeenCalled();
    expect(wrapper.find("[data-testid='new-session-error']").text()).toContain(
      "invalid story YAML"
    );
    wrapper.unmount();
  });

  it("Rescan calls rescanStories and refreshes the cards", async () => {
    listStories.mockResolvedValue([story({ title: "Before" })]);
    rescanStories.mockResolvedValue([
      story({ path: "/repo/stories/a/app.yaml", title: "After A" }),
      story({ path: "/repo/stories/b/app.yaml", title: "After B" }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();
    expect(wrapper.findAll("[data-testid='story-card']")).toHaveLength(1);

    await wrapper.find("[data-testid='rescan-btn']").trigger("click");
    await flushPromises();

    expect(rescanStories).toHaveBeenCalledTimes(1);
    expect(wrapper.findAll("[data-testid='story-card']")).toHaveLength(2);
    wrapper.unmount();
  });

  it("renders an Open link per active session", async () => {
    listSessions.mockResolvedValue([
      session({ session_id: "sess-aaaa1111" }),
      session({ session_id: "sess-bbbb2222" }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    const rows = wrapper.findAll("[data-testid='session-row']");
    expect(rows).toHaveLength(2);
    const open = wrapper.find("[data-testid='session-open']");
    expect(open.attributes("href")).toBe("/s/sess-aaaa1111");
    wrapper.unmount();
  });

  it("renders operation run status and detail on session rows", async () => {
    listSessions.mockResolvedValue([
      session({
        session_id: "sess-waiting",
        operation_run: {
          operation_id: "bugfix_full",
          title: "Fix bug",
          status: "waiting",
          mode: "autonomous",
          execution_mode: "one-shot",
          phase: "run_regression",
          from: "idle",
          to: "bugfix.reproduce",
          stop_reason: "needs-human",
          stop_detail: "Regression gate was never RED.",
          terminal_state: "__exit__needs-human",
        },
      }),
      session({ session_id: "sess-plain" }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    const operations = wrapper.findAll("[data-testid='session-operation']");
    expect(operations).toHaveLength(2);
    expect(operations[0].attributes("data-operation-status")).toBe("waiting");
    expect(operations[0].find("[data-testid='session-operation-title']").text()).toBe("Fix bug");
    expect(operations[0].find("[data-testid='session-operation-status']").text()).toBe(
      "waiting for needs-human"
    );
    expect(operations[0].find("[data-testid='session-operation-detail']").text()).toContain(
      "Regression gate was never RED."
    );
    const summary = operations[0].find("[data-testid='session-operation-summary']");
    expect(summary.text()).toContain("mode autonomous");
    expect(summary.text()).toContain("execution one-shot");
    expect(summary.text()).toContain("phase run regression");
    expect(summary.text()).toContain("route idle -> bugfix.reproduce");
    expect(summary.text()).toContain("terminal __exit__needs-human");
    expect(summary.text()).toContain("stop needs-human");
    expect(operations[1].text()).toBe("—");
    wrapper.unmount();
  });

  it("renders a terminal artifact action on completed operation rows", async () => {
    markAutoNavDone();
    listSessions.mockResolvedValue([
      session({
        session_id: "sess-completed",
        terminal: true,
        operation_run: {
          operation_id: "bugfix_full",
          title: "Fix bug",
          status: "completed",
          terminal_state: "__exit__direct-ship",
          terminal_artifact: "artifacts/qa-report.md",
          terminal_artifact_handle: "qa-report#abc123",
        },
      }),
    ]);

    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    const artifact = wrapper.find("[data-testid='session-operation-artifact-open']");
    expect(artifact.exists()).toBe(true);
    expect(artifact.text()).toBe("Artifact");
    expect(artifact.attributes("href")).toBe("/artifact/qa-report%23abc123");
    expect(artifact.attributes("target")).toBe("_blank");
    expect(artifactUrl).toHaveBeenCalledWith("qa-report#abc123");
    wrapper.unmount();
  });

  it("drives a running operation from the session row and refreshes sessions", async () => {
    markAutoNavDone();
    const running = session({
      session_id: "sess-running",
      operation_run: {
        operation_id: "bugfix_full",
        title: "Fix bug",
        status: "running",
        mode: "supervised",
        run_in_background: true,
      },
    });
    const completed = session({
      session_id: "sess-running",
      terminal: true,
      operation_run: {
        operation_id: "bugfix_full",
        title: "Fix bug",
        status: "completed",
        terminal_state: "__exit__direct-ship",
      },
    });
    listSessions.mockResolvedValueOnce([running]).mockResolvedValueOnce([completed]);
    driveOperation.mockResolvedValue({
      mode: "transitioned",
      state: "__exit__direct-ship",
      turn_number: 4,
    });

    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='session-drive-operation']").trigger("click");
    await flushPromises();

    expect(driveOperation).toHaveBeenCalledWith("sess-running");
    expect(listSessions).toHaveBeenCalledTimes(2);
    expect(wrapper.find("[data-testid='session-operation-status']").text()).toBe("completed");
    expect(wrapper.find("[data-testid='session-drive-operation']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("does not show Drive for waiting operations", async () => {
    markAutoNavDone();
    listSessions.mockResolvedValue([
      session({
        session_id: "sess-waiting",
        operation_run: {
          operation_id: "bugfix_full",
          title: "Fix bug",
          status: "waiting",
        },
      }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='session-drive-operation']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("does not show Drive for interactive running operations", async () => {
    markAutoNavDone();
    listSessions.mockResolvedValue([
      session({
        session_id: "sess-interactive",
        operation_run: {
          operation_id: "guided_review",
          title: "Guided review",
          status: "running",
          mode: "interactive",
          run_in_background: true,
        },
      }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='session-operation-status']").text()).toBe("running in background");
    expect(wrapper.find("[data-testid='session-drive-operation']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("auto-navigates to the drive surface for a single live session", async () => {
    listSessions.mockResolvedValue([session({ session_id: "only-one" })]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(replace).toHaveBeenCalledWith("/s/only-one/chat");
    wrapper.unmount();
  });

  it("auto-navigates a single terminal session to the observer", async () => {
    listSessions.mockResolvedValue([
      session({ session_id: "only-done", terminal: true }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(replace).toHaveBeenCalledWith("/s/only-done");
    wrapper.unmount();
  });

  it("does NOT re-navigate on a reload after the first auto-nav (single session)", async () => {
    // Regression: editing the URL back to "/" triggers a hard reload. The
    // auto-nav guard must survive that reload (sessionStorage, not an in-memory
    // module flag) so the user lands on the stories list instead of being
    // bounced straight back into the one live session.
    listSessions.mockResolvedValue([session({ session_id: "only-one" })]);

    // First load of the tab → auto-nav fires once.
    const first = mount(HomeView, mountOpts);
    await flushPromises();
    expect(replace).toHaveBeenCalledWith("/s/only-one/chat");
    first.unmount();

    // A hard reload re-mounts HomeView from scratch (do NOT clear storage — the
    // tab is the same). The guard is already set, so we must stay on "/".
    replace.mockReset();
    const second = mount(HomeView, mountOpts);
    await flushPromises();
    expect(replace).not.toHaveBeenCalled();
    expect(second.find("[data-testid='home-view']").exists()).toBe(true);
    second.unmount();
  });

  it("does NOT auto-nav into the single session when a session view already spent the guard", async () => {
    // Regression: when a tab's FIRST mount is a session view (a pasted /s/:id
    // link, or the push right after starting a session), that view marks the
    // per-tab auto-nav guard spent (see lib/auto-nav + InteractiveView/RunView).
    // The user's first "← Stories" click then mounts HomeView with one live
    // session — and it must NOT bounce them straight back in.
    markAutoNavDone();
    listSessions.mockResolvedValue([session({ session_id: "only-one" })]);

    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(replace).not.toHaveBeenCalled();
    expect(wrapper.find("[data-testid='home-view']").exists()).toBe(true);
    wrapper.unmount();
  });

  it("renders a getting-started tour CTA on the empty-stories branch", async () => {
    listStories.mockResolvedValue([]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    const empty = wrapper.find("[data-testid='stories-empty']");
    expect(empty.exists()).toBe(true);
    // Helpful hint about where stories live, plus the CTA.
    expect(empty.text()).toContain("stories/");
    expect(wrapper.find("[data-testid='take-tour-btn']").exists()).toBe(true);
    wrapper.unmount();
  });

  it("Take the tour CTA calls the tour store's start()", async () => {
    listStories.mockResolvedValue([]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='take-tour-btn']").trigger("click");
    expect(tourStart).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("stays on / when there are multiple sessions", async () => {
    listSessions.mockResolvedValue([
      session({ session_id: "a" }),
      session({ session_id: "b" }),
    ]);
    const wrapper = mount(HomeView, mountOpts);
    await flushPromises();

    expect(replace).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});

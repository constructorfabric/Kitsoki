import { describe, it, expect, vi, beforeEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import StartBugfixView from "../../src/views/StartBugfixView.vue";

const routerReplace = vi.hoisted(() => vi.fn());
const routeQuery = vi.hoisted(() => ({
  id: "B-123",
  path: ".artifacts/issues/bugs/B-123.md",
  title: "Broken report toast",
}));
const liveSourceMock = vi.hoisted(() => ({
  listStories: vi.fn(),
  newSession: vi.fn(),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: routeQuery }),
  useRouter: () => ({ replace: routerReplace }),
}));

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => liveSourceMock),
}));

describe("StartBugfixView", () => {
  beforeEach(() => {
    routerReplace.mockReset();
    liveSourceMock.listStories.mockReset();
    liveSourceMock.newSession.mockReset();
    routeQuery.id = "B-123";
    routeQuery.path = ".artifacts/issues/bugs/B-123.md";
    routeQuery.title = "Broken report toast";
    delete (routeQuery as Record<string, unknown>).url;
  });

  it("creates a seeded bugfix session and routes to chat with a launch draft", async () => {
    liveSourceMock.listStories.mockResolvedValue([
      { app_id: "bugfix", path: "/repo/stories/bugfix/app.yaml", title: "Bugfix", active_sessions: [] },
    ]);
    liveSourceMock.newSession.mockResolvedValue("sess-1");

    mount(StartBugfixView);
    await flushPromises();

    expect(liveSourceMock.newSession).toHaveBeenCalledWith(
      "/repo/stories/bugfix/app.yaml",
      {
        initialWorld: {
          ticket_source_mode: "local",
          ticket_source_ref: ".artifacts/issues/bugs/B-123.md",
          ticket_url: "",
          ticket_repo: "",
          thread: ".artifacts/issues/bugs/B-123.md",
          oversight_mode: "no-gate",
          judge_mode: "human",
        },
      }
    );
    expect(routerReplace).toHaveBeenCalledWith({
      path: "/s/sess-1/chat",
      query: { draft: "work ticket B-123 titled Broken report toast" },
    });
  });
});

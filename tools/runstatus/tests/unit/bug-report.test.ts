/**
 * Component tests for the "Report bug" surface in MetaButton. The new flow is
 * capture → review modal → submit, so the launcher item now kicks off the
 * store's trigger() action (capture) rather than fire-and-forget filing. The
 * action is spied to drive states deterministically (no html2canvas, no RPC).
 * We assert the capturing toast, the post-submit filed toast, the error toast,
 * and that the launcher is absent in snapshot mode.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import MetaButton from "../../src/components/meta/MetaButton.vue";
import { setEmbeddedOverride } from "../../src/lib/embed.js";
import { useBugReportStore } from "../../src/stores/bugReport.js";

// MetaButton uses useRoute(); provide a minimal stub so mounting works without
// a real router.
vi.mock("vue-router", () => ({
  useRoute: () => ({ params: { sessionId: "pub-1" } }),
}));

const liveSourceMock = vi.hoisted(() => ({
  bugStatus: vi.fn(),
}));

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    metaModes: vi.fn().mockResolvedValue([]),
    bugStatus: liveSourceMock.bugStatus,
  })),
}));

function open(wrapper: ReturnType<typeof mount>) {
  return wrapper.get('[data-testid="meta-button"]').trigger("click");
}

describe("MetaButton — Report bug", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    liveSourceMock.bugStatus.mockReset();
    liveSourceMock.bugStatus.mockResolvedValue({
      mode: "local",
      can_file: true,
    });
    delete (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__;
  });
  afterEach(() => {
    delete (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__;
    setEmbeddedOverride(null);
  });

  it("clicking Report bug kicks off capture (trigger) and shows the capturing toast", async () => {
    const store = useBugReportStore();
    // Stub trigger() so it ends in the capturing state (modal would open on
    // reviewing; here we just assert capture started).
    const trigger = vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "capturing";
    });

    const wrapper = mount(MetaButton);
    await open(wrapper);
    await wrapper.get('[data-testid="meta-report-bug"]').trigger("click");
    await flushPromises();

    expect(trigger).toHaveBeenCalledTimes(1);
    expect(
      wrapper.get('[data-testid="bug-toast-capturing"]').exists()
    ).toBe(true);
  });

  it("Alt+click starts a placed bug report with target context", async () => {
    const store = useBugReportStore();
    const trigger = vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "reviewing";
    });
    const host = document.createElement("div");
    host.innerHTML = `<p data-testid="translated-label">Guardar cambios</p>`;
    document.body.appendChild(host);

    mount(MetaButton, { attachTo: document.body });
    host
      .querySelector('[data-testid="translated-label"]')!
      .dispatchEvent(
        new MouseEvent("click", {
          bubbles: true,
          cancelable: true,
          altKey: true,
          clientX: 42,
          clientY: 77,
        })
      );
    await flushPromises();

    expect(trigger).toHaveBeenCalledTimes(1);
    const opts = trigger.mock.calls[0][0];
    expect(opts.defaultTitle).toBe("Bug report at clicked location");
    expect(opts.placement).toMatchObject({
      x: 42,
      y: 77,
      selector: '[data-testid="translated-label"]',
      text: "Guardar cambios",
    });
  });

  it("Alt+click reports button text instead of ignoring or activating it", async () => {
    const store = useBugReportStore();
    const trigger = vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "reviewing";
    });
    const host = document.createElement("div");
    host.innerHTML = `<button data-testid="translated-button">Guardar cambios</button>`;
    document.body.appendChild(host);
    const button = host.querySelector(
      '[data-testid="translated-button"]'
    ) as HTMLButtonElement;
    const activated = vi.fn();
    button.addEventListener("click", activated);

    mount(MetaButton, { attachTo: document.body });
    button.dispatchEvent(
      new MouseEvent("click", {
        bubbles: true,
        cancelable: true,
        altKey: true,
        clientX: 42,
        clientY: 77,
      })
    );
    await flushPromises();

    expect(trigger).toHaveBeenCalledTimes(1);
    expect(activated).not.toHaveBeenCalled();
    expect(trigger.mock.calls[0][0].placement).toMatchObject({
      selector: '[data-testid="translated-button"]',
      text: "Guardar cambios",
    });
  });

  it("Alt+right-click shows a point menu before starting the report", async () => {
    const store = useBugReportStore();
    const trigger = vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "reviewing";
    });
    const host = document.createElement("div");
    host.innerHTML = `<p data-testid="translated-label">Guardar cambios</p>`;
    document.body.appendChild(host);

    const wrapper = mount(MetaButton, { attachTo: document.body });
    host
      .querySelector('[data-testid="translated-label"]')!
      .dispatchEvent(
        new MouseEvent("contextmenu", {
          bubbles: true,
          cancelable: true,
          altKey: true,
          clientX: 42,
          clientY: 77,
        })
      );
    await flushPromises();

    expect(trigger).not.toHaveBeenCalled();
    const item = document.querySelector('[data-testid="bug-point-menu-report"]');
    expect(item).not.toBeNull();
    expect(item?.textContent).toContain("Report bug here");

    await wrapper.get('[data-testid="bug-point-menu-report"]').trigger("click");
    await flushPromises();

    expect(trigger).toHaveBeenCalledTimes(1);
    expect(trigger.mock.calls[0][0].placement).toMatchObject({
      x: 42,
      y: 77,
      selector: '[data-testid="translated-label"]',
      text: "Guardar cambios",
    });
  });

  it("ignores Alt+click inside editable fields", async () => {
    const store = useBugReportStore();
    const trigger = vi.spyOn(store, "trigger").mockResolvedValue();
    const input = document.createElement("input");
    document.body.appendChild(input);

    mount(MetaButton, { attachTo: document.body });
    input.dispatchEvent(
      new MouseEvent("click", {
        bubbles: true,
        cancelable: true,
        altKey: true,
        clientX: 5,
        clientY: 6,
      })
    );
    await flushPromises();

    expect(trigger).not.toHaveBeenCalled();
  });

  it("shows the filed toast with the path after a successful submit", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockResolvedValue();
    store.title = "Foyer button does nothing";
    // Simulate the post-submit filed state the modal would produce.
    store.filed = {
      id: "2026-06-12T130405Z-foyer-button-does-nothing",
      path: "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md",
    };
    store.status = "filed";

    const wrapper = mount(MetaButton);
    await flushPromises();

    expect(wrapper.get('[data-testid="bug-toast-path"]').text()).toContain(
      "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md"
    );
    const open = wrapper.get('[data-testid="bug-toast-open"]');
    expect(open.exists()).toBe(true);
    expect(open.attributes("title")).toBe("Open the filed bug");
    const fix = wrapper.get('[data-testid="bug-toast-bugfix"]');
    expect(fix.attributes("target")).toBe("_blank");
    const href = fix.attributes("href") ?? "";
    expect(href).toContain("#/start/bugfix?");
    const params = new URLSearchParams(href.split("?")[1] ?? "");
    expect(params.get("id")).toBe("2026-06-12T130405Z-foyer-button-does-nothing");
    expect(params.get("path")).toBe(
      "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md"
    );
    expect(params.get("title")).toBe("Foyer button does nothing");
  });

  it("shows privacy feedback in the filed toast", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockResolvedValue();
    store.filed = {
      id: "2026-06-12T130405Z-foyer-button-does-nothing",
      path: "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md",
      privacy: {
        status: "passed_with_substitutions",
        message: "deterministic substitutions applied",
        follow_up_path:
          "issues/bugs/2026-06-12T130405Z-privacy-strip-sensitive-bug-report-data-high-entropy.md",
      },
    };
    store.status = "filed";

    const wrapper = mount(MetaButton);
    await flushPromises();

    const privacy = wrapper.get('[data-testid="bug-toast-privacy"]').text();
    expect(privacy).toContain("deterministic substitutions applied");
    expect(privacy).toContain("privacy-strip-sensitive-bug-report-data");
  });

  it("opens the filed bug path when the filed toast open action is clicked", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockResolvedValue();
    store.filed = {
      id: "2026-06-12T130405Z-foyer-button-does-nothing",
      path: "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md",
    };
    store.status = "filed";
    const openSpy = vi
      .spyOn(window, "open")
      .mockImplementation(() => null);

    const wrapper = mount(MetaButton);
    await wrapper.get('[data-testid="bug-toast-open"]').trigger("click");
    await flushPromises();

    expect(openSpy).toHaveBeenCalledWith(
      "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md",
      "_blank"
    );
  });

  it("surfaces an error state in the toast when filing fails", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "error";
      store.error = "write failed";
    });

    const wrapper = mount(MetaButton);
    await open(wrapper);
    await wrapper.get('[data-testid="meta-report-bug"]').trigger("click");
    await flushPromises();

    expect(wrapper.get('[data-testid="bug-toast-error"]').text()).toContain(
      "write failed"
    );
  });

  it("warns and disables Report bug when GitHub auth is missing", async () => {
    liveSourceMock.bugStatus.mockResolvedValue({
      mode: "github",
      repo: "o/r",
      can_file: false,
      github_auth_configured: false,
      warning:
        "Report bug cannot file to GitHub repo o/r because GitHub auth is missing. Filing bugs is critical; run `kitsoki gh-agent login` or set GH_TOKEN/GITHUB_TOKEN.",
      setup_hint: "GitHub auth is not configured",
    });
    const store = useBugReportStore();
    const trigger = vi.spyOn(store, "trigger").mockResolvedValue();

    const wrapper = mount(MetaButton);
    await flushPromises();
    expect(
      wrapper.get('[data-testid="meta-status-bug-unavailable"]').attributes("title")
    ).toContain("GitHub auth is missing");
    await open(wrapper);

    expect(wrapper.get('[data-testid="bug-report-warning"]').text()).toContain(
      "Filing bugs is critical"
    );
    const item = wrapper.get('[data-testid="meta-report-bug"]');
    expect(item.attributes("disabled")).toBeDefined();
    expect(item.text()).toContain("Fix GitHub auth");
    expect(trigger).not.toHaveBeenCalled();
  });

  it("hides the Report bug item (whole launcher) in snapshot mode", () => {
    (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__ = {};
    const wrapper = mount(MetaButton);
    expect(wrapper.find('[data-testid="meta-launcher"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="meta-report-bug"]').exists()).toBe(false);
  });

  it("keeps the normal web launcher floating", () => {
    setEmbeddedOverride(false);
    const wrapper = mount(MetaButton);
    const launcher = wrapper.get('[data-testid="meta-launcher"]');
    expect(launcher.attributes("data-placement")).toBe("floating");
    expect(launcher.classes()).toContain("meta-launcher--floating");
  });

  it("suppresses the global floating launcher inside the VS Code embed", () => {
    setEmbeddedOverride(true);
    const wrapper = mount(MetaButton);
    expect(wrapper.find('[data-testid="meta-launcher"]').exists()).toBe(false);
  });

  it("allows the VS Code embed to render the topbar launcher variant", () => {
    setEmbeddedOverride(true);
    const wrapper = mount(MetaButton, { props: { placement: "topbar" } });
    const launcher = wrapper.get('[data-testid="meta-launcher"]');
    expect(launcher.attributes("data-placement")).toBe("topbar");
    expect(launcher.classes()).toContain("meta-launcher--topbar");
  });
});

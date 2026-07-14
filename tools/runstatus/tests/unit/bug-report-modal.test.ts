/**
 * Component tests for BugReportModal — the review-before-file surface. We drive
 * the bugReport store to "reviewing" with a stubbed source (bugPreview resolves
 * a held capture + HAR; reportBug resolves an issue id/path) and injected
 * capture deps (no rrweb, no network). Then we mount the modal
 * and assert: HAR summary rows render, the raw-HAR toggle reveals raw JSON,
 * typing a description + clicking submit files with description + console_logs +
 * error_info, and cancel returns the store to idle.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import BugReportModal from "../../src/components/BugReportModal.vue";
import { useBugReportStore } from "../../src/stores/bugReport.js";
import type { BugSource } from "../../src/stores/bugReport.js";

function stubSource() {
  return {
    bugPreview: vi.fn().mockResolvedValue({
      capture_id: "cap-123-0",
      har: {
        log: {
          entries: [
            {
              request: { method: "POST", url: "/rpc" },
              response: { status: 200 },
            },
            {
              request: { method: "GET", url: "/rpc/events" },
              response: { status: 500 },
            },
          ],
        },
      },
      depth: 2,
      capacity: 16,
    }),
    reportBug: vi
      .fn()
      .mockResolvedValue({ id: "id-1", path: "issues/bugs/id-1.md" }),
    lastRpcError: vi.fn().mockReturnValue(null),
  };
}

const deps = {
  snapshotEvents: () => [{ type: 2 }, { type: 3 }],
  snapshotHar: () => ({
    log: {
      version: "1.2",
      creator: { name: "test-browser", version: "1" },
      entries: [
        {
          startedDateTime: "2026-07-08T00:00:00Z",
          time: 4,
          request: { method: "GET", url: "https://app.test/real-api" },
          response: { status: 503 },
        },
      ],
    },
  }),
  recentConsole: () => [{ level: "warn", ts: 1, text: "heads up" }],
  gatherErrorInfo: () => ({
    errors: [{ message: "boom" }],
    last_rpc: { method: "x", code: 1, message: "y" },
  }),
};

async function toReviewing(source: ReturnType<typeof stubSource>) {
  const store = useBugReportStore();
  await store.trigger({
    source: source as unknown as BugSource,
    defaultTitle: "Bug report",
    deps,
  });
  return store;
}

async function toReviewingWithPlacement(source: ReturnType<typeof stubSource>) {
  const store = useBugReportStore();
  await store.trigger({
    source: source as unknown as BugSource,
    defaultTitle: "Bug report at clicked location",
    placement: {
      x: 12,
      y: 34,
      selector: '[data-testid="translated-label"]',
      text: "Guardar cambios",
      route: "/#/s/pub-1/chat",
    },
    deps,
  });
  return store;
}

describe("BugReportModal", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    document.body.innerHTML = "";
  });

  it("renders nothing until the store is reviewing", () => {
    mount(BugReportModal);
    expect(document.querySelector('[data-testid="bug-modal"]')).toBeNull();
  });

  it("renders HAR summary rows and toggles raw HAR", async () => {
    const source = stubSource();
    await toReviewing(source);
    mount(BugReportModal);
    await flushPromises();

    expect(document.querySelector('[data-testid="bug-modal"]')).not.toBeNull();
    const rows = document.querySelectorAll('[data-testid="bug-modal-har-row"]');
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toContain("POST");
    expect(rows[0].textContent).toContain("/rpc");
    expect(source.bugPreview).toHaveBeenCalledWith({
      har_json: JSON.stringify(deps.snapshotHar()),
    });

    expect(document.querySelector('[data-testid="bug-modal-har-raw"]')).toBeNull();
    (
      document.querySelector(
        '[data-testid="bug-modal-har-raw-toggle"]'
      ) as HTMLButtonElement
    ).click();
    await flushPromises();
    const raw = document.querySelector('[data-testid="bug-modal-har-raw"]');
    expect(raw).not.toBeNull();
    expect(raw!.textContent).toContain("/rpc/events");
  });

  it("keeps the review modal visible while the privacy check is running", async () => {
    const source = stubSource();
    let resolveReport!: (value: { id: string; path: string }) => void;
    source.reportBug.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveReport = resolve;
        })
    );
    const store = await toReviewing(source);
    mount(BugReportModal);
    await flushPromises();

    (
      document.querySelector(
        '[data-testid="bug-modal-submit"]'
      ) as HTMLButtonElement
    ).click();
    await flushPromises();

    expect(store.status).toBe("submitting");
    expect(document.querySelector('[data-testid="bug-modal"]')).not.toBeNull();
    const submit = document.querySelector(
      '[data-testid="bug-modal-submit"]'
    ) as HTMLButtonElement;
    const cancel = document.querySelector(
      '[data-testid="bug-modal-cancel"]'
    ) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(cancel.disabled).toBe(true);
    expect(submit.textContent).toContain("Checking privacy");
    expect(
      document.querySelector('[data-testid="bug-modal-submit-status"]')!
        .textContent
    ).toContain("Checking privacy before filing");

    resolveReport({ id: "id-1", path: "issues/bugs/id-1.md" });
    await flushPromises();
    expect(store.status).toBe("filed");
  });

  it("submit files the held capture with description, console_logs, error_info", async () => {
    const source = stubSource();
    const store = await toReviewing(source);
    mount(BugReportModal);
    await flushPromises();

    const desc = document.querySelector(
      '[data-testid="bug-modal-description"]'
    ) as HTMLTextAreaElement;
    desc.value = "the foyer button does nothing";
    desc.dispatchEvent(new Event("input"));
    await flushPromises();

    (
      document.querySelector(
        '[data-testid="bug-modal-submit"]'
      ) as HTMLButtonElement
    ).click();
    await flushPromises();

    expect(source.reportBug).toHaveBeenCalledTimes(1);
    const params = source.reportBug.mock.calls[0][0];
    expect(params.capture_id).toBe("cap-123-0");
    expect(params.description).toBe("the foyer button does nothing");
    expect(params.screenshot_png_b64).toBeUndefined();
    expect(typeof params.console_logs).toBe("string");
    expect(JSON.parse(params.console_logs)[0].text).toBe("heads up");
    expect(typeof params.error_info).toBe("string");
    expect(JSON.parse(params.error_info).errors[0].message).toBe("boom");
    expect(typeof params.rrweb_events).toBe("string");

    expect(store.status).toBe("filed");
    expect(store.filed?.path).toBe("issues/bugs/id-1.md");
  });

  it("shows clicked placement context and submits it in the reviewed description", async () => {
    const source = stubSource();
    await toReviewingWithPlacement(source);
    mount(BugReportModal);
    await flushPromises();

    expect(
      document.querySelector('[data-testid="bug-modal-placement-target"]')!
        .textContent
    ).toContain('[data-testid="translated-label"]');
    expect(
      document.querySelector('[data-testid="bug-modal-placement-point"]')!
        .textContent
    ).toContain("12");
    expect(
      (
        document.querySelector(
          '[data-testid="bug-modal-description"]'
        ) as HTMLTextAreaElement
      ).value
    ).toContain("Clicked location:");

    (
      document.querySelector(
        '[data-testid="bug-modal-submit"]'
      ) as HTMLButtonElement
    ).click();
    await flushPromises();

    const params = source.reportBug.mock.calls[0][0];
    expect(params.description).toContain("Clicked location:");
    expect(params.description).toContain("Guardar cambios");
  });

  it("cancel discards and returns to idle", async () => {
    const source = stubSource();
    const store = await toReviewing(source);
    mount(BugReportModal);
    await flushPromises();

    (
      document.querySelector(
        '[data-testid="bug-modal-cancel"]'
      ) as HTMLButtonElement
    ).click();
    await flushPromises();
    expect(store.status).toBe("idle");
    expect(source.reportBug).not.toHaveBeenCalled();
  });
});

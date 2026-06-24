import { mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import InboxBadge from "../../src/components/InboxBadge.vue";
import { useInboxStore } from "../../src/stores/inbox.js";
import { useProposalsStore } from "../../src/stores/proposals.js";

describe("InboxBadge", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("surfaces active work even when there are no unread notifications", () => {
    const inbox = useInboxStore();
    inbox.workSummary = {
      items: 2,
      needs_attention: 1,
      jobs_running: 0,
      jobs_awaiting_input: 1,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 1,
      backgrounded_chats: 0,
    };

    const wrapper = mount(InboxBadge);

    expect(wrapper.get('[data-testid="inbox-badge-count"]').text()).toBe("2");
    expect(wrapper.get('[data-testid="inbox-badge"]').attributes("data-active-work")).toBe("2");
    expect(wrapper.get('[data-testid="inbox-badge"]').attributes("data-needs-attention")).toBe("true");
    expect(wrapper.get('[data-testid="inbox-badge"]').classes()).toContain("inbox-badge--attention");
  });

  it("includes queued proposal review work in the global count and attention", () => {
    const inbox = useInboxStore();
    const proposals = useProposalsStore();
    inbox.workSummary = {
      items: 1,
      needs_attention: 0,
      jobs_running: 1,
      jobs_awaiting_input: 0,
      jobs_terminal: 0,
      notifications_unread: 0,
      notifications_action_required: 0,
      pending_drives: 0,
      backgrounded_chats: 0,
    };
    proposals.push({
      id: "proposal-1",
      kind: "write_mode",
      title: "Allow edit",
    });

    const wrapper = mount(InboxBadge);

    expect(wrapper.get('[data-testid="inbox-badge-count"]').text()).toBe("2");
    expect(wrapper.get('[data-testid="inbox-badge"]').attributes("data-active-work")).toBe("1");
    expect(wrapper.get('[data-testid="inbox-badge"]').attributes("data-proposals")).toBe("1");
    expect(wrapper.get('[data-testid="inbox-badge"]').attributes("data-needs-attention")).toBe("true");
    expect(wrapper.get('[data-testid="inbox-badge"]').classes()).toContain("inbox-badge--attention");
  });
});

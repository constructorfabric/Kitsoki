/**
 * Component tests for OperatorQuestionModal. The modal renders the active
 * forwarded question (head of the store queue), enforces a selection before the
 * submit button enables, and on submit calls store.answer with the answer map
 * keyed by question text. No live server / LLM — the store's answer action is
 * spied. The modal Teleports to <body>, so assertions query document.body.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import OperatorQuestionModal from "../../src/components/OperatorQuestionModal.vue";
import { useOperatorQuestionStore } from "../../src/stores/operatorQuestions.js";
import type { OperatorQuestionFrame } from "../../src/data/live-source.js";

function singleFrame(): OperatorQuestionFrame {
  return {
    session_id: "pub-1",
    question_id: "q-1",
    questions: [
      {
        question: "Ship it?",
        header: "Ship",
        options: [
          { label: "Yes", description: "go now" },
          { label: "No" },
        ],
      },
    ],
  };
}

function multiFrame(): OperatorQuestionFrame {
  return {
    session_id: "pub-1",
    question_id: "q-2",
    questions: [
      {
        question: "Which?",
        header: "Pick",
        multiSelect: true,
        options: [{ label: "A" }, { label: "B" }, { label: "C" }],
      },
    ],
  };
}

describe("OperatorQuestionModal", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    document.body.innerHTML = "";
  });

  it("renders nothing when the queue is empty", () => {
    mount(OperatorQuestionModal);
    expect(document.querySelector('[data-testid="operator-question-modal"]')).toBeNull();
  });

  it("single-select: submit is gated until an option is picked, then answers by label", async () => {
    const store = useOperatorQuestionStore();
    store.queue.push(singleFrame());
    const answer = vi.spyOn(store, "answer").mockResolvedValue();

    mount(OperatorQuestionModal);
    const submit = document.querySelector('[data-testid="oq-submit"]') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);

    (document.querySelector('[data-testid="oq-option-0-0"]') as HTMLButtonElement).click();
    await Promise.resolve();
    expect(submit.disabled).toBe(false);

    submit.click();
    expect(answer).toHaveBeenCalledWith({ "Ship it?": "Yes" });
  });

  it("single-select: custom answer is gated until text is entered, then submits the text", async () => {
    const store = useOperatorQuestionStore();
    store.queue.push(singleFrame());
    const answer = vi.spyOn(store, "answer").mockResolvedValue();

    mount(OperatorQuestionModal);
    const submit = document.querySelector('[data-testid="oq-submit"]') as HTMLButtonElement;
    (document.querySelector('[data-testid="oq-option-0-custom"]') as HTMLButtonElement).click();
    await Promise.resolve();

    expect(submit.disabled).toBe(true);
    const input = document.querySelector('[data-testid="oq-custom-answer-0"]') as HTMLInputElement;
    input.value = "Use the canary branch after docs are updated";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await Promise.resolve();

    expect(submit.disabled).toBe(false);
    submit.click();
    expect(answer).toHaveBeenCalledWith({
      "Ship it?": "Use the canary branch after docs are updated",
    });
  });

  it("multi-select: toggles labels into an array and submits the list", async () => {
    const store = useOperatorQuestionStore();
    store.queue.push(multiFrame());
    const answer = vi.spyOn(store, "answer").mockResolvedValue();

    mount(OperatorQuestionModal);
    (document.querySelector('[data-testid="oq-option-0-0"]') as HTMLButtonElement).click();
    (document.querySelector('[data-testid="oq-option-0-2"]') as HTMLButtonElement).click();
    await Promise.resolve();

    (document.querySelector('[data-testid="oq-submit"]') as HTMLButtonElement).click();
    expect(answer).toHaveBeenCalledWith({ "Which?": ["A", "C"] });
  });

  it("shows a queued-count hint when more than one question is pending", () => {
    const store = useOperatorQuestionStore();
    store.queue.push(singleFrame(), multiFrame());
    mount(OperatorQuestionModal);
    expect(document.querySelector(".oq-more")?.textContent).toContain("+1 more queued");
  });
});

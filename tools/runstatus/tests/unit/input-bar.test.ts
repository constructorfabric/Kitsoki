import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import InputBar from "../../src/components/InputBar.vue";
import type { IntentInfo } from "../../src/types.js";

const startIntent: IntentInfo = { name: "start", title: "Start", has_slots: false };
const confirmIntent: IntentInfo = {
  name: "confirm",
  title: "Confirm",
  has_slots: false,
};
const discussIntent: IntentInfo = {
  name: "discuss",
  title: "Discuss",
  text_slot: "message",
  has_slots: true,
};
const submitIntent: IntentInfo = {
  name: "submit_answers",
  title: "Submit",
  text_slot: "answer",
  has_slots: true,
};
const answerIntent: IntentInfo = {
  name: "answer",
  title: "Answer",
  text_slot: "text",
  has_slots: true,
};

describe("InputBar", () => {
  it("renders a button for each no-slot intent", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, confirmIntent, discussIntent] },
    });
    const buttons = wrapper.findAll(".input-bar__action-btn");
    expect(buttons.length).toBe(2);
    expect(buttons.map((b) => b.text())).toEqual(["Start", "Confirm"]);
    wrapper.unmount();
  });

  it("does not render an action button for slotted/text intents", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent] },
    });
    expect(wrapper.findAll(".input-bar__action-btn").length).toBe(0);
    expect(wrapper.find(".input-bar__composer").exists()).toBe(true);
    wrapper.unmount();
  });

  it("emits 'intent' with empty slots when a no-slot button is clicked", async () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, confirmIntent] },
    });
    await wrapper.findAll(".input-bar__action-btn")[1].trigger("click");
    const ev = wrapper.emitted("intent");
    expect(ev).toBeTruthy();
    expect(ev![0]).toEqual(["confirm", {}]);
    wrapper.unmount();
  });

  it("Send emits 'intent'(name, {slot: text}) for the first text_slot intent (no 'send' — text intents use session.submit, not session.turn)", async () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, discussIntent] },
    });
    await wrapper.find(".input-bar__textarea").setValue("hello there");
    await wrapper.find(".input-bar__composer").trigger("submit");

    // Text-slot intents submit via 'intent' (→ session.submit / SubmitDirect).
    // 'send' (→ session.turn / semantic router) is only emitted by the semantic textarea.
    expect(wrapper.emitted("send")).toBeFalsy();

    const intentEv = wrapper.emitted("intent");
    expect(intentEv).toBeTruthy();
    expect(intentEv![0]).toEqual(["discuss", { message: "hello there" }]);
    wrapper.unmount();
  });

  it("binds Send to the FIRST text_slot intent when multiple exist, and shows a selector", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent, submitIntent] },
    });
    expect(wrapper.find(".input-bar__select").exists()).toBe(true);
    const opts = wrapper.findAll(".input-bar__select option").map((o) => o.text());
    expect(opts).toEqual(["Discuss", "Submit"]);
    wrapper.unmount();
  });

  it("defaults the composer to the room's default_intent (not the first text intent), so a typed reply routes to the sink", async () => {
    // Regression: PRD clarifying lists submit_answers/regenerate/answer as
    // text intents; without honoring default_intent the composer bound typed
    // prose to whichever sorted first and a single answer skipped to brief.
    const wrapper = mount(InputBar, {
      props: {
        intents: [submitIntent, answerIntent],
        defaultIntent: "answer",
      },
    });
    // The selector shows submit first, but `answer` is the active selection.
    const select = wrapper.find(".input-bar__select")
      .element as HTMLSelectElement;
    expect(select.value).toBe("answer");

    await wrapper.find(".input-bar__textarea").setValue("ephemeral, resets each reload");
    await wrapper.find(".input-bar__composer").trigger("submit");
    const intentEv = wrapper.emitted("intent");
    expect(intentEv).toBeTruthy();
    expect(intentEv![0]).toEqual(["answer", { text: "ephemeral, resets each reload" }]);
    wrapper.unmount();
  });

  it("falls back to the first text intent when default_intent is absent or not a text intent", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [submitIntent, answerIntent], defaultIntent: "skip" },
    });
    const select = wrapper.find(".input-bar__select")
      .element as HTMLSelectElement;
    expect(select.value).toBe("submit_answers");
    wrapper.unmount();
  });

  it("hides the selector when only one text_slot intent exists", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent] },
    });
    expect(wrapper.find(".input-bar__select").exists()).toBe(false);
    wrapper.unmount();
  });

  it("disables actions and composer while pending", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, discussIntent], pending: true },
    });
    expect(
      (wrapper.find(".input-bar__action-btn").element as HTMLButtonElement).disabled,
    ).toBe(true);
    expect(
      (wrapper.find(".input-bar__textarea").element as HTMLTextAreaElement).disabled,
    ).toBe(true);
    wrapper.unmount();
  });

  it("does not emit on submit when the input is empty", async () => {
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent] },
    });
    await wrapper.find(".input-bar__composer").trigger("submit");
    expect(wrapper.emitted("send")).toBeFalsy();
    wrapper.unmount();
  });

  it("shows a semantic textarea and emits 'send' (raw text) when typedView has elements but no choice/form", async () => {
    const semanticView = {
      Elements: [
        { Kind: "prose" as const, Source: "Say what you want to do." },
        { Kind: "list" as const, Items: [{ Label: "tickets" }, { Label: "drive" }] },
      ],
    };
    // No-slot intents only (nav intents like main room).
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, confirmIntent], typedView: semanticView },
    });
    // No action buttons — semantic textarea takes over.
    expect(wrapper.findAll(".input-bar__action-btn").length).toBe(0);
    expect(wrapper.find(".input-bar__textarea").exists()).toBe(true);

    await wrapper.find(".input-bar__textarea").setValue("find auth bugs");
    await wrapper.find(".input-bar__composer--semantic").trigger("submit");

    const sendEv = wrapper.emitted("send");
    expect(sendEv).toBeTruthy();
    expect(sendEv![0]).toEqual(["find auth bugs", ""]);
    wrapper.unmount();
  });

  it("does not show semantic textarea when typedView is absent (legacy path)", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent, confirmIntent] },
    });
    expect(wrapper.find(".input-bar__textarea").exists()).toBe(false);
    expect(wrapper.findAll(".input-bar__action-btn").length).toBe(2);
    wrapper.unmount();
  });

  // ── Choice-element mutual-exclusion invariants ────────────────────────────────
  // These guard against a class of regression where adding a choice: element to
  // a room's view inadvertently hides the primary text input or breaks navigation.

  it("choice items present: text-slot composer is hidden even when text-slot intents exist", () => {
    // This is the exact regression that was introduced: design room had
    // discuss + capture_existing as text-slot intents, a choice: element was
    // added to the view, and the text-slot composer silently disappeared.
    const captureIntent: IntentInfo = {
      name: "capture_existing",
      title: "Reference Docs",
      text_slot: "paths",
      has_slots: true,
    };
    const viewWithChoice = {
      Elements: [
        {
          Kind: "choice" as const,
          ChoiceMode: "single",
          ChoicePrompt: "Actions",
          ChoiceItems: [
            { Label: "capture_existing", Intent: "capture_existing", Param: { Slot: "paths", Type: "string" } },
            { Label: "quit", Intent: "quit" },
          ],
        },
      ],
    };
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent, captureIntent], typedView: viewWithChoice },
    });
    // Choice items are shown.
    expect(wrapper.find(".input-bar__forms").exists()).toBe(true);
    // Text-slot composer must NOT be shown — discuss has nowhere to go.
    expect(wrapper.find('[data-testid="composer"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("choice items present: semantic textarea is also hidden (choice is the only input)", () => {
    const viewWithChoice = {
      Elements: [
        {
          Kind: "choice" as const,
          ChoiceMode: "single",
          ChoicePrompt: "Actions",
          ChoiceItems: [{ Label: "quit", Intent: "quit" }],
        },
      ],
    };
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent], typedView: viewWithChoice },
    });
    expect(wrapper.find('[data-testid="composer"]').exists()).toBe(false);
    expect(wrapper.find(".input-bar__composer--semantic").exists()).toBe(false);
    // But the choice buttons are shown.
    expect(wrapper.find('[data-testid="intent-actions"]').exists()).toBe(true);
    wrapper.unmount();
  });

  // ── Free-text floor ───────────────────────────────────────────────────────────
  // The text-only contract (transports.md §7) requires every room to be
  // drivable by typing. A choice/form widget would otherwise hide all free text,
  // so we render a de-emphasized free-text floor below it that routes via
  // session.turn (semantic router → off-ramp) — emitted as 'send', distinct from
  // the labeled-intent composer's 'intent'/session.submit.

  const choiceOnlyView = {
    Elements: [
      {
        Kind: "choice" as const,
        ChoiceMode: "single",
        ChoicePrompt: "Actions",
        ChoiceItems: [
          { Label: "Start", Intent: "start" },
          { Label: "quit", Intent: "quit" },
        ],
      },
    ],
  };

  const formOnlyView = {
    Elements: [
      {
        Kind: "choice" as const,
        ChoiceMode: "form",
        ChoicePrompt: "Enter values",
        ChoiceIntent: "submit_values",
        ChoiceFields: [{ Name: "qty", Type: "int" }],
      },
    ],
  };

  it("choice present: a free-text floor is shown and emits 'send' (raw text via session.turn)", async () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent], typedView: choiceOnlyView },
    });
    // Choice buttons are the primary affordance…
    expect(wrapper.find('[data-testid="intent-actions"]').exists()).toBe(true);
    // …and the floor is present beneath them.
    const floor = wrapper.find('[data-testid="text-floor"]');
    expect(floor.exists()).toBe(true);

    await wrapper.find('[data-testid="text-floor-input"]').setValue("something off-menu");
    await floor.trigger("submit");

    const sendEv = wrapper.emitted("send");
    expect(sendEv).toBeTruthy();
    expect(sendEv![0]).toEqual(["something off-menu", ""]);
    // The floor never fires a labeled intent — that path is for the widget.
    expect(wrapper.emitted("intent")).toBeFalsy();
    wrapper.unmount();
  });

  it("applies an initial raw draft to the free-text floor", () => {
    const wrapper = mount(InputBar, {
      props: {
        intents: [startIntent],
        typedView: choiceOnlyView,
        initialRawDraft: "work ticket B-123 titled Broken toast",
      },
      attachTo: document.body,
    });
    const input = wrapper.find('[data-testid="text-floor-input"]')
      .element as HTMLTextAreaElement;
    expect(input.value).toBe("work ticket B-123 titled Broken toast");
    wrapper.unmount();
  });

  it("keeps an initial raw draft when the choice view arrives after mount", async () => {
    const wrapper = mount(InputBar, {
      props: {
        intents: [],
        initialRawDraft: "work ticket B-123 titled Broken toast",
      },
      attachTo: document.body,
    });
    expect(wrapper.find('[data-testid="text-floor-input"]').exists()).toBe(false);
    await wrapper.setProps({ intents: [startIntent], typedView: choiceOnlyView });
    const input = wrapper.find('[data-testid="text-floor-input"]')
      .element as HTMLTextAreaElement;
    expect(input.value).toBe("work ticket B-123 titled Broken toast");
    wrapper.unmount();
  });

  it("warning choices recommend the first action and typed action prefixes beat raw search", async () => {
    const warningChoiceView = {
      Elements: [
        {
          Kind: "banner" as const,
          Source: "Session warning",
          Color: "blue",
        },
        {
          Kind: "choice" as const,
          ChoiceMode: "single",
          ChoicePrompt: "Recommended actions",
          ChoiceItems: [
            { Label: "Reload story", Intent: "reload_story" },
            { Label: "Dismiss warning", Intent: "dismiss_warning" },
          ],
        },
      ],
    };
    const wrapper = mount(InputBar, {
      props: { intents: [], typedView: warningChoiceView },
    });

    const first = wrapper.find('[data-testid="intent-btn-reload_story"]');
    expect(first.classes()).toContain("input-bar__action-btn--warning-default");
    expect(first.text()).toContain("recommended");

    const input = wrapper.find<HTMLInputElement>('[data-testid="text-floor-input"]');
    expect(input.attributes("placeholder")).toContain("Recommended: Reload story");

    await input.trigger("keydown", { key: "Tab" });
    expect((input.element as HTMLTextAreaElement).value).toBe("Reload story");

    await input.setValue("");
    await wrapper.find('[data-testid="text-floor"]').trigger("submit");
    expect(wrapper.emitted("intent")![0]).toEqual([
      "reload_story",
      {},
      "Reload story",
    ]);

    await input.setValue("dismiss");
    await wrapper.find('[data-testid="text-floor"]').trigger("submit");
    expect(wrapper.emitted("intent")![1]).toEqual([
      "dismiss_warning",
      {},
      "Dismiss warning",
    ]);
    expect(wrapper.emitted("send")).toBeFalsy();
    wrapper.unmount();
  });

  it("choice actions can be manually collapsed and restored at normal height", async () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent], typedView: choiceOnlyView },
    });

    expect(wrapper.find('[data-testid="intent-actions"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="text-floor"]').exists()).toBe(true);

    await wrapper.find('[data-testid="input-collapse"]').trigger("click");

    expect(wrapper.classes()).toContain("input-bar--collapsed");
    expect(wrapper.find('[data-testid="intent-actions"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="text-floor"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="composer"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="input-disclose"]').exists()).toBe(true);

    await wrapper.find('[data-testid="input-disclose"]').trigger("click");

    expect(wrapper.classes()).not.toContain("input-bar--collapsed");
    expect(wrapper.find('[data-testid="intent-actions"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="text-floor"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("form present: a free-text floor is shown too", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [], typedView: formOnlyView },
    });
    expect(wrapper.find(".input-bar__form-grid").exists()).toBe(true);
    expect(wrapper.find('[data-testid="text-floor"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("no floor on semantic or text-slot rooms — they already expose a text box", () => {
    const semanticView = {
      Elements: [{ Kind: "prose" as const, Source: "Say what you want." }],
    };
    const semantic = mount(InputBar, {
      props: { intents: [startIntent], typedView: semanticView },
    });
    expect(semantic.find('[data-testid="text-floor"]').exists()).toBe(false);
    semantic.unmount();

    const textSlot = mount(InputBar, { props: { intents: [discussIntent] } });
    expect(textSlot.find('[data-testid="text-floor"]').exists()).toBe(false);
    textSlot.unmount();
  });

  it("the floor is disabled while a turn is pending", () => {
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent], typedView: choiceOnlyView, pending: true },
    });
    const ta = wrapper.find('[data-testid="text-floor-input"]').element as HTMLTextAreaElement;
    expect(ta.disabled).toBe(true);
    wrapper.unmount();
  });

  it("a floor draft survives a widget→semantic switch (shared rawDraft)", async () => {
    // The TUI's /input restores a prior draft across the widget↔text toggle;
    // the floor shares rawDraft with the semantic composer so a half-typed
    // message is not lost when the room's view changes.
    const semanticView = {
      Elements: [{ Kind: "prose" as const, Source: "Say what you want." }],
    };
    const wrapper = mount(InputBar, {
      props: { intents: [startIntent], typedView: choiceOnlyView },
    });
    await wrapper.find('[data-testid="text-floor-input"]').setValue("draft in progress");
    // Room transitions to a semantic view (choice gone).
    await wrapper.setProps({ typedView: semanticView });
    const composer = wrapper.find('[data-testid="composer-input"]').element as HTMLTextAreaElement;
    expect(composer.value).toBe("draft in progress");
    wrapper.unmount();
  });

  it("when two text-slot intents exist, dropdown defaults to the first one listed", () => {
    // Guards the priority-ordering fix: discuss must be first so it is the
    // default selection, not capture_existing ("Reference Docs").
    const captureIntent: IntentInfo = {
      name: "capture_existing",
      title: "Reference Docs",
      text_slot: "paths",
      has_slots: true,
    };
    // discuss first → should be selected by default.
    const wrapper = mount(InputBar, {
      props: { intents: [discussIntent, captureIntent] },
    });
    const select = wrapper.find<HTMLSelectElement>(".input-bar__select");
    expect(select.exists()).toBe(true);
    // First option is Discuss, and it is the active intent for the composer.
    expect(select.element.value).toBe("discuss");
    wrapper.unmount();
  });
});

// ── Room-level config tests ───────────────────────────────────────────────────
//
// These tests use IntentInfo shapes that EXACTLY mirror what
// OrchestratorDriver.IntentInfo returns for the dev-story design room
// (stories/dev-story/rooms/design.yaml + app.yaml).  They connect the
// room's YAML configuration to the InputBar surface the operator sees.
//
// Regression class:
//   A. Priority ordering — discuss (85) must sort before capture_existing (78)
//      in AllowedIntents, so the InputBar dropdown defaults to "Discuss" not
//      "Reference Docs". The Go test in internal/orchestrator/ guards the server
//      side; this test guards the browser's rendering of the server's response.
//
//   B. Choice element kills composer — adding a choice: element to the design
//      room's view must NOT silently hide the text-slot composer.
//
//   C. Semantic textarea fallback — when no text-slot intents are present but
//      a typed view is (no choice), the semantic textarea must appear.

describe("InputBar — design room config", () => {
  // Realistic IntentInfo shapes as returned by OrchestratorDriver.IntentInfo
  // for the design state. Ordering here mirrors AllowedIntents priority sort:
  // discuss (85) > capture_existing (78). Both have a single string slot.
  const designIntents: IntentInfo[] = [
    { name: "discuss", title: "Discuss", text_slot: "message", has_slots: true },
    { name: "capture_existing", title: "Reference Docs", text_slot: "paths", has_slots: true },
    { name: "look", title: "Look", has_slots: false },
    { name: "quit", title: "Quit", has_slots: false },
  ];

  // Design room's typed view: banner + conditional prose (both branches) +
  // conditional kv — but NO choice element. This is the view that triggers
  // the text-slot composer path (not the choice path, not semantic).
  const designTypedView = {
    Elements: [
      {
        Kind: "banner" as const,
        BannerText: "INTAKE",
        BannerSubtitle: "Describe your idea — search runs next",
        BannerColor: "#10B981",
      },
      {
        Kind: "prose" as const,
        Source: "Describe the change you want to propose.",
      },
      {
        Kind: "kv" as const,
        KVPairs: [{ Key: "Existing docs", Value: "" }],
      },
    ],
  };

  it("Test A — design room: discuss is the default intent in the composer dropdown", () => {
    // Asserts that when AllowedIntents arrives in priority order (discuss
    // first, capture_existing second), the InputBar's dropdown defaults to
    // "discuss" and the composer is visible.
    const wrapper = mount(InputBar, {
      props: { intents: designIntents, typedView: designTypedView },
    });

    // The composer is visible because there are text-slot intents and no choice.
    expect(wrapper.find('[data-testid="composer"]').exists()).toBe(true);

    // The select shows both text-slot intents.
    const select = wrapper.find<HTMLSelectElement>('[data-testid="composer-select"]');
    expect(select.exists()).toBe(true);

    // discuss is first in the intents array → it is the default selection.
    // This is the regression guard: if capture_existing arrives first (wrong
    // priority ordering), this would be "capture_existing".
    expect(select.element.value).toBe("discuss");

    wrapper.unmount();
  });

  it("Test B — adding a choice: element to the design room view silently kills the idea composer", () => {
    // Documents the regression class: a choice: element added to the view
    // takes over the InputBar, hiding the text-slot composer entirely.
    // The text-slot intents are still present on the server side — they just
    // have no UI affordance for the operator to reach them.
    const viewWithChoice = {
      Elements: [
        ...designTypedView.Elements,
        {
          Kind: "choice" as const,
          ChoiceMode: "single",
          ChoicePrompt: "Actions",
          ChoiceItems: [
            {
              Label: "Capture existing docs",
              Intent: "capture_existing",
              Param: { Slot: "paths", Type: "string", Placeholder: "docs/..." },
            },
            { Label: "Quit", Intent: "quit" },
          ],
        },
      ],
    };
    const wrapper = mount(InputBar, {
      props: { intents: designIntents, typedView: viewWithChoice },
    });

    // The choice element is shown (the param form for capture_existing).
    expect(wrapper.find(".input-bar__forms").exists()).toBe(true);

    // The text-slot composer MUST NOT be shown — "discuss" has no affordance.
    // This is a REGRESSION if it fires: the operator cannot describe their idea.
    expect(wrapper.find('[data-testid="composer"]').exists()).toBe(false);

    wrapper.unmount();
  });

  it("Test C — design room without text-slot intents and with typed view shows semantic textarea", () => {
    // When only no-slot navigation intents (look, quit) are present and the
    // view has structured elements but no choice, the semantic textarea appears.
    // This is the path a future "view-only / navigate-away" state would take.
    const navOnlyIntents: IntentInfo[] = [
      { name: "look", title: "Look", has_slots: false },
      { name: "quit", title: "Quit", has_slots: false },
    ];
    const wrapper = mount(InputBar, {
      props: { intents: navOnlyIntents, typedView: designTypedView },
    });

    // The semantic composer IS shown (typed view + no choice + no text intents).
    const composer = wrapper.find('[data-testid="composer"]');
    expect(composer.exists()).toBe(true);
    expect(composer.classes()).toContain("input-bar__composer--semantic");

    // There is no intent dropdown (semantic routing, not slot-bound).
    expect(wrapper.find('[data-testid="composer-select"]').exists()).toBe(false);

    wrapper.unmount();
  });
});

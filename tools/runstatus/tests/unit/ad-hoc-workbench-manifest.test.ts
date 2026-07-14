import { describe, expect, it } from "vitest";
import { AD_HOC_WORKBENCH_TOUR_STEPS } from "../../src/tour/ad-hoc-workbench-manifest.js";

describe("ad-hoc workbench narration", () => {
  it("explains protected-main capsule delivery instead of promising a simple read-only toggle", () => {
    const writeModeSteps = AD_HOC_WORKBENCH_TOUR_STEPS.filter((step) =>
      step.id.startsWith("awb-writemode-"),
    );
    const narration = writeModeSteps.map((step) => `${step.title} ${step.body}`).join(" ");

    expect(narration).not.toContain("Read-only until you say so");
    expect(narration).toContain("managed capsule");
    expect(narration).toContain("staging/local");
    expect(narration).toContain("never unlocks the protected primary checkout");
  });
});

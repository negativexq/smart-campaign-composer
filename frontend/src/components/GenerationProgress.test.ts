import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import GenerationProgress from "./GenerationProgress.vue";

describe("GenerationProgress", () => {
  it("says what each language is doing and how many are finished", () => {
    const wrapper = mount(GenerationProgress, {
      props: {
        writes: "SMS and email",
        languages: [
          { language: "tr", name: "Turkish", state: "done" },
          { language: "de", name: "German", state: "writing" },
          { language: "fr", name: "French", state: "waiting" },
        ],
      },
    });
    expect(wrapper.get('[data-testid="generation-count"]').text()).toBe("1 of 3 languages");
    expect(wrapper.get('[data-testid="generation-tr"]').text()).toContain("Written and checked");
    expect(wrapper.get('[data-testid="generation-de"]').text()).toContain("Writing SMS and email");
    expect(wrapper.get('[data-testid="generation-fr"]').text()).toContain("Up next");
    expect(wrapper.get('[data-testid="generation-progress"]').attributes("aria-live")).toBe("polite");
  });

  it("names the version of a test", () => {
    const wrapper = mount(GenerationProgress, { props: { writes: "SMS", variant: "B", languages: [{ language: "en", name: "English", state: "writing" }] } });
    expect(wrapper.text()).toContain("Writing version B");
    expect(wrapper.text()).toContain("0 of 1 language");
  });
});

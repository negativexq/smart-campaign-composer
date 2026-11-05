import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import DuplicateDialog from "./DuplicateDialog.vue";

function preview(audience: { total: number; sms: number; email: number }, reached: number) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ not_messaged_within_days: null, source_rule: null, audience, reached_before: reached, last_sent_at: null }))),
  );
}

describe("DuplicateDialog", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("says when a copy would message nobody, and still lets it be made", async () => {
    preview({ total: 229, sms: 0, email: 0 }, 0);
    const wrapper = mount(DuplicateDialog, { props: { campaignId: "c1", campaignName: "Lapsed" } });
    await flushPromises();

    expect(wrapper.find("[data-testid=duplicate-nobody]").text()).toContain("so the copy would send nothing");
    expect(wrapper.find("[data-testid=duplicate-clear]").exists()).toBe(false);
    const button = wrapper.find("[data-testid=duplicate-confirm]");
    expect(button.text()).toBe("Create copy anyway");
    expect(button.classes()).toContain("button--secondary");
    expect((button.element as HTMLButtonElement).disabled).toBe(false);
  });

  it("asks plainly when the copy reaches people", async () => {
    preview({ total: 4142, sms: 2963, email: 3335 }, 3913);
    const wrapper = mount(DuplicateDialog, { props: { campaignId: "c1", campaignName: "Lapsed" } });
    await flushPromises();

    expect(wrapper.find("[data-testid=duplicate-nobody]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=duplicate-reached]").text()).toContain("3,913 of them already received this campaign");
    const button = wrapper.find("[data-testid=duplicate-confirm]");
    expect(button.text()).toBe("Create copy");
    expect(button.classes()).toContain("button--primary");
  });
});

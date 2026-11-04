import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { NotificationEntry } from "../api/campaigns";
import NotificationBell from "./NotificationBell.vue";

const entry = (id: number, type: string, details: Record<string, unknown> = {}): NotificationEntry => ({
  id,
  campaign_id: "c1",
  campaign_name: "Cart Recovery",
  type,
  actor_type: "system",
  actor: null,
  details,
  occurred_at: "2026-11-03T12:00:00Z",
});

function stub(entries: NotificationEntry[]) {
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ notifications: entries }) }) as Response));
}

async function open(entries: NotificationEntry[]) {
  stub(entries);
  const wrapper = mount(NotificationBell, { attachTo: document.body });
  await flushPromises();
  return wrapper;
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("notifications", () => {
  it("counts what is new and clears the count when opened", async () => {
    const wrapper = await open([entry(2, "campaign.run", { queued: 4 }), entry(1, "campaign.completed", { accepted: 3 })]);
    expect(wrapper.find("[data-testid=notifications-count]").text()).toBe("2");
    expect(wrapper.find("[data-testid=notifications-panel]").exists()).toBe(false);

    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    expect(wrapper.findAll("[data-testid=notification]")).toHaveLength(2);
    expect(wrapper.find("[data-testid=notifications-count]").exists()).toBe(false);
    wrapper.unmount();

    const again = await open([entry(3, "campaign.paused", { waiting: 2 }), entry(2, "campaign.run", { queued: 4 })]);
    expect(again.find("[data-testid=notifications-count]").text()).toBe("1");
    again.unmount();
  });

  it("marks problems apart from starts and ends", async () => {
    const wrapper = await open([
      entry(5, "campaign.paused", { waiting: 2, reason: "provider_unreachable", after_minutes: 10 }),
      entry(4, "content.generation_failed", { reason: "refused" }),
      entry(3, "campaign.completed", { accepted: 3, failed: 1 }),
      entry(2, "campaign.completed", { accepted: 3 }),
      entry(1, "campaign.run", { queued: 4, skipped: 0 }),
    ]);
    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    const items = wrapper.findAll("[data-testid=notification]");
    expect(items.map((item) => item.attributes("data-tone"))).toEqual(["error", "error", "error", "done", "started"]);
    expect(items[0].text()).toContain("The provider could not be reached for 10 minutes");
    expect(items[0].attributes("href")).toBe("/campaigns/c1");
    wrapper.unmount();
  });

  it("clears only what ended well, and problems stay and lead to their campaign", async () => {
    const list = [entry(3, "campaign.paused", { waiting: 2 }), entry(2, "campaign.completed", { accepted: 3 }), entry(1, "campaign.run", { queued: 4 })];
    const wrapper = await open(list);
    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    await wrapper.find("[data-testid=notifications-clear]").trigger("click");

    const items = wrapper.findAll("[data-testid=notification]");
    expect(items.map((item) => item.attributes("data-tone"))).toEqual(["error", "started"]);
    expect(items[0].attributes("href")).toBe("/campaigns/c1");
    // Nothing that ended well is left, so there is nothing more to clear.
    expect(wrapper.find("[data-testid=notifications-clear]").exists()).toBe(false);
    wrapper.unmount();

    const again = await open([entry(4, "campaign.completed", { accepted: 1 }), ...list]);
    await again.find("[data-testid=notifications-toggle]").trigger("click");
    expect(again.findAll("[data-testid=notification]").map((item) => item.attributes("data-tone"))).toEqual(["done", "error", "started"]);
    again.unmount();
  });

  it("checks for news without being asked", async () => {
    vi.useFakeTimers();
    const wrapper = await open([]);
    expect(wrapper.find("[data-testid=notifications-count]").exists()).toBe(false);
    stub([entry(1, "campaign.completed", { accepted: 3 })]);
    await vi.advanceTimersByTimeAsync(15000);
    expect(wrapper.find("[data-testid=notifications-count]").text()).toBe("1");
    wrapper.unmount();
  });

  it("stays open when clearing removes the button that was clicked", async () => {
    const wrapper = await open([entry(1, "campaign.completed", { accepted: 3 })]);
    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    const button = wrapper.find("[data-testid=notifications-clear]").element as HTMLElement;
    // A real click lets the page update before the event reaches the document.
    const parent = button.parentElement!;
    button.addEventListener("click", () => button.remove());
    // Put back once the check has run, so the page can remove it itself.
    document.addEventListener("click", () => parent.append(button), { once: true });
    button.click();
    await flushPromises();
    expect(wrapper.find("[data-testid=notifications-panel]").exists()).toBe(true);
    expect(wrapper.find("[data-testid=notifications-empty]").exists()).toBe(true);
    wrapper.unmount();
  });

  it("closes on a click elsewhere and on Escape", async () => {
    const wrapper = await open([]);
    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    expect(wrapper.find("[data-testid=notifications-empty]").exists()).toBe(true);
    document.body.click();
    await flushPromises();
    expect(wrapper.find("[data-testid=notifications-panel]").exists()).toBe(false);

    await wrapper.find("[data-testid=notifications-toggle]").trigger("click");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    expect(wrapper.find("[data-testid=notifications-panel]").exists()).toBe(false);
    wrapper.unmount();
  });
});

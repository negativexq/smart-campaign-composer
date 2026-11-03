import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Campaign, Progress } from "../api/campaigns";
import AudienceReport from "./AudienceReport.vue";

const breakdown = {
  everyone: 5000,
  steps: [
    { field: "city", remaining: 1787 },
    { field: "cart_added_within_days", remaining: 511 },
    { field: "no_purchase_since_cart", remaining: 467 },
  ],
};

const audience = { total: 467, sms: 330, email: 384 };

const campaign = {
  id: "c1",
  brief: "Istanbul cart reminder",
  status: "COMPLETED",
  provider: "mock",
  channels: ["sms", "email"],
  proposal: { assumptions: [], evidence: [], unsupported_conditions: [] },
  conditions: [
    { field: "city", label: "City is", value: "İstanbul" },
    { field: "cart_added_within_days", label: "Added to cart in the last", value: "30 days" },
    { field: "no_purchase_since_cart", label: "Purchased after adding to cart", value: "No" },
  ],
  audience,
  audience_as_of: "2026-11-03T12:00:00Z",
  approved_at: "2026-11-03T12:30:00Z",
  audience_snapshot: { counts: audience, breakdown },
} as unknown as Campaign;

const progress = {
  total: 934,
  by_channel: { sms: { SENT: 330, SKIPPED: 137 }, email: { SENT: 383, SKIPPED: 83, FAILED: 1 } },
} as unknown as Progress;

async function open(current: Campaign, currentProgress: Progress | null = null, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: status < 400, status, json: async () => (status < 400 ? breakdown : { error: "down" }) }) as Response),
  );
  const wrapper = mount(AudienceReport, { props: { campaign: current, progress: currentProgress } });
  await flushPromises();
  return wrapper;
}

describe("AudienceReport", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("leads with the results, then how they were narrowed, then the rules", async () => {
    const text = (await open(campaign)).text();

    expect(text.indexOf("Matched customers")).toBeLessThan(text.indexOf("How the audience was narrowed"));
    expect(text.indexOf("How the audience was narrowed")).toBeLessThan(text.indexOf("Approved segment rules"));
    expect(text.indexOf("Approved segment rules")).toBeLessThan(text.indexOf("Show calculation details"));
  });

  it("works out the shares and what each step excluded", async () => {
    const wrapper = await open(campaign);

    expect(wrapper.find("[data-testid=kpi-matched]").text()).toContain("9.3% of total");
    expect(wrapper.find("[data-testid=kpi-sms]").text()).toContain("70.7% of matched");
    expect(wrapper.find("[data-testid=kpi-email]").text()).toContain("82.2% of matched");
    const steps = wrapper.findAll("[data-testid=narrowing-step]").map((step) => step.text());
    expect(steps[0]).toContain("Starting population");
    expect(steps[1]).toContain("3,213 excluded");
    expect(steps[2]).toContain("1,276 excluded");
    expect(steps[3]).toContain("44 excluded");
  });

  it("calls it final only when a snapshot was really stored", async () => {
    const final = await open(campaign);
    expect(final.find("[data-testid=audience-final]").exists()).toBe(true);
    expect(final.find("[data-testid=audience-snapshot-line]").text()).toContain("Approved audience snapshot");

    const draft = await open({ ...campaign, approved_at: undefined, audience_snapshot: undefined });
    expect(draft.find("[data-testid=audience-final]").exists()).toBe(false);
    expect(draft.find("[data-testid=audience-snapshot-line]").text()).toContain("Can still change");
    expect(draft.text()).toContain("Segment rules");
    expect(draft.text()).not.toContain("Approved segment rules");

    const older = await open({ ...campaign, audience_snapshot: undefined });
    expect(older.find("[data-testid=audience-snapshot-line]").text()).not.toContain("snapshot");
    expect(older.find("[data-testid=audience-snapshot-line]").text()).not.toContain("Can still change");
  });

  it("shows actual reach only from delivery records", async () => {
    expect((await open(campaign)).find("[data-testid=actual-reach]").exists()).toBe(false);

    const sent = await open(campaign, progress);
    expect(sent.find("[data-testid=reach-sms]").findAll("td").map((cell) => cell.text())).toEqual(["SMS", "330", "330", "137", "0"]);
    expect(sent.find("[data-testid=reach-email]").findAll("td").map((cell) => cell.text())).toEqual(["Email", "384", "383", "83", "1"]);
    expect(sent.find("[data-testid=actual-reach]").text()).toContain("not that a person received it");
  });

  it("only lists a channel the campaign uses", async () => {
    const wrapper = await open({ ...campaign, channels: ["email"] });

    expect(wrapper.find("[data-testid=kpi-sms]").exists()).toBe(false);
  });

  it("keeps the rules when the counts cannot be loaded", async () => {
    const wrapper = await open(campaign, null, 500);

    expect(wrapper.find("[data-testid=breakdown-unavailable]").exists()).toBe(true);
    expect(wrapper.text()).toContain("City is İstanbul");
    expect(wrapper.findAll("[data-testid=narrowing-step]").length).toBe(0);
  });
});

import { enableAutoUnmount, flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "../App.vue";
import type { Campaign } from "../api/campaigns";
import { currentPath, navigate } from "../lib/navigation";

const campaign: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Istanbul cart reminder",
  status: "DRAFT",
  version: 1,
  proposal: {
    assumptions: [{ field: "cart_added_within_days", note: "The brief gives no time window, so 30 days is assumed." }],
    unsupported_conditions: [],
  },
  conditions: [
    { field: "city", label: "City is", value: "İstanbul" },
    { field: "cart_added_within_days", label: "Added to cart in the last", value: "30 days" },
  ],
  channels: ["sms", "email"],
  audience: { total: 1284, sms: 900, email: 1000 },
  audience_as_of: "2026-11-03T12:00:00Z",
  blockers: [],
  content_limits: { sms_name_length: 21, email_name_length: 21 },
  content_warnings: [],
  created_at: "2026-11-03T12:00:00Z",
  updated_at: "2026-11-03T12:00:00Z",
};

type Route = { status?: number; body: unknown };

function stubApi(routes: Record<string, Route | Route[]>) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${url}`;
    let route = routes[key];
    if (Array.isArray(route)) route = route.length > 1 ? route.shift()! : route[0];
    const found = route ?? { status: 404, body: { error: "not stubbed" } };
    const status = found.status ?? 200;
    return { ok: status < 400, status, json: async () => found.body } as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

const shared = {
  "GET /api/session": { body: { identity: "marketer@example.com" } },
  "GET /api/system": { body: { checks: { database: "ok" } } },
  "GET /api/config": { body: { llm_provider: "mock", llm_model: "keyword-matcher" } },
  "GET /api/overview": {
    body: { counts: { total: 0, drafts: 0, active: 0, completed: 0, failed: 0 }, recent_campaigns: [], recent_activity: [] },
  },
};

function postsTo(fetchMock: ReturnType<typeof stubApi>, url: string) {
  return fetchMock.mock.calls.filter(([called, init]) => called === url && init?.method === "POST");
}

enableAutoUnmount(afterEach);

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

describe("brief to audience", () => {
  const unsearched = { ...campaign, proposal: undefined, conditions: [], audience_as_of: undefined };

  it("opens the audience page at once and looks for the audience there", async () => {
    let answer: (value: unknown) => void = () => {};
    const fetchMock = stubApi({
      ...shared,
      "POST /api/campaigns": { status: 201, body: { id: "c1" } },
      "POST /api/campaigns/c1/segment": { body: campaign },
      "GET /api/campaigns/c1": { body: unsearched },
    });
    const respond = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url === "/api/campaigns/c1/segment") await new Promise((resolve) => (answer = resolve));
      return respond(url, init);
    });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    await wrapper.find("textarea").setValue("Istanbul cart reminder");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(JSON.parse(postsTo(fetchMock, "/api/campaigns")[0][1]!.body as string)).toEqual({ brief: "Istanbul cart reminder", channels: ["sms", "email"] });
    expect(currentPath.value).toBe("/campaigns/c1/edit");
    expect(wrapper.find("[aria-current=step]").text()).toContain("Audience");
    expect(wrapper.find("[data-testid=brief-text]").text()).toBe("Istanbul cart reminder");
    expect(wrapper.find("[data-testid=audience-loading]").exists()).toBe(true);
    expect(wrapper.text()).not.toContain("1,284");

    answer(null);
    await flushPromises();
    expect(wrapper.find("[data-testid=audience-loading]").exists()).toBe(false);
    expect(wrapper.find(".rules--staged").exists()).toBe(true);
    expect(wrapper.find(".rest--staged").text()).toContain("1,284");
  });

  it("keeps the brief and can try again when finding an audience fails", async () => {
    const fetchMock = stubApi({
      ...shared,
      "POST /api/campaigns": { status: 201, body: { id: "c1" } },
      "POST /api/campaigns/c1/segment": [
        { status: 422, body: { error: "Try naming a city." } },
        { body: campaign },
      ],
      "GET /api/campaigns/c1": { body: unsearched },
    });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    await wrapper.find("textarea").setValue("hello");
    await wrapper.find("form").trigger("submit");
    await vi.waitFor(() => expect(wrapper.find("[role=alert]").exists()).toBe(true));

    expect(wrapper.find("[role=alert]").text()).toContain("Try naming a city.");
    expect(wrapper.find("[data-testid=brief-text]").exists()).toBe(true);

    await wrapper.findAll("button").find((button) => button.text() === "Find audience")!.trigger("click");
    await vi.waitFor(() => expect(wrapper.text()).toContain("1,284"));

    expect(postsTo(fetchMock, "/api/campaigns")).toHaveLength(1);
  });

  it("does not start a model call just because a draft was reopened", async () => {
    const fetchMock = stubApi({ ...shared, "GET /api/campaigns/c1": { body: unsearched } });
    navigate("/campaigns/c1/edit");
    mount(App);
    await flushPromises();

    expect(postsTo(fetchMock, "/api/campaigns/c1/segment")).toHaveLength(0);
  });

  it("names the mock so nobody mistakes it for a real model", async () => {
    stubApi(shared);
    const wrapper = mount(App);
    await flushPromises();
    expect(wrapper.find("[data-testid=ai-mode]").text()).toBe("Mock AI");
  });
});

describe("audience page", () => {
  async function open(routes: Record<string, Route | Route[]>) {
    navigate("/campaigns/c1/edit");
    const fetchMock = stubApi({ ...shared, ...routes });
    const wrapper = mount(App);
    await flushPromises();
    return { wrapper, fetchMock };
  }

  it("builds up in order only after a search, not when reopened", async () => {
    const { wrapper } = await open({ "GET /api/campaigns/c1": { body: campaign } });
    await flushPromises();

    expect(wrapper.find(".funnel--staged").exists()).toBe(false);
    expect(wrapper.find(".rest--staged").exists()).toBe(false);
  });

  it("puts the conditions before the number and marks assumptions", async () => {
    const { wrapper } = await open({ "GET /api/campaigns/c1": { body: campaign } });
    const text = wrapper.text();

    expect(text.indexOf("İstanbul")).toBeLessThan(text.indexOf("1,284"));
    expect(wrapper.find("[data-testid=assumption-note]").text()).toContain("30 days is assumed");
  });

  it("explains each condition in a sentence and how many people it leaves", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "GET /api/campaigns/c1/audience/breakdown": {
        body: { everyone: 5000, steps: [{ field: "city", remaining: 2000 }, { field: "cart_added_within_days", remaining: 1284 }] },
      },
    });
    await flushPromises();
    const steps = wrapper.findAll("[data-testid=audience-step]");

    expect(steps[0].text()).toContain("All customers");
    expect(steps[0].text()).toContain("5,000");
    expect(steps[1].text()).toContain("City");
    expect(steps[1].text()).toContain("İstanbul");
    expect(steps[1].text()).toContain("2,000");
    expect(steps[2].text()).toContain("Added to cart");
    expect(steps[2].text()).toContain("Last 30 days");
    expect(steps[1].text()).toContain("40.0%");
    expect(wrapper.find("[data-testid=segment-rules]").text()).toContain("City is İstanbul");
    expect(wrapper.find("[data-testid=audience-narrowing]").text()).toContain("3,000 excluded");
    expect(steps[2].text()).toContain("1,284");
    expect(wrapper.text()).not.toContain("cart_added_within_days");
  });

  it("shows the words behind each condition and never claims words nobody wrote", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": {
        body: {
          ...campaign,
          conditions: [...campaign.conditions, { field: "language", label: "Language is", value: "Turkish" }, { field: "not_messaged_within_days", label: "No message from us in the last", value: "7 days" }],
          not_messaged_within_days: 7,
          proposal: {
            assumptions: campaign.proposal!.assumptions,
            evidence: [{ field: "city", quote: "İstanbul'daki müşteriler" }],
            unsupported_conditions: ["VIP"],
          },
        },
      },
    });
    await flushPromises();
    const sources = wrapper.findAll("[data-testid=step-source]").map((source) => source.text());

    expect(sources[0]).toBe("İstanbul'daki müşteriler");
    expect(sources).toContain("Added by AI, not in your brief");
    expect(sources).toContain("From Options");
    expect(sources.length).toBe(3); // the assumed window has its own label
  });

  it("shows what the model left out of the audience", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: { ...campaign, proposal: { ...campaign.proposal!, not_audience: ["Instagram"] } } },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("Not used for the audience");
    expect(wrapper.text()).toContain("Instagram");
  });

  it("shows which parts of the brief became the rule and blocks on the part that did not", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": {
        body: {
          ...campaign,
          brief: "Newsletter subscribers in İstanbul, say hello",
          blockers: ['The brief asks for "Newsletter subscribers", which no audience filter can express.'],
          conditions: [{ field: "city", label: "City is", value: "İstanbul" }],
          proposal: {
            ...campaign.proposal!,
            not_audience: ["hello"],
            brief_parts: [
              { text: "Newsletter subscribers", kind: "audience" },
              { text: "İstanbul", kind: "audience", used_as: ["city"] },
              { text: "say hello", kind: "message" },
            ],
          },
        },
      },
    });
    await flushPromises();

    expect(wrapper.find("[data-testid=brief-text]").text()).toBe("Newsletter subscribers in İstanbul, say hello");
    expect(wrapper.find(".part--missing").text()).toBe("Newsletter subscribers");
    expect(wrapper.find(".part--used").text()).toBe("İstanbul");
    expect(wrapper.find("[data-testid=parts-used]").text()).toBe("1 of 2 audience parts used");
    const rows = wrapper.findAll("[data-testid=audience-part]").map((row) => row.text());
    expect(rows[0]).toContain("Not used, no filter for this");
    expect(rows[1]).toContain("City is İstanbul");
    expect(wrapper.find("[data-testid=other-parts]").text()).toBe("say hello");
    expect(wrapper.text()).toContain("This blocks approval");
    expect(wrapper.find("[data-testid=continue-content]").exists()).toBe(false);
  });

  it("warns about an unsure reading without stopping the campaign", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: { ...campaign, proposal: { ...campaign.proposal!, unsure: ["ödeme yapmayan"] } } },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("Check how this was read");
    expect(wrapper.text()).toContain("ödeme yapmayan");
    expect(wrapper.find("[data-testid=continue-content]").exists()).toBe(true);
  });

  it("says when a refinement was not done, without blocking approval", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": {
        body: { ...campaign, proposal: { ...campaign.proposal!, not_applied: ['Nothing changed for your request: "make it 14 days".'] } },
      },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("Not done");
    expect(wrapper.text()).toContain("make it 14 days");
    expect(wrapper.text()).not.toContain("This blocks approval");
  });

  it("makes no claim about quotes for a proposal made before they existed", async () => {
    const { wrapper } = await open({ "GET /api/campaigns/c1": { body: campaign } });
    await flushPromises();

    expect(wrapper.find("[data-testid=step-source]").exists()).toBe(false);
  });

  it("shows a waiting state while the AI works, then the audience", async () => {
    let answer: (value: unknown) => void = () => {};
    const { wrapper, fetchMock } = await open({ "GET /api/campaigns/c1": { body: campaign } });
    const original = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "POST") {
        await new Promise((resolve) => (answer = resolve));
        return { ok: true, status: 200, json: async () => campaign } as Response;
      }
      return original(url, init);
    });
    await wrapper.find("#refinement").setValue("make it 14 days");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(wrapper.find("[data-testid=audience-loading]").text()).toContain("Updating the segment rule");
    expect(wrapper.find("[data-testid=audience-narrowing]").exists()).toBe(false);

    answer(null);
    await flushPromises();
    expect(wrapper.find("[data-testid=audience-loading]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=audience-narrowing]").exists()).toBe(true);
    expect(wrapper.find(".funnel--staged").exists()).toBe(true);
  });

  it("still lists the conditions when the counts cannot be loaded", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "GET /api/campaigns/c1/audience/breakdown": { status: 500, body: { error: "down" } },
    });
    await flushPromises();

    expect(wrapper.text()).toContain("City is İstanbul");
    expect(wrapper.find("[data-testid=breakdown-unavailable]").exists()).toBe(true);
    expect(wrapper.findAll("[data-testid=audience-step]").every((step) => !step.find(".count").exists())).toBe(true);
  });

  it("explains blockers and why it cannot continue", async () => {
    const blocked = { ...campaign, blockers: ["The brief asks for \"age\"."] };
    const { wrapper } = await open({ "GET /api/campaigns/c1": { body: blocked } });

    expect(wrapper.text()).toContain("This blocks approval");
    expect(wrapper.find("[data-testid=continue-hint]").text()).toContain("Resolve the issues");
  });

  it("sends a refinement and shows the new audience", async () => {
    const narrowed = { ...campaign, audience: { total: 12, sms: 5, email: 6 } };
    const { wrapper, fetchMock } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { body: narrowed },
    });

    await wrapper.find("#refinement").setValue("make it 4 days");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();

    expect(JSON.parse(postsTo(fetchMock, "/api/campaigns/c1/segment")[0][1]!.body as string)).toEqual({ refinement: "make it 4 days" });
    expect(wrapper.text()).toContain("12");
    expect((wrapper.find("#refinement").element as HTMLInputElement).value).toBe("");
  });

  it("shows what a refinement changed in the rule and the audience", async () => {
    const earlier = {
      ...campaign,
      audience: { total: 40, sms: 20, email: 20 },
      conditions: [
        { field: "city", label: "City is", value: "Ankara" },
        { field: "cart_added_within_days", label: "Cart added within", value: "7 days" },
        { field: "language", label: "Language is", value: "Turkish" },
      ],
    };
    const later = {
      ...earlier,
      audience: { total: 12, sms: 5, email: 6 },
      conditions: [
        { field: "city", label: "City is", value: "Ankara" },
        { field: "cart_added_within_days", label: "Cart added within", value: "14 days" },
        { field: "purchased_within_days", label: "Purchased within", value: "30 days" },
      ],
    };
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: earlier },
      "POST /api/campaigns/c1/segment": { body: later },
    });
    expect(wrapper.find("[data-testid=refinement-diff]").exists()).toBe(false);

    await wrapper.find("#refinement").setValue("make it 14 days");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();

    const changes = wrapper.findAll("[data-testid=refinement-change]").map((row) => [row.find(".kind").text(), row.text().replace(row.find(".kind").text(), "").replace(/\s+/g, " ").trim()]);
    expect(changes).toEqual([
      ["Changed", "Cart added within 7 days → Cart added within 14 days"],
      ["Added", "Purchased within 30 days"],
      ["Removed", "Language is Turkish"],
    ]);
    expect(wrapper.find("[data-testid=refinement-audience]").text().replace(/\s+/g, " ")).toContain("40 → 12");
  });

  it("does not claim a change when a refinement made none", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { body: campaign },
    });
    await wrapper.find("#refinement").setValue("make it nicer");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();
    expect(wrapper.find("[data-testid=refinement-diff]").exists()).toBe(false);
  });

  it("shows the brief the refinement rewrote and says so", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { body: { ...campaign, brief: "Ankara customers in the last 14 days" } },
    });
    expect(wrapper.find('[data-testid="brief-updated"]').exists()).toBe(false);

    await wrapper.find("#refinement").setValue("make it 14 days");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();

    expect(wrapper.find('[data-testid="brief-text"]').text()).toBe("Ankara customers in the last 14 days");
    expect(wrapper.find('[data-testid="brief-updated"]').text()).toContain("make it 14 days");
  });

  it("does not say the brief changed when the refinement left it alone", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { body: campaign },
    });

    await wrapper.find("#refinement").setValue("make it 14 days");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();

    expect(wrapper.find('[data-testid="brief-updated"]').exists()).toBe(false);
  });

  it("keeps the current audience and the refinement when refining fails", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { status: 503, body: { error: "The AI service is unavailable right now." } },
    });

    await wrapper.find("#refinement").setValue("make it 4 days");
    await wrapper.find("[data-testid=refine-form]").trigger("submit");
    await flushPromises();

    expect(wrapper.find("[role=alert]").text()).toContain("unavailable");
    expect(wrapper.text()).toContain("1,284");
    expect((wrapper.find("#refinement").element as HTMLInputElement).value).toBe("make it 4 days");
  });

  it("edits the brief", async () => {
    const edited = { ...campaign, brief: "Ankara only" };
    const { wrapper, fetchMock } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { body: edited },
    });

    await wrapper.findAll("button").find((button) => button.text() === "Edit brief")!.trigger("click");
    await wrapper.find("#edit-brief").setValue("Ankara only");
    await wrapper.findAll("button").find((button) => button.text() === "Find audience again")!.trigger("click");
    await flushPromises();

    expect(JSON.parse(postsTo(fetchMock, "/api/campaigns/c1/segment")[0][1]!.body as string)).toEqual({ brief: "Ankara only" });
    expect(wrapper.find("[data-testid=brief-text]").text()).toBe("Ankara only");
  });

  it("shows an error for a campaign that does not exist", async () => {
    const { wrapper } = await open({ "GET /api/campaigns/c1": { status: 404, body: { error: "This campaign does not exist." } } });
    expect(wrapper.find("[role=alert]").text()).toContain("does not exist");
  });
});

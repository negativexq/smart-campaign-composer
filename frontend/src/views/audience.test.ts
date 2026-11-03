import { flushPromises, mount } from "@vue/test-utils";
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

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

describe("brief to audience", () => {
  it("creates the campaign and opens its audience", async () => {
    const fetchMock = stubApi({
      ...shared,
      "POST /api/campaigns": { status: 201, body: { id: "c1" } },
      "POST /api/campaigns/c1/segment": { body: campaign },
      "GET /api/campaigns/c1": { body: campaign },
    });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    await wrapper.find("textarea").setValue("Istanbul cart reminder");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(JSON.parse(postsTo(fetchMock, "/api/campaigns")[0][1]!.body as string)).toEqual({ brief: "Istanbul cart reminder" });
    expect(currentPath.value).toBe("/campaigns/c1/edit");
    expect(wrapper.find("[aria-current=step]").text()).toContain("Audience");
    expect(wrapper.text()).toContain("1,284");
  });

  it("keeps the brief and reuses the campaign when finding an audience fails", async () => {
    const fetchMock = stubApi({
      ...shared,
      "POST /api/campaigns": { status: 201, body: { id: "c1" } },
      "POST /api/campaigns/c1/segment": [
        { status: 422, body: { error: "Try naming a city." } },
        { body: campaign },
      ],
      "GET /api/campaigns/c1": { body: campaign },
    });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    await wrapper.find("textarea").setValue("hello");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(wrapper.find("[role=alert]").text()).toContain("Try naming a city.");
    expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("hello");

    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(postsTo(fetchMock, "/api/campaigns")).toHaveLength(1);
    expect(currentPath.value).toBe("/campaigns/c1/edit");
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

  it("puts the conditions before the number and marks assumptions", async () => {
    const { wrapper } = await open({ "GET /api/campaigns/c1": { body: campaign } });
    const text = wrapper.text();

    expect(text.indexOf("İstanbul")).toBeLessThan(text.indexOf("1,284"));
    expect(text).toContain("Assumption, please check");
    expect(wrapper.find("[data-testid=assumption-notes]").text()).toContain("30 days is assumed");
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
    await wrapper.findAll("form").at(-1)!.trigger("submit");
    await flushPromises();

    expect(JSON.parse(postsTo(fetchMock, "/api/campaigns/c1/segment")[0][1]!.body as string)).toEqual({ refinement: "make it 4 days" });
    expect(wrapper.text()).toContain("12");
    expect((wrapper.find("#refinement").element as HTMLInputElement).value).toBe("");
  });

  it("keeps the current audience and the refinement when refining fails", async () => {
    const { wrapper } = await open({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/segment": { status: 503, body: { error: "The AI service is unavailable right now." } },
    });

    await wrapper.find("#refinement").setValue("make it 4 days");
    await wrapper.findAll("form").at(-1)!.trigger("submit");
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

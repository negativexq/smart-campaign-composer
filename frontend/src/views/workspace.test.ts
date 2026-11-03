import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "../App.vue";
import type { ActivityEntry, Campaign, CampaignSummary, Overview } from "../api/campaigns";
import { describeActivity } from "../lib/activity";
import { navigate } from "../lib/navigation";

const summary = (overrides: Partial<CampaignSummary> = {}): CampaignSummary => ({
  id: "c1",
  name: "Cart Recovery",
  brief: "Istanbul cart reminder",
  status: "DRAFT",
  channels: [],
  deliveries_total: 0,
  deliveries_processed: 0,
  created_at: "2026-11-03T12:00:00Z",
  updated_at: "2026-11-03T12:00:00Z",
  ...overrides,
});

const activity: ActivityEntry[] = [
  { campaign_id: "c1", campaign_name: "Cart Recovery", type: "campaign.created", details: {}, occurred_at: "2026-11-03T12:00:00Z" },
  {
    campaign_id: "c1",
    campaign_name: "Cart Recovery",
    type: "segment.proposed",
    details: { trigger: "refinement", provider: "mock", audience: { total: 1284 }, unsupported_conditions: 0 },
    occurred_at: "2026-11-03T12:01:00Z",
  },
];

const campaign: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Istanbul cart reminder",
  status: "DRAFT",
  version: 1,
  proposal: { assumptions: [], unsupported_conditions: [] },
  conditions: [{ field: "city", label: "City is", value: "İstanbul" }],
  audience: { total: 1284, sms: 900, email: 1000 },
  audience_as_of: "2026-11-03T12:00:00Z",
  blockers: [],
  content_limits: { sms_name_length: 21, email_name_length: 21 },
  content_warnings: [],
  created_at: "2026-11-03T12:00:00Z",
  updated_at: "2026-11-03T12:00:00Z",
};

const emptyOverview: Overview = {
  counts: { total: 0, drafts: 0, active: 0, completed: 0, failed: 0 },
  recent_campaigns: [],
  recent_activity: [],
};

function stubApi(routes: Record<string, unknown>) {
  const fetchMock = vi.fn(async (url: string) => {
    const known = url in routes;
    return { ok: known, status: known ? 200 : 404, json: async () => (known ? routes[url] : { error: "Not found." }) } as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

async function open(path: string, routes: Record<string, unknown>) {
  navigate(path);
  const fetchMock = stubApi({ "/readyz": {}, "/api/config": { llm_provider: "mock", llm_model: "m" }, ...routes });
  const wrapper = mount(App);
  await flushPromises();
  return { wrapper, fetchMock };
}

afterEach(() => {
  navigate("/");
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("overview", () => {
  it("is the landing page and shows database totals", async () => {
    const { wrapper } = await open("/", {
      "/api/overview": {
        counts: { total: 24, drafts: 3, active: 2, completed: 18, failed: 1 },
        recent_campaigns: [summary()],
        recent_activity: activity,
      },
    });

    expect(wrapper.find("h1").text()).toContain("at a glance");
    expect(wrapper.text()).toContain("24");
    expect(wrapper.text()).toContain("Cart Recovery");
    expect(wrapper.text()).toContain("Audience refined");
  });

  it("guides a new workspace to its first campaign instead of showing zeros", async () => {
    const { wrapper } = await open("/", { "/api/overview": emptyOverview });

    expect(wrapper.find("[data-testid=empty-workspace] a").attributes("href")).toBe("/campaigns/new");
    expect(wrapper.text()).not.toContain("Total campaigns");
  });

  it("shows an error when the workspace cannot load", async () => {
    const { wrapper } = await open("/", {});
    expect(wrapper.find("[role=alert]").text()).toContain("could not load");
  });
});

describe("campaign list", () => {
  beforeEach(() => vi.useFakeTimers());

  async function settle() {
    await vi.runAllTimersAsync();
    await flushPromises();
  }

  it("shows real progress and a dash when nothing was dispatched", async () => {
    const { wrapper } = await open("/campaigns", {
      "/api/campaigns": {
        campaigns: [
          summary(),
          summary({ id: "c2", status: "DISPATCHING", channels: ["email", "sms"], deliveries_total: 200, deliveries_processed: 137 }),
        ],
      },
    });
    await settle();

    const rows = wrapper.findAll("[data-testid=campaign-row]");
    expect(rows[0].text()).toContain("Draft");
    expect(rows[0].text()).toContain("—");
    expect(rows[1].text()).toContain("Sending");
    expect(rows[1].text()).toContain("Email + SMS");
    expect(rows[1].text()).toContain("68%");
    expect(rows[1].find("a").attributes("href")).toBe("/campaigns/c2");
  });

  it("filters by group and search on the server", async () => {
    const { wrapper, fetchMock } = await open("/campaigns", {
      "/api/campaigns": { campaigns: [summary()] },
      "/api/campaigns?group=drafts": { campaigns: [] },
      "/api/campaigns?group=drafts&q=winter+sale": { campaigns: [] },
    });
    await settle();

    await wrapper.findAll("button").find((button) => button.text() === "Drafts")!.trigger("click");
    await settle();
    expect(wrapper.find("[data-testid=no-results]").exists()).toBe(true);

    await wrapper.find("#campaign-search").setValue("winter sale");
    await settle();
    expect(fetchMock).toHaveBeenCalledWith("/api/campaigns?group=drafts&q=winter+sale", undefined);
  });

  it("offers the first campaign on an empty list", async () => {
    const { wrapper } = await open("/campaigns", { "/api/campaigns": { campaigns: [] } });
    await settle();
    expect(wrapper.find("[data-testid=no-campaigns] a").attributes("href")).toBe("/campaigns/new");
  });
});

describe("campaign detail", () => {
  const routes = { "/api/campaigns/c1": campaign, "/api/campaigns/c1/activity": { activity } };

  it("offers only sections that have real data", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);
    expect(wrapper.findAll("[role=tab]").map((tab) => tab.text())).toEqual(["Overview", "Audience", "Activity"]);
  });

  it("lets a draft continue in the wizard", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);
    expect(wrapper.find("[data-testid=continue-editing]").attributes("href")).toBe("/campaigns/c1/edit");
  });

  it("does not offer editing once a campaign has left drafting", async () => {
    const { wrapper } = await open("/campaigns/c1", { ...routes, "/api/campaigns/c1": { ...campaign, status: "COMPLETED" } });
    expect(wrapper.find("[data-testid=continue-editing]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=status-badge]").text()).toBe("Completed");
  });

  it("switches between sections", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);

    await wrapper.find("#tab-audience").trigger("click");
    expect(wrapper.find("#panel-audience").text()).toContain("İstanbul");
    expect(wrapper.find("#tab-audience").attributes("aria-selected")).toBe("true");

    await wrapper.find("#tab-activity").trigger("click");
    expect(wrapper.findAll("[data-testid=activity-entry]")).toHaveLength(2);
    expect(wrapper.find("#panel-activity").text()).toContain("Audience refined");
  });

  it("still opens when the activity cannot load", async () => {
    const { wrapper } = await open("/campaigns/c1", { "/api/campaigns/c1": campaign });
    expect(wrapper.text()).toContain("1,284");

    await wrapper.find("#tab-activity").trigger("click");
    expect(wrapper.find("[role=alert]").text()).toContain("could not load the activity");
  });

  it("explains a campaign that does not exist", async () => {
    const { wrapper } = await open("/campaigns/c1", {});
    expect(wrapper.find("[role=alert]").text()).toContain("could not open");
  });
});

describe("campaign name", () => {
  function stubRename(result: { status: number; body: unknown }) {
    const base = vi.mocked(fetch).getMockImplementation()!;
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) =>
      init?.method === "PATCH"
        ? ({ ok: result.status < 400, status: result.status, json: async () => result.body } as Response)
        : base(url, init as never),
    );
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  const routes = { "/api/campaigns/c1": campaign, "/api/campaigns/c1/activity": { activity } };

  it("heads the detail page, marks a suggestion and keeps the brief visible", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);

    expect(wrapper.find("h1").text()).toBe("Cart Recovery");
    expect(wrapper.find("[data-testid=campaign-name]").text()).toContain("AI suggested");
    expect(wrapper.find("[data-testid=brief-text]").text()).toBe("Istanbul cart reminder");
  });

  it("does not call a name someone chose a suggestion", async () => {
    const { wrapper } = await open("/campaigns/c1", { ...routes, "/api/campaigns/c1": { ...campaign, name_source: "user" } });
    expect(wrapper.find("[data-testid=campaign-name]").text()).not.toContain("AI suggested");
  });

  it("renames and refreshes the page", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);
    const fetchMock = stubRename({ status: 200, body: { ...campaign, name: "Black Friday", name_source: "user" } });

    await wrapper.find("[data-testid=rename]").trigger("click");
    await wrapper.find("#campaign-name-input").setValue("Black Friday");
    await wrapper.find("[data-testid=campaign-name] form").trigger("submit");
    await flushPromises();

    const patch = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
    expect(JSON.parse(patch[1]!.body as string)).toEqual({ name: "Black Friday" });
    expect(wrapper.find("h1").text()).toBe("Black Friday");
  });

  it("keeps the typed name and explains when renaming fails", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);
    stubRename({ status: 400, body: { error: "Write a campaign name of up to 80 characters." } });

    await wrapper.find("[data-testid=rename]").trigger("click");
    await wrapper.find("#campaign-name-input").setValue("Too long");
    await wrapper.find("[data-testid=campaign-name] form").trigger("submit");
    await flushPromises();

    expect(wrapper.find("[role=alert]").text()).toContain("up to 80 characters");
    expect((wrapper.find("#campaign-name-input").element as HTMLInputElement).value).toBe("Too long");
  });

  it("cancels without saving", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);
    await wrapper.find("[data-testid=rename]").trigger("click");
    await wrapper.find("#campaign-name-input").setValue("Something else");
    await wrapper.findAll("button").find((button) => button.text() === "Cancel")!.trigger("click");

    expect(wrapper.find("h1").text()).toBe("Cart Recovery");
  });
});

describe("activity wording", () => {
  it("reads a rename as a sentence", () => {
    expect(describeActivity({ ...activity[0], type: "campaign.renamed", details: { name: "Black Friday" } }).title).toBe('Renamed to "Black Friday"');
  });

  it("keeps events it does not recognise rather than hiding them", () => {
    expect(describeActivity({ ...activity[0], type: "campaign.something_new" }).title).toBe("campaign.something_new");
  });

  it("mentions unsupported conditions", () => {
    const entry = { ...activity[1], details: { ...activity[1].details, unsupported_conditions: 2 } };
    expect(describeActivity(entry).detail).toContain("2 unsupported conditions");
  });
});

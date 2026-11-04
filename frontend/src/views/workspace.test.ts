import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "../App.vue";
import type { ActivityEntry, Campaign, CampaignSummary, Overview } from "../api/campaigns";
import { describeActivity } from "../lib/activity";
import { navigate } from "../lib/navigation";
import CampaignsView from "./CampaignsView.vue";
import AnalyticsView from "./AnalyticsView.vue";
import AudiencesView from "./AudiencesView.vue";
import DonutChart from "../components/DonutChart.vue";
import { defaultChannels, storedAutoRefresh } from "../lib/preferences";
import ChannelsView from "./ChannelsView.vue";
import HelpView from "./HelpView.vue";
import SettingsView from "./SettingsView.vue";
import ComposeView from "./ComposeView.vue";
import DeliveryMonitorView from "./DeliveryMonitorView.vue";
import TemplatesView from "./TemplatesView.vue";

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
  { campaign_id: "c1", campaign_name: "Cart Recovery", type: "campaign.created", actor_type: "user", actor: "marketer@example.com", details: {}, occurred_at: "2026-11-03T12:00:00Z" },
  {
    actor_type: "system",
    actor: null,
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
  channels: ["sms", "email"],
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
  const fetchMock = stubApi({ "/api/session": { identity: "marketer@example.com" }, "/api/system": { checks: { database: "ok" } }, "/api/config": { llm_provider: "mock", llm_model: "m" }, ...routes });
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

  it("shows more campaigns past the first page and how many there are", async () => {
    const everyone = Array.from({ length: 130 }, (_, index) => summary({ id: `c${index}`, name: `Campaign ${index}` }));
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const offset = Number(new URL(url, "http://test").searchParams.get("offset") ?? 0);
      // As the backend does: never more than 100 at once. The second page repeats one row, as it does after a campaign is created.
      const start = offset ? offset - 1 : 0;
      return { ok: true, status: 200, json: async () => ({ campaigns: everyone.slice(start, start + 100), total: everyone.length }) } as Response;
    }));
    const wrapper = mount(CampaignsView);
    await flushPromises();
    expect(wrapper.find("[data-testid=campaign-count]").text()).toBe("Showing 100 of 130 campaigns");

    await wrapper.find("[data-testid=more-campaigns]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=campaign-count]").text()).toBe("130 campaigns");
    expect(wrapper.find("[data-testid=more-campaigns]").exists()).toBe(false);
  });

  it("leads from each total to the campaigns it counts", async () => {
    const { wrapper } = await open("/", {
      "/api/overview": { counts: { total: 24, drafts: 3, active: 2, completed: 18, failed: 1 }, recent_campaigns: [summary()], recent_activity: activity },
    });
    const links = wrapper.findAll("[aria-label='Campaign totals'] a").map((link) => link.attributes("href"));
    expect(links).toEqual(["/campaigns", "/campaigns?group=active", "/campaigns?group=completed", "/campaigns?group=drafts", "/campaigns?group=failed"]);
  });

  it("opens the filter the address names and keeps it there", async () => {
    window.history.replaceState({}, "", "/campaigns?group=failed");
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ campaigns: [], total: 0 }) }) as Response));
    const wrapper = mount(CampaignsView);
    await flushPromises();
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("group=failed"))).toBe(true);
    await wrapper.findAll(".filters button")[0].trigger("click");
    expect(window.location.search).toBe("");
    wrapper.unmount();
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

describe("channels", () => {
  it("shows what each channel's calls did and that one provider serves both", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        channels: [
          { channel: "sms", provider: "Mock provider", last_accepted_at: "2026-11-03T12:00:00Z", calls_last_hour: 12, not_accepted_last_hour: 3, busiest_second_last_hour: 9 },
          { channel: "email", provider: "Mock provider", last_accepted_at: null, calls_last_hour: 0, not_accepted_last_hour: 0, busiest_second_last_hour: 0 },
        ],
      }),
    }) as Response));
    const wrapper = mount(ChannelsView);
    await flushPromises();
    const sms = wrapper.find("[data-testid=channel-sms]").text();
    expect(sms).toContain("Mock provider");
    expect(sms).toContain("12");
    expect(wrapper.find("[data-testid=busiest-sms]").text()).toBe("9");
    expect(wrapper.find("[data-testid=channel-email]").text()).toContain("Nothing yet");
    expect(wrapper.find("[data-testid=channels]").text()).toContain("go up and down together");
  });
});

describe("audiences", () => {
  const saved = { id: "a1", name: "Istanbul abandoners", conditions: [{ field: "city", label: "City is", value: "Istanbul" }], audience: { total: 40, sms: 30, email: 20 }, counted_at: "2026-11-03T12:00:00Z", source_campaign_id: "c1", source_campaign_name: "Winter", created_by: null, created_at: "2026-11-03T12:00:00Z" };

  it("shows the rule and last count, counts again, and deletes only after asking", async () => {
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return { ok: true, status: 204, json: async () => { throw new Error("empty"); } } as unknown as Response;
      const body = url.endsWith("/recount") ? { ...saved, audience: { total: 44, sms: 30, email: 20 } } : { audiences: [saved] };
      return { ok: true, status: 200, json: async () => body } as Response;
    });
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = mount(AudiencesView);
    await flushPromises();
    expect(wrapper.text()).toContain("City is Istanbul");
    expect(wrapper.find("[data-testid=audience-total]").text()).toBe("40");
    expect(wrapper.find("[data-testid=use-audience]").attributes("href")).toBe("/campaigns/new?audience=a1");

    await wrapper.find("[data-testid=recount-audience]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=audience-total]").text()).toBe("44");
    expect(wrapper.find("[data-testid=audience-recounted]").text()).toBe("Counted again. Changed: customers 40 to 44.");

    fetchMock.mockImplementationOnce(async () => ({ ok: true, status: 200, json: async () => ({ ...saved, audience: { total: 44, sms: 30, email: 20 } }) }) as Response);
    await wrapper.find("[data-testid=recount-audience]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=audience-recounted]").text()).toBe("Counted again. Still 44 customers, nothing changed.");

    await wrapper.find("[data-testid=delete-audience]").trigger("click");
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await wrapper.find("[data-testid=confirm-delete-audience]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=no-audiences]").exists()).toBe(true);
  });
});

describe("a new campaign from a saved audience", () => {
  it("brings the rule, asks only for the message and can drop the audience", async () => {
    window.history.replaceState({}, "", "/campaigns/new?audience=a1");
    const saved = { id: "a1", name: "Istanbul abandoners", conditions: [{ field: "city", label: "City is", value: "Istanbul" }], audience: { total: 40, sms: 30, email: 20 }, counted_at: "2026-11-03T12:00:00Z", source_campaign_id: null, source_campaign_name: null, created_by: null, created_at: "2026-11-03T12:00:00Z" };
    const fetchMock = vi.fn(async (url: string, _init?: RequestInit) => ({ ok: true, status: url === "/api/campaigns" ? 201 : 200, json: async () => (url === "/api/campaigns" ? { id: "new" } : { audiences: [saved], notifications: [] }) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = mount(ComposeView);
    await flushPromises();
    expect(wrapper.find("[data-testid=chosen-audience]").text()).toContain("City is Istanbul");
    expect(wrapper.text()).toContain("What should the message say?");
    expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("");

    await wrapper.find("textarea").setValue("Free shipping this week.");
    await wrapper.find("form").trigger("submit");
    await flushPromises();
    const created = fetchMock.mock.calls.find(([url]) => url === "/api/campaigns");
    expect(JSON.parse(String(created?.[1]?.body))).toMatchObject({ brief: "Free shipping this week.", saved_audience_id: "a1" });
    window.history.replaceState({}, "", "/");
  });
});

describe("analytics", () => {
  it("adds channels up, redraws on a new period and lists the worst campaign first", async () => {
    window.history.replaceState({}, "", "/analytics");
    const channel = { channel: "sms", planned: 10, skipped: 2, accepted: 6, failed: 1, unknown: 1, on_the_way: 0, delivered: 5, bounced: 1, waiting: 0, opened: 0 };
    const fetchMock = vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () => (url.endsWith("days=7")
        ? { days: 7, channels: [], calls: [], campaigns: [] }
        : { days: 30, channels: [channel, { ...channel, channel: "email" }], calls: [{ day: "2026-11-03T00:00:00Z", accepted: 3, not_accepted: 1 }], campaigns: [{ id: "c1", name: "Winter", status: "COMPLETED_WITH_FAILURES", accepted: 12, failed: 2, unknown: 2 }] }),
    }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = mount(AnalyticsView);
    await flushPromises();
    expect(fetchMock).toHaveBeenCalledWith("/api/analytics?days=30", undefined);
    expect(wrapper.find("[data-testid=analytics-result-failed]").text()).toContain("4");
    expect(wrapper.find("[data-testid=calls-table]").text()).toContain("25.0%");
    expect(wrapper.find("[data-testid=analytics-campaign]").text()).toContain("25.0%");
    await wrapper.find("[data-testid=calls-day]").trigger("mouseenter");
    expect(wrapper.find("[data-testid=calls-readout]").text()).toContain("3 accepted, 1 not accepted, 25.0% error rate");

    await wrapper.find("[data-testid=analytics-range]").setValue("7");
    await flushPromises();
    expect(window.location.search).toBe("?days=7");
    expect(wrapper.find("[data-testid=analytics-empty]").exists()).toBe(true);
  });
});

describe("settings", () => {
  it("shows what the app runs with and remembers the monitor's refresh", async () => {
    window.localStorage.removeItem("delivery-monitor-refresh");
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ llm_provider: "openai", llm_model: "gpt-x", rate_limit_per_second: 100, max_attempts: 5, customers: 6000 }) }) as Response));
    const wrapper = mount(SettingsView);
    await flushPromises();
    expect(wrapper.find("[data-testid=settings-provider]").text()).toBe("OpenAI");
    expect(wrapper.find("[data-testid=settings-model]").text()).toBe("gpt-x");
    expect(wrapper.find("[data-testid=settings-rate]").text()).toContain("100");
    expect(wrapper.find("[data-testid=settings-attempts]").text()).toBe("5");
    expect(wrapper.find("[data-testid=settings-customers]").text()).toContain("6,000");

    await wrapper.find("[data-testid=settings-auto-refresh]").setValue("30");
    expect(wrapper.find("[data-testid=settings-saved]").exists()).toBe(true);
    expect(storedAutoRefresh()).toBe("30");

    await wrapper.find("[data-testid=settings-theme]").setValue("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    await wrapper.find("[data-testid=settings-theme]").setValue("light");
    expect(document.documentElement.dataset.theme).toBe("light");

    await wrapper.find("[data-testid=settings-channel-sms]").setValue(false);
    expect(defaultChannels()).toEqual(["email"]);
    expect(wrapper.find("[data-testid=settings-channel-email]").attributes("disabled")).toBeDefined();
    await wrapper.find("[data-testid=settings-channel-sms]").setValue(true);
    expect(defaultChannels()).toEqual(["sms", "email"]);
    window.localStorage.clear();
  });
});

describe("help", () => {
  it("explains every delivery status and links the documents", () => {
    const wrapper = mount(HelpView);
    expect(wrapper.find("[data-testid=help-statuses]").text()).toContain("does not confirm delivery");
    expect(wrapper.find("[data-testid=help-documents]").findAll("a")).toHaveLength(2);
  });
});

describe("donut chart", () => {
  it("keeps a tiny slice visible, says so honestly and fills the ring", () => {
    const wrapper = mount(DonutChart, {
      props: { title: "By channel", testPrefix: "c", unit: ["delivery", "deliveries"] as [string, string], slices: [{ key: "sms", label: "SMS", count: 1, color: "#000" }, { key: "email", label: "Email", count: 999, color: "#111" }, { key: "none", label: "None", count: 0, color: "#222" }] },
    });
    const length = (key: string) => Number(wrapper.find(`[data-testid=c-arc-${key}]`).attributes("stroke-dasharray")?.split(" ")[0]);
    expect(length("sms")).toBeGreaterThanOrEqual(2);
    expect(length("none")).toBe(0);
    expect(length("sms") + length("email")).toBeCloseTo(98, 5);
    expect(wrapper.find("[data-testid=c-sms]").text()).toContain("<1%");
    expect(wrapper.find("[data-testid=c-email]").text()).toContain(">99%");
  });
});

describe("delivery monitor refresh", () => {
  it("refreshes on a click, on the chosen interval and not when turned off", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    window.localStorage.removeItem("delivery-monitor-refresh");
    window.history.replaceState({}, "", "/delivery-monitor");
    const fetchMock = vi.fn(async (_url: string) => ({ ok: true, status: 200, json: async () => ({ counts: {}, channels: {}, reasons: [], total: 0, deliveries: [], paused: [] }) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    const calls = () => fetchMock.mock.calls.filter(([url]) => String(url).startsWith("/api/delivery-monitor")).length;
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();
    expect(calls()).toBe(1);
    expect(wrapper.find("[data-testid=monitor-updated]").text()).toContain("next in 10s");
    expect((wrapper.find("[data-testid=monitor-auto-refresh]").element as HTMLSelectElement).value).toBe("10");

    await wrapper.find("[data-testid=monitor-refresh]").trigger("click");
    await flushPromises();
    expect(calls()).toBe(2);

    vi.advanceTimersByTime(4_000);
    await flushPromises();
    expect(wrapper.find("[data-testid=monitor-updated]").text()).toContain("next in 6s");
    vi.advanceTimersByTime(6_000);
    await flushPromises();
    expect(calls()).toBe(3);

    await wrapper.find("[data-testid=monitor-auto-refresh]").setValue("30");
    vi.advanceTimersByTime(29_000);
    await flushPromises();
    expect(calls()).toBe(3);
    vi.advanceTimersByTime(1_000);
    await flushPromises();
    expect(calls()).toBe(4);

    await wrapper.find("[data-testid=monitor-auto-refresh]").setValue("0");
    vi.advanceTimersByTime(120_000);
    await flushPromises();
    expect(calls()).toBe(4);
    expect(window.localStorage.getItem("delivery-monitor-refresh")).toBe("0");
    expect(wrapper.find("[data-testid=monitor-updated]").text()).not.toContain("next in");

    wrapper.unmount();
    window.localStorage.removeItem("delivery-monitor-refresh");
    vi.useRealTimers();
  });
});

describe("delivery monitor", () => {
  const row = { id: "d1", campaign_id: "c1", campaign_name: "Winter sale", user_id: 7, first_name: "Ayşe", channel: "sms", status: "FAILED", calls: 3, response_code: 500, reason: "server_error", next_attempt_at: "2026-11-03T12:00:00Z", updated_at: "2026-11-03T12:00:00Z" };
  const answer = (overrides = {}) => ({ counts: { QUEUED: 4, SENDING: 0, RETRYING: 2, UNKNOWN: 0, FAILED: 1 }, channels: { sms: 5, email: 2 }, reasons: [{ reason: "", count: 6 }, { reason: "server_error", count: 1 }], total: 1, deliveries: [row], paused: [], ...overrides });
  const stubMonitor = (body: (url: string) => unknown) => {
    const fetchMock = vi.fn(async (url: string) => ({ ok: true, status: 200, json: async () => body(url) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  };

  it("counts each status, links to the campaign and opens the attempt history", async () => {
    window.history.replaceState({}, "", "/delivery-monitor");
    stubMonitor((url) => (url.includes("/attempts") ? { attempts: [{ attempt: 1, response_code: 500, outcome: "server_error", error: "", occurred_at: "2026-11-03T12:00:00Z" }] } : answer()));
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();

    expect(wrapper.find("[data-testid=count-QUEUED]").text()).toContain("4");
    expect(wrapper.find("[data-testid=monitor-campaign]").attributes("href")).toBe("/campaigns/c1?tab=deliveries&status=FAILED&channel=sms");
    await wrapper.find("[data-testid=monitor-history]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=monitor-attempts]").text()).toContain("500");
  });

  it("draws each breakdown to scale, redraws it on filter and filters from a slice", async () => {
    window.history.replaceState({}, "", "/delivery-monitor");
    const fetchMock = stubMonitor((url) =>
      url.includes("reason=server_error") ? answer({ channels: { sms: 1, email: 3 }, reasons: [{ reason: "server_error", count: 4 }] }) : answer(),
    );
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();
    expect(wrapper.find("[data-testid=channel-sms]").text()).toContain("71%");
    const arc = () => wrapper.find("[data-testid=channel-arc-email]");
    const before = arc().attributes("stroke-dasharray");

    // Every row of a legend filters, and the choice shows in the filters under the charts.
    await wrapper.find("[data-testid=reason-none]").trigger("click");
    await flushPromises();
    expect(fetchMock).toHaveBeenCalledWith("/api/delivery-monitor?limit=100&reason=none", undefined);
    expect((wrapper.find("[data-testid=monitor-reason]").element as HTMLSelectElement).value).toBe("none");
    await wrapper.find("[data-testid=reason-none]").trigger("click");
    await flushPromises();
    await wrapper.find("[data-testid=count-UNKNOWN]").trigger("click");
    await flushPromises();
    expect((wrapper.find("[data-testid=monitor-status]").element as HTMLSelectElement).value).toBe("UNKNOWN");
    await wrapper.find("[data-testid=monitor-status]").setValue("");
    await flushPromises();
    await wrapper.find("[data-testid=reason-server_error]").trigger("click");
    await flushPromises();
    expect(fetchMock).toHaveBeenCalledWith("/api/delivery-monitor?limit=100&reason=server_error", undefined);
    expect(window.location.search).toBe("?reason=server_error");
    // The same element, so the browser slides it to its new share.
    expect(arc().attributes("stroke-dasharray")).not.toBe(before);
    expect(arc().attributes("stroke-dasharray")?.startsWith("74")).toBe(true);
    expect(wrapper.find("[data-testid=channel-email]").text()).toContain("75%");

    await wrapper.find("[data-testid=channel-arc-email]").trigger("click");
    await flushPromises();
    expect(window.location.search).toBe("?channel=email&reason=server_error");
    await wrapper.find("[data-testid=channel-email]").trigger("click");
    await flushPromises();
    expect(window.location.search).toBe("?reason=server_error");
  });

  it("filters on the server and keeps the filters in the address", async () => {
    window.history.replaceState({}, "", "/delivery-monitor?channel=sms&outcome=timeout&within_hours=1");
    const fetchMock = stubMonitor(() => answer());
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();
    expect(fetchMock).toHaveBeenCalledWith("/api/delivery-monitor?limit=100&channel=sms&outcome=timeout&within_hours=1", undefined);

    await wrapper.find("[data-testid=count-FAILED]").trigger("click");
    await flushPromises();
    expect(window.location.search).toBe("?status=FAILED&channel=sms&outcome=timeout&within_hours=1");
    await wrapper.find("[data-testid=count-FAILED]").trigger("click");
    await flushPromises();
    expect(window.location.search).toBe("?channel=sms&outcome=timeout&within_hours=1");
    await wrapper.find("[data-testid=monitor-clear]").trigger("click");
    await flushPromises();
    expect(window.location.search).toBe("");
  });

  it("names a paused campaign and says when nothing needs watching", async () => {
    window.history.replaceState({}, "", "/delivery-monitor");
    stubMonitor(() => answer({ total: 0, deliveries: [], paused: [{ id: "c2", name: "Bursa" }] }));
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();
    expect(wrapper.find("[data-testid=monitor-paused]").text()).toContain("Bursa");
    expect(wrapper.find("[data-testid=monitor-paused] a").attributes("href")).toBe("/campaigns/c2");
    expect(wrapper.find("[data-testid=monitor-empty]").text()).toBe("Nothing is waiting and nothing has failed.");
  });

  it("shows more past the first page", async () => {
    window.history.replaceState({}, "", "/delivery-monitor");
    const everyone = Array.from({ length: 130 }, (_, index) => ({ ...row, id: `d${index}` }));
    stubMonitor((url) => {
      const address = new URL(url, "http://test");
      const offset = Number(address.searchParams.get("offset") ?? 0);
      return answer({ total: 130, deliveries: everyone.slice(offset, offset + Number(address.searchParams.get("limit"))) });
    });
    const wrapper = mount(DeliveryMonitorView);
    await flushPromises();
    expect(wrapper.find("[data-testid=monitor-count]").text()).toBe("Showing 100 of 130 deliveries");
    await wrapper.find("[data-testid=monitor-more]").trigger("click");
    await flushPromises();
    expect(wrapper.findAll("[data-testid=monitor-row]")).toHaveLength(130);
    expect(wrapper.find("[data-testid=monitor-more]").exists()).toBe(false);
  });
});

describe("templates", () => {
  it("keeps images and the brand kit as tabs of one Templates page", async () => {
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const body = url === "/api/session" ? { identity: "marketer@example.com" } : url === "/api/system" ? { checks: { database: "ok" } } : url === "/api/config" ? { llm_provider: "mock", llm_model: "m" } : url === "/api/assets" ? { assets: [] } : { notifications: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    }));
    navigate("/templates/images");
    const wrapper = mount(App);
    await flushPromises();
    expect(wrapper.find("[data-testid=template-tabs] [aria-current=page]").text()).toBe("Images");
    expect(wrapper.find("[data-testid=asset-library]").exists()).toBe(true);
    expect(wrapper.findAll("a").filter((link) => link.text() === "Brand kit").map((link) => link.attributes("href"))).toEqual(["/templates/brand"]);
    wrapper.unmount();
  });

  it("shows each design from the backend, its real use, and starts a campaign from it", async () => {
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      const body = url === "/api/email-templates"
        ? { templates: [{ id: "classic", version: "aaa", campaigns: 3 }, { id: "banner", version: "bbb", campaigns: 1 }, { id: "offer", version: "ccc", campaigns: 0 }] }
        : url === "/api/email-designs"
          ? { designs: [] }
          : { subject: "s", html: `<p>${JSON.parse(String(init?.body)).email_template}</p>`, text: "t" };
      return { ok: true, status: 200, json: async () => body } as Response;
    }));
    const wrapper = mount(TemplatesView);
    await flushPromises();
    await flushPromises();

    const banner = wrapper.find("[data-testid=template-banner]");
    expect(banner.find("[data-testid=template-use]").text()).toContain("Used by 1 campaign ");
    expect(banner.find("[data-testid=template-use]").text()).toContain("bbb");
    expect(banner.find("[data-testid=template-frame]").attributes("srcdoc")).toBe("<p>banner</p>");
    expect(banner.find("[data-testid=use-template]").attributes("href")).toBe("/campaigns/new?template=banner");
  });

  it("lists your own templates with a real preview, and deletes one only after asking", async () => {
    const design = { id: "d1", name: "Winter", accent: "#112233", page: "#FFFFFF", button: "pill", corners: 8, font: "sans", blocks: [], version: "ver1", campaigns: 2, updated_at: "" };
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") return { ok: true, status: 204, json: async () => null } as Response;
      const body = url === "/api/email-templates"
        ? { templates: [] }
        : url === "/api/email-designs"
          ? { designs: [design] }
          : (() => {
            const sent = JSON.parse(String(init?.body)).email_design;
            // The real API refuses fields it does not expect, as saved ones like these.
            if (sent && ("version" in sent || "campaigns" in sent || "updated_at" in sent)) return { error: "not valid" };
            return { subject: "s", html: `<p>${sent?.name ?? "built-in"}</p>`, text: "t" };
          })();
      return { ok: true, status: 200, json: async () => body } as Response;
    });
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = mount(TemplatesView);
    await flushPromises();
    await flushPromises();

    const card = wrapper.find("[data-testid=design-d1]");
    expect(card.find("[data-testid=template-frame]").attributes("srcdoc")).toBe("<p>Winter</p>");
    expect(card.find("[data-testid=template-use]").text()).toContain("Used by 2 campaigns");
    expect(card.find("[data-testid=use-template]").attributes("href")).toBe("/campaigns/new?template=custom:d1");

    await card.find("[data-testid=delete-design]").trigger("click");
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await wrapper.find("[data-testid=confirm-delete-design]").trigger("click");
    await flushPromises();
    expect(fetchMock).toHaveBeenCalledWith("/api/email-designs/d1", expect.objectContaining({ method: "DELETE" }));
    expect(wrapper.find("[data-testid=no-designs]").exists()).toBe(true);
  });

  it("says so when the templates cannot be loaded", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: false, status: 500, json: async () => ({ error: "no" }) }) as Response));
    const wrapper = mount(TemplatesView);
    await flushPromises();
    expect(wrapper.text()).toContain("We could not load the templates");
    expect(wrapper.find("[data-testid=use-template]").exists()).toBe(false);
  });
});

describe("campaign list", () => {
  beforeEach(() => vi.useFakeTimers());

  async function settle() {
    await vi.advanceTimersByTimeAsync(1000);
    await flushPromises();
  }

  it("shows real progress and a dash when nothing was dispatched", async () => {
    const { wrapper } = await open("/campaigns", {
      "/api/campaigns": {
        campaigns: [
          summary(),
          summary({ id: "c2", status: "DISPATCHING", channels: ["email", "sms"], deliveries_total: 200, deliveries_processed: 137 }),
        ],
        total: 2,
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

  it("filters by channel and age and sorts on the server", async () => {
    const { wrapper, fetchMock } = await open("/campaigns", { "/api/campaigns": { campaigns: [summary()], total: 1 } });
    await settle();

    await wrapper.find("#campaign-channel").setValue("sms");
    await settle();
    expect(fetchMock).toHaveBeenCalledWith("/api/campaigns?channel=sms", undefined);
    await wrapper.find("#campaign-age").setValue("30");
    await settle();
    expect(fetchMock).toHaveBeenCalledWith("/api/campaigns?channel=sms&created_within_days=30", undefined);
    await wrapper.find("#campaign-sort").setValue("name");
    await settle();
    expect(fetchMock).toHaveBeenCalledWith("/api/campaigns?channel=sms&created_within_days=30&sort=name", undefined);
  });

  it("filters by group and search on the server", async () => {
    const { wrapper, fetchMock } = await open("/campaigns", {
      "/api/campaigns": { campaigns: [summary()], total: 1 },
      "/api/campaigns?group=drafts": { campaigns: [], total: 0 },
      "/api/campaigns?group=drafts&q=winter+sale": { campaigns: [], total: 0 },
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
    const { wrapper } = await open("/campaigns", { "/api/campaigns": { campaigns: [], total: 0 } });
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
    expect(wrapper.find("[data-testid=audience-final]").exists()).toBe(false);
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

  it("heads the detail page and opens the overview with the brief", async () => {
    const { wrapper } = await open("/campaigns/c1", routes);

    expect(wrapper.find("h1").text()).toBe("Cart Recovery");
    expect(wrapper.find("[data-testid=campaign-name]").text()).not.toContain("AI suggested");
    expect(wrapper.find("[data-testid=brief-text]").text()).toBe("Istanbul cart reminder");
    expect(wrapper.find("#panel-overview").element.firstElementChild?.getAttribute("aria-label")).toBe("Campaign brief");
  });

  it("marks a suggested name while the campaign is being set up", async () => {
    const { wrapper } = await open("/campaigns/c1/edit", routes);

    expect(wrapper.find("[data-testid=campaign-name]").text()).toContain("AI suggested");
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

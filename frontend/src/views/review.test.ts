import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Campaign, Progress } from "../api/campaigns";
import { currentPath, navigate } from "../lib/navigation";
import { systemHealth } from "../lib/systemHealth";
import DeliveriesPanel from "../components/DeliveriesPanel.vue";
import CampaignDetailView from "./CampaignDetailView.vue";
import ReviewView from "./ReviewView.vue";

const ready: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Reminder",
  status: "READY_FOR_REVIEW",
  channels: ["sms", "email"],
  version: 1,
  proposal: { assumptions: [], unsupported_conditions: [] },
  conditions: [{ field: "city", label: "City is", value: "İstanbul" }],
  audience: { total: 9, sms: 7, email: 6 },
  blockers: [],
  content: { sms: "Hi {{first_name}}", email_subject: "Subject", email_preheader: "", email_headline: "", email_body: "Body", email_cta_label: "", email_cta_url: "", email_template: "", edited: [] },
  content_limits: { sms_name_length: 21, email_name_length: 21 },
  content_warnings: [],
  created_at: "2026-11-03T12:00:00Z",
  updated_at: "2026-11-03T12:00:00Z",
};
const progress: Progress = {
  total: 18,
  processed: 18,
  attempts: 15,
  by_status: { SENT: 12, FAILED: 1, SKIPPED: 5 },
  by_channel: { sms: { SENT: 6, SKIPPED: 2 }, email: { SENT: 6, FAILED: 1, SKIPPED: 3 } },
  responses: { "202": 12, "500": 3 },
};

type Route = { status?: number; body: unknown };

class FakeEventSource {
  static last: FakeEventSource | null = null;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  listeners: Record<string, (event: MessageEvent<string>) => void> = {};
  constructor(public url: string) {
    FakeEventSource.last = this;
  }
  addEventListener(name: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners[name] = listener;
  }
  close() {
    this.closed = true;
  }
  emit(name: string, data: unknown) {
    this.listeners[name]({ data: JSON.stringify(data) } as MessageEvent<string>);
  }
}

function stub(routes: Record<string, Route>) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const route = routes[`${init?.method ?? "GET"} ${url}`] ?? { status: 404, body: { error: "not stubbed" } };
    const status = route.status ?? 200;
    return { ok: status < 400, status, json: async () => route.body } as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function called(fetchMock: ReturnType<typeof stub>, method: string, url: string) {
  return fetchMock.mock.calls.filter(([calledUrl, init]) => calledUrl === url && init?.method === method);
}

beforeEach(() => {
  FakeEventSource.last = null;
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  navigate("/");
  systemHealth.value = null;
  vi.unstubAllGlobals();
});

describe("review", () => {
  async function open(campaign: Campaign = ready, extra: Record<string, Route> = {}) {
    const fetchMock = stub({
      "GET /api/campaigns/c1": { body: campaign },
      "POST /api/campaigns/c1/approve": { body: { ...campaign, status: "APPROVED" } },
      "POST /api/campaigns/c1/dispatch": { status: 202, body: { ...campaign, status: "DISPATCHING" } },
      "POST /api/campaigns/c1/schedule": { body: { ...campaign, status: "SCHEDULED" } },
      ...extra,
    });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();
    return { wrapper, fetchMock };
  }

  it("shows who receives what before anything is approved", async () => {
    const { wrapper, fetchMock } = await open();
    expect(wrapper.find("[data-testid=review-conditions]").text()).toContain("İstanbul");
    expect(wrapper.find("[data-testid=review-sms]").text()).toContain("Hi there");
    expect(wrapper.find("[data-testid=review-email]").text()).toContain("Subject");
    expect(wrapper.find("[data-testid=approve]").text()).toBe("Approve and send 13 messages");
    expect(called(fetchMock, "POST", "/api/campaigns/c1/approve")).toHaveLength(0);
  });

  it("shows every language's messages and who reads them", async () => {
    const content = ready.content!;
    const { wrapper } = await open({
      ...ready,
      content: { ...content, language: "tr", sms: "Merhaba", translations: [{ ...content, language: "en", sms: "Hello" }] },
      languages: [
        { language: "tr", total: 7, sms: 6, email: 4, sms_name_length: 7, email_name_length: 7 },
        { language: "en", total: 2, sms: 1, email: 2, sms_name_length: 21, email_name_length: 21 },
      ],
    });
    expect(wrapper.find("[data-testid=review-tr]").text()).toContain("Merhaba");
    expect(wrapper.find("[data-testid=review-tr]").text()).toContain("10 messages");
    expect(wrapper.find("[data-testid=review-en]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("Open English before approving");
    await wrapper.find("[data-testid=review-language-en]").trigger("click");
    expect(wrapper.find("[data-testid=review-en]").text()).toContain("Hello");
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeUndefined();
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("Each person gets Turkish or English");
  });

  const twoLanguages: Campaign = {
    ...ready,
    content: { ...ready.content!, language: "tr", sms: "Merhaba {{first_name}}", translations: [{ ...ready.content!, language: "en", sms: "Hello {{first_name}}" }] },
    languages: [
      { language: "tr", total: 7, sms: 6, email: 4, sms_name_length: 7, email_name_length: 7 },
      { language: "en", total: 2, sms: 1, email: 2, sms_name_length: 21, email_name_length: 21 },
    ],
  };
  const samples = {
    "GET /api/campaigns/c1/sample-recipients": {
      body: {
        recipients: [
          { id: 1, label: "Can", first_name: "Can", language: "tr" },
          { id: 2, label: "Emily", first_name: "Emily", language: "en" },
        ],
      },
    },
  };

  it("marks the language it opens with as selected", async () => {
    const { wrapper } = await open(twoLanguages);
    expect(wrapper.find("[data-testid=review-language-tr]").classes()).toContain("tab--active");
    expect(wrapper.find("[data-testid=review-language-en]").classes()).not.toContain("tab--active");
  });

  it("previews each language with the name of someone who reads it", async () => {
    const { wrapper } = await open(twoLanguages, samples);
    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Merhaba Can");
    await wrapper.find("[data-testid=review-language-en]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Hello Emily");
  });

  it("cannot be approved while a language has no messages", async () => {
    const { wrapper } = await open({
      ...ready,
      content: { ...ready.content!, language: "tr" },
      languages: [
        { language: "tr", total: 7, sms: 6, email: 4, sms_name_length: 7, email_name_length: 7 },
        { language: "en", total: 2, sms: 1, email: 2, sms_name_length: 21, email_name_length: 21 },
      ],
    });
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("English has no messages");
  });

  it("shows a note about several fields once, under its own title", async () => {
    const message = "Each person receives the language they prefer.";
    const { wrapper } = await open({
      ...ready,
      content_warnings: [
        { field: "sms", reason: "not_applied", message },
        { field: "email_body", reason: "not_applied", message },
      ],
    });
    expect(wrapper.text().split(message)).toHaveLength(2);
    expect(wrapper.text()).toContain("Not done");
    expect(wrapper.text()).not.toContain("Differs from the brief");
  });

  it("approves and sends, then opens the campaign", async () => {
    const { wrapper, fetchMock } = await open();
    await wrapper.find("[data-testid=approve]").trigger("click");
    await flushPromises();
    expect(called(fetchMock, "POST", "/api/campaigns/c1/approve")).toHaveLength(1);
    expect(called(fetchMock, "POST", "/api/campaigns/c1/dispatch")).toHaveLength(1);
    expect(called(fetchMock, "POST", "/api/campaigns/c1/schedule")).toHaveLength(0);
    expect(currentPath.value).toBe("/campaigns/c1");
  });

  it("does not send when a schedule is chosen", async () => {
    const { wrapper, fetchMock } = await open();
    await wrapper.findAll("input[type=radio]")[3].setValue(true);
    expect((wrapper.find("[data-testid=approve]").element as HTMLButtonElement).disabled).toBe(true);
    expect(wrapper.find("[data-testid=schedule-problem]").text()).toContain("day");

    await wrapper.find("[data-testid=day-mon]").trigger("click");
    await wrapper.find("[data-testid=day-wed]").trigger("click");
    await wrapper.find("[data-testid=schedule-time]").setValue("09:30");
    expect(wrapper.find("[data-testid=approve]").text()).toBe("Approve and schedule");
    await wrapper.find("[data-testid=approve]").trigger("click");
    await flushPromises();

    const [[, init]] = called(fetchMock, "POST", "/api/campaigns/c1/schedule");
    expect(JSON.parse(String(init?.body))).toMatchObject({ kind: "weekly", days: ["mon", "wed"], time: "09:30" });
    expect(called(fetchMock, "POST", "/api/campaigns/c1/dispatch")).toHaveLength(0);
  });

  it("says that repeated sends never reach anyone twice", async () => {
    const { wrapper } = await open();
    await wrapper.findAll("input[type=radio]")[2].setValue(true);
    expect(wrapper.find("[data-testid=once-per-person]").text()).toContain("Nobody gets this message twice");
  });

  it("can be retried when the second step fails", async () => {
    const { wrapper, fetchMock } = await open(ready, {
      "POST /api/campaigns/c1/dispatch": { status: 500, body: { error: "Something broke." } },
    });
    await wrapper.find("[data-testid=approve]").trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("Something broke.");
    expect(currentPath.value).not.toBe("/campaigns/c1");
    await wrapper.find("[data-testid=approve]").trigger("click");
    await flushPromises();
    expect(called(fetchMock, "POST", "/api/campaigns/c1/approve")).toHaveLength(2);
  });

  it("lists a discount check only when the campaign has a discount", async () => {
    const without = await open({ ...ready, brief: "Remind them about their cart", offer_percent: undefined });
    expect(without.wrapper.text()).not.toContain("Discount");

    const entered = await open({ ...ready, brief: "Remind them about their cart", offer_percent: 25 });
    expect(entered.wrapper.text()).toContain("Every message carries the 25% discount you entered.");
  });

  it("will not approve with blockers", async () => {
    const { wrapper } = await open({ ...ready, blockers: ["No users match."] });
    expect((wrapper.find("[data-testid=approve]").element as HTMLButtonElement).disabled).toBe(true);
  });

  it("is closed once the campaign moved on", async () => {
    const { wrapper } = await open({ ...ready, status: "DISPATCHING" });
    expect(wrapper.find("[data-testid=approve]").exists()).toBe(false);
    expect(wrapper.find("a").attributes("href")).toBe("/campaigns/c1");
  });
});

describe("review of a test", () => {
  const test: Campaign = { ...ready, ab_test: true, content: { ...ready.content!, variant_b: { ...ready.content!, variant: "B", sms: "Second {{first_name}}" } }, variant_a_percent: 30, variant_audience: [{ variant: "A", language: "tr", total: 50, sms: 40, email: 45 }, { variant: "B", language: "tr", total: 96, sms: 80, email: 90 }] };

  it("shows both versions before anyone approves", async () => {
    stub({ "GET /api/campaigns/c1": { body: test } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();
    expect(wrapper.find("[data-testid=review-variant-A]").exists()).toBe(true);
    await wrapper.find("[data-testid=review-version-B]").trigger("click");
    expect(wrapper.find("[data-testid=review-variant-B]").text()).toContain("Second");
    expect(wrapper.find("[data-testid=people-B]").text()).toBe("70% · 96 people");
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("names no winner");
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeUndefined();
  });

  it("warns without blocking when nobody lands on a side", async () => {
    stub({ "GET /api/campaigns/c1": { body: { ...test, variant_a_percent: 1, variant_audience: [{ variant: "B", language: "tr", total: 96, sms: 80, email: 90 }] } } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();
    await wrapper.find("[data-testid=review-version-B]").trigger("click");
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("Nobody in this audience lands on version A");
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeUndefined();
  });

  it("blocks approval without the second version", async () => {
    stub({ "GET /api/campaigns/c1": { body: { ...ready, ab_test: true } } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();
    expect(wrapper.find("[data-testid=review-checks]").text()).toContain("second version is not written");
    expect(wrapper.find("[data-testid=approve]").attributes("disabled")).toBeDefined();
  });
});

describe("campaign page after approval", () => {
  function routes(campaign: Campaign, counts: Progress = { ...progress, total: 0, processed: 0, by_status: {}, by_channel: {}, responses: {} }): Record<string, Route> {
    return {
      "GET /api/campaigns/c1": { body: campaign },
      "GET /api/campaigns/c1/activity": { body: { activity: [] } },
      "GET /api/campaigns/c1/progress": { body: counts },
    };
  }

  async function open(all: Record<string, Route>) {
    const fetchMock = stub(all);
    const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
    await flushPromises();
    return { wrapper, fetchMock };
  }

  it("stays on the open tab after a refresh", async () => {
    const all = routes(ready);
    const first = await open(all);
    await first.wrapper.find("#tab-content").trigger("click");
    expect(window.location.search).toBe("?tab=content");
    first.wrapper.unmount();

    const { wrapper } = await open(all);
    expect(wrapper.find("#panel-content").exists()).toBe(true);
    await wrapper.find("#tab-overview").trigger("click");
    expect(window.location.search).toBe("");
  });

  it("opens the overview when the address names a tab this campaign does not have", async () => {
    window.history.replaceState({}, "", "/campaigns/c1?tab=deliveries");
    const { wrapper } = await open(routes(ready));
    expect(wrapper.find("#panel-overview").exists()).toBe(true);
    expect(window.location.search).toBe("");
  });

  it("marks the language it opens with and previews it with a reader's name", async () => {
    const content = ready.content!;
    const { wrapper } = await open({
      ...routes({
        ...ready,
        content: { ...content, language: "tr", sms: "Merhaba {{first_name}}", translations: [{ ...content, language: "en", sms: "Hello {{first_name}}" }] },
        languages: [
          { language: "tr", total: 7, sms: 6, email: 4, sms_name_length: 7, email_name_length: 7 },
          { language: "en", total: 2, sms: 1, email: 2, sms_name_length: 21, email_name_length: 21 },
        ],
      }),
      "GET /api/campaigns/c1/sample-recipients": { body: { recipients: [{ id: 1, label: "Can", first_name: "Can", language: "tr" }] } },
    });
    await wrapper.find("#tab-content").trigger("click");
    expect(wrapper.find("[data-testid=content-language-tr]").classes()).toContain("tab--active");
    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Merhaba Can");
  });

  it("says a stopped campaign lost nothing, and resumes it", async () => {
    const { wrapper, fetchMock } = await open({
      ...routes({ ...ready, status: "PAUSED" }),
      "POST /api/campaigns/c1/resume": { body: { ...ready, status: "DISPATCHING" } },
    });
    expect(wrapper.find("[data-testid=paused]").text()).toContain("Nothing is lost");
    await wrapper.find("[data-testid=resume]").trigger("click");
    await flushPromises();
    expect(called(fetchMock, "POST", "/api/campaigns/c1/resume")).toHaveLength(1);
  });

  it("offers sending or scheduling an approved campaign", async () => {
    const { wrapper, fetchMock } = await open({
      ...routes({ ...ready, status: "APPROVED" }),
      "POST /api/campaigns/c1/dispatch": { status: 202, body: { ...ready, status: "DISPATCHING" } },
    });
    expect(wrapper.find("[data-testid=continue-editing]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=choose-time]").attributes("href")).toBe("/campaigns/c1/review");
    await wrapper.find("[data-testid=send-now]").trigger("click");
    await flushPromises();
    expect(called(fetchMock, "POST", "/api/campaigns/c1/dispatch")).toHaveLength(1);
  });

  it("shows the schedule and can cancel it", async () => {
    const schedule = { spec: { kind: "weekly" as const }, description: "Monday at 09:30 (UTC)", next_run_at: "2026-11-09T09:30:00Z", last_run_at: null, runs: 0 };
    const { wrapper, fetchMock } = await open({
      ...routes({ ...ready, status: "SCHEDULED", schedule }),
      "DELETE /api/campaigns/c1/schedule": { body: { ...ready, status: "APPROVED" } },
    });
    expect(wrapper.find("[data-testid=schedule-description]").text()).toBe("Monday at 09:30 (UTC)");
    expect(wrapper.find("[data-testid=next-run]").exists()).toBe(true);
    await wrapper.find("[data-testid=cancel-schedule]").trigger("click");
    await flushPromises();
    expect(called(fetchMock, "DELETE", "/api/campaigns/c1/schedule")).toHaveLength(1);
  });

  it("follows sending as it happens and closes the stream when it ends", async () => {
    const all = routes({ ...ready, status: "DISPATCHING" }, { ...progress, processed: 3 });
    const { wrapper } = await open(all);
    expect(wrapper.find("[data-testid=progress-count]").text()).toContain("3 of 18");
    const stream = FakeEventSource.last!;
    expect(stream.url).toBe("/api/campaigns/c1/events");

    stream.emit("update", { status: "DISPATCHING", progress: { ...progress, processed: 9 } });
    await flushPromises();
    expect(wrapper.find("[data-testid=progress-count]").text()).toContain("9 of 18");

    all["GET /api/campaigns/c1"] = { body: { ...ready, status: "COMPLETED" } };
    stream.emit("update", { status: "COMPLETED", progress });
    stream.emit("end", {});
    await flushPromises();
    expect(wrapper.find("[data-testid=progress-count]").exists()).toBe(false);
    expect(wrapper.text()).toContain("Completed");
    expect(stream.closed).toBe(true);
  });

  it("does not open a stream for a campaign that cannot change", async () => {
    await open(routes({ ...ready, status: "COMPLETED" }, progress));
    expect(FakeEventSource.last).toBeNull();
  });

  it("closes the stream when leaving the page", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "SCHEDULED" }));
    wrapper.unmount();
    expect(FakeEventSource.last?.closed).toBe(true);
  });

  it("keeps following when sending starts", async () => {
    const { wrapper } = await open({ ...routes({ ...ready, status: "APPROVED" }), "POST /api/campaigns/c1/dispatch": { status: 202, body: {} } });
    const stream = FakeEventSource.last;
    await wrapper.find("[data-testid=send-now]").trigger("click");
    await flushPromises();
    expect(FakeEventSource.last).toBe(stream);
    expect(stream?.closed).toBe(false);
  });

  it("says when live updates stop and when they return", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "DISPATCHING" }, progress));
    FakeEventSource.last!.onerror!();
    await flushPromises();
    expect(wrapper.find("[data-testid=live-disconnected]").exists()).toBe(true);
    FakeEventSource.last!.onopen!();
    await flushPromises();
    expect(wrapper.find("[data-testid=live-disconnected]").exists()).toBe(false);
  });

  it("says what holds up sending, only while sending", async () => {
    systemHealth.value = { status: "degraded", checks: { database: "ok", worker: "down", redis: "ok" } };
    const { wrapper } = await open(routes({ ...ready, status: "DISPATCHING" }, progress));
    expect(wrapper.find("[data-testid=sending-blocked]").text()).toContain("Sending worker is down");
    expect(wrapper.find("[data-testid=sending-blocked]").text()).not.toContain("Rate limiter");

    const done = await open(routes({ ...ready, status: "COMPLETED" }, progress));
    expect(done.wrapper.find("[data-testid=sending-blocked]").exists()).toBe(false);
  });

  it("says who created the campaign and who set up its schedule", async () => {
    const schedule = { spec: { kind: "once" as const }, description: "Once", next_run_at: "2026-11-09T09:30:00Z", last_run_at: null, runs: 0, created_by: "ayse" };
    const { wrapper } = await open(routes({ ...ready, status: "SCHEDULED", schedule, created_by: "marketer@example.com" }));
    expect(wrapper.find("[data-testid=created-by]").text()).toBe("marketer@example.com");
    expect(wrapper.find("[data-testid=schedule-by]").text()).toContain("ayse");
  });

  it("does not invent a creator for older campaigns", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "COMPLETED", created_by: null }));
    expect(wrapper.find("[data-testid=created-by]").text()).toBe("Not recorded");
  });

  it("reports the outcome honestly once finished", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "COMPLETED_WITH_FAILURES" }, progress));
    expect(wrapper.find("[data-testid=provider-summary]").text()).toContain("15 attempts");
    expect(wrapper.find("[data-testid=provider-summary]").text()).toContain("500 × 3");
    expect(wrapper.text()).toContain("does not confirm delivery");
    expect(wrapper.find("[data-testid=channel-table]").text()).toContain("SMS");
  });

  it("explains why rate limited calls are not counted as attempts", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "COMPLETED" }, { ...progress, responses: { "202": 12, "429": 4 } }));
    expect(wrapper.text()).toContain("do not use up an attempt");
  });

  it("lists deliveries by person with their history", async () => {
    const row = { id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 2, response_code: 202, reason: "", provider_message_id: "m", next_attempt_at: "", sent_at: null, updated_at: "" };
    const { wrapper } = await open({
      ...routes({ ...ready, status: "COMPLETED" }, progress),
      "GET /api/campaigns/c1/deliveries?limit=100": { body: { deliveries: [row], total: 1 } },
      "GET /api/campaigns/c1/report": { body: { reasons: [], problems: [], timeline: { bucket_seconds: 0, buckets: [] }, engagement: [] } },
      "GET /api/campaigns/c1/deliveries?limit=100&status=FAILED": { body: { deliveries: [], total: 0 } },
      "GET /api/campaigns/c1/deliveries/d1/attempts": {
        body: { attempts: [{ attempt: 1, response_code: 500, outcome: "server_error", error: "", occurred_at: "2026-11-03T12:00:00Z" }, { attempt: 2, response_code: 202, outcome: "accepted", error: "", occurred_at: "2026-11-03T12:00:05Z" }] },
      },
    });
    await wrapper.find("#tab-deliveries").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=delivery-row]").text()).toContain("Ayşe");
    expect(wrapper.find("[data-testid=delivery-row]").text()).toContain("Accepted");

    await wrapper.find("[data-testid=toggle-attempts]").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=attempt-list]").text()).toContain("Provider error");

    await wrapper.find("[data-testid=filter-status]").setValue("FAILED");
    await flushPromises();
    expect(wrapper.find("[data-testid=deliveries-empty]").exists()).toBe(true);
  });

  it("reaches every delivery, however many there are", async () => {
    const row = (index: number) => ({ id: `d${index}`, user_id: index, first_name: `Person ${index}`, channel: "sms", status: "SENT", attempts: 1, response_code: 202, reason: "", provider_message_id: "m", created_at: "2026-11-03T12:00:00Z", updated_at: "2026-11-03T12:00:00Z" });
    const everyone = Array.from({ length: 700 }, (_, index) => row(index));
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const address = new URL(url, "http://test");
      if (!address.pathname.endsWith("/deliveries")) return { ok: true, status: 200, json: async () => ({ cities: [] }) } as Response;
      requested.push(address.search);
      // As the backend does: never more than 500 for one request.
      const limit = Math.min(Number(address.searchParams.get("limit")), 500);
      const offset = Number(address.searchParams.get("offset") ?? 0);
      return { ok: true, status: 200, json: async () => ({ deliveries: everyone.slice(offset, offset + limit), total: everyone.length }) } as Response;
    }));
    const wrapper = mount(DeliveriesPanel, { props: { campaignId: "c1", channels: ["sms"] } });
    await flushPromises();
    expect(wrapper.findAll("[data-testid=delivery-row]")).toHaveLength(100);
    expect(wrapper.find("[data-testid=delivery-count]").text()).toBe("Showing 100 of 700 deliveries");
    for (let shown = 200; shown <= 700; shown += 100) {
      expect(wrapper.find("[data-testid=load-more]").exists()).toBe(true);
      await wrapper.find("[data-testid=load-more]").trigger("click");
      await flushPromises();
      expect(wrapper.findAll("[data-testid=delivery-row]")).toHaveLength(shown);
    }
    expect(requested).toContain("?limit=100&offset=600");
    // The list ends exactly on a full page, and nothing offers more.
    expect(wrapper.find("[data-testid=load-more]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=delivery-count]").text()).toBe("700 deliveries");

    // What is on screen is read again in requests the backend answers in full.
    requested.length = 0;
    await wrapper.setProps({ refreshKey: 1 });
    await flushPromises();
    expect(requested).toEqual(expect.arrayContaining(["?limit=500", "?limit=200&offset=500"]));
    expect(wrapper.findAll("[data-testid=delivery-row]")).toHaveLength(700);
    expect(wrapper.find("[data-testid=load-more]").exists()).toBe(false);
  });

  it("filters deliveries by provider calls and by city", async () => {
    const row = { id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 1, response_code: 202, reason: "", next_attempt_at: "2026-11-03T12:00:00Z", sent_at: null, updated_at: "2026-11-03T12:00:00Z" };
    const fetchMock = stub({
      ...routes({ ...ready, status: "COMPLETED" }, progress),
      "GET /api/campaigns/c1/deliveries?limit=100": { body: { deliveries: [row], total: 1 } },
      "GET /api/campaigns/c1/delivery-cities": { body: { cities: ["Ankara", "Bursa"] } },
      "GET /api/campaigns/c1/report": { body: { reasons: [], problems: [], timeline: { bucket_seconds: 0, buckets: [] }, engagement: [] } },
      "GET /api/campaigns/c1/deliveries?limit=100&calls=2%2B": { body: { deliveries: [], total: 0 } },
      "GET /api/campaigns/c1/deliveries?limit=100&calls=2%2B&city=Bursa": { body: { deliveries: [row], total: 1 } },
    });
    const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
    await flushPromises();
    await wrapper.find("#tab-deliveries").trigger("click");
    await flushPromises();
    await wrapper.find("[data-testid=filter-calls]").setValue("2+");
    await flushPromises();
    expect(fetchMock.mock.calls.some(([url]) => url === "/api/campaigns/c1/deliveries?limit=100&calls=2%2B")).toBe(true);
    await wrapper.find("[data-testid=filter-city]").setValue("Bursa");
    await flushPromises();
    expect(wrapper.find("[data-testid=active-filter]").text()).toContain("Called more than once · Bursa");
    expect(wrapper.find("[data-testid=delivery-row]").exists()).toBe(true);
    expect(wrapper.find("[data-testid=export-csv]").attributes("href")).toBe("/api/campaigns/c1/deliveries.csv?calls=2%2B&city=Bursa");
  });

  it("exports what the filters keep and copies the campaign", async () => {
    stub({
      ...routes({ ...ready, status: "COMPLETED" }, progress),
      "GET /api/campaigns/c1/deliveries?limit=100": { body: { deliveries: [{ id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 1, response_code: 202, reason: "", next_attempt_at: "2026-11-03T12:00:00Z", sent_at: null, updated_at: "2026-11-03T12:00:00Z" }], total: 1 } },
      "GET /api/campaigns/c1/report": { body: { reasons: [], problems: [], timeline: { bucket_seconds: 0, buckets: [] }, engagement: [] } },
      "GET /api/campaigns/c1/deliveries?limit=100&channel=sms": { body: { deliveries: [{ id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 1, response_code: 202, reason: "", next_attempt_at: "2026-11-03T12:00:00Z", sent_at: null, updated_at: "2026-11-03T12:00:00Z" }], total: 1 } },
      "POST /api/campaigns/c1/duplicate": { body: { ...ready, id: "c2" } },
    });
    const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
    await flushPromises();
    await wrapper.find("#tab-deliveries").trigger("click");
    await flushPromises();
    expect(wrapper.find("[data-testid=export-csv]").attributes("href")).toBe("/api/campaigns/c1/deliveries.csv");
    await wrapper.find("[data-testid=filter-channel]").setValue("sms");
    await flushPromises();
    expect(wrapper.find("[data-testid=export-csv]").attributes("href")).toBe("/api/campaigns/c1/deliveries.csv?channel=sms");

    await wrapper.find("[data-testid=duplicate]").trigger("click");
    await flushPromises();
    expect(currentPath.value).toBe("/campaigns/c2/review");
  });

  describe("report", () => {
    const row = { id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 2, response_code: 202, reason: "", provider_message_id: "m", next_attempt_at: "2026-11-03T12:00:00Z", sent_at: null, updated_at: "2026-11-03T12:00:00Z" };
    const report = {
      engagement: [{ channel: "sms", accepted: 10, opened: 4 }, { channel: "email", accepted: 12, opened: 0 }],
      reasons: [{ reason: "no_consent", deliveries: 5 }, { reason: "server_error", deliveries: 1 }],
      problems: [{ outcome: "rate_limited", deliveries: 3, attempts: 4 }],
      timeline: {
        bucket_seconds: 10,
        buckets: [
          { at: "2026-11-03T12:00:00Z", accepted: 7, rate_limited: 4, server_error: 0, rejected: 0, timeout: 0 },
          { at: "2026-11-03T12:00:10Z", accepted: 0, rate_limited: 0, server_error: 0, rejected: 0, timeout: 0 },
          { at: "2026-11-03T12:00:20Z", accepted: 5, rate_limited: 0, server_error: 1, rejected: 0, timeout: 0 },
        ],
      },
    };

    async function openReport(body: unknown = report) {
      const fetchMock = stub({
        ...routes({ ...ready, status: "COMPLETED" }, progress),
        "GET /api/campaigns/c1/report": { body },
        "GET /api/campaigns/c1/deliveries?limit=100": { body: { deliveries: [row], total: 1 } },
        "GET /api/campaigns/c1/deliveries?limit=100&status=SENT&channel=sms": { body: { deliveries: [row], total: 1 } },
        "GET /api/campaigns/c1/deliveries?limit=100&outcome=rate_limited": { body: { deliveries: [], total: 0 } },
        "GET /api/campaigns/c1/deliveries?limit=100&status=SENT&channel=sms&arrival=bounced": { body: { deliveries: [{ ...row, bounced_at: "2026-11-03T12:00:05Z" }], total: 1 } },
        "GET /api/campaigns/c1/deliveries?limit=100&reason=server_error": { body: { deliveries: [], total: 0 } },
        "GET /api/campaigns/c1/deliveries?limit=100&channel=sms&opened=yes": { body: { deliveries: [{ ...row, opened_at: "2026-11-03T12:05:00Z" }], total: 1 } },
        "GET /api/campaigns/c1/deliveries?limit=100&status=SENT&channel=email&opened=no": { body: { deliveries: [row], total: 1 } },
        "GET /api/campaigns/c1/deliveries?limit=100&from=2026-11-03T12%3A00%3A00Z&to=2026-11-03T12%3A00%3A10.000Z": { body: { deliveries: [], total: 0 } },
      });
      const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
      await flushPromises();
      await wrapper.find("#tab-deliveries").trigger("click");
      await flushPromises();
      return { wrapper, fetchMock };
    }

    const asked = (fetchMock: ReturnType<typeof stub>, query: string) =>
      fetchMock.mock.calls.filter(([url]) => url === `/api/campaigns/c1/deliveries?limit=100&${query}`).length;

    it("compares a test's versions and names no winner", async () => {
      const { wrapper } = await openReport({
        ...report,
        variants: [
          { variant: "A", channel: "sms", planned: 10, accepted: 9, delivered: 8, bounced: 1, waiting: 0, opened: 0 },
          { variant: "B", channel: "sms", planned: 11, accepted: 11, delivered: 7, bounced: 2, waiting: 2, opened: 1 },
        ],
      });
      const card = wrapper.find("[data-testid=variants]");
      expect(card.text()).toContain("No winner is named");
      expect(wrapper.find("[data-testid=variant-sms-B]").text()).toContain("11");
    });

    it("selecting a problem lists the people it happened to, and selecting it again clears it", async () => {
      const { wrapper, fetchMock } = await openReport();
      await wrapper.find("[data-testid=problem-rate_limited]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "outcome=rate_limited")).toBe(1);
      expect(wrapper.find("[data-testid=active-filter]").text()).toContain("Had a call: Rate limited");
      expect(wrapper.find("[data-testid=filter-outcome]").element).toHaveProperty("value", "rate_limited");
      expect(wrapper.find("[data-testid=problem-rate_limited]").attributes("aria-pressed")).toBe("true");

      await wrapper.find("[data-testid=problem-rate_limited]").trigger("click");
      await flushPromises();
      expect(wrapper.find("[data-testid=active-filter]").exists()).toBe(false);
      expect(wrapper.find("[data-testid=delivery-row]").exists()).toBe(true);
    });

    it("keeps filters in the address, and a chart click keeps the city", async () => {
      window.history.replaceState({}, "", "/campaigns/c1?tab=deliveries&city=Bursa");
      const { wrapper, fetchMock } = await openReport();
      expect(asked(fetchMock, "city=Bursa")).toBe(1);
      expect(wrapper.find("[data-testid=active-filter]").text()).toContain("Bursa");

      await wrapper.find("[data-testid=segment-sms-SENT]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "status=SENT&channel=sms&city=Bursa")).toBe(1);
      expect(window.location.search).toBe("?tab=deliveries&city=Bursa&status=SENT&channel=sms");

      // Clicking the tab already open must not lose them.
      await wrapper.find("#tab-deliveries").trigger("click");
      expect(window.location.search).toBe("?tab=deliveries&city=Bursa&status=SENT&channel=sms");

      await wrapper.find("[data-testid=segment-sms-SENT]").trigger("click");
      await flushPromises();
      expect(window.location.search).toBe("?tab=deliveries&city=Bursa");

      await wrapper.find("[data-testid=clear-filters]").trigger("click");
      await flushPromises();
      expect(window.location.search).toBe("?tab=deliveries");
    });

    it("selecting a reason replaces the earlier selection", async () => {
      const { wrapper, fetchMock } = await openReport();
      await wrapper.find("[data-testid=problem-rate_limited]").trigger("click");
      await flushPromises();
      await wrapper.find("[data-testid=reason-server_error]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "reason=server_error")).toBe(1);
      expect(wrapper.find("[data-testid=filter-outcome]").element).toHaveProperty("value", "");
    });

    it("selecting a channel segment filters by channel and status", async () => {
      const { wrapper, fetchMock } = await openReport();
      await wrapper.find("[data-testid=segment-sms-SENT]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "status=SENT&channel=sms")).toBe(1);
      expect(wrapper.find("[data-testid=active-filter]").text()).toContain("SMS");
    });

    it("selecting a time bar lists who was called in that period, and a quiet period cannot be selected", async () => {
      const { wrapper, fetchMock } = await openReport();
      const bars = wrapper.findAll("[data-testid=timeline-bar]");
      expect(bars).toHaveLength(3);
      expect(bars[1].attributes("disabled")).toBeDefined();
      await bars[0].trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "from=2026-11-03T12%3A00%3A00Z&to=2026-11-03T12%3A00%3A10.000Z")).toBe(1);
      await wrapper.find("[data-testid=clear-filters]").trigger("click");
      await flushPromises();
      expect(wrapper.find("[data-testid=active-filter]").exists()).toBe(false);
    });

    it("shows opens per channel in that channel's words and lists who opened", async () => {
      const { wrapper, fetchMock } = await openReport();
      expect(wrapper.find("[data-testid=engagement-sms]").text()).toBe("4 of 10 read");
      expect(wrapper.find("[data-testid=engagement-email]").text()).toBe("0 of 12 opened");
      expect(wrapper.find("[data-testid=opened-email]").exists()).toBe(false);

      await wrapper.find("[data-testid=opened-sms]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "channel=sms&opened=yes")).toBe(1);
      expect(wrapper.find("[data-testid=delivery-opened]").text()).not.toBe("—");
      expect(wrapper.find("[data-testid=active-filter]").text()).toContain("Opened or read");

      await wrapper.find("[data-testid=unopened-email]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "status=SENT&channel=email&opened=no")).toBe(1);
      expect(wrapper.find("[data-testid=delivery-opened]").text()).toBe("—");
    });

    it("follows messages to arrival, calls it simulated and lists who bounced", async () => {
      const lifecycle = [{ channel: "sms", planned: 20, eligible: 15, called: 15, accepted: 10, delivered: 8, bounced: 2, waiting: 0, arrived_unknown: 1 }];
      const { wrapper, fetchMock } = await openReport({ ...report, lifecycle });
      expect(wrapper.find("[data-testid=simulated]").text()).toBe("Simulated arrival");
      expect(wrapper.find("[data-testid=lifecycle-sms]").text()).toContain("80%");
      expect(wrapper.find("[data-testid=arrived-unknown]").text()).toContain("1 message");
      expect(wrapper.find("[data-testid=step-sms-waiting]").attributes("disabled")).toBeDefined();

      await wrapper.find("[data-testid=step-sms-bounced]").trigger("click");
      await flushPromises();
      expect(asked(fetchMock, "status=SENT&channel=sms&arrival=bounced")).toBe(1);
      expect(wrapper.find("[data-testid=active-filter]").text()).toContain("Bounced");
    });

    it("offers the time chart as a table of the periods with calls", async () => {
      const { wrapper } = await openReport();
      expect(wrapper.findAll("[data-testid=timeline-table] tbody tr")).toHaveLength(2);
    });

    it("says so when nothing went wrong and draws no timeline before any call", async () => {
      const { wrapper } = await openReport({ reasons: [], problems: [], timeline: { bucket_seconds: 0, buckets: [] }, engagement: [] });
      expect(wrapper.find("[data-testid=engagement-chart]").exists()).toBe(false);
      expect(wrapper.find("[data-testid=no-reasons]").exists()).toBe(true);
      expect(wrapper.find("[data-testid=no-problems]").exists()).toBe(true);
      expect(wrapper.find("[data-testid=timeline-chart]").exists()).toBe(false);
      expect(wrapper.find("[data-testid=channel-chart]").exists()).toBe(true);
    });
  });

  it("has no deliveries tab before anything is queued", async () => {
    const { wrapper } = await open(routes(ready));
    expect(wrapper.find("#tab-deliveries").exists()).toBe(false);
  });
});

describe("the checks before sending", () => {
  it("says what was checked, with the email's real link", async () => {
    stub({ "GET /api/campaigns/c1": { body: { ...ready, content: { ...ready.content, email_cta_url: "https://shop.example.com/sale", email_cta_label: "Go" } } } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();

    const checks = wrapper.find("[data-testid=review-checks]");
    expect(checks.text()).toContain("The button opens https://shop.example.com/sale");
    expect(checks.findAll("[data-state=attention]")).toHaveLength(0);
  });

  it("draws attention to an offer that differs from the brief", async () => {
    stub({ "GET /api/campaigns/c1": { body: { ...ready, content_warnings: [{ field: "sms", reason: "invented_discount", message: "SMS mentions 50%." }] } } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();

    const flagged = wrapper.findAll("[data-testid=review-checks] [data-state=attention]");
    expect(flagged).toHaveLength(1);
    expect(flagged[0].text()).toContain("SMS mentions 50%.");
  });

  it("leaves out email checks for a campaign without email", async () => {
    stub({ "GET /api/campaigns/c1": { body: { ...ready, channels: ["sms"] } } });
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();

    expect(wrapper.find("[data-testid=review-checks]").text()).not.toContain("Unsubscribe");
  });
});

import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Campaign, Progress } from "../api/campaigns";
import { currentPath, navigate } from "../lib/navigation";
import CampaignDetailView from "./CampaignDetailView.vue";
import ReviewView from "./ReviewView.vue";

const ready: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Reminder",
  status: "READY_FOR_REVIEW",
  version: 1,
  proposal: { assumptions: [], unsupported_conditions: [] },
  conditions: [{ field: "city", label: "City is", value: "İstanbul" }],
  audience: { total: 9, sms: 7, email: 6 },
  blockers: [],
  content: { sms: "Hi {{first_name}}", email_subject: "Subject", email_body: "Body", edited: [] },
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

afterEach(() => {
  navigate("/");
  vi.useRealTimers();
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
    expect(wrapper.find("[data-testid=review-sms]").text()).toContain("Hi {{first_name}}");
    expect(wrapper.find("[data-testid=review-email]").text()).toContain("Subject");
    expect(wrapper.find("[data-testid=approve]").text()).toBe("Approve and send 13 messages");
    expect(called(fetchMock, "POST", "/api/campaigns/c1/approve")).toHaveLength(0);
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

  it("updates while sending and stops when it ends", async () => {
    vi.useFakeTimers();
    const sending = routes({ ...ready, status: "DISPATCHING" }, { ...progress, processed: 3 });
    const { wrapper, fetchMock } = await open(sending);
    expect(wrapper.find("[data-testid=progress-count]").text()).toContain("3 of 18");

    sending["GET /api/campaigns/c1"] = { body: { ...ready, status: "COMPLETED" } };
    sending["GET /api/campaigns/c1/progress"] = { body: progress };
    await vi.advanceTimersByTimeAsync(2100);
    await flushPromises();
    expect(wrapper.find("[data-testid=progress-count]").exists()).toBe(false);

    const reads = fetchMock.mock.calls.length;
    await vi.advanceTimersByTimeAsync(10000);
    expect(fetchMock.mock.calls.length).toBe(reads);
  });

  it("reports the outcome honestly once finished", async () => {
    const { wrapper } = await open(routes({ ...ready, status: "COMPLETED_WITH_FAILURES" }, progress));
    expect(wrapper.find("[data-testid=provider-summary]").text()).toContain("15 attempts");
    expect(wrapper.find("[data-testid=provider-summary]").text()).toContain("500 × 3");
    expect(wrapper.text()).toContain("does not confirm delivery");
    expect(wrapper.find("[data-testid=channel-table]").text()).toContain("SMS");
  });

  it("lists deliveries by person with their history", async () => {
    const row = { id: "d1", user_id: 1, first_name: "Ayşe", channel: "sms", status: "SENT", attempts: 2, response_code: 202, reason: "", provider_message_id: "m", next_attempt_at: "", sent_at: null, updated_at: "" };
    const { wrapper } = await open({
      ...routes({ ...ready, status: "COMPLETED" }, progress),
      "GET /api/campaigns/c1/deliveries?limit=100": { body: { deliveries: [row] } },
      "GET /api/campaigns/c1/deliveries?limit=100&status=FAILED": { body: { deliveries: [] } },
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

  it("has no deliveries tab before anything is queued", async () => {
    const { wrapper } = await open(routes(ready));
    expect(wrapper.find("#tab-deliveries").exists()).toBe(false);
  });
});

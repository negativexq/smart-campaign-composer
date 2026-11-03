import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Campaign } from "../api/campaigns";
import DeliveryProgress from "../components/DeliveryProgress.vue";
import { navigate } from "../lib/navigation";
import AudienceView from "./AudienceView.vue";
import ComposeView from "./ComposeView.vue";
import ContentView from "./ContentView.vue";
import ReviewView from "./ReviewView.vue";

const smsOnly: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Reminder",
  status: "READY_FOR_REVIEW",
  channels: ["sms"],
  version: 1,
  audience: { total: 9, sms: 7, email: 0 },
  conditions: [],
  audience_as_of: "2026-11-03T12:00:00Z",
  blockers: [],
  content: { sms: "Hi {{first_name}}", email_subject: "", email_body: "", edited: [] },
  content_limits: { sms_name_length: 8, email_name_length: 8 },
  proposal: { assumptions: [], unsupported_conditions: [] },
} as unknown as Campaign;

function stub(campaign: Campaign) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.endsWith("/sample-recipients") ? { recipients: [{ first_name: "Ayşe" }] } : campaign;
      return { ok: true, status: 200, json: async () => body } as Response;
    }),
  );
}

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

describe("choosing channels", () => {
  function composeStub() {
    const fetchMock = vi.fn(async (url: string) => {
      const body = url === "/api/campaigns" ? { id: "c9" } : smsOnly;
      return { ok: true, status: 200, json: async () => body } as Response;
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("starts with both and sends what was chosen", async () => {
    const fetchMock = composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("[data-testid=channel-email]").setValue(false);
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    const [, init] = fetchMock.mock.calls.find(([url]) => url === "/api/campaigns") as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ brief: "Spring sale", channels: ["sms"] });
  });

  it("keeps the product's order however they are ticked", async () => {
    const fetchMock = composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("[data-testid=channel-sms]").setValue(false);
    await wrapper.find("[data-testid=channel-sms]").setValue(true);
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    const [, init] = fetchMock.mock.calls.find(([url]) => url === "/api/campaigns") as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string).channels).toEqual(["sms", "email"]);
  });

  it("says why it cannot continue with none", async () => {
    composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("[data-testid=channel-sms]").setValue(false);
    await wrapper.find("[data-testid=channel-email]").setValue(false);

    expect(wrapper.find("button[type=submit]").attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=brief-hint]").text()).toBe("Choose at least one channel.");
  });

  it("cannot change them once the campaign exists", async () => {
    const fetchMock = vi.fn(async (url: string) =>
      url === "/api/campaigns"
        ? ({ ok: true, status: 201, json: async () => ({ id: "c9" }) } as Response)
        : ({ ok: false, status: 502, json: async () => ({ error: "Try again" }) } as Response),
    );
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(wrapper.find("[data-testid=channel-picker]").attributes("disabled")).toBeDefined();
  });
});

describe("a campaign on one channel", () => {
  it("edits and previews only that channel", async () => {
    stub(smsOnly);
    const wrapper = mount(ContentView, { props: { campaignId: "c1" } });
    await flushPromises();

    expect(wrapper.text()).toContain("SMS");
    expect(wrapper.text()).not.toContain("Email subject");
    expect(wrapper.text()).not.toContain("Email body");
    expect(wrapper.findComponent({ name: "EmailPreview" }).exists()).toBe(false);
  });

  it("reviews only that channel", async () => {
    stub(smsOnly);
    const wrapper = mount(ReviewView, { props: { campaignId: "c1" } });
    await flushPromises();

    expect(wrapper.find("[data-testid=review-sms]").exists()).toBe(true);
    expect(wrapper.find("[data-testid=review-email]").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("Email");
  });

  it("counts reach only on that channel", async () => {
    stub({ ...smsOnly, status: "DRAFT" });
    const wrapper = mount(AudienceView, { props: { campaignId: "c1" } });
    await flushPromises();

    expect(wrapper.text()).toContain("Reachable by SMS");
    expect(wrapper.text()).not.toContain("Reachable by email");
  });

  it("reports progress only on that channel", () => {
    const wrapper = mount(DeliveryProgress, {
      props: {
        live: false,
        progress: { total: 3, processed: 3, attempts: 3, by_status: { SENT: 3 }, by_channel: { sms: { SENT: 3 }, email: {} }, responses: {} },
      },
    });

    expect(wrapper.text()).toContain("SMS");
    expect(wrapper.text()).not.toContain("Email");
  });
});

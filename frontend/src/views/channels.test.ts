import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Campaign } from "../api/campaigns";
import DeliveryProgress from "../components/DeliveryProgress.vue";
import EmailPreview from "../components/EmailPreview.vue";
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
  content_warnings: [],
  proposal: { assumptions: [], unsupported_conditions: [] },
} as unknown as Campaign;

function stub(campaign: Campaign) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.endsWith("/sample-recipients") ? { recipients: [{ first_name: "Ayşe" }] } : campaign;
      if (String(url).endsWith("/audience/breakdown")) return { ok: true, status: 200, json: async () => ({ everyone: 10, steps: [] }) } as Response;
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

describe("email preview", () => {
  const draft = { email_subject: "Hi {{first_name}}", email_preheader: "Soon", email_body: "Hello {{first_name}}" };
  const recipients = [{ id: 1, label: "Ayşe", first_name: "Ayşe" }];

  function previewStub(result: () => Response) {
    const fetchMock = vi.fn(async () => result());
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }
  const answer = { ok: true, status: 200, json: async () => ({ subject: "Hi Ayşe", html: "<table>designed</table>", text: "Hello Ayşe\n\nUnsubscribe" }) } as Response;

  it("shows what the backend renders, for the chosen person, without scripts", async () => {
    const fetchMock = previewStub(() => answer);
    const wrapper = mount(EmailPreview, { props: { message: draft, recipients } });
    await flushPromises();

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/email-preview");
    expect(JSON.parse(init.body as string)).toEqual({ email_headline: "", email_cta_label: "", email_cta_url: "", ...draft, first_name: "Ayşe" });
    const frame = wrapper.find("[data-testid=email-frame]");
    expect(frame.attributes("srcdoc")).toContain("designed");
    expect(frame.attributes("sandbox")).toBe("allow-same-origin");
    expect(wrapper.find("[data-testid=email-subject]").text()).toBe("Hi Ayşe");
    expect(wrapper.find("[data-testid=email-preheader]").text()).toBe("Soon");
  });

  it("asks only for what the template uses, so stored content can be previewed", async () => {
    const fetchMock = previewStub(() => answer);
    mount(EmailPreview, { props: { message: { ...draft, sms: "Hi", edited: ["sms"] } as never, recipients } });
    await flushPromises();

    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const sent = JSON.parse(init.body as string);
    expect(sent.edited).toBeUndefined();
    expect(sent.sms).toBeUndefined();
  });

  it("switches between the desktop, mobile and plain text views", async () => {
    previewStub(() => answer);
    const wrapper = mount(EmailPreview, { props: { message: draft, recipients } });
    await flushPromises();

    expect(wrapper.find("[data-testid=email-frame]").attributes("style")).toContain("width: 640px");
    await wrapper.find("[data-testid=email-mode-mobile]").trigger("click");
    expect(wrapper.find("[data-testid=email-frame]").attributes("style")).toContain("width: 375px");
    await wrapper.find("[data-testid=email-mode-text]").trigger("click");
    expect(wrapper.find("[data-testid=email-frame]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=email-text]").text()).toContain("Unsubscribe");
    expect(wrapper.find("[data-testid=email-mode-text]").attributes("aria-pressed")).toBe("true");
  });

  it("still shows the text when the design cannot be fetched", async () => {
    previewStub(() => ({ ok: false, status: 500, json: async () => ({ error: "no" }) }) as Response);
    const wrapper = mount(EmailPreview, { props: { message: draft, recipients } });
    await flushPromises();

    expect(wrapper.find("[data-testid=email-frame]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=email-body]").text()).toBe("Hello Ayşe");
  });
});

describe("campaign options", () => {
  function composeStub() {
    const fetchMock = vi.fn(async (url: string) => {
      const body = url === "/api/campaigns" ? { id: "c9" } : smsOnly;
      return { ok: true, status: 200, json: async () => body } as Response;
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }
  const sentBody = (fetchMock: ReturnType<typeof composeStub>) => {
    const [, init] = fetchMock.mock.calls.find(([url]) => url === "/api/campaigns") as unknown as [string, RequestInit];
    return JSON.parse(init.body as string);
  };

  it("sends only what was chosen", async () => {
    const fetchMock = composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(sentBody(fetchMock)).toEqual({ brief: "Spring sale", channels: ["sms", "email"] });
  });

  it("sends the tone, discount and days to skip", async () => {
    const fetchMock = composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("#campaign-tone").setValue("formal");
    await wrapper.find("#campaign-has-offer").setValue(true);
    await wrapper.find("#campaign-offer").setValue(30);
    await wrapper.find("#campaign-quiet").setValue("7");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(sentBody(fetchMock)).toMatchObject({ tone: "formal", offer_percent: 30, not_messaged_within_days: 7 });
  });

  it("keeps options closed and asks for no discount unless one is added", async () => {
    const fetchMock = composeStub();
    const wrapper = mount(ComposeView);
    expect(wrapper.find("[data-testid=campaign-options]").attributes("open")).toBeUndefined();
    expect(wrapper.find("#campaign-offer").exists()).toBe(false);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("form").trigger("submit");
    await flushPromises();

    expect(sentBody(fetchMock)).not.toHaveProperty("offer_percent");
  });

  it("says which options are set while they are closed", async () => {
    composeStub();
    const wrapper = mount(ComposeView);
    expect(wrapper.find("[data-testid=options-summary]").text()).toBe("Optional");
    await wrapper.find("#campaign-tone").setValue("formal");
    await wrapper.find("#campaign-has-offer").setValue(true);
    await wrapper.find("#campaign-offer").setValue(30);

    expect(wrapper.find("[data-testid=options-summary]").text()).toBe("Formal · 30% discount");
  });

  it("says why a discount cannot be used", async () => {
    composeStub();
    const wrapper = mount(ComposeView);
    await wrapper.find("#brief").setValue("Spring sale");
    await wrapper.find("#campaign-has-offer").setValue(true);
    await wrapper.find("#campaign-offer").setValue(150);

    expect(wrapper.find("button[type=submit]").attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=brief-hint]").text()).toBe("Enter a discount from 1 to 100.");
  });
});

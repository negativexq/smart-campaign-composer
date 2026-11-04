import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Campaign } from "../api/campaigns";
import { currentPath, navigate } from "../lib/navigation";
import ContentView from "./ContentView.vue";
import CampaignDetailView from "./CampaignDetailView.vue";

const base: Campaign = {
  id: "c1",
  name: "Cart Recovery",
  name_source: "ai",
  brief: "Istanbul cart reminder",
  status: "DRAFT",
  channels: ["sms", "email"],
  version: 1,
  proposal: { assumptions: [], unsupported_conditions: [] },
  conditions: [],
  audience: { total: 9, sms: 7, email: 7 },
  blockers: [],
  content_limits: { sms_name_length: 21, email_name_length: 21 },
  content_warnings: [],
  created_at: "2026-11-03T12:00:00Z",
  updated_at: "2026-11-03T12:00:00Z",
};
const stored = { sms: "Hi {{first_name}}", email_subject: "Subject", email_preheader: "", email_headline: "", email_body: "Body", email_cta_label: "", email_cta_url: "", email_template: "", edited: [] };
const ready: Campaign = { ...base, status: "READY_FOR_REVIEW", content: stored };
const recipients = [
  { id: 1, label: "Ayşe", first_name: "Ayşe" },
  { id: 2, label: "No first name", first_name: "" },
];

function streamOf(...chunks: string[]) {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(sink) {
      chunks.forEach((chunk) => sink.enqueue(encoder.encode(chunk)));
      sink.close();
    },
  });
}

const event = (name: string, data: unknown) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;

function stub(campaign: Campaign, generate?: () => Promise<unknown>, put?: () => unknown) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith("/generate")) return (await generate!()) as Response;
    if (url.endsWith("/sample-recipients")) return { ok: true, status: 200, json: async () => ({ recipients }) } as Response;
    if (init?.method === "PUT") return put!() as Response;
    return { ok: true, status: 200, json: async () => campaign } as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function generateCalls(fetchMock: ReturnType<typeof stub>) {
  return fetchMock.mock.calls.filter(([url]) => url.endsWith("/generate"));
}

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

function open(channel: "sms" | "email" = "sms") {
  return mount(ContentView, { props: { campaignId: "c1", channel } });
}

describe("content page", () => {
  describe("with two versions to compare", () => {
    const test: Campaign = {
      ...ready,
      ab_test: true,
      content: { ...stored, language: "tr", sms: "A {{first_name}}", variant_b: { ...stored, language: "tr", variant: "B", sms: "B {{first_name}}" } },
    };

    it("opens version A and moves to version B", async () => {
      stub(test);
      const wrapper = open();
      await flushPromises();
      expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("A {{first_name}}");
      await wrapper.find("[data-testid=variant-B]").trigger("click");
      await flushPromises();
      expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("B {{first_name}}");
    });

    it("saves an edit as that version's", async () => {
      const fetchMock = stub(test, undefined, () => ({ ok: true, status: 200, json: async () => test }));
      const wrapper = open();
      await flushPromises();
      await wrapper.find("[data-testid=variant-B]").trigger("click");
      await flushPromises();
      await wrapper.find("textarea").setValue("Edited {{first_name}}");
      await wrapper.find("[data-testid=variant-A]").trigger("click");
      await flushPromises();
      const saved = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PUT");
      expect(JSON.parse(String((saved?.[1] as RequestInit).body))).toMatchObject({ variant: "B", sms: "Edited {{first_name}}" });
    });

    it("counts each language's people inside the version", async () => {
      stub({
        ...test,
        languages: [
          { language: "tr", total: 10, sms: 9, email: 9, sms_name_length: 7, email_name_length: 7 },
          { language: "en", total: 3, sms: 3, email: 3, sms_name_length: 7, email_name_length: 7 },
        ],
        content: { ...test.content!, translations: [{ ...stored, language: "en" }], variant_b: { ...test.content!.variant_b!, translations: [{ ...stored, language: "en", variant: "B" }] } },
        variant_a_percent: 30,
        variant_audience: [
          { variant: "A", language: "tr", total: 3, sms: 2, email: 3 },
          { variant: "A", language: "en", total: 1, sms: 1, email: 1 },
          { variant: "B", language: "tr", total: 7, sms: 7, email: 6 },
          { variant: "B", language: "en", total: 2, sms: 2, email: 2 },
        ],
      });
      const wrapper = open();
      await flushPromises();
      expect(wrapper.find("[data-testid=variant-A]").text()).toContain("30% · 3 people");
      expect(wrapper.find("[data-testid=language-tr]").text()).toContain("2 people");
      await wrapper.find("[data-testid=variant-B]").trigger("click");
      await flushPromises();
      expect(wrapper.find("[data-testid=variant-B]").text()).toContain("70% · 9 people");
      expect(wrapper.find("[data-testid=language-tr]").text()).toContain("7 people");
    });

    it("says when the second version is not written", async () => {
      stub({ ...test, content: { ...stored, language: "tr" } });
      const wrapper = open();
      await flushPromises();
      expect(wrapper.find("[data-testid=variant-tabs]").exists()).toBe(false);
      expect(wrapper.text()).toContain("the second is not written yet");
    });
  });

  describe("with two languages", () => {
    const english = { ...stored, language: "en", sms: "Hello {{first_name}}" };
    const twoLanguages: Campaign = {
      ...ready,
      content: { ...stored, language: "tr", sms: "Merhaba {{first_name}}", translations: [english] },
      languages: [
        { language: "tr", total: 7, sms: 6, email: 5, sms_name_length: 7, email_name_length: 7 },
        { language: "en", total: 2, sms: 1, email: 2, sms_name_length: 21, email_name_length: 21 },
      ],
    };

    it("opens the largest language and shows each one's people", async () => {
      stub(twoLanguages);
      const wrapper = open();
      await flushPromises();
      expect(wrapper.find("[data-testid=language-tr]").text()).toContain("6 people");
      expect(wrapper.find("[data-testid=language-en]").text()).toContain("1 person");
      expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("Merhaba {{first_name}}");
      await wrapper.find("[data-testid=language-en]").trigger("click");
      await flushPromises();
      expect((wrapper.find("textarea").element as HTMLTextAreaElement).value).toBe("Hello {{first_name}}");
    });

    it("saves an edit as that language's before moving on", async () => {
      const fetchMock = stub(twoLanguages, undefined, () => ({ ok: true, status: 200, json: async () => twoLanguages }));
      const wrapper = open();
      await flushPromises();
      await wrapper.find("[data-testid=language-en]").trigger("click");
      await flushPromises();
      await wrapper.find("textarea").setValue("Edited {{first_name}}");
      await wrapper.find("[data-testid=language-tr]").trigger("click");
      await flushPromises();
      const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
      expect(JSON.parse(String(put?.[1]?.body))).toMatchObject({ language: "en", sms: "Edited {{first_name}}" });
    });

    it("says which version is being written after the first", async () => {
      let finish: () => void = () => {};
      const stream = new ReadableStream({
        start(controller) {
          const encoder = new TextEncoder();
          controller.enqueue(encoder.encode(event("started", {}) + event("language", { language: "en" })));
          finish = () => {
            controller.enqueue(encoder.encode(event("done", twoLanguages)));
            controller.close();
          };
        },
      });
      stub(base, async () => ({ ok: true, status: 200, body: stream }));
      const wrapper = open();
      await flushPromises();
      await vi.waitFor(() => expect(wrapper.find("[data-testid=message-state]").text()).toBe("Writing the English version"));
      finish();
      await flushPromises();
      await vi.waitFor(() => expect(wrapper.find("[data-testid=language-tabs]").exists()).toBe(true));
    });
  });

  it("writes messages on arrival and only marks them validated at the end", async () => {
    const fetchMock = stub(base, async () => ({
      ok: true,
      status: 200,
      body: streamOf(event("started", {}), event("delta", { field: "sms", text: "Hi " }), event("delta", { field: "sms", text: "{{first_name}}" }), event("done", ready)),
    }));
    const wrapper = open();
    await flushPromises();
    await vi.waitFor(() => expect(wrapper.find("[data-testid=regenerate]").text()).toBe("Regenerate"));

    expect(generateCalls(fetchMock)).toHaveLength(1);
    expect(wrapper.findAll("textarea")[0].element.value).toBe("Hi {{first_name}}");
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Validated");
  });

  it("discards streamed text when the stream starts over", async () => {
    stub(base, async () => ({
      ok: true,
      status: 200,
      body: streamOf(event("delta", { field: "sms", text: "abandoned" }), event("reset", {}), event("delta", { field: "sms", text: "kept" }), event("done", { ...ready, content: { ...stored, sms: "kept" } })),
    }));
    const wrapper = open();
    await vi.waitFor(() => expect(wrapper.find("[data-testid=regenerate]").text()).toBe("Regenerate"));
    expect(wrapper.findAll("textarea")[0].element.value).toBe("kept");
  });

  it("does not generate again when content exists", async () => {
    const fetchMock = stub(ready);
    const wrapper = open();
    await flushPromises();
    expect(generateCalls(fetchMock)).toHaveLength(0);
    expect(wrapper.findAll("textarea")[0].element.value).toBe(stored.sms);
  });

  it("shows partial text is gone and offers a retry when generation fails", async () => {
    const fetchMock = stub(base, async () => ({
      ok: true,
      status: 200,
      body: streamOf(event("delta", { field: "sms", text: "partial" }), event("error", { error: "The AI service is unavailable." })),
    }));
    const wrapper = open();
    await vi.waitFor(() => expect(wrapper.text()).toContain("The AI service is unavailable."));
    expect(wrapper.findAll("textarea")[0].element.value).toBe("");
    expect(wrapper.find("[data-testid=regenerate]").text()).toBe("Try again");
    expect(generateCalls(fetchMock)).toHaveLength(1);
  });

  it("does not retry by itself after an earlier failure", async () => {
    const fetchMock = stub({ ...base, status: "GENERATION_FAILED" });
    const wrapper = open();
    await flushPromises();
    expect(generateCalls(fetchMock)).toHaveLength(0);
    expect(wrapper.find("[data-testid=regenerate]").text()).toBe("Try again");
  });

  it("keeps existing messages when a regeneration fails", async () => {
    stub(ready, async () => ({ ok: true, status: 200, body: streamOf(event("error", { error: "Nope." })) }));
    const wrapper = open();
    await flushPromises();
    await wrapper.find("form").trigger("submit");
    await vi.waitFor(() => expect(wrapper.text()).toContain("Nope."));
    expect(wrapper.findAll("textarea")[0].element.value).toBe(stored.sms);
    expect(wrapper.text()).toContain("unchanged");
  });

  it("stops on request", async () => {
    stub(base, () => new Promise(() => {}));
    const wrapper = open();
    await flushPromises();
    const fetchMock = vi.mocked(fetch);
    const [, init] = fetchMock.mock.calls.find(([url]) => String(url).endsWith("/generate"))!;
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Writing");
    await wrapper.find("[data-testid=stop]").trigger("click");
    expect(init?.signal?.aborted).toBe(true);
  });

  it("shows what the backend rejected on the field and stays on the step", async () => {
    navigate("/campaigns/c1/sms");
    stub(ready, undefined, () => ({
      ok: false,
      status: 422,
      json: async () => ({ error: "Fix the highlighted messages.", issues: [{ field: "sms", reason: "too_long", message: "SMS is too long." }] }),
    }));
    const wrapper = open();
    await flushPromises();

    await wrapper.find("textarea").setValue("Changed");
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Unsaved changes");
    await wrapper.find("[data-testid=continue]").trigger("click");
    await flushPromises();

    expect(wrapper.find("[data-testid=message-problem]").text()).toBe("SMS is too long.");
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Needs changes");
    expect(currentPath.value).toBe("/campaigns/c1/sms");
  });

  it("saves what was typed when moving on", async () => {
    const put = vi.fn(() => ({ ok: true, status: 200, json: async () => ({ ...ready, content: { ...stored, sms: "Changed", edited: ["sms"] } }) }));
    const fetchMock = stub(ready, undefined, put);
    const wrapper = open();
    await flushPromises();

    await wrapper.find("textarea").setValue("Changed");
    await wrapper.find("[data-testid=continue]").trigger("click");
    await flushPromises();

    expect(put).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls.find(([, options]) => options?.method === "PUT")!;
    expect(JSON.parse(init!.body as string).sms).toBe("Changed");
    expect(currentPath.value).toBe("/campaigns/c1/email");
  });

  it("moves on without saving when nothing changed", async () => {
    const put = vi.fn();
    stub(ready, undefined, put);
    const wrapper = open("email");
    await flushPromises();

    await wrapper.find("[data-testid=continue]").trigger("click");
    await flushPromises();

    expect(put).not.toHaveBeenCalled();
    expect(currentPath.value).toBe("/campaigns/c1/review");
  });

  it("goes SMS, email, review and back the same way", async () => {
    stub(ready);
    const sms = open("sms");
    await flushPromises();
    expect(sms.find("[data-testid=continue]").text()).toBe("Continue to email");
    expect(sms.find("[data-testid=back]").text()).toBe("Back to audience");

    const email = open("email");
    await flushPromises();
    expect(email.find("[data-testid=continue]").text()).toBe("Continue to review");
    expect(email.find("[data-testid=back]").text()).toBe("Back to SMS");
  });

  it("shows one channel at a time with one state for the whole message", async () => {
    stub(ready);
    const sms = open("sms");
    await flushPromises();
    expect(sms.findAll("textarea")).toHaveLength(1);
    expect(sms.findAll("[data-testid=message-state]")).toHaveLength(1);
    expect(sms.text()).not.toContain("Subject");
    expect(sms.find("[data-testid=phone-message]").exists()).toBe(true);

    const email = open("email");
    await flushPromises();
    expect(email.findAll("[data-testid=message-state]")).toHaveLength(1);
    expect(email.text()).toContain("Subject");
    expect(email.find("[data-testid=phone-message]").exists()).toBe(false);
  });

  it("keeps an optional button out of the way until it has a problem", async () => {
    stub(ready, undefined, () => ({
      ok: false,
      status: 422,
      json: async () => ({ error: "Fix it.", issues: [{ field: "email_cta_url", reason: "invalid_link", message: "Bad link." }] }),
    }));
    const wrapper = open("email");
    await flushPromises();
    const button = () => wrapper.find("[data-testid=email-button]").element as HTMLDetailsElement;
    expect(button().open).toBe(false);

    await wrapper.findAll("textarea")[0].setValue("Changed");
    await wrapper.find("[data-testid=continue]").trigger("click");
    await flushPromises();

    expect(button().open).toBe(true);
  });

  it("regenerates only this channel and leaves the other one alone", async () => {
    const newSms = { ...ready, content: { ...stored, sms: "New" } };
    const fetchMock = stub(ready, async () => ({ ok: true, status: 200, body: streamOf(event("delta", { field: "sms", text: "New" }), event("done", newSms)) }));
    const wrapper = open("sms");
    await flushPromises();
    expect(wrapper.find("[data-testid=regenerate-scope]").text()).toContain("The email stays as it is.");

    await wrapper.find("form").trigger("submit");
    await vi.waitFor(() => expect(wrapper.find("textarea").element.value).toBe("New"));

    const [, init] = generateCalls(fetchMock)[0];
    expect(JSON.parse(init!.body as string).channel).toBe("sms");
  });

  it("previews the template as soon as it is picked and saves it when moving on", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const put = vi.fn(() => ({ ok: true, status: 200, json: async () => ({ ...ready, content: { ...stored, email_template: "banner", edited: ["email_template"] } }) }));
    const fetchMock = stub(ready, undefined, put);
    const wrapper = open("email");
    await flushPromises();
    expect((wrapper.find("[data-testid=email-template-classic]").element as HTMLInputElement).checked).toBe(true);

    await wrapper.find("[data-testid=email-template-banner]").setValue();
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Unsaved changes");
    expect(wrapper.find("[data-testid=message-state]").text()).not.toContain("Edited");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();
    const previews = fetchMock.mock.calls.filter(([url]) => url.endsWith("/api/email-preview"));
    expect(JSON.parse(previews.at(-1)![1]!.body as string).email_template).toBe("banner");

    await wrapper.find("[data-testid=continue]").trigger("click");
    await flushPromises();
    vi.useRealTimers();
    const [, init] = fetchMock.mock.calls.find(([, options]) => options?.method === "PUT")!;
    expect(JSON.parse(init!.body as string).email_template).toBe("banner");
  });

  it("keeps a template picked just now when the text is regenerated", async () => {
    const newText = { ...ready, content: { ...stored, email_subject: "New" } };
    stub(ready, async () => ({ ok: true, status: 200, body: streamOf(event("done", newText)) }));
    const wrapper = open("email");
    await flushPromises();

    await wrapper.find("[data-testid=email-template-offer]").setValue();
    await wrapper.find("form").trigger("submit");
    await vi.waitFor(() => expect(wrapper.findAll("textarea")[0].element.value).toBe("New"));

    expect((wrapper.find("[data-testid=email-template-offer]").element as HTMLInputElement).checked).toBe(true);
  });

  it("says when an instruction was not done", async () => {
    stub({ ...ready, content_warnings: [{ field: "sms", reason: "not_applied", message: "Your instruction mentions 3, but this message does not." }] });
    const sms = open("sms");
    await flushPromises();

    expect(sms.text()).toContain("Not done");
    expect(sms.text()).toContain("Your instruction mentions 3");
    expect(sms.text()).not.toContain("Differs from the brief");
  });

  it("shows only the warnings of this channel", async () => {
    const warned = { ...ready, content_warnings: [{ field: "email_body", reason: "invented_discount", message: "Email mentions 40%." }] };
    stub(warned);
    const sms = open("sms");
    await flushPromises();
    expect(sms.text()).not.toContain("Email mentions 40%.");

    const email = open("email");
    await flushPromises();
    expect(email.text()).toContain("Email mentions 40%.");
  });

  it("sends people on when the campaign does not use this channel", async () => {
    stub({ ...ready, channels: ["email"] });
    const wrapper = open("sms");
    await flushPromises();
    expect(wrapper.find("a").attributes("href")).toBe("/campaigns/c1/email");
    expect(wrapper.find("textarea").exists()).toBe(false);
  });

  it("counts length for the longest name in the audience", async () => {
    stub(ready);
    const wrapper = open();
    await flushPromises();
    // "Hi " plus a 21 character name.
    expect(wrapper.find("[data-testid=char-count]").text()).toContain("24 / 160");
  });

  it("previews messages for a person without a name using the fallback", async () => {
    stub(ready);
    const wrapper = open();
    await flushPromises();
    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Hi Ayşe");
    await wrapper.find("select").setValue("2");
    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Hi there");
  });

  it("surfaces differences from the brief for a person to judge", async () => {
    stub({ ...ready, content_warnings: [{ field: "sms", reason: "invented_discount", message: "SMS mentions 40%." }] });
    const wrapper = open();
    await flushPromises();
    expect(wrapper.text()).toContain("Differs from the brief");
    expect(wrapper.text()).toContain("SMS mentions 40%.");
  });

  it("sends people back when the audience is not ready", async () => {
    stub({ ...base, blockers: ["No users match."] });
    const wrapper = open();
    await flushPromises();
    expect(wrapper.find("a").attributes("href")).toBe("/campaigns/c1/edit");
  });
});

describe("content on the campaign page", () => {
  it("shows a read-only tab once content exists", async () => {
    vi.stubGlobal("fetch", vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () => (url.endsWith("/activity") ? { activity: [] } : { ...ready, content: { ...stored, edited: ["sms"] } }),
    })));
    const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
    await flushPromises();

    expect(wrapper.find("[data-testid=continue-editing]").attributes("href")).toBe("/campaigns/c1/sms");
    await wrapper.find("#tab-content").trigger("click");
    expect(wrapper.find("[data-testid=content-sms]").text()).toContain("Edited by you");
    expect(wrapper.find("[data-testid=content-email]").text()).toContain("AI suggested");
    expect(wrapper.find("textarea").exists()).toBe(false);
  });

  it("has no content tab before content exists", async () => {
    vi.stubGlobal("fetch", vi.fn(async (url: string) => ({
      ok: true,
      status: 200,
      json: async () => (url.endsWith("/activity") ? { activity: [] } : base),
    })));
    const wrapper = mount(CampaignDetailView, { props: { campaignId: "c1" } });
    await flushPromises();
    expect(wrapper.find("#tab-content").exists()).toBe(false);
  });
});

import { mount } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";

import ApprovalSummary from "./ApprovalSummary.vue";
import AudienceRule from "./AudienceRule.vue";
import BriefComposer from "./BriefComposer.vue";
import CampaignStepper from "./CampaignStepper.vue";
import DeliveryTimeline from "./DeliveryTimeline.vue";
import MessageEditor from "./MessageEditor.vue";
import MetricCard from "./MetricCard.vue";
import PhonePreview from "./PhonePreview.vue";
import StatusBadge from "./StatusBadge.vue";
import SystemHealthCard from "./SystemHealthCard.vue";
import ValidationNotice from "./ValidationNotice.vue";
import { systemHealth } from "../lib/systemHealth";

const steps = [
  { id: "brief", label: "Brief" },
  { id: "audience", label: "Audience" },
  { id: "content", label: "Content" },
];

describe("CampaignStepper", () => {
  it("marks only the current step for assistive technology", () => {
    const wrapper = mount(CampaignStepper, { props: { steps, currentId: "audience" } });

    const current = wrapper.findAll("[aria-current=step]");
    expect(current).toHaveLength(1);
    expect(current[0].text()).toContain("Audience");
  });

  it("says completed in text, not only with color", () => {
    const wrapper = mount(CampaignStepper, { props: { steps, currentId: "audience" } });

    const [brief, audience] = wrapper.findAll("li");
    expect(brief.text()).toContain("Completed");
    expect(audience.text()).not.toContain("Completed");
  });
});

describe("BriefComposer", () => {
  const props = { modelValue: "", actionLabel: "Find audience", suggestions: ["Win back cart abandoners"] };

  it("blocks an empty brief and explains why", () => {
    const wrapper = mount(BriefComposer, { props });

    expect(wrapper.find("button[type=submit]").attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=brief-hint]").text()).toContain("Write a brief");
  });

  it("submits a written brief", async () => {
    const wrapper = mount(BriefComposer, { props: { ...props, modelValue: "Send a reminder" } });

    await wrapper.find("form").trigger("submit");

    expect(wrapper.emitted("submit")).toHaveLength(1);
  });

  it("does not submit while unavailable and gives the reason", async () => {
    const wrapper = mount(BriefComposer, {
      props: { ...props, modelValue: "Send a reminder", unavailableReason: "Audience generation is not available yet." },
    });

    await wrapper.find("form").trigger("submit");

    expect(wrapper.emitted("submit")).toBeUndefined();
    expect(wrapper.find("[data-testid=brief-hint]").text()).toContain("not available yet");
  });

  it("fills the brief from a suggestion", async () => {
    const wrapper = mount(BriefComposer, { props });

    await wrapper.find("[data-testid=brief-suggestion]").trigger("click");

    expect(wrapper.emitted("update:modelValue")?.[0]).toEqual(["Win back cart abandoners"]);
  });

  it("has a real label on the text area", () => {
    const wrapper = mount(BriefComposer, { props });

    const id = wrapper.find("textarea").attributes("id");
    expect(wrapper.find(`label[for=${id}]`).exists()).toBe(true);
  });
});

describe("AudienceRule", () => {
  it("separates AI suggestions from user edits", () => {
    const suggested = mount(AudienceRule, { props: { label: "City", value: "İstanbul", origin: "ai" } });
    const edited = mount(AudienceRule, { props: { label: "City", value: "İstanbul", origin: "user" } });

    expect(suggested.text()).toContain("AI suggested");
    expect(edited.text()).toContain("Edited by you");
  });

  it("flags assumptions so they get checked", () => {
    const wrapper = mount(AudienceRule, { props: { label: "Window", value: "30 days", origin: "ai", assumed: true } });

    expect(wrapper.text()).toContain("Assumption");
  });
});

describe("MetricCard", () => {
  it("shows the value and label with thousands separators", () => {
    const wrapper = mount(MetricCard, { props: { label: "Matching customers", value: 1284 } });

    expect(wrapper.text()).toContain("1,284");
    expect(wrapper.text()).toContain("Matching customers");
  });
});

describe("MessageEditor", () => {
  const props = { label: "SMS", modelValue: "Hi {{first_name}}", limit: 160, state: "draft" as const, origin: "ai" as const };

  it("shows the character count against the limit", () => {
    const wrapper = mount(MessageEditor, { props });

    expect(wrapper.find("[data-testid=char-count]").text()).toBe("17 / 160");
  });

  it("reports how far a message is over the limit", () => {
    const wrapper = mount(MessageEditor, { props: { ...props, modelValue: "x".repeat(172) } });

    expect(wrapper.find("[data-testid=char-count]").text()).toBe("172 / 160, 12 over");
    expect(wrapper.find("textarea").attributes("aria-invalid")).toBe("true");
  });

  it("locks the text and calls it a draft while streaming", () => {
    const wrapper = mount(MessageEditor, { props: { ...props, state: "streaming" } });

    expect(wrapper.find("textarea").attributes("readonly")).toBeDefined();
    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Draft");
    expect(wrapper.text()).not.toContain("Validated");
  });

  it("never shows validated for a message that breaks a local rule", () => {
    const wrapper = mount(MessageEditor, { props: { ...props, state: "validated", modelValue: "x".repeat(172) } });

    expect(wrapper.find("[data-testid=message-state]").text()).not.toContain("Validated");
  });

  it("shows validated after backend confirmation", () => {
    const wrapper = mount(MessageEditor, { props: { ...props, state: "validated" } });

    expect(wrapper.find("[data-testid=message-state]").text()).toContain("Validated");
  });

  it("names unsupported variables", () => {
    const wrapper = mount(MessageEditor, { props: { ...props, modelValue: "Use {{coupon}}" } });

    expect(wrapper.text()).toContain("coupon");
    expect(wrapper.find("textarea").attributes("aria-invalid")).toBe("true");
  });

  it("emits edits as the user types", async () => {
    const wrapper = mount(MessageEditor, { props });

    await wrapper.find("textarea").setValue("Edited");

    expect(wrapper.emitted("update:modelValue")?.[0]).toEqual(["Edited"]);
  });

  it("inserts a supported variable", async () => {
    const wrapper = mount(MessageEditor, { props: { ...props, modelValue: "Hi " } });

    await wrapper.find("[data-testid=insert-variable]").trigger("click");

    expect(wrapper.emitted("update:modelValue")?.[0]).toEqual(["Hi {{first_name}}"]);
  });
});

describe("PhonePreview", () => {
  const recipients = [
    { id: 1, label: "Ayşe", first_name: "Ayşe" },
    { id: 2, label: "No first name", first_name: "" },
  ];

  it("renders the message for the selected recipient", async () => {
    const wrapper = mount(PhonePreview, { props: { message: "Hi {{first_name}}!", recipients } });

    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Hi Ayşe!");

    await wrapper.find("select").setValue("2");

    expect(wrapper.find("[data-testid=phone-message]").text()).toBe("Hi there!");
  });

  it("shows the personalized length, which can differ from the template", () => {
    const wrapper = mount(PhonePreview, { props: { message: "Hi {{first_name}}!", recipients } });

    expect(wrapper.find("[data-testid=phone-length]").text()).toContain("8");
  });
});

describe("ValidationNotice", () => {
  it("announces errors immediately", () => {
    const wrapper = mount(ValidationNotice, { props: { tone: "error", title: "SMS is too long" } });

    expect(wrapper.attributes("role")).toBe("alert");
  });

  it("announces other states politely", () => {
    const wrapper = mount(ValidationNotice, { props: { tone: "success", title: "Content validated" } });

    expect(wrapper.attributes("role")).toBe("status");
  });
});

describe("ApprovalSummary", () => {
  const props = { smsCount: 1240, emailCount: 1510, conditions: ["City is İstanbul"] };

  it("states the exact send in the button", () => {
    const wrapper = mount(ApprovalSummary, { props });

    expect(wrapper.find("button").text()).toBe("Approve & send 2,750 messages");
  });

  it("uses the singular for one message", () => {
    const wrapper = mount(ApprovalSummary, { props: { ...props, smsCount: 1, emailCount: 0 } });

    expect(wrapper.find("button").text()).toBe("Approve & send 1 message");
  });

  it("emits approve once the user decides", async () => {
    const wrapper = mount(ApprovalSummary, { props });

    await wrapper.find("button").trigger("click");

    expect(wrapper.emitted("approve")).toHaveLength(1);
  });

  it("blocks approval with no recipients and says why", async () => {
    const wrapper = mount(ApprovalSummary, { props: { ...props, smsCount: 0, emailCount: 0 } });

    expect(wrapper.find("button").attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("No one can be reached");
  });

  it("lists every blocker", () => {
    const wrapper = mount(ApprovalSummary, { props: { ...props, blockers: ["SMS is 12 characters over"] } });

    expect(wrapper.find("button").attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("SMS is 12 characters over");
  });

  it("prevents a second approval while submitting", async () => {
    const wrapper = mount(ApprovalSummary, { props: { ...props, submitting: true } });

    await wrapper.find("button").trigger("click");

    expect(wrapper.find("button").attributes("disabled")).toBeDefined();
    expect(wrapper.emitted("approve")).toBeUndefined();
  });
});

describe("StatusBadge", () => {
  it("shows a text label for every state", () => {
    const wrapper = mount(StatusBadge, { props: { status: "UNKNOWN" } });

    expect(wrapper.text()).toBe("Outcome unknown");
  });
});

describe("DeliveryTimeline", () => {
  it("explains that acceptance is not delivery", () => {
    const wrapper = mount(DeliveryTimeline, {
      props: { events: [{ id: "1", status: "SENT" as const, title: "SMS to user 12", at: "12:04:31" }] },
    });

    expect(wrapper.text()).toContain("Accepted");
    expect(wrapper.text()).toContain("does not confirm delivery");
  });

  it("has a useful empty state", () => {
    const wrapper = mount(DeliveryTimeline, { props: { events: [] } });

    expect(wrapper.text()).toContain("No delivery activity yet");
  });
});

describe("SystemHealthCard", () => {
  afterEach(() => {
    systemHealth.value = null;
  });

  it("names what is down and what that means", () => {
    systemHealth.value = { status: "degraded", checks: { database: "ok", redis: "down" } };
    const wrapper = mount(SystemHealthCard);
    expect(wrapper.find("[data-testid=health-database]").text()).toBe("Up");
    expect(wrapper.find("[data-testid=health-redis]").text()).toContain("Messages wait");
    expect(wrapper.find("[data-testid=health-worker]").exists()).toBe(false);
  });

  it("does not claim anything when the system cannot be reached", () => {
    systemHealth.value = { status: "unavailable", checks: {} };
    expect(mount(SystemHealthCard).find("[data-testid=system-health-unknown]").exists()).toBe(true);
  });
});

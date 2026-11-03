import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App.vue";
import { navigate } from "./lib/navigation";

function stubReadiness(response: Partial<Response> | Error) {
  const fetchMock =
    response instanceof Error
      ? vi.fn().mockRejectedValue(response)
      : vi.fn(async (url: string) => (url === "/api/system" ? { json: async () => ({ checks: { database: "ok" } }), ...response } : response));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("keeps the brief at its own address", async () => {
    stubReadiness({ ok: true });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("h1").text()).toBe("Bring your next campaign to life.");
    expect(wrapper.find("textarea").exists()).toBe(true);
  });

  it("shows the campaign status and current step while creating", async () => {
    stubReadiness({ ok: true });
    navigate("/campaigns/new");
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("[data-testid=campaign-status]").text()).toBe("Draft");
    expect(wrapper.find("[aria-current=step]").text()).toContain("Brief");
  });

  it("separates daily work from reference material in the sidebar", async () => {
    stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();

    const groups = wrapper.findAll("[data-testid=nav-group]");
    expect(groups.map((group) => group.find("[data-testid=nav-group-title]").text())).toEqual(["Workspace", "Reference"]);
    expect(groups[0].text()).toContain("Overview");
    expect(groups[0].text()).toContain("Campaigns");
    expect(groups[0].text()).not.toContain("Design system");
    expect(wrapper.find("[data-testid=new-campaign]").attributes("href")).toBe("/campaigns/new");
    expect(groups[1].text()).toContain("Design system");
  });

  it("says when a page does not exist", async () => {
    stubReadiness({ ok: true });
    navigate("/nope");
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("h1").text()).toBe("This page does not exist.");
  });

  it("shows the system as operational when the API is ready", async () => {
    const fetchMock = stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledWith("/api/system");
    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Operational");
  });

  it("says sending is affected when only the worker is down", async () => {
    stubReadiness({ ok: true, json: async () => ({ checks: { database: "ok", worker: "down" } }) });
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Sending affected");
  });

  it("shows the system as unavailable when the database is down", async () => {
    stubReadiness({ ok: true, json: async () => ({ checks: { database: "down", worker: "ok" } }) });
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Unavailable");
  });

  it("shows the system as unavailable when the API reports a problem", async () => {
    stubReadiness({ ok: false });
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Unavailable");
  });

  it("shows the system as unavailable when the API cannot be reached", async () => {
    stubReadiness(new Error("network down"));
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Unavailable");
  });

  it("labels the design review page as sample data", async () => {
    stubReadiness({ ok: true });
    navigate("/design");
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("h1").text()).toBe("Signal design system");
    expect(wrapper.text()).toContain("Sample data");
    expect(wrapper.text()).toContain("Approve & send");
  });
});

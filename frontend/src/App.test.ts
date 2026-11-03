import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App.vue";
import { navigate } from "./lib/navigation";

function stubReadiness(response: Partial<Response> | Error) {
  const fetchMock =
    response instanceof Error
      ? vi.fn().mockRejectedValue(response)
      : vi.fn().mockResolvedValue(response);
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

afterEach(() => {
  navigate("/");
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("opens on the brief, not an empty dashboard", async () => {
    stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("h1").text()).toBe("Bring your next campaign to life.");
    expect(wrapper.find("textarea").exists()).toBe(true);
  });

  it("always shows the campaign status and current step", async () => {
    stubReadiness({ ok: true });
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
    expect(groups[0].text()).toContain("New campaign");
    expect(groups[0].text()).not.toContain("Design system");
    expect(groups[1].text()).toContain("Design system");
  });

  it("shows the system as operational when the API is ready", async () => {
    const fetchMock = stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledWith("/readyz");
    expect(wrapper.find("[data-testid=system-status]").text()).toBe("Operational");
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

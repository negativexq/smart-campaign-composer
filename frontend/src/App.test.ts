import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it, vi } from "vitest";

import App from "./App.vue";

function stubReadiness(response: Partial<Response> | Error) {
  const fetchMock =
    response instanceof Error
      ? vi.fn().mockRejectedValue(response)
      : vi.fn().mockResolvedValue(response);
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("App", () => {
  it("offers a brief field as the first step", async () => {
    stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();

    expect(wrapper.find("h1").text()).toBe("New campaign");
    expect(wrapper.find("textarea").attributes("placeholder")).toContain("Describe");
  });

  it("keeps generation disabled and says why", async () => {
    stubReadiness({ ok: true });
    const wrapper = mount(App);
    await flushPromises();
    await wrapper.find("textarea").setValue("Send a 30% discount reminder");

    const button = wrapper.find("button");
    expect(button.attributes("disabled")).toBeDefined();
    expect(wrapper.find("[data-testid=action-hint]").text()).not.toBe("");
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
});

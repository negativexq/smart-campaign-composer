import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { EmailDesign } from "../api/campaigns";
import AssetLibrary from "../components/AssetLibrary.vue";
import BrandKitView from "./BrandKitView.vue";
import TemplateBuilderView from "./TemplateBuilderView.vue";

const LOGO = "11111111-2222-3333-4444-555555555555";
const BRAND = { name: "Acme", logo_asset_id: LOGO, accent: "#0A7A5C", page: "#EEF2F1", button: "pill", font: "serif", footer: "Acme Ltd" };
const ASSET = { id: LOGO, name: "logo.png", mime: "image/png", bytes: 2048, width: 200, height: 80, in_use: false, url: `/api/assets/${LOGO}/file`, created_at: "" };

type Call = { url: string; init?: RequestInit; body?: Record<string, unknown> };
let calls: Call[];

function stubApi(overrides: Record<string, unknown> = {}) {
  calls = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ url, init, body });
    const replies: Record<string, unknown> = {
      "/api/brand": BRAND,
      "/api/assets": init?.method === "POST" ? ASSET : { assets: [ASSET] },
      "/api/email-designs": init?.method === "POST" ? { ...body, id: "new" } : { designs: [] },
      "/api/email-preview": { subject: "s", html: "<html><body>drawn</body></html>", text: "t" },
      ...overrides,
    };
    const reply = replies[url];
    return { ok: true, status: 200, json: async () => reply ?? {} } as Response;
  }));
}

const previews = () => calls.filter((call) => call.url === "/api/email-preview");
const lastPreview = () => previews().at(-1)?.body?.email_design as EmailDesign;
const typesInOrder = (wrapper: ReturnType<typeof mount>) => wrapper.findAll("[data-testid=layers] li").map((row) => row.attributes("data-testid"));

async function open() {
  const wrapper = mount(TemplateBuilderView);
  await flushPromises();
  await flushPromises();
  return wrapper;
}

async function settle() {
  await vi.advanceTimersByTimeAsync(250);
  await flushPromises();
}

describe("template studio", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("starts from the brand kit, with the brand's logo already in place", async () => {
    stubApi();
    const wrapper = await open();
    expect(lastPreview().accent).toBe("#0A7A5C");
    expect(lastPreview().footer).toBe("Acme Ltd");
    const logo = lastPreview().blocks.find((block) => block.type === "logo");
    expect(logo?.asset_id).toBe(LOGO);
    expect(previews().at(-1)?.body?.editing).toBe(true);
    expect(wrapper.find("[data-testid=design-dirty]").exists()).toBe(false);
  });

  it("opens a block's settings when it is clicked in the preview or in the list", async () => {
    stubApi();
    const wrapper = mount(TemplateBuilderView, { attachTo: document.body });
    await flushPromises();
    await flushPromises();
    const settings = wrapper.find("[data-testid=properties]");
    expect(wrapper.find("[data-testid=side-blocks]").attributes("aria-pressed")).toBe("true");
    expect(settings.classes()).toContain("panel--off");

    const frame = wrapper.find("[data-testid=design-frame]");
    const doc = (frame.element as HTMLIFrameElement).contentDocument!;
    const headline = lastPreview().blocks.find((block) => block.slot === "headline")!;
    doc.body.innerHTML = `<p id="outside">margin</p><div data-block="${headline.id}"><h1 id="inside">Words</h1></div>`;
    await frame.trigger("load");

    doc.getElementById("inside")!.click();
    await flushPromises();
    expect(settings.find("h2").text()).toBe("Headline");
    expect(settings.classes()).not.toContain("panel--off");
    expect(wrapper.find("[data-testid=side-settings]").attributes("aria-pressed")).toBe("true");

    // Clicking beside the blocks lets go of the block without hiding the panel.
    doc.getElementById("outside")!.click();
    await flushPromises();
    expect(settings.find("h2").text()).toBe("Email style");
    expect(settings.classes()).not.toContain("panel--off");

    // Adding several blocks stays in the list; choosing one from it opens the settings.
    await wrapper.find("[data-testid=side-blocks]").trigger("click");
    await wrapper.find("[data-testid=add-divider]").trigger("click");
    expect(settings.classes()).toContain("panel--off");
    await wrapper.find("[data-testid=layer-cta] .layer-name").trigger("click");
    await flushPromises();
    expect(settings.classes()).not.toContain("panel--off");
    wrapper.unmount();
  });

  it("lets the page hear the frame's clicks in every browser, and opens the chooser for an empty picture", async () => {
    stubApi();
    const wrapper = mount(TemplateBuilderView, { attachTo: document.body });
    await flushPromises();
    await flushPromises();
    const frame = wrapper.find("[data-testid=design-frame]");
    // Without allow-scripts Safari silently drops listeners on a sandboxed frame.
    expect(frame.attributes("sandbox")).toContain("allow-scripts");

    const doc = (frame.element as HTMLIFrameElement).contentDocument!;
    const blocks = lastPreview().blocks;
    const empty = blocks.find((block) => block.type === "image" && !block.asset_id)!;
    const logo = blocks.find((block) => block.type === "logo" && block.asset_id)!;
    doc.body.innerHTML = `<div id="empty" data-block="${empty.id}">Choose a picture</div><div id="logo" data-block="${logo.id}">logo</div>`;
    await frame.trigger("load");

    doc.getElementById("empty")!.click();
    await flushPromises();
    expect(wrapper.find("[data-testid=properties] h2").text()).toBe("Image");
    expect(wrapper.findComponent(AssetLibrary).exists()).toBe(true);

    // A block that already has its picture opens its settings, not the chooser.
    doc.getElementById("logo")!.click();
    await flushPromises();
    expect(wrapper.find("[data-testid=properties] h2").text()).toBe("Logo");
    expect(wrapper.findComponent(AssetLibrary).exists()).toBe(false);
    wrapper.unmount();
  });

  it("adds, reorders and removes blocks, and the backend draws each change", async () => {
    stubApi();
    const wrapper = await open();
    const before = typesInOrder(wrapper);

    await wrapper.find("[data-testid=add-divider]").trigger("click");
    await settle();
    expect(lastPreview().blocks.some((block) => block.type === "divider")).toBe(true);
    expect(wrapper.find("[data-testid=design-dirty]").exists()).toBe(true);

    await wrapper.findAll("[data-testid=layer-down]")[0].trigger("click");
    await settle();
    expect(typesInOrder(wrapper)[1]).toBe(before[0]);

    await wrapper.find("[data-testid=layer-divider] .layer-name").trigger("click");
    await wrapper.find("[data-testid=block-remove]").trigger("click");
    await settle();
    expect(lastPreview().blocks.some((block) => block.type === "divider")).toBe(false);
  });

  it("does not let the blocks the AI writes be removed or filled in", async () => {
    stubApi();
    const wrapper = await open();
    await wrapper.find("[data-testid=layer-headline] .layer-name").trigger("click");
    expect(wrapper.find("[data-testid=slot-note]").exists()).toBe(true);
    expect(wrapper.find("[data-testid=block-remove]").exists()).toBe(false);
    expect(wrapper.find("[data-testid=block-text]").exists()).toBe(false);

    await wrapper.find("[data-testid=block-style]").setValue("band");
    await settle();
    expect(lastPreview().blocks.find((block) => block.slot === "headline")?.style).toBe("band");
  });

  it("says fixed words are not translated, and saves the whole document", async () => {
    stubApi();
    const wrapper = await open();
    await wrapper.find("[data-testid=add-text]").trigger("click");
    expect(wrapper.find("[data-testid=fixed-note]").text()).toContain("not translated");
    await wrapper.find("[data-testid=block-text]").setValue("Free returns");

    const save = wrapper.find("[data-testid=design-save]");
    expect(save.attributes("disabled")).toBeDefined();
    await wrapper.find("[data-testid=design-name]").setValue("Winter");
    await save.trigger("click");
    await flushPromises();
    const saved = calls.find((call) => call.url === "/api/email-designs" && call.init?.method === "POST")?.body as unknown as EmailDesign;
    expect(saved.name).toBe("Winter");
    expect(saved.blocks.some((block) => block.text === "Free returns")).toBe(true);
    expect(saved.blocks.filter((block) => block.slot).map((block) => block.slot).sort()).toEqual(["body", "cta", "headline"]);
  });

  it("shows why the server refused a template, and stays on the page", async () => {
    stubApi();
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/api/email-designs" && init?.method === "POST") return { ok: false, status: 400, json: async () => ({ error: "Describe each image" }) } as Response;
      const reply = url === "/api/brand" ? BRAND : url === "/api/email-preview" ? { html: "<html></html>" } : {};
      return { ok: true, status: 200, json: async () => reply } as Response;
    });
    const wrapper = await open();
    await wrapper.find("[data-testid=design-name]").setValue("Winter");
    await wrapper.find("[data-testid=design-save]").trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("Describe each image");
    expect(wrapper.find("[data-testid=design-dirty]").exists()).toBe(true);
  });

  it("does not draw a color that is not a color", async () => {
    stubApi();
    const wrapper = await open();
    await settle();
    const drawn = previews().length;
    await wrapper.find("[data-testid=design-accent]").setValue("red");
    await settle();
    expect(wrapper.find("[data-testid=design-color-problem]").exists()).toBe(true);
    expect(previews().length).toBe(drawn);
  });
});

describe("image library", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uploads a picture and hands it to whoever is choosing", async () => {
    stubApi();
    vi.mocked(fetch).mockImplementation(async (_input, init) => ({ ok: true, status: 200, json: async () => (init?.method === "POST" ? ASSET : { assets: [] }) }) as Response);
    const wrapper = mount(AssetLibrary);
    await flushPromises();
    expect(wrapper.find("[data-testid=no-assets]").exists()).toBe(true);

    const input = wrapper.find("[data-testid=asset-upload]");
    Object.defineProperty(input.element, "files", { value: [new File(["x"], "logo.png", { type: "image/png" })], configurable: true });
    await input.trigger("change");
    await flushPromises();
    expect(wrapper.emitted("choose")?.[0][0]).toEqual(ASSET);
  });

  it("refuses a file that is too big without sending it", async () => {
    stubApi();
    const wrapper = mount(AssetLibrary);
    await flushPromises();
    const input = wrapper.find("[data-testid=asset-upload]");
    const huge = new File(["x"], "huge.png");
    Object.defineProperty(huge, "size", { value: 6 * 1024 * 1024 });
    Object.defineProperty(input.element, "files", { value: [huge], configurable: true });
    await input.trigger("change");
    await flushPromises();
    expect(wrapper.find("[data-testid=asset-error]").text()).toContain("5 MB");
    expect(calls.some((call) => call.init?.method === "POST")).toBe(false);
  });

  it("offers no delete for a picture in use", async () => {
    stubApi({ "/api/assets": { assets: [{ ...ASSET, in_use: true }] } });
    const wrapper = mount(AssetLibrary, { props: { manage: true } });
    await flushPromises();
    expect(wrapper.find("[data-testid=asset-in-use]").exists()).toBe(true);
    expect(wrapper.find("[data-testid=asset-delete]").exists()).toBe(false);
  });
});

describe("brand kit", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads, changes and saves the brand", async () => {
    stubApi();
    const wrapper = mount(BrandKitView);
    await flushPromises();
    expect(wrapper.find("[data-testid=brand-logo]").attributes("src")).toBe(`/api/assets/${LOGO}/file`);
    await wrapper.find("[data-testid=brand-name]").setValue("Acme Store");
    await wrapper.find("[data-testid=brand-form]").trigger("submit");
    await flushPromises();
    const saved = calls.find((call) => call.url === "/api/brand" && call.init?.method === "PUT");
    expect(saved?.body?.name).toBe("Acme Store");
    expect(wrapper.find("[data-testid=brand-saved]").exists()).toBe(true);
  });
});

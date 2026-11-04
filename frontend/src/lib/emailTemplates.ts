import type { BlockType, Brand, EmailBlock, EmailDesign } from "../api/campaigns";

export const EMAIL_TEMPLATES = [
  { id: "classic", name: "Classic", hint: "Clean and simple" },
  { id: "banner", name: "Announcement", hint: "Big colored header" },
  { id: "offer", name: "Offer", hint: "Centered, headline in a box" },
];

type SampleEmail = { email_subject: string; email_preheader: string; email_headline: string; email_body: string; email_cta_label: string; email_cta_url: string };

const sample = (words: string[], email_headline: string, message: string, email_cta_label: string, greeting = "Hi {{first_name}},") => ({
  words,
  email: {
    email_subject: email_headline,
    email_preheader: "Sample text",
    email_headline,
    email_body: `${greeting}\n\n${message}`,
    email_cta_label,
    email_cta_url: "https://example.com",
  } satisfies SampleEmail,
});

/** Only to show a design, so templates do not all read alike. Campaigns get their own words from the model. */
const SAMPLES = [
  sample(["hero", "cart", "reminder"], "Still thinking it over?", "The things you picked are still in your cart, ready when you are.", "Back to your cart"),
  sample(["flash", "sale", "discount"], "30% off, today only", "Our biggest sale of the season ends at midnight. Everything in the store is included.", "Shop the sale"),
  sample(["launch", "arrival", "new", "collection", "product"], "Meet the new collection", "Fresh designs have just landed, made for the days ahead. Be among the first to see them.", "See what is new"),
  sample(["event", "invite", "opening"], "You are invited", "Join us this Saturday evening for music, early access and a few surprises.", "Save your seat"),
  sample(["letter", "note", "autumn", "miss", "back"], "It has been a while", "We were thinking of you and wanted to say hello. A lot has changed since your last visit.", "Come and see", "Dear {{first_name}},"),
  sample(["member", "vip", "night", "private"], "An evening just for you", "The doors open early for our closest customers. Your place is already kept.", "Reserve your place", "Dear {{first_name}},"),
  sample(["news", "newsletter", "bulletin", "spring", "digest"], "What is new this season", "Three stories from us this month: a new look, a helpful guide and a customer favorite.", "Read the newsletter"),
  sample(["offer", "shipping", "deal", "free"], "Free shipping this weekend", "Order anything before Sunday night and it reaches your door at no extra cost.", "Start shopping"),
  sample(["announcement", "update", "banner"], "Something big is changing", "We have made ordering simpler and faster. Here is what it means for you.", "See the changes"),
  sample(["classic", "welcome", "hello"], "Welcome, it is good to have you", "Thanks for joining us. Here is a short tour of what you can do from today.", "Take the tour"),
];

export const SAMPLE_EMAIL = SAMPLES[0].email;

const wordsOf = (name: string) => name.toLocaleLowerCase("en-US").split(/[^\p{L}\p{N}]+/u);

/**
 * Sample words for each named template, in order. A name that suggests a kind of email gets
 * words of that kind, the rest take the next ones free, so no two read alike while any are left.
 */
export function samplesFor(names: string[]): SampleEmail[] {
  const taken = new Set<number>();
  const chosen: number[] = names.map((name) => {
    const words = wordsOf(name);
    const fits = SAMPLES.findIndex((item, index) => !taken.has(index) && item.words.some((word) => words.includes(word)));
    if (fits >= 0) taken.add(fits);
    return fits;
  });
  let next = 0;
  return chosen.map((index) => {
    if (index >= 0) return SAMPLES[index].email;
    const free = SAMPLES.findIndex((_, candidate) => !taken.has(candidate));
    if (free >= 0) {
      taken.add(free);
      return SAMPLES[free].email;
    }
    return SAMPLES[next++ % SAMPLES.length].email;
  });
}

export const customTemplateName = (id: string) => `custom:${id}`;
export const isCustomTemplate = (name?: string) => Boolean(name?.startsWith("custom:"));

export const BLOCK_TYPES: { type: BlockType; label: string }[] = [
  { type: "logo", label: "Logo" },
  { type: "image", label: "Image" },
  { type: "heading", label: "Heading" },
  { type: "text", label: "Text" },
  { type: "button", label: "Button" },
  { type: "divider", label: "Divider" },
];

const SLOT_LABELS: Record<string, string> = { headline: "Headline", body: "Message", cta: "Button" };

export function blockLabel(block: EmailBlock): string {
  if (block.slot) return SLOT_LABELS[block.slot];
  return BLOCK_TYPES.find((item) => item.type === block.type)?.label ?? block.type;
}

let made = 0;
const nextId = () => `b${Date.now().toString(36)}-${(made++).toString(36)}`;

/** A new block. A logo arrives as the brand's own, so it is not chosen again each time. */
export function newBlock(type: BlockType, brand?: Brand | null): EmailBlock {
  const id = nextId();
  switch (type) {
    case "logo":
      return { id, type, width: 30, align: "left", asset_id: brand?.logo_asset_id || "", alt: brand?.name ?? "" };
    case "image":
      return { id, type, width: 100, align: "center", asset_id: "", alt: "" };
    case "heading":
      return { id, type, text: "", align: "left", style: "plain" };
    case "text":
      return { id, type, text: "", align: "left" };
    case "button":
      return { id, type, label: "", url: "", align: "left" };
    default:
      return { id, type };
  }
}

const slot = (name: NonNullable<EmailBlock["slot"]>, type: BlockType, extra: Partial<EmailBlock> = {}): EmailBlock => ({ id: nextId(), type, slot: name, align: "left", ...extra });

/** Starting arrangements. Each keeps the three blocks a campaign fills. */
export const LAYOUTS: { id: string; name: string; hint: string; blocks: (brand?: Brand | null) => EmailBlock[] }[] = [
  {
    id: "hero",
    name: "Hero image",
    hint: "Logo, big picture, message",
    blocks: (brand) => [newBlock("logo", brand), newBlock("image", brand), slot("headline", "heading", { style: "plain" }), slot("body", "text"), slot("cta", "button")],
  },
  {
    id: "announcement",
    name: "Announcement",
    hint: "Colored headline band",
    blocks: () => [slot("headline", "heading", { style: "band" }), slot("body", "text"), slot("cta", "button")],
  },
  {
    id: "offer",
    name: "Offer",
    hint: "Centered, headline in a box",
    blocks: (brand) => [
      { ...newBlock("logo", brand), align: "center" },
      slot("headline", "heading", { style: "box", align: "center" }),
      slot("body", "text", { align: "center" }),
      slot("cta", "button", { align: "center" }),
    ],
  },
  {
    id: "letter",
    name: "Letter",
    hint: "Reads like a note, signed with the logo",
    blocks: (brand) => [slot("headline", "heading", { style: "rule" }), slot("body", "text"), slot("cta", "button"), newBlock("divider"), { ...newBlock("logo", brand), width: 20 }],
  },
  {
    id: "newsletter",
    name: "Newsletter",
    hint: "Logo on top, sections split by lines",
    blocks: (brand) => [
      { ...newBlock("logo", brand), align: "center" },
      newBlock("divider"),
      slot("headline", "heading", { style: "rule" }),
      slot("body", "text"),
      newBlock("image", brand),
      slot("cta", "button"),
    ],
  },
  {
    id: "launch",
    name: "Launch",
    hint: "Picture first, big centered headline",
    blocks: () => [newBlock("image"), slot("headline", "heading", { style: "display", align: "center" }), slot("body", "text", { align: "center" }), slot("cta", "button", { align: "center" })],
  },
  {
    id: "event",
    name: "Event",
    hint: "Color band, picture, centered details",
    blocks: (brand) => [
      slot("headline", "heading", { style: "band", align: "center" }),
      newBlock("image", brand),
      slot("body", "text", { align: "center" }),
      slot("cta", "button", { align: "center" }),
      { ...newBlock("logo", brand), align: "center", width: 20 },
    ],
  },
  {
    id: "flash",
    name: "Flash sale",
    hint: "Big headline and button before the rest",
    blocks: (brand) => [newBlock("logo", brand), slot("headline", "heading", { style: "display" }), slot("cta", "button"), newBlock("image", brand), slot("body", "text")],
  },
];

export function brandedDesign(brand?: Brand | null): EmailDesign {
  return {
    id: "",
    name: "",
    accent: brand?.accent ?? "#315BE8",
    page: brand?.page ?? "#F5F7FA",
    button: brand?.button ?? "rounded",
    corners: 8,
    font: brand?.font ?? "sans",
    footer: brand?.footer ?? "",
    blocks: LAYOUTS[0].blocks(brand),
  };
}

/** Moves one item to a new place in a list, without changing the list it was given. */
export function moved<T>(items: T[], from: number, to: number): T[] {
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

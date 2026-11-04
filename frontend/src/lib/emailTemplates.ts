import type { BlockType, Brand, EmailBlock, EmailDesign } from "../api/campaigns";

export const EMAIL_TEMPLATES = [
  { id: "classic", name: "Classic", hint: "Clean and simple" },
  { id: "banner", name: "Announcement", hint: "Big colored header" },
  { id: "offer", name: "Offer", hint: "Centered, headline in a box" },
];

/** Only to show a design. Campaigns get their own words from the model. */
export const SAMPLE_EMAIL = {
  email_subject: "Your cart is still waiting",
  email_preheader: "Sample text",
  email_headline: "Still thinking it over?",
  email_body: "Hi {{first_name}},\n\nThis is sample text. Your campaign's own message goes here, written for its audience.",
  email_cta_label: "Back to your cart",
  email_cta_url: "https://example.com",
};

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

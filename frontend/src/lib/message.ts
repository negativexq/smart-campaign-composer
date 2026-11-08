export const SUPPORTED_VARIABLES = ["first_name", "discount_code", "link"] as const;

// discount_code is filled only for a campaign that has a code; without one it stays a variable.
// link is the SMS's short link, which the backend makes for each message when it is sent.
export type MessageValues = { first_name: string; discount_code?: string; link?: string };

// Mirrors content.ShortLinkBase: every SMS's {{link}} becomes this and eight characters.
export const SHORT_LINK_BASE = "https://url.short/";
export const SHORT_LINK_LENGTH = SHORT_LINK_BASE.length + 8;
// What a preview shows for {{link}}: each person gets a code of their own when it is sent.
export const SAMPLE_SHORT_LINK = `${SHORT_LINK_BASE}a1B2c3D4`;

const FALLBACKS = { first_name: "there" };
const NAME_FALLBACKS: Record<string, string> = { tr: "müşterimiz", en: "there", de: "Kundschaft", fr: "cher client", es: "cliente" };
const VARIABLE = /\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g;

// Counts Unicode code points. The backend counts the same way, so a message
// never passes in the browser and then fails on the server.
export function countCharacters(text: string): number {
  return [...text].length;
}

// The length once sent to the person with the longest name, or to someone whose
// name is replaced by the fallback. It matches the limit the backend enforces.
export function longestRenderedLength(template: string, longestName: number, code = ""): number {
  const fill = Math.max(longestName, FALLBACKS.first_name.length);
  return countCharacters(
    template.replace(VARIABLE, (placeholder, name: string) => {
      if (name === "first_name") return "x".repeat(fill);
      if (name === "discount_code" && code) return code;
      if (name === "link") return "x".repeat(SHORT_LINK_LENGTH);
      return placeholder;
    }),
  );
}

export function renderMessage(template: string, values: MessageValues, language = ""): string {
  return template.replace(VARIABLE, (placeholder, name: string) => {
    if (name === "first_name") return values.first_name || fallbackName(language);
    if (name === "discount_code" && values.discount_code) return values.discount_code;
    if (name === "link" && values.link) return values.link;
    return placeholder;
  });
}

/** The word that stands in for a missing first name, in the reader's language. */
export function fallbackName(language: string): string {
  return NAME_FALLBACKS[language] ?? FALLBACKS.first_name;
}

/**
 * Variables the backend will refuse: any it does not know, discount_code without a code,
 * and link outside an SMS whose brief has a link.
 */
export function findUnknownVariables(template: string, code = "", link = ""): string[] {
  const names = [...template.matchAll(VARIABLE)].map((match) => match[1]);
  return [...new Set(names.filter((name) => !isSupported(name) || (name === "discount_code" && !code) || (name === "link" && !link)))];
}

/** Whether a message carries the link. */
export function hasLink(template: string): boolean {
  return [...template.matchAll(VARIABLE)].some((match) => match[1] === "link");
}

/** Whether a message carries the discount code. */
export function hasCode(template: string): boolean {
  return [...template.matchAll(VARIABLE)].some((match) => match[1] === "discount_code");
}

function isSupported(name: string): boolean {
  return (SUPPORTED_VARIABLES as readonly string[]).includes(name);
}

// The GSM-7 alphabet fits more into a segment, but one character outside it, such
// as the Turkish ş or ı, moves the whole message to UCS-2.
const GSM_BASIC = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà";
const GSM_EXTENDED = "^{}\\[~]|€\f";

export type SmsSegments = { encoding: "GSM-7" | "Unicode"; segments: number };

export function smsSegments(text: string): SmsSegments {
  if (!text) return { encoding: "GSM-7", segments: 0 };
  let units = 0;
  for (const char of text) {
    if (GSM_BASIC.includes(char)) units += 1;
    else if (GSM_EXTENDED.includes(char)) units += 2;
    else {
      // UCS-2 counts UTF-16 units, so an emoji takes two.
      const length = text.length;
      return { encoding: "Unicode", segments: length <= 70 ? 1 : Math.ceil(length / 67) };
    }
  }
  return { encoding: "GSM-7", segments: units <= 160 ? 1 : Math.ceil(units / 153) };
}

/** The discount a campaign offers, from the form or else from its brief. */
export function campaignDiscount(campaign: { offer_percent?: number | null; brief: string }): string {
  if (campaign.offer_percent) return `${campaign.offer_percent}%`;
  return campaign.brief.match(/%\s*\d+|\d+\s*%/)?.[0] ?? "";
}

const CODE_SHAPE = /^[A-Z0-9][A-Z0-9-]{2,29}$/;
const CODE_WORDS = new Set(["kod", "kodu", "kodunu", "koduyla", "kodlu", "kupon", "kuponu", "kuponunu", "code", "coupon", "promo"]);

/** Whether text can be a discount code: capitals, digits and hyphens, 3 to 30 of them. */
export function validCode(code: string): boolean {
  return CODE_SHAPE.test(code);
}

// Mirrors content.linkPattern: a link ends before trailing punctuation.
const BRIEF_LINK = /https?:\/\/[^\s<>"]+[^\s<>".,;:!?)]/g;

/** The links a brief names, each once, in order, as the backend reads them. */
export function briefLinks(brief: string): string[] {
  return [...new Set(brief.match(BRIEF_LINK) ?? [])];
}

/**
 * The codes a brief names next to a word for one, as in "kodu SONBAHAR15" or "code SPRING10".
 * The backend reads the brief the same way and has the last word.
 */
export function briefCodes(brief: string): string[] {
  const words = brief.split(/[\s:"'“”’]+/).filter(Boolean);
  const found: string[] = [];
  words.forEach((word, index) => {
    const bare = word.replace(/[.,;!?)]+$/, "");
    if (!validCode(bare) || !/[A-Z]/.test(bare)) return;
    // A word for a code joined to it, not across a comma or the end of a sentence.
    const joined = (other: string | undefined) => Boolean(other) && !/[.,;!?]$/.test(other!) && CODE_WORDS.has(other!.toLocaleLowerCase("tr"));
    const before = words[index - 1];
    const named =
      joined(before) ||
      (["code", "kodu"].includes(before?.toLocaleLowerCase("tr") ?? "") && joined(words[index - 2])) ||
      (bare === word && CODE_WORDS.has(words[index + 1]?.toLocaleLowerCase("tr").replace(/[.,;!?]+$/, "") ?? ""));
    if (named && !found.includes(bare)) found.push(bare);
  });
  return found;
}

/** Says how many people a channel misses and why, since consent is the only reason. */
export function reachHint(total: number, reached: number, channel: string): string {
  const missed = Math.max(total - reached, 0);
  if (!missed) return `Everyone matching consented to ${channel}`;
  return `${missed.toLocaleString("en-US")} not reachable: no ${channel} consent`;
}

type Warning = { field: string; reason: string; message: string };

/** A note about several fields of one request reads as one note. */
export function uniqueWarnings<T extends Warning>(warnings: T[]): T[] {
  return warnings.filter((warning, index) => warnings.findIndex((other) => other.reason === warning.reason && other.message === warning.message) === index);
}

export function warningTitle(warning: Warning): string {
  if (warning.reason === "no_button") return "No button";
  return warning.reason === "not_applied" ? "Not done" : "Differs from the brief";
}

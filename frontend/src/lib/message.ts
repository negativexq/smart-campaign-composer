export const SUPPORTED_VARIABLES = ["first_name"] as const;

export type MessageValues = Record<(typeof SUPPORTED_VARIABLES)[number], string>;

const FALLBACKS: MessageValues = { first_name: "there" };
const NAME_FALLBACKS: Record<string, string> = { tr: "müşterimiz", en: "there" };
const VARIABLE = /\{\{\s*([a-zA-Z0-9_]+)\s*\}\}/g;

// Counts Unicode code points. The backend counts the same way, so a message
// never passes in the browser and then fails on the server.
export function countCharacters(text: string): number {
  return [...text].length;
}

// The length once sent to the person with the longest name, or to someone whose
// name is replaced by the fallback. It matches the limit the backend enforces.
export function longestRenderedLength(template: string, longestName: number): number {
  const fill = Math.max(longestName, FALLBACKS.first_name.length);
  return countCharacters(
    template.replace(VARIABLE, (placeholder, name: string) => (isSupported(name) ? "x".repeat(fill) : placeholder)),
  );
}

export function renderMessage(template: string, values: MessageValues, language = ""): string {
  return template.replace(VARIABLE, (placeholder, name: string) => {
    if (!isSupported(name)) return placeholder;
    return values[name] || fallbackName(language);
  });
}

/** The word that stands in for a missing first name, in the reader's language. */
export function fallbackName(language: string): string {
  return NAME_FALLBACKS[language] ?? FALLBACKS.first_name;
}

export function findUnknownVariables(template: string): string[] {
  const names = [...template.matchAll(VARIABLE)].map((match) => match[1]);
  return [...new Set(names.filter((name) => !isSupported(name)))];
}

function isSupported(name: string): name is keyof MessageValues {
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
  return warning.reason === "not_applied" ? "Not done" : "Differs from the brief";
}

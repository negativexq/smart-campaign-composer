import type { Campaign, LanguageAudience, MessageVersion } from "../api/campaigns";
import { fallbackName } from "./message";

const NAMES: Record<string, string> = { tr: "Turkish", en: "English", de: "German", fr: "French", es: "Spanish" };
/** Who gets a message when a campaign does not write in their language. */
const FALLBACK = "en";

export function languageName(language: string): string {
  return NAMES[language] ?? language;
}

function reachable(audience: LanguageAudience, channel?: "sms" | "email"): number {
  if (channel === "sms") return audience.sms;
  if (channel === "email") return audience.email;
  return audience.sms + audience.email;
}

/** Whose name to preview a version with: its own readers first, then anyone who reads it instead. */
export function recipientsFor<T extends { language?: string }>(recipients: T[], language: string, also: string[] = []): T[] {
  const readers = [language, ...also].flatMap((code) => recipients.filter((recipient) => recipient.language === code));
  return readers.length ? readers : recipients;
}

/** The language a reader of this one gets their message in. */
export function versionLanguage(campaign: Campaign, language: string): string {
  return NAMES[language] && !campaign.excluded_languages?.includes(language) ? language : FALLBACK;
}

/** A version with everyone who reads it, including those who fall back to it. */
export type VersionAudience = LanguageAudience & { also: string[] };

/** The versions this channel needs with their readers, largest first, and English last if only those who fall back read it. */
export function audienceLanguages(campaign: Campaign, channel?: "sms" | "email"): VersionAudience[] {
  const people = (campaign.languages ?? []).filter((audience) => reachable(audience, channel) > 0);
  const versions: VersionAudience[] = people
    .filter((audience) => versionLanguage(campaign, audience.language) === audience.language)
    .map((audience) => ({ ...audience, also: [] }));
  for (const audience of people) {
    if (versionLanguage(campaign, audience.language) === audience.language) continue;
    let english = versions.find((version) => version.language === FALLBACK);
    if (!english) {
      english = { language: FALLBACK, total: 0, sms: 0, email: 0, sms_name_length: 0, email_name_length: 0, also: [] };
      versions.push(english);
    }
    english.total += audience.total;
    english.sms += audience.sms;
    english.email += audience.email;
    english.sms_name_length = Math.max(english.sms_name_length, audience.sms_name_length);
    english.email_name_length = Math.max(english.email_name_length, audience.email_name_length);
    english.also.push(audience.language);
  }
  return versions;
}

/** Each language someone reachable reads, for choosing which to write in. English cannot be left out. */
export function languageChoices(campaign: Campaign): { language: string; name: string; people: number; chosen: boolean; locked: boolean }[] {
  return (campaign.languages ?? [])
    .filter((audience) => NAMES[audience.language] && reachable(audience) > 0)
    .map((audience) => ({
      language: audience.language,
      name: languageName(audience.language),
      people: audience.total,
      chosen: versionLanguage(campaign, audience.language) === audience.language,
      locked: audience.language === FALLBACK,
    }));
}

/** Names as a sentence reads them: "A, B and C". */
export function listNames(names: string[], last = "and"): string {
  return names.length < 2 ? names.join("") : `${names.slice(0, -1).join(", ")} ${last} ${names[names.length - 1]}`;
}

export function versionFor(campaign: Campaign, language: string, variant = "A"): MessageVersion | undefined {
  const content = variant === "B" ? campaign.content?.variant_b : campaign.content;
  if (!content) return undefined;
  if (content.language === language || !language) return content;
  return content.translations?.find((translation) => translation.language === language);
}

/** Languages with people to reach and nothing written for them. */
export function missingLanguages(campaign: Campaign, channel?: "sms" | "email"): string[] {
  // What was approved before languages were kept goes out as it is.
  if (!campaign.content || campaign.approved_at) return [];
  const languages = audienceLanguages(campaign, channel).map((audience) => audience.language);
  // A draft from before languages were kept was written for the largest one.
  if (!campaign.content.language) return languages.slice(1);
  const second = Boolean(campaign.content.variant_b);
  return languages.filter((language) => !versionFor(campaign, language) || (second && !versionFor(campaign, language, "B")));
}

/** How many people land on one side of a test, for one language or channel if asked. */
export function variantCount(campaign: Campaign, variant: string, filter: { language?: string; channel?: "sms" | "email" } = {}): number {
  return (campaign.variant_audience ?? [])
    .filter((side) => side.variant === variant && (!filter.language || versionLanguage(campaign, side.language) === filter.language))
    .reduce((sum, side) => sum + (filter.channel ? side[filter.channel] : side.total), 0);
}

/** The same count as words. */
export function variantPeople(campaign: Campaign, variant: string, filter: { language?: string; channel?: "sms" | "email" } = {}): string {
  const total = variantCount(campaign, variant, filter);
  return `${total.toLocaleString("en-US")} ${total === 1 ? "person" : "people"}`;
}

/** The share of people on one side of a test, as a percentage. */
export function variantShare(campaign: Campaign, variant: string): number {
  const first = campaign.variant_a_percent ?? 50;
  return variant === "B" ? 100 - first : first;
}

/** A side of a test that nobody in this audience lands on. */
export function emptySide(campaign: Campaign): string | undefined {
  if (!campaign.variant_audience) return undefined;
  return ["A", "B"].find((variant) => !variantCount(campaign, variant));
}

/** A test that still has only its first version. */
export function missingSecondVersion(campaign: Campaign): boolean {
  return Boolean(campaign.ab_test && campaign.content && !campaign.content.variant_b && !campaign.approved_at);
}

/** Every version with the people who read it, for reading before approval. */
export function languageVersions(campaign: Campaign, variant = "A"): { language: string; name: string; sms: number; email: number; also: string[]; version: MessageVersion }[] {
  return audienceLanguages(campaign).flatMap((audience) => {
    const version = campaign.content?.language ? versionFor(campaign, audience.language, variant) : undefined;
    return version ? [{ language: audience.language, name: languageName(audience.language), sms: audience.sms, email: audience.email, also: audience.also, version }] : [];
  });
}

/** The longest {{first_name}} this language's readers see, which the limit counts. */
export function longestNameFor(campaign: Campaign, language: string, channel: "sms" | "email"): number {
  const audience = audienceLanguages(campaign).find((entry) => entry.language === language);
  if (!audience) return channel === "sms" ? campaign.content_limits.sms_name_length : campaign.content_limits.email_name_length;
  return Math.max(channel === "sms" ? audience.sms_name_length : audience.email_name_length, [...fallbackName(language)].length);
}

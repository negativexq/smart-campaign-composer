import type { Campaign, LanguageAudience, MessageVersion } from "../api/campaigns";
import { fallbackName } from "./message";

const NAMES: Record<string, string> = { tr: "Turkish", en: "English" };

export function languageName(language: string): string {
  return NAMES[language] ?? language;
}

function reachable(audience: LanguageAudience, channel?: "sms" | "email"): number {
  if (channel === "sms") return audience.sms;
  if (channel === "email") return audience.email;
  return audience.sms + audience.email;
}

/** The languages people read on this channel that we write in, largest first. */
export function audienceLanguages(campaign: Campaign, channel?: "sms" | "email"): LanguageAudience[] {
  return (campaign.languages ?? []).filter((audience) => NAMES[audience.language] && reachable(audience, channel) > 0);
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
    .filter((side) => side.variant === variant && (!filter.language || side.language === filter.language))
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
export function languageVersions(campaign: Campaign, variant = "A"): { language: string; name: string; sms: number; email: number; version: MessageVersion }[] {
  return audienceLanguages(campaign).flatMap((audience) => {
    const version = campaign.content?.language ? versionFor(campaign, audience.language, variant) : undefined;
    return version ? [{ language: audience.language, name: languageName(audience.language), sms: audience.sms, email: audience.email, version }] : [];
  });
}

/** The longest {{first_name}} this language's readers see, which the limit counts. */
export function longestNameFor(campaign: Campaign, language: string, channel: "sms" | "email"): number {
  const audience = campaign.languages?.find((entry) => entry.language === language);
  if (!audience) return channel === "sms" ? campaign.content_limits.sms_name_length : campaign.content_limits.email_name_length;
  return Math.max(channel === "sms" ? audience.sms_name_length : audience.email_name_length, [...fallbackName(language)].length);
}

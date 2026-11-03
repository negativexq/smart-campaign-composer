import type { Campaign, Condition } from "../api/campaigns";

const sentences: Record<string, (value: string) => string> = {
  city: (value) => `Lives in ${value}`,
  language: (value) => `Prefers ${value}`,
  cart_added_within_days: (value) => `Added to cart in last ${value}`,
  no_purchase_since_cart: () => "No purchase after cart",
  purchased_within_days: (value) => `Purchased in last ${value}`,
  not_purchased_within_days: (value) => `No purchase in last ${value}`,
  not_messaged_within_days: (value) => `No message from us in last ${value}`,
};

/** One condition in the words a marketer would use. */
export function conditionSentence(condition: Condition): string {
  return sentences[condition.field]?.(condition.value) ?? `${condition.label} ${condition.value}`;
}

/** A share of a whole as a percentage, or nothing when there is no whole. */
export function percentOf(count: number, whole: number): string {
  return whole ? `${((count / whole) * 100).toFixed(1)}%` : "";
}

/** A condition as one rule, reading naturally where its label and value would not. */
export function ruleText(condition: Condition): string {
  return condition.field === "no_purchase_since_cart" ? conditionSentence(condition) : `${condition.label} ${condition.value}`;
}

/** Where a condition came from, never claiming words nobody wrote. */
export function sourceOf(campaign: Campaign, condition: Condition): { quotes: string[]; text: string } {
  const evidence = campaign.proposal?.evidence;
  const quotes = (evidence ?? []).filter((entry) => entry.field === condition.field).map((entry) => entry.quote);
  if (quotes.length) return { quotes, text: "" };
  if (condition.field === "not_messaged_within_days" && campaign.not_messaged_within_days) return { quotes, text: "From Options" };
  // Older proposals were made before quotes existed, so they have nothing to say here.
  const assumed = campaign.proposal?.assumptions.some((assumption) => assumption.field === condition.field);
  if (!evidence || assumed) return { quotes, text: "" };
  return { quotes, text: "Added by AI, not in your brief" };
}

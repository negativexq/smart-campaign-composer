import type { Condition } from "../api/campaigns";

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

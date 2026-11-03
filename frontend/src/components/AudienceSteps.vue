<script setup lang="ts">
import { computed } from "vue";

import type { AudienceBreakdown, Campaign, Condition } from "../api/campaigns";

const props = defineProps<{ campaign: Campaign; breakdown: AudienceBreakdown | null; failed: boolean; hideRule?: boolean }>();

const formatCount = (count: number) => count.toLocaleString("en-US");
const assumptions = computed(() => props.campaign.proposal?.assumptions ?? []);
// Older proposals were made before quotes existed, so they have nothing to say here.
const evidence = computed(() => props.campaign.proposal?.evidence);

/** Where a condition came from, never claiming words nobody wrote. */
function sourceOf(condition: Condition): { quotes: string[]; text: string } {
  const quotes = (evidence.value ?? []).filter((entry) => entry.field === condition.field).map((entry) => entry.quote);
  if (quotes.length) return { quotes, text: "" };
  if (condition.field === "not_messaged_within_days" && props.campaign.not_messaged_within_days) return { quotes, text: "From Options" };
  if (!evidence.value || assumptions.value.some((assumption) => assumption.field === condition.field)) return { quotes, text: "" };
  return { quotes, text: "Added by AI, not in your brief" };
}

const percentOfAll = (count: number) => `${((count / (props.breakdown?.everyone || 1)) * 100).toFixed(1)}%`;

/** The dimension each condition cuts by, so a person can check what was picked for it. */
const dimensions: Record<string, { name: string; window?: boolean }> = {
  city: { name: "City" },
  language: { name: "Language" },
  cart_added_within_days: { name: "Added to cart", window: true },
  no_purchase_since_cart: { name: "Bought after that" },
  purchased_within_days: { name: "Purchased", window: true },
  not_purchased_within_days: { name: "No purchase", window: true },
  not_messaged_within_days: { name: "No message from us", window: true },
};

const steps = computed(() =>
  props.campaign.conditions.map((condition) => ({
    field: condition.field,
    rule: `${condition.label} ${condition.value}`,
    dimension: dimensions[condition.field]?.name ?? condition.label,
    selected: dimensions[condition.field]?.window ? `Last ${condition.value}` : condition.value,
    source: sourceOf(condition),
    notes: assumptions.value.filter((assumption) => assumption.field === condition.field),
    remaining: props.breakdown?.steps.find((step) => step.field === condition.field)?.remaining,
  })),
);

const otherNotes = computed(() => assumptions.value.filter((assumption) => !props.campaign.conditions.some((condition) => condition.field === assumption.field)));

</script>

<template>
  <div class="audience-steps">
    <p v-if="!campaign.conditions.length" class="muted">No condition could be applied from this brief.</p>
    <template v-else>
      <p v-if="!hideRule" class="rule" data-testid="segment-rule">
        <template v-for="(step, index) in steps" :key="step.field">
          <span v-if="index" class="muted small">AND</span>
          <code class="condition">{{ step.rule }}</code>
        </template>
      </p>
      <div class="matrix">
        <table>
          <thead>
            <tr class="muted small">
              <th scope="col">Breakdown</th>
              <th scope="col">Selected</th>
              <th scope="col">In the brief</th>
              <th scope="col" class="number">People</th>
              <th scope="col" class="number share">Share</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="breakdown" data-testid="audience-step">
              <td>Main audience</td>
              <td><strong>All customers</strong></td>
              <td class="muted">–</td>
              <td class="number"><strong class="count">{{ formatCount(breakdown.everyone) }}</strong></td>
              <td class="number share muted">100%</td>
            </tr>
            <tr v-for="step in steps" :key="step.field" data-testid="audience-step">
              <td>{{ step.dimension }}</td>
              <td><strong>{{ step.selected }}</strong></td>
              <td>
                <template v-if="step.source.quotes.length">
                  <span class="found" aria-hidden="true">✓ </span><span data-testid="step-source">{{ step.source.quotes.join(", ") }}</span>
                </template>
                <span v-else-if="step.source.text" :class="step.source.text === 'From Options' ? 'muted' : 'assumption'" data-testid="step-source">{{ step.source.text }}</span>
                <span v-for="note in step.notes" :key="note.note" class="assumption small" data-testid="assumption-note">{{ note.note }}</span>
              </td>
              <td class="number"><strong v-if="step.remaining !== undefined" class="count">{{ formatCount(step.remaining) }}</strong></td>
              <td class="number share muted"><template v-if="step.remaining !== undefined">{{ percentOfAll(step.remaining) }}</template></td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
    <p v-for="note in otherNotes" :key="note.field" class="assumption small" data-testid="assumption-note">{{ note.note }}</p>
    <p v-if="failed" class="muted small" data-testid="breakdown-unavailable">The counts could not be loaded. The rule above still applies.</p>
  </div>
</template>

<style scoped>
.audience-steps {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.rule {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
}

.condition {
  padding: 2px var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  background: var(--color-surface);
  font-family: inherit;
  font-weight: 500;
}

.matrix {
  overflow-x: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
}

th,
td {
  padding: var(--space-2) var(--space-3) var(--space-2) 0;
  border-bottom: 1px solid var(--color-border);
  font-weight: 400;
  text-align: left;
  vertical-align: top;
}

th:last-child,
td:last-child {
  padding-right: 0;
}

.number {
  text-align: right;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.found {
  color: var(--color-success);
}

@media (max-width: 480px) {
  .share {
    display: none;
  }
}

.assumption {
  display: block;
  color: var(--color-warning);
}

.count {
  font-weight: 600;
}
</style>

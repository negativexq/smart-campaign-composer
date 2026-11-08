<script setup lang="ts">
import { computed } from "vue";

import type { AudienceBreakdown, Campaign } from "../api/campaigns";
import { assumedText, conditionSentence, sourceOf } from "../lib/audience";

const props = defineProps<{
  campaign: Campaign;
  breakdown: AudienceBreakdown | null;
  failed: boolean;
  /** Builds the bars one after another, for someone checking a new audience. */
  staged?: boolean;
  /** How long the staged build waits for what comes before it, in milliseconds. */
  startAfter?: number;
  /** Shows the brief's wording under each step, for someone checking the rule. */
  showSources?: boolean;
}>();

const formatCount = (count: number) => count.toLocaleString("en-US");

const rows = computed(() => {
  if (!props.breakdown) return [];
  const everyone = props.breakdown.everyone;
  const list = [{ key: "everyone", label: "All customers", count: everyone, note: "Starting population", source: { quotes: [] as string[], text: "" }, notes: [] as string[] }];
  let before = everyone;
  for (const condition of props.campaign.conditions) {
    const count = props.breakdown.steps.find((step) => step.field === condition.field)?.remaining;
    if (count === undefined) continue;
    list.push({
      key: condition.field,
      label: conditionSentence(condition),
      count,
      note: `${formatCount(before - count)} excluded`,
      source: sourceOf(props.campaign, condition),
      notes: (props.campaign.proposal?.assumptions ?? []).filter((assumption) => assumption.field === condition.field).map((assumption) => assumption.note),
    });
    before = count;
  }
  return list.map((row, index) => ({
    ...row,
    width: `${everyone ? (row.count / everyone) * 100 : 0}%`,
    final: index === list.length - 1,
  }));
});
</script>

<template>
  <section class="block card" :class="{ 'block--staged': staged }" :style="{ '--start': `${startAfter ?? 0}ms` }" data-testid="audience-narrowing">
    <div class="head">
      <div>
        <h3>How the audience was narrowed</h3>
        <p class="muted small">Each condition is applied to the remaining audience.</p>
      </div>
      <span v-if="rows.length" class="pill small">{{ rows.length }} steps</span>
    </div>
    <p v-if="failed" class="muted small" data-testid="breakdown-unavailable">The step-by-step counts could not be loaded.</p>
    <div v-else-if="!breakdown" class="waiting" aria-hidden="true" data-testid="funnel-waiting">
      <span v-for="index in campaign.conditions.length + 1" :key="index" class="track"></span>
    </div>
    <ol class="funnel" :class="{ 'funnel--staged': staged }">
      <li v-for="(row, index) in rows" :key="row.key" class="row" :style="{ '--step': index }" data-testid="narrowing-step">
        <div class="line">
          <span :class="{ strong: row.final }">{{ row.label }}</span>
          <strong class="count">{{ formatCount(row.count) }}</strong>
        </div>
        <div class="track" aria-hidden="true"><span class="fill" :class="{ 'fill--final': row.final }" :style="{ width: row.width }"></span></div>
        <div class="details small">
          <span class="muted">{{ row.note }}</span>
          <template v-if="showSources">
            <span v-if="row.source.quotes.length" class="source" data-testid="funnel-source"><span class="found" aria-hidden="true">✓ </span>{{ row.source.quotes.join(", ") }}</span>
            <span v-else-if="row.source.text" class="muted" data-testid="funnel-source">{{ row.source.text }}</span>
          </template>
        </div>
        <span v-for="note in row.notes" :key="note" class="assumption small" data-testid="funnel-note">{{ assumedText(note) }}</span>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.block {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  padding: var(--space-4);
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

h3,
p {
  margin: 0;
}

h3 {
  font-size: inherit;
  font-weight: 600;
}

.pill {
  flex-shrink: 0;
  padding: 2px var(--space-2);
  border-radius: 999px;
  background: var(--color-surface-subtle);
}

.waiting {
  display: grid;
  gap: var(--space-6);
}

.funnel {
  display: grid;
  gap: var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.funnel:empty {
  display: none;
}

.row {
  display: grid;
  gap: var(--space-1);
}

.line,
.details {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 0 var(--space-4);
}

.strong {
  font-weight: 600;
}

.count {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.track {
  display: block;
  height: 8px;
  overflow: hidden;
  border-radius: 4px;
  background: var(--color-surface-subtle);
}

.fill {
  display: block;
  height: 100%;
  border-radius: 4px;
  background: color-mix(in srgb, var(--color-primary) 40%, var(--color-surface));
}

.fill--final {
  background: var(--color-primary);
}

.found {
  color: var(--color-success);
}

.assumption {
  color: var(--color-warning);
}

.block--staged {
  animation: row-in 280ms ease-out both;
  animation-delay: var(--start);
}

.funnel--staged .row {
  animation: row-in 280ms ease-out both;
  animation-delay: calc(var(--start) + var(--step) * 700ms);
}

.funnel--staged .fill {
  animation: bar-in 1000ms cubic-bezier(0.2, 0.7, 0.2, 1) both;
  animation-delay: calc(var(--start) + var(--step) * 700ms + 80ms);
}

@keyframes row-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}

@keyframes bar-in {
  from {
    width: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .block--staged,
  .funnel--staged .row,
  .funnel--staged .fill {
    animation: none;
  }
}
</style>

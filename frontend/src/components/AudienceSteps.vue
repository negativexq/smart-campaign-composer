<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { getAudienceBreakdown, type AudienceBreakdown, type Campaign, type Condition } from "../api/campaigns";

const props = defineProps<{ campaign: Campaign }>();

const breakdown = ref<AudienceBreakdown | null>(null);
const failed = ref(false);

const sentences: Record<string, (value: string) => string> = {
  city: (value) => `Lives in ${value}`,
  language: (value) => `Prefers ${value}`,
  cart_added_within_days: (value) => `Added something to their cart in the last ${value}`,
  no_purchase_since_cart: () => "Has not bought anything since that cart",
  purchased_within_days: (value) => `Bought something in the last ${value}`,
  not_purchased_within_days: (value) => `Has not bought anything in the last ${value}`,
  not_messaged_within_days: (value) => `Has had no campaign message from us in the last ${value}`,
};

const sentence = (condition: Condition) => sentences[condition.field]?.(condition.value) ?? `${condition.label} ${condition.value}`;
const formatCount = (count: number) => count.toLocaleString("en-US");
const assumptions = computed(() => props.campaign.proposal?.assumptions ?? []);
// Older proposals were made before quotes existed, so they have nothing to say here.
const evidence = computed(() => props.campaign.proposal?.evidence);

/** Where a condition came from, never claiming words nobody wrote. */
function sourceOf(condition: Condition): { quotes: string[]; text: string } {
  const quotes = (evidence.value ?? []).filter((entry) => entry.field === condition.field).map((entry) => entry.quote);
  if (quotes.length) return { quotes, text: "" };
  if (condition.field === "not_messaged_within_days" && props.campaign.not_messaged_within_days) return { quotes, text: "You chose this in Options." };
  if (!evidence.value || assumptions.value.some((assumption) => assumption.field === condition.field)) return { quotes, text: "" };
  return { quotes, text: "AI added this. It is not in what you wrote, so please check it." };
}

const steps = computed(() => {
  let before = breakdown.value?.everyone;
  return props.campaign.conditions.map((condition) => {
    const remaining = breakdown.value?.steps.find((step) => step.field === condition.field)?.remaining;
    const removed = before !== undefined && remaining !== undefined ? before - remaining : undefined;
    before = remaining;
    return {
      field: condition.field,
      text: sentence(condition),
      source: sourceOf(condition),
      notes: assumptions.value.filter((assumption) => assumption.field === condition.field),
      remaining,
      removed,
    };
  });
});

const otherNotes = computed(() => assumptions.value.filter((assumption) => !props.campaign.conditions.some((condition) => condition.field === assumption.field)));
const unsupported = computed(() => props.campaign.proposal?.unsupported_conditions ?? []);
const consent = computed(() =>
  props.campaign.channels.map((channel) => {
    const name = channel === "sms" ? "SMS" : "email";
    const missing = props.campaign.audience.total - props.campaign.audience[channel as "sms" | "email"];
    return missing ? `${formatCount(missing)} of these people have not agreed to ${name}, so they get no ${name}.` : `Everyone here has agreed to ${name}.`;
  }),
);
const proposedBy = computed(() => (props.campaign.provider === "mock" ? "the built-in test model" : props.campaign.model));

function share(count: number | undefined) {
  const everyone = breakdown.value?.everyone;
  return count === undefined || !everyone ? "0%" : `${(count / everyone) * 100}%`;
}

watch(
  () => [props.campaign.id, props.campaign.audience_as_of],
  async () => {
    const asked = props.campaign.audience_as_of;
    breakdown.value = null;
    failed.value = false;
    try {
      const loaded = await getAudienceBreakdown(props.campaign.id);
      if (asked === props.campaign.audience_as_of) breakdown.value = loaded;
    } catch {
      if (asked === props.campaign.audience_as_of) failed.value = true;
    }
  },
  { immediate: true },
);
</script>

<template>
  <div class="audience-steps">
    <p v-if="!campaign.conditions.length" class="muted">No condition could be applied from this brief.</p>
    <ol v-else class="steps">
      <li v-if="breakdown" class="step" data-testid="audience-step">
        <div class="line">
          <span>All customers</span>
          <strong class="count">{{ formatCount(breakdown.everyone) }}</strong>
        </div>
        <div class="bar" aria-hidden="true"><span :style="{ width: '100%' }"></span></div>
      </li>
      <li v-for="step in steps" :key="step.field" class="step" data-testid="audience-step">
        <p v-if="step.source.quotes.length" class="source small" data-testid="step-source">
          <span class="muted">Because you wrote </span>
          <template v-for="(quote, index) in step.source.quotes" :key="quote">
            <span v-if="index" class="muted">&nbsp;and&nbsp;</span>
            <q class="quote">{{ quote }}</q>
          </template>
        </p>
        <p v-else-if="step.source.text" class="source muted small" data-testid="step-source">{{ step.source.text }}</p>
        <div class="line">
          <span class="text">
            {{ step.text }}
            <span v-if="step.notes.length" class="assumption small">Assumption, please check</span>
          </span>
          <span v-if="step.remaining !== undefined" class="tally">
            <strong class="count">{{ formatCount(step.remaining) }} left</strong>
            <span v-if="step.removed" class="muted small">{{ formatCount(step.removed) }} removed</span>
          </span>
        </div>
        <div v-if="breakdown" class="bar" aria-hidden="true"><span :style="{ width: share(step.remaining) }"></span></div>
        <p v-for="note in step.notes" :key="note.note" class="muted small" data-testid="assumption-note">{{ note.note }}</p>
      </li>
      <li v-for="condition in unsupported" :key="condition" class="step" data-testid="unsupported-step">
        <p class="source small"><span class="muted">Your brief also asks for</span> <strong class="quote">{{ condition }}</strong></p>
        <div class="line">
          <span class="text">No filter can do this, so it is not applied</span>
          <span class="assumption small">Blocks approval</span>
        </div>
      </li>
    </ol>
    <p v-for="note in otherNotes" :key="note.field" class="muted small" data-testid="assumption-note">{{ note.note }}</p>
    <p v-if="failed" class="muted small" data-testid="breakdown-unavailable">The step-by-step counts could not be loaded. The conditions above still apply.</p>
    <section v-if="campaign.proposal" class="guards" data-testid="rule-guards">
      <h3 class="guards-title small">What the app makes sure of</h3>
      <ul class="muted small">
        <li>The AI's answer must fit the filters the app allows. It is never run as a database query.</li>
        <li v-if="evidence">Quotes are checked word for word against what you wrote.</li>
        <li v-for="line in consent" :key="line">{{ line }}</li>
        <li v-if="proposedBy" data-testid="rule-origin">Proposed by {{ proposedBy }}. Only the conditions listed here are applied.</li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.audience-steps {
  display: grid;
  gap: var(--space-3);
}

.steps {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}

.step {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-3) 0;
  border-bottom: 1px solid var(--color-border);
}

.step:first-child {
  padding-top: 0;
}

.source {
  margin: 0;
  white-space: pre-wrap;
}

.quote {
  color: var(--color-text);
  font-weight: 500;
}

.guards {
  display: grid;
  gap: var(--space-1);
}

.guards-title {
  margin: 0;
  font-weight: 500;
}

.guards ul {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-4);
}

.line {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-1) var(--space-4);
}

.text {
  display: flex;
  flex: 1 1 240px;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 var(--space-2);
  min-width: 0;
}

.assumption {
  color: var(--color-warning);
  font-weight: 500;
}

.tally {
  display: flex;
  flex-shrink: 0;
  align-items: baseline;
  gap: var(--space-2);
}

.count {
  font-variant-numeric: tabular-nums;
}

.bar {
  height: 4px;
  overflow: hidden;
  border-radius: 2px;
  background: var(--color-surface-subtle);
}

.bar span {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--color-primary);
}
</style>

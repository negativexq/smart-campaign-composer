<script setup lang="ts">
import { computed } from "vue";

import type { Campaign, Progress } from "../api/campaigns";
import { conditionSentence, percentOf } from "../lib/audience";
import { formatChannel, formatDateTime } from "../lib/format";
import { useAudienceBreakdown } from "../lib/useAudienceBreakdown";
import AudienceSteps from "./AudienceSteps.vue";

const props = defineProps<{ campaign: Campaign; progress: Progress | null }>();

const { breakdown, failed } = useAudienceBreakdown(() => props.campaign);

const formatCount = (count: number) => count.toLocaleString("en-US");
const isFinal = computed(() => Boolean(props.campaign.audience_snapshot));
const snapshotLine = computed(() => {
  const { approved_at: approved, audience_as_of: counted } = props.campaign;
  if (isFinal.value && approved) return `Approved audience snapshot · ${formatDateTime(approved)}`;
  if (!counted) return "";
  // Approved before snapshots existed: these counts are recalculated, not frozen.
  return approved ? `Counted on ${formatDateTime(counted)}` : `Counted on ${formatDateTime(counted)} · Can still change`;
});

const usesChannel = (channel: string) => props.campaign.channels.includes(channel);
const matched = computed(() => props.campaign.audience.total);
const everyoneHint = computed(() => {
  const share = percentOf(matched.value, breakdown.value?.everyone ?? 0);
  return share ? `${share} of total` : "";
});
const eligibleHint = (count: number) => {
  const share = percentOf(count, matched.value);
  return share ? `${share} of matched` : "";
};

const rows = computed(() => {
  if (!breakdown.value) return [];
  const everyone = breakdown.value.everyone;
  const list = [{ key: "everyone", label: "All customers", count: everyone, note: "Starting population" }];
  let before = everyone;
  for (const condition of props.campaign.conditions) {
    const count = breakdown.value.steps.find((step) => step.field === condition.field)?.remaining;
    if (count === undefined) continue;
    list.push({ key: condition.field, label: conditionSentence(condition), count, note: `${formatCount(before - count)} excluded` });
    before = count;
  }
  return list.map((row, index) => ({
    ...row,
    width: `${everyone ? (row.count / everyone) * 100 : 0}%`,
    final: index === list.length - 1,
  }));
});

const generatedBy = computed(() => (props.campaign.provider === "mock" ? "the built-in test model" : props.campaign.model));

const reach = computed(() => {
  const byChannel = props.progress?.by_channel ?? {};
  return props.campaign.channels.map((channel) => {
    const counts = byChannel[channel] ?? {};
    return {
      channel,
      planned: channel === "sms" ? props.campaign.audience.sms : props.campaign.audience.email,
      accepted: counts.SENT ?? 0,
      skipped: counts.SKIPPED ?? 0,
      failed: (counts.FAILED ?? 0) + (counts.UNKNOWN ?? 0),
      waiting: (counts.QUEUED ?? 0) + (counts.SENDING ?? 0) + (counts.RETRYING ?? 0),
    };
  });
});
const hasWaiting = computed(() => reach.value.some((row) => row.waiting));
</script>

<template>
  <div class="report" data-testid="audience-report">
    <header class="report-head">
      <div>
        <h2>Target audience</h2>
        <p class="muted" data-testid="audience-snapshot-line">{{ snapshotLine }}</p>
      </div>
      <span v-if="isFinal" class="final small" data-testid="audience-final">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="7" width="10" height="7" rx="1.5" /><path d="M5 7V5a3 3 0 0 1 6 0v2" /></svg>
        Final
      </span>
    </header>

    <div class="kpis">
      <div class="kpi card" data-testid="kpi-matched">
        <span class="muted small">Matched customers</span>
        <strong class="kpi-value">{{ formatCount(matched) }}</strong>
        <span class="muted small">{{ everyoneHint }}</span>
      </div>
      <div v-if="usesChannel('sms')" class="kpi card" data-testid="kpi-sms">
        <span class="muted small">SMS eligible</span>
        <strong class="kpi-value">{{ formatCount(campaign.audience.sms) }}</strong>
        <span class="muted small">{{ eligibleHint(campaign.audience.sms) }}</span>
      </div>
      <div v-if="usesChannel('email')" class="kpi card" data-testid="kpi-email">
        <span class="muted small">Email eligible</span>
        <strong class="kpi-value">{{ formatCount(campaign.audience.email) }}</strong>
        <span class="muted small">{{ eligibleHint(campaign.audience.email) }}</span>
      </div>
    </div>

    <section class="block card" data-testid="audience-narrowing">
      <div class="block-head">
        <div>
          <h3>How the audience was narrowed</h3>
          <p class="muted small">Each condition is applied to the remaining audience.</p>
        </div>
        <span v-if="rows.length" class="pill small">{{ rows.length }} steps</span>
      </div>
      <p v-if="failed" class="muted small" data-testid="breakdown-unavailable">The step-by-step counts could not be loaded.</p>
      <ol class="funnel">
        <li v-for="row in rows" :key="row.key" class="funnel-row" data-testid="narrowing-step">
          <div class="funnel-line">
            <span :class="{ strong: row.final }">{{ row.label }}</span>
            <strong class="count">{{ formatCount(row.count) }}</strong>
          </div>
          <div class="track" aria-hidden="true"><span class="fill" :class="{ 'fill--final': row.final }" :style="{ width: row.width }"></span></div>
          <span class="muted small">{{ row.note }}</span>
        </li>
      </ol>
    </section>

    <section class="block card" data-testid="segment-rules">
      <div class="block-head">
        <h3>{{ isFinal ? "Approved segment rules" : "Segment rules" }}</h3>
        <span class="validated small">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M8 1.5 13.5 3.5V8c0 3.2-2.3 5.5-5.5 6.5C4.8 13.5 2.5 11.2 2.5 8V3.5L8 1.5Z" /><path d="m5.5 8 1.8 1.8L10.5 6.5" /></svg>
          Validated
        </span>
      </div>
      <p class="rules">
        <template v-for="(condition, index) in campaign.conditions" :key="condition.field">
          <span v-if="index" class="muted small">AND</span>
          <span class="rule-chip">{{ condition.field === "no_purchase_since_cart" ? conditionSentence(condition) : `${condition.label} ${condition.value}` }}</span>
        </template>
      </p>
      <p class="muted small" data-testid="rule-origin">
        <template v-if="generatedBy">Generated by {{ generatedBy }} from the brief. </template>The backend verified the conditions.
      </p>
    </section>

    <details class="block card details" data-testid="calculation-details">
      <summary class="details-toggle">Show calculation details</summary>
      <AudienceSteps :campaign="campaign" :breakdown="breakdown" :failed="false" hide-rule />
    </details>

    <section v-if="progress?.total" class="block card" data-testid="actual-reach">
      <div class="block-head">
        <div>
          <h3>Actual reach</h3>
          <p class="muted small">Consent is checked again when sending, so this comes from the delivery records.</p>
        </div>
      </div>
      <div class="reach-table">
        <table>
          <thead>
            <tr class="muted small">
              <th scope="col">Channel</th>
              <th scope="col" class="number">Eligible</th>
              <th scope="col" class="number">Accepted by provider</th>
              <th scope="col" class="number">Skipped</th>
              <th scope="col" class="number">Failed or unknown</th>
              <th v-if="hasWaiting" scope="col" class="number">Waiting</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in reach" :key="row.channel" :data-testid="`reach-${row.channel}`">
              <td>{{ formatChannel(row.channel) }}</td>
              <td class="number">{{ formatCount(row.planned) }}</td>
              <td class="number"><strong>{{ formatCount(row.accepted) }}</strong></td>
              <td class="number">{{ formatCount(row.skipped) }}</td>
              <td class="number">{{ formatCount(row.failed) }}</td>
              <td v-if="hasWaiting" class="number">{{ formatCount(row.waiting) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="muted small">Accepted means the provider took the message, not that a person received it.</p>
    </section>
  </div>
</template>

<style scoped>
.report {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.report-head,
.block-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.report-head p,
.block-head p,
h2,
h3 {
  margin: 0;
}

h3 {
  font-size: inherit;
  font-weight: 600;
}

.final,
.validated {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-success);
  font-weight: 500;
}

.final {
  padding: 2px var(--space-2);
  border: 1px solid currentColor;
  border-radius: 999px;
}

.kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
}

.kpi {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-4);
}

.kpi-value {
  font-size: 2rem;
  font-weight: 600;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
}

.block {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  padding: var(--space-4);
}

.pill {
  flex-shrink: 0;
  padding: 2px var(--space-2);
  border-radius: 999px;
  background: var(--color-surface-subtle);
}

.funnel {
  display: grid;
  gap: var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.funnel-row {
  display: grid;
  gap: var(--space-1);
}

.funnel-line {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-4);
}

.strong {
  font-weight: 600;
}

.count {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.track {
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

.rules {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
}

.rule-chip {
  padding: var(--space-1) var(--space-2);
  border-radius: 6px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.block > p {
  margin: 0;
}

.details-toggle {
  font-weight: 500;
  cursor: pointer;
}

.details:not([open]) {
  gap: 0;
}

.reach-table {
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
}

th:last-child,
td:last-child {
  padding-right: 0;
}

.number {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

td strong {
  font-weight: 600;
}
</style>

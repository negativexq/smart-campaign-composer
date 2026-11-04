<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { errorMessage, getAnalytics, type Analytics, type CallDay } from "../api/campaigns";
import CampaignStatusBadge from "../components/CampaignStatusBadge.vue";
import DonutChart, { type DonutSlice } from "../components/DonutChart.vue";
import MetricCard from "../components/MetricCard.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { formatChannel } from "../lib/format";

const RANGES = [
  { value: "7", label: "Last 7 days" },
  { value: "30", label: "Last 30 days" },
  { value: "90", label: "Last 90 days" },
];
const CHANNEL_COLORS: Record<string, string> = { sms: "#2a78d6", email: "#eb6834" };
const RESULT_COLORS = { accepted: "#1baf7a", failed: "#e34948", skipped: "#858FA1", onTheWay: "#2a78d6" };

// In the address, so a refresh keeps it and a link can share it.
const named = new URLSearchParams(window.location.search).get("days") ?? "";
const days = ref(RANGES.some((range) => range.value === named) ? named : "30");
const analytics = ref<Analytics | null>(null);
const error = ref("");
const hovered = ref<CallDay | null>(null);
let latest = 0;

async function load() {
  const request = ++latest;
  try {
    const result = await getAnalytics(days.value);
    if (request !== latest) return;
    analytics.value = result;
    error.value = "";
  } catch (failure) {
    if (request === latest) error.value = errorMessage(failure);
  }
}

const sum = (pick: (channel: Analytics["channels"][number]) => number) => (analytics.value?.channels ?? []).reduce((total, channel) => total + pick(channel), 0);
const totals = computed(() => ({
  planned: sum((c) => c.planned),
  accepted: sum((c) => c.accepted),
  notAccepted: sum((c) => c.failed + c.unknown),
  bounced: sum((c) => c.bounced),
  skipped: sum((c) => c.skipped),
  onTheWay: sum((c) => c.on_the_way),
}));
const channelSlices = computed<DonutSlice[]>(() =>
  ["sms", "email"].map((channel) => ({
    key: channel,
    label: formatChannel(channel),
    count: analytics.value?.channels.find((item) => item.channel === channel)?.planned ?? 0,
    color: CHANNEL_COLORS[channel],
    selectable: false,
  })),
);
const resultSlices = computed<DonutSlice[]>(() => [
  { key: "accepted", label: "Accepted", count: totals.value.accepted, color: RESULT_COLORS.accepted, selectable: false },
  { key: "failed", label: "Failed or unknown", count: totals.value.notAccepted, color: RESULT_COLORS.failed, selectable: false },
  { key: "skipped", label: "Skipped", count: totals.value.skipped, color: RESULT_COLORS.skipped, selectable: false },
  { key: "on-the-way", label: "On the way", count: totals.value.onTheWay, color: RESULT_COLORS.onTheWay, selectable: false },
]);

const calls = computed(() => analytics.value?.calls ?? []);
const axisMax = computed(() => Math.max(1, ...calls.value.map((day) => day.accepted + day.not_accepted)));
const share = (count: number) => `${(count / axisMax.value) * 100}%`;
const dayLabel = (day: string) => new Date(day).toLocaleDateString("en-GB", { day: "numeric", month: "short", timeZone: "UTC" });
const count = (value: number) => value.toLocaleString("en-GB");
const errorRate = (notAccepted: number, all: number) => (all ? `${((notAccepted / all) * 100).toFixed(1)}%` : "—");
const shown = computed(() => hovered.value);

watch(days, (value) => {
  window.history.replaceState({}, "", `${window.location.pathname}?days=${value}`);
  void load();
});
onMounted(load);
</script>

<template>
  <div class="analytics">
    <header class="intro">
      <div>
        <h1>Compare results across campaigns.</h1>
        <p class="muted">Accepted means the mock provider took a message. It does not mean a person got it.</p>
      </div>
      <label class="range">
        <span class="visually-hidden">Period</span>
        <select v-model="days" class="field select" data-testid="analytics-range">
          <option v-for="range in RANGES" :key="range.value" :value="range.value">{{ range.label }}</option>
        </select>
      </label>
    </header>

    <ValidationNotice v-if="error" tone="error" title="We could not load the analytics">{{ error }}</ValidationNotice>
    <p v-else-if="!analytics" class="muted" role="status">Loading analytics...</p>
    <template v-else>
      <p v-if="!totals.planned" class="muted" data-testid="analytics-empty">No campaign sent anything in this period.</p>

      <section class="metrics" aria-label="Totals">
        <MetricCard label="Messages planned" :value="totals.planned" />
        <MetricCard label="Accepted" :value="totals.accepted" />
        <MetricCard label="Failed or unknown" :value="totals.notAccepted" />
        <MetricCard label="Bounced" :value="totals.bounced" hint="Simulated report" />
      </section>

      <section class="charts" aria-label="How messages are spread">
        <DonutChart title="By channel" :slices="channelSlices" test-prefix="analytics-channel" :unit="['message', 'messages']" />
        <DonutChart title="By result" :slices="resultSlices" test-prefix="analytics-result" :unit="['message', 'messages']" />
      </section>

      <section class="card chart" data-testid="calls-chart">
        <div class="chart-head">
          <h2>Provider calls by day</h2>
          <p class="legend small">
            <span><i class="swatch" :style="{ background: RESULT_COLORS.accepted }"></i>Accepted</span>
            <span><i class="swatch" :style="{ background: RESULT_COLORS.failed }"></i>Not accepted</span>
          </p>
        </div>
        <p v-if="!calls.length" class="muted small">No calls in this period.</p>
        <template v-else>
          <div class="columns" role="img" :aria-label="`Provider calls per day, highest ${count(axisMax)}. The table below has every day.`">
            <div
              v-for="day in calls"
              :key="day.day"
              class="column"
              data-testid="calls-day"
              @mouseenter="hovered = day"
              @mouseleave="hovered = null"
            >
              <span class="bar bar--accepted" :style="{ height: share(day.accepted) }"></span>
              <span v-if="day.not_accepted" class="bar bar--failed" :style="{ height: share(day.not_accepted) }"></span>
            </div>
          </div>
          <p class="small muted readout" data-testid="calls-readout">
            <template v-if="shown">{{ dayLabel(shown.day) }}: {{ count(shown.accepted) }} accepted, {{ count(shown.not_accepted) }} not accepted, {{ errorRate(shown.not_accepted, shown.accepted + shown.not_accepted) }} error rate</template>
            <template v-else>Point at a day to read it. Highest day: {{ count(axisMax) }} calls.</template>
          </p>
          <details class="small">
            <summary>View as table</summary>
            <table class="table" data-testid="calls-table">
              <thead><tr><th scope="col">Day</th><th scope="col" class="num">Accepted</th><th scope="col" class="num">Not accepted</th><th scope="col" class="num">Error rate</th></tr></thead>
              <tbody>
                <tr v-for="day in calls" :key="day.day">
                  <th scope="row">{{ dayLabel(day.day) }}</th>
                  <td class="num mono">{{ count(day.accepted) }}</td>
                  <td class="num mono">{{ count(day.not_accepted) }}</td>
                  <td class="num mono">{{ errorRate(day.not_accepted, day.accepted + day.not_accepted) }}</td>
                </tr>
              </tbody>
            </table>
          </details>
        </template>
      </section>

      <section v-if="analytics.campaigns.length" class="section">
        <h2>Campaigns, highest failure rate first</h2>
        <div class="card table-wrap">
          <table class="table" data-testid="analytics-campaigns">
            <thead>
              <tr>
                <th scope="col">Campaign</th>
                <th scope="col">Status</th>
                <th scope="col" class="num">Accepted</th>
                <th scope="col" class="num">Failed</th>
                <th scope="col" class="num">Unknown</th>
                <th scope="col" class="num">Failure rate</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in analytics.campaigns" :key="row.id" data-testid="analytics-campaign">
                <th scope="row"><a :href="`/campaigns/${row.id}?tab=deliveries`">{{ row.name }}</a></th>
                <td><CampaignStatusBadge :status="row.status" /></td>
                <td class="num mono">{{ count(row.accepted) }}</td>
                <td class="num mono">{{ count(row.failed) }}</td>
                <td class="num mono">{{ count(row.unknown) }}</td>
                <td class="num mono">{{ errorRate(row.failed + row.unknown, row.accepted + row.failed + row.unknown) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.analytics,
.section {
  display: grid;
  gap: var(--space-6);
  min-width: 0;
}

.section {
  gap: var(--space-3);
}

.intro {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
}

.select {
  width: auto;
  height: 40px;
  padding-block: 0;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
}

.charts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: var(--space-3);
}

.chart {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  min-width: 0;
}

.chart-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.chart-head h2 {
  margin: 0;
  font-size: inherit;
  font-weight: 600;
}

.legend {
  display: flex;
  gap: var(--space-3);
  margin: 0;
}

.swatch {
  display: inline-block;
  width: 10px;
  height: 10px;
  margin-right: var(--space-1);
  border-radius: 3px;
}

.columns {
  display: flex;
  align-items: stretch;
  gap: 4px;
  height: 180px;
  border-bottom: 1px solid var(--color-border);
}

.column {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  justify-content: flex-end;
  min-width: 2px;
  max-width: 48px;
}

.column:hover {
  background: var(--color-surface-subtle);
}

.bar {
  display: block;
  min-height: 2px;
  border-radius: 4px 4px 0 0;
  transition: height 300ms ease;
}

.bar--accepted {
  order: 2;
  background: #1baf7a;
  border-radius: 0;
}

.bar--failed {
  order: 1;
  background: #e34948;
}

.bar--accepted:only-child {
  border-radius: 4px 4px 0 0;
}

.readout {
  margin: 0;
}

.table-wrap {
  position: relative;
  overflow-x: auto;
}

.table {
  width: 100%;
  border-collapse: collapse;
}

.table th,
.table td {
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  font-weight: 400;
  text-align: left;
  white-space: nowrap;
}

.table thead th {
  color: var(--color-text-muted);
  font-size: 13px;
}

.table .num {
  text-align: right;
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

@media (prefers-reduced-motion: reduce) {
  .bar {
    transition: none;
  }
}
</style>

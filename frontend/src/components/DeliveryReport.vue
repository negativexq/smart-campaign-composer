<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, getReport, type Progress, type Report, type TimelineBucket } from "../api/campaigns";
import {
  OUTCOMES,
  REASONS,
  bucketQuery,
  callsIn,
  channelBars,
  formatBucket,
  formatBucketWidth,
  matches,
  niceMax,
  notAccepted,
  type ChannelSegment,
  type DeliveryQuery,
} from "../lib/report";
import ValidationNotice from "./ValidationNotice.vue";

// Live updates arrive every second, and this read is heavier than the counters.
const REFRESH_MS = 5000;

const props = defineProps<{ campaignId: string; progress: Progress; query: DeliveryQuery; refreshKey?: number }>();
const emit = defineEmits<{ select: [selection: Partial<DeliveryQuery>] }>();

const report = ref<Report | null>(null);
const error = ref("");
const hovered = ref<number | null>(null);
let lastLoad = 0;
let timer: ReturnType<typeof setTimeout> | undefined;

async function load() {
  lastLoad = Date.now();
  try {
    report.value = await getReport(props.campaignId);
    error.value = "";
  } catch (failure) {
    error.value = errorMessage(failure);
  }
}

function scheduleLoad() {
  clearTimeout(timer);
  timer = setTimeout(load, Math.max(0, lastLoad + REFRESH_MS - Date.now()));
}

watch(() => props.refreshKey, scheduleLoad);
onMounted(load);
onBeforeUnmount(() => clearTimeout(timer));

const bars = computed(() => channelBars(props.progress));
const legend = computed(() => {
  const totals = new Map<ChannelSegment["status"], { label: string; count: number }>();
  for (const bar of bars.value) {
    for (const segment of bar.segments) {
      const entry = totals.get(segment.status) ?? { label: segment.label, count: 0 };
      entry.count += segment.count;
      totals.set(segment.status, entry);
    }
  }
  return [...totals].map(([status, entry]) => ({ status, ...entry }));
});

const buckets = computed(() => report.value?.timeline.buckets ?? []);
const width = computed(() => report.value?.timeline.bucket_seconds ?? 1);
const axisMax = computed(() => niceMax(Math.max(0, ...buckets.value.map(callsIn))));
const busyBuckets = computed(() => buckets.value.filter((bucket) => callsIn(bucket) > 0));
const totals = computed(() => ({
  accepted: buckets.value.reduce((sum, bucket) => sum + bucket.accepted, 0),
  notAccepted: buckets.value.reduce((sum, bucket) => sum + notAccepted(bucket), 0),
}));
const reasonMax = computed(() => Math.max(1, ...(report.value?.reasons ?? []).map((item) => item.deliveries)));
const problemMax = computed(() => Math.max(1, ...(report.value?.problems ?? []).map((item) => item.deliveries)));

const height = (count: number) => (count ? `max(2px, ${(count / axisMax.value) * 100}%)` : "0");

function describeBucket(bucket: TimelineBucket) {
  return `${formatBucket(bucket.at, width.value)}: ${bucket.accepted} accepted, ${notAccepted(bucket)} not accepted`;
}

const segmentSelection = (channel: string, segment: ChannelSegment): Partial<DeliveryQuery> | null =>
  segment.status === "WAITING" ? null : { channel, status: segment.status };

function pick(selection: Partial<DeliveryQuery> | null) {
  if (selection) emit("select", selection);
}

const isPicked = (selection: Partial<DeliveryQuery> | null) => Boolean(selection) && matches(props.query, selection as Partial<DeliveryQuery>);
</script>

<template>
  <section class="report" aria-label="Delivery report">
    <ValidationNotice v-if="error" tone="error" title="We could not load the report">{{ error }}</ValidationNotice>

    <article class="card chart" data-testid="channel-chart">
      <header class="chart-head">
        <h3 class="title">Result by channel</h3>
        <ul class="legend">
          <li v-for="item in legend" :key="item.status">
            <span class="swatch" :class="`mark--${item.status}`"></span>
            {{ item.label }} <span class="mono">{{ item.count.toLocaleString("en-US") }}</span>
          </li>
        </ul>
      </header>
      <div v-for="bar in bars" :key="bar.id" class="channel-row">
        <span class="channel-name">{{ bar.label }}</span>
        <div class="stack">
          <button
            v-for="segment in bar.segments"
            :key="segment.status"
            class="segment"
            :class="[`mark--${segment.status}`, { 'is-picked': isPicked(segmentSelection(bar.id, segment)) }]"
            :style="{ flexGrow: segment.count }"
            type="button"
            :disabled="segment.status === 'WAITING'"
            :title="`${bar.label}: ${segment.count} ${segment.label}`"
            :aria-label="`${bar.label}: ${segment.count} ${segment.label}. Show these deliveries.`"
            :aria-pressed="isPicked(segmentSelection(bar.id, segment))"
            :data-testid="`segment-${bar.id}-${segment.status}`"
            @click="pick(segmentSelection(bar.id, segment))"
          ></button>
        </div>
        <span class="mono small total">{{ bar.total.toLocaleString("en-US") }}</span>
      </div>
    </article>

    <article v-if="buckets.length" class="card chart" data-testid="timeline-chart">
      <header class="chart-head">
        <div>
          <h3 class="title">Calls to the provider over time</h3>
          <p class="small muted">Each bar is one {{ formatBucketWidth(width) }}. Select a bar to see who was called then.</p>
        </div>
        <ul class="legend">
          <li><span class="swatch swatch--accepted"></span> Accepted <span class="mono">{{ totals.accepted.toLocaleString("en-US") }}</span></li>
          <li><span class="swatch swatch--problem"></span> Not accepted <span class="mono">{{ totals.notAccepted.toLocaleString("en-US") }}</span></li>
        </ul>
      </header>
      <div class="plot-wrap">
        <div class="y-axis small muted mono" aria-hidden="true">
          <span>{{ axisMax.toLocaleString("en-US") }}</span>
          <span>0</span>
        </div>
        <div class="plot" @mouseleave="hovered = null">
          <button
            v-for="(bucket, index) in buckets"
            :key="bucket.at"
            class="column"
            :class="{ 'is-picked': isPicked(bucketQuery(bucket, width)) }"
            type="button"
            :disabled="!callsIn(bucket)"
            :aria-label="`${describeBucket(bucket)}. Show these deliveries.`"
            :aria-pressed="isPicked(bucketQuery(bucket, width))"
            data-testid="timeline-bar"
            @mouseenter="hovered = index"
            @focus="hovered = index"
            @blur="hovered = null"
            @click="pick(bucketQuery(bucket, width))"
          >
            <span class="fill">
              <span class="part part--problem" :style="{ height: height(notAccepted(bucket)) }"></span>
              <span class="part part--accepted" :style="{ height: height(bucket.accepted) }"></span>
            </span>
          </button>
          <div
            v-if="hovered !== null && buckets[hovered]"
            class="tooltip small"
            role="status"
            :class="{ 'tooltip--left': hovered > buckets.length / 2 }"
            :style="{ left: `${((hovered + 0.5) / buckets.length) * 100}%` }"
          >
            <strong>{{ formatBucket(buckets[hovered].at, width) }}</strong>
            <span><span class="mono">{{ buckets[hovered].accepted }}</span> accepted</span>
            <span v-if="buckets[hovered].rate_limited"><span class="mono">{{ buckets[hovered].rate_limited }}</span> rate limited</span>
            <span v-if="buckets[hovered].server_error"><span class="mono">{{ buckets[hovered].server_error }}</span> provider errors</span>
            <span v-if="buckets[hovered].rejected"><span class="mono">{{ buckets[hovered].rejected }}</span> rejected</span>
            <span v-if="buckets[hovered].timeout"><span class="mono">{{ buckets[hovered].timeout }}</span> no response</span>
          </div>
        </div>
        <div class="x-axis small muted mono" aria-hidden="true">
          <span>{{ formatBucket(buckets[0].at, width) }}</span>
          <span>{{ formatBucket(buckets[buckets.length - 1].at, width) }}</span>
        </div>
      </div>
      <details class="table-view small">
        <summary>View as table</summary>
        <div class="table-wrap">
          <table class="table" data-testid="timeline-table">
            <thead>
              <tr>
                <th scope="col">Time</th>
                <th scope="col" class="num">Accepted</th>
                <th scope="col" class="num">Rate limited</th>
                <th scope="col" class="num">Provider errors</th>
                <th scope="col" class="num">Rejected</th>
                <th scope="col" class="num">No response</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="bucket in busyBuckets" :key="bucket.at">
                <th scope="row" class="mono">{{ formatBucket(bucket.at, width) }}</th>
                <td class="num mono">{{ bucket.accepted }}</td>
                <td class="num mono">{{ bucket.rate_limited }}</td>
                <td class="num mono">{{ bucket.server_error }}</td>
                <td class="num mono">{{ bucket.rejected }}</td>
                <td class="num mono">{{ bucket.timeout }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </article>

    <div v-if="report" class="pair">
      <article class="card chart" data-testid="reason-chart">
        <header>
          <h3 class="title">Why messages did not go out</h3>
          <p class="small muted">How each message that was not accepted ended.</p>
        </header>
        <ul v-if="report.reasons.length" class="rows">
          <li v-for="item in report.reasons" :key="item.reason">
            <button
              class="row"
              :class="{ 'is-picked': isPicked({ reason: item.reason }) }"
              type="button"
              :aria-pressed="isPicked({ reason: item.reason })"
              :data-testid="`reason-${item.reason}`"
              @click="pick({ reason: item.reason })"
            >
              <span class="row-label small">{{ REASONS[item.reason] ?? item.reason }}</span>
              <span class="row-value small mono">{{ item.deliveries.toLocaleString("en-US") }}</span>
              <span class="track"><span class="bar" :style="{ width: `${(item.deliveries / reasonMax) * 100}%` }"></span></span>
            </button>
          </li>
        </ul>
        <p v-else class="small muted" data-testid="no-reasons">Nothing has ended without being accepted.</p>
      </article>

      <article class="card chart" data-testid="problem-chart">
        <header>
          <h3 class="title">Problems along the way</h3>
          <p class="small muted">Messages with at least one failed call. Many were accepted on a later try.</p>
        </header>
        <ul v-if="report.problems.length" class="rows">
          <li v-for="item in report.problems" :key="item.outcome">
            <button
              class="row"
              :class="{ 'is-picked': isPicked({ outcome: item.outcome }) }"
              type="button"
              :aria-pressed="isPicked({ outcome: item.outcome })"
              :data-testid="`problem-${item.outcome}`"
              @click="pick({ outcome: item.outcome })"
            >
              <span class="row-label small">{{ OUTCOMES[item.outcome] ?? item.outcome }}</span>
              <span class="row-value small mono">{{ item.deliveries.toLocaleString("en-US") }} <span class="muted">({{ item.attempts.toLocaleString("en-US") }} calls)</span></span>
              <span class="track"><span class="bar" :style="{ width: `${(item.deliveries / problemMax) * 100}%` }"></span></span>
            </button>
          </li>
        </ul>
        <p v-else class="small muted" data-testid="no-problems">No failed calls so far.</p>
      </article>
    </div>
  </section>
</template>

<style scoped>
.report,
.chart {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.chart {
  align-content: start;
  padding: var(--space-4);
}

.chart-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
  color: var(--color-text-muted);
}

.legend li {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.legend .mono {
  color: var(--color-text);
}

.swatch {
  width: 10px;
  height: 10px;
  border-radius: 2px;
}

/* Delivery states. Unknown is textured because amber and red are close for
   colour-blind readers, and failure must never rely on hue alone. */
.mark--SENT { background: var(--color-success); }
.mark--SKIPPED { background: var(--color-border-input); }
.mark--FAILED { background: var(--color-error); }
.mark--UNKNOWN { background: repeating-linear-gradient(135deg, var(--color-warning) 0 3px, var(--color-surface) 3px 5px); }
.mark--WAITING { background: var(--color-surface-subtle); box-shadow: inset 0 0 0 1px var(--color-border); }

.channel-row {
  display: grid;
  grid-template-columns: 48px minmax(0, 1fr) 56px;
  align-items: center;
  gap: var(--space-3);
}

.channel-name {
  font-weight: 500;
}

.total {
  text-align: right;
}

.stack {
  display: flex;
  gap: 2px;
  min-width: 0;
}

.segment {
  flex: 1 1 0;
  min-width: 6px;
  height: 20px;
  padding: 0;
  border: 0;
  cursor: pointer;
}

.segment:first-child { border-radius: 4px 0 0 4px; }
.segment:last-child { border-radius: 0 4px 4px 0; }
.segment:first-child:last-child { border-radius: 4px; }
.segment:disabled { cursor: default; }
.segment:not(:disabled):hover { opacity: 0.8; }

.segment.is-picked,
.column.is-picked {
  outline: 2px solid var(--color-text);
  outline-offset: 1px;
}

.plot-wrap {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-1) var(--space-2);
}

.y-axis {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  text-align: right;
}

.plot {
  position: relative;
  display: flex;
  align-items: stretch;
  justify-content: center;
  gap: 2px;
  height: 160px;
  border-bottom: 1px solid var(--color-border);
  background: linear-gradient(var(--color-border), var(--color-border)) top / 100% 1px no-repeat;
}

.column {
  display: flex;
  flex: 1 1 0;
  justify-content: center;
  align-items: stretch;
  min-width: 0;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: pointer;
  border-radius: 2px;
}

.column:not(:disabled):hover {
  background: var(--color-surface-subtle);
}

.column:disabled {
  cursor: default;
}

.fill {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  gap: 1px;
  width: 100%;
  max-width: 24px;
}

.part--accepted,
.swatch--accepted { background: var(--color-primary); }
.part--problem,
.swatch--problem { background: var(--color-warning); }
.part:first-child { border-radius: 4px 4px 0 0; }

.tooltip {
  position: absolute;
  top: var(--space-2);
  z-index: 2;
  display: grid;
  gap: 2px;
  min-width: 128px;
  margin-left: var(--space-2);
  padding: var(--space-2);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgb(23 32 51 / 12%);
  pointer-events: none;
}

.tooltip--left {
  transform: translateX(calc(-100% - 2 * var(--space-2)));
}

.x-axis {
  display: flex;
  grid-column: 2;
  justify-content: space-between;
}

.table-view summary {
  color: var(--color-text-muted);
  cursor: pointer;
}

.table-view .table-wrap {
  margin-top: var(--space-2);
  max-height: 240px;
  overflow: auto;
}

.pair {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: var(--space-4);
}

.chart header p {
  margin: var(--space-1) 0 0;
}

.rows {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--space-1) var(--space-3);
  width: 100%;
  padding: var(--space-2);
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.row:hover {
  background: var(--color-surface-subtle);
}

.row.is-picked {
  background: var(--color-primary-soft);
}

.track {
  grid-column: 1 / -1;
  height: 8px;
  overflow: hidden;
  background: var(--color-surface-subtle);
  border-radius: 4px;
}

.bar {
  display: block;
  height: 100%;
  background: var(--color-text-muted);
  border-radius: 0 4px 4px 0;
}
</style>

<script setup lang="ts">
import { RotateCw } from "lucide-vue-next";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, getDeliveryAttempts, getDeliveryMonitor, type DeliveryAttempt, type DeliveryMonitor, type MonitorQuery } from "../api/campaigns";
import DonutChart, { type DonutSlice } from "../components/DonutChart.vue";
import StatusBadge from "../components/StatusBadge.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { AUTO_REFRESH, storeAutoRefresh, storedAutoRefresh } from "../lib/preferences";
import { deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";
import { formatChannel, formatDateTime, shownOf } from "../lib/format";
import { OUTCOMES, REASONS } from "../lib/report";
import { systemHealth } from "../lib/systemHealth";

const PAGE = 100;
// The most the backend returns for one request.
const LARGEST_REQUEST = 500;
// An answer this late is treated as lost, so one stuck request cannot stop the refreshing.
const STUCK_MS = 15000;
// A color follows what it stands for, so a filter never repaints what stays on the chart.
// Checked with the data-viz validator for color vision differences; labels carry the rest.
const STATUS_COLORS: Record<string, string> = { QUEUED: "#2a78d6", SENDING: "#1baf7a", RETRYING: "#eda100", UNKNOWN: "#6250d6", FAILED: "#e34948" };
const CHANNEL_COLORS: Record<string, string> = { sms: "#2a78d6", email: "#eb6834" };
const REASON_COLORS: Record<string, string> = { rate_limited: "#eda100", server_error: "#e34948", rejected: "#2a78d6", timeout: "#6250d6", lease_expired: "#1baf7a" };
const OTHER_REASON_COLOR = "#4a5568";
const NO_REASON_COLOR = "#858FA1";
const STATUSES: DeliveryStatus[] = ["QUEUED", "SENDING", "RETRYING", "UNKNOWN", "FAILED"];
const PROBLEMS = ["rate_limited", "server_error", "rejected", "timeout"];
const REASON_FILTERS = ["server_error", "rejected", "timeout", "rate_limited", "lease_expired"];
const WINDOWS = [
  { value: "1", label: "Last hour" },
  { value: "24", label: "Last 24 hours" },
  { value: "168", label: "Last 7 days" },
];

// In the address, so a refresh keeps them and a link can share them.
const params = new URLSearchParams(window.location.search);
const query = ref<MonitorQuery>({
  status: params.get("status") ?? "",
  channel: params.get("channel") ?? "",
  outcome: params.get("outcome") ?? "",
  reason: params.get("reason") ?? "",
  within_hours: params.get("within_hours") ?? "",
});
const monitor = ref<DeliveryMonitor | null>(null);
const error = ref("");
const openId = ref("");
const attempts = ref<Record<string, DeliveryAttempt[]>>({});
const refreshing = ref(false);
const updatedAt = ref<Date | null>(null);
const autoRefresh = ref(storedAutoRefresh());
let latest = 0;
const secondsLeft = ref(0);
let loading = 0;
let loadStarted = 0;
let timer: ReturnType<typeof setInterval> | undefined;

const isFiltered = computed(() => Object.values(query.value).some(Boolean));
const rows = computed(() => monitor.value?.deliveries ?? []);
const hasMore = computed(() => rows.value.length < (monitor.value?.total ?? 0));
// Both channels share one provider, so one answer covers them.
const provider = computed(() => {
  const check = systemHealth.value?.checks.provider;
  if (!check) return { label: "Provider not checked yet", tone: "unknown" };
  return check === "ok" ? { label: "Provider reachable", tone: "ok" } : { label: "Provider not reachable", tone: "down" };
});

// Reads what is on screen again, in requests the backend answers in full.
async function load(count: number) {
  const request = ++latest;
  loading += 1;
  loadStarted = Date.now();
  try {
    let result = await getDeliveryMonitor(query.value, Math.min(count, LARGEST_REQUEST));
    while (result.deliveries.length < count && result.deliveries.length < result.total) {
      const next = await getDeliveryMonitor(query.value, Math.min(count - result.deliveries.length, LARGEST_REQUEST), result.deliveries.length);
      if (!next.deliveries.length) break;
      result = { ...next, deliveries: [...result.deliveries, ...next.deliveries] };
    }
    if (request !== latest) return;
    monitor.value = result;
    error.value = "";
    updatedAt.value = new Date();
  } catch (failure) {
    if (request === latest) error.value = errorMessage(failure);
  } finally {
    loading -= 1;
  }
}

const reload = () => load(Math.max(rows.value.length, PAGE));

async function refreshNow() {
  refreshing.value = true;
  secondsLeft.value = Number(autoRefresh.value);
  await reload();
  refreshing.value = false;
}

// A tick waits for the last answer, or a slow one would be replaced before it ever shows.
function startAutoRefresh() {
  clearInterval(timer);
  const seconds = Number(autoRefresh.value);
  secondsLeft.value = seconds;
  if (!seconds) return;
  timer = setInterval(() => {
    if (secondsLeft.value > 1) {
      secondsLeft.value -= 1;
      return;
    }
    if (document.hidden || (loading && Date.now() - loadStarted < STUCK_MS)) return;
    secondsLeft.value = seconds;
    void reload();
  }, 1000);
}

watch(autoRefresh, (value) => {
  storeAutoRefresh(value);
  startAutoRefresh();
});

async function showMore() {
  await load(rows.value.length + PAGE);
}

const statusSlices = computed<DonutSlice[]>(() =>
  STATUSES.map((status) => ({ key: status, label: deliveryStatusMeta[status].label, count: monitor.value?.counts[status] ?? 0, color: STATUS_COLORS[status], icon: deliveryStatusMeta[status].icon })),
);
const channelSlices = computed<DonutSlice[]>(() =>
  ["sms", "email"].map((channel) => ({ key: channel, label: formatChannel(channel), count: monitor.value?.channels[channel] ?? 0, color: CHANNEL_COLORS[channel] })),
);
// Always drawn in this order, not by size: only neighbours on the ring have to be told
// apart, and a fixed order keeps the same colours next to each other as the numbers move.
const REASON_ORDER = ["rejected", "lease_expired", "rate_limited", "timeout", "server_error"];
const reasonRank = (reason: string) => {
  if (!reason) return REASON_ORDER.length + 1;
  const rank = REASON_ORDER.indexOf(reason);
  return rank < 0 ? REASON_ORDER.length : rank;
};
const reasonSlices = computed<DonutSlice[]>(() =>
  [...(monitor.value?.reasons ?? [])].sort((a, b) => reasonRank(a.reason) - reasonRank(b.reason)).map(({ reason, count }) => ({
    key: reason || "none",
    label: reason ? (REASONS[reason] ?? reason) : "No problem yet",
    count,
    color: reason ? (REASON_COLORS[reason] ?? OTHER_REASON_COLOR) : NO_REASON_COLOR,
  })),
);

// Selecting what is already selected clears it.
function pick(field: "status" | "channel" | "reason", value: string) {
  query.value[field] = query.value[field] === value ? "" : value;
}

function clearFilters() {
  query.value = { status: "", channel: "", outcome: "", reason: "", within_hours: "" };
}

async function toggle(campaignId: string, id: string) {
  if (openId.value === id) {
    openId.value = "";
    return;
  }
  openId.value = id;
  try {
    attempts.value[id] = await getDeliveryAttempts(campaignId, id);
  } catch (failure) {
    error.value = errorMessage(failure);
  }
}

const campaignLink = (row: { campaign_id: string; status: string; channel: string }) =>
  `/campaigns/${row.campaign_id}?tab=deliveries&status=${row.status}&channel=${row.channel}`;

watch(
  query,
  (current) => {
    const search = new URLSearchParams(Object.entries(current).filter(([, value]) => value)).toString();
    window.history.replaceState({}, "", `${window.location.pathname}${search ? `?${search}` : ""}`);
    openId.value = "";
    void load(PAGE);
  },
  { deep: true },
);
onMounted(() => {
  void load(PAGE);
  startAutoRefresh();
});
onBeforeUnmount(() => clearInterval(timer));
</script>

<template>
  <div class="monitor">
    <header class="intro">
      <h1>See what is stuck, across every campaign.</h1>
      <p class="muted">Messages on their way, and those that ended badly. Accepted and skipped messages are in each campaign's report.</p>
      <div class="status-row">
        <p class="small provider" data-testid="monitor-provider"><span class="dot" :class="`dot--${provider.tone}`" aria-hidden="true"></span>{{ provider.label }}</p>
        <div class="refresh">
          <span v-if="updatedAt" class="small muted" data-testid="monitor-updated">Updated {{ updatedAt.toLocaleTimeString("en-GB") }}<template v-if="secondsLeft">, next in {{ secondsLeft }}s</template></span>
          <button class="button button--secondary" type="button" :disabled="refreshing" data-testid="monitor-refresh" @click="refreshNow">
            <RotateCw :size="14" :class="{ spinning: refreshing }" aria-hidden="true" />
            {{ refreshing ? "Refreshing..." : "Refresh" }}
          </button>
          <label class="small muted" for="monitor-auto-refresh">Auto refresh</label>
          <select id="monitor-auto-refresh" v-model="autoRefresh" class="field select" data-testid="monitor-auto-refresh">
            <option v-for="option in AUTO_REFRESH" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </div>
      </div>
    </header>

    <ValidationNotice v-for="paused in monitor?.paused ?? []" :key="paused.id" tone="warning" title="Sending is paused" data-testid="monitor-paused">
      <a :href="`/campaigns/${paused.id}`">{{ paused.name }}</a> stopped after getting nowhere. Open it to resume.
    </ValidationNotice>

    <section v-if="!error" class="charts" aria-label="How these deliveries are spread">
      <DonutChart title="By status" :slices="statusSlices" :selected="query.status" test-prefix="count" :unit="['delivery', 'deliveries']" @select="pick('status', $event)" />
      <DonutChart title="By channel" :slices="channelSlices" :selected="query.channel" test-prefix="channel" :unit="['delivery', 'deliveries']" @select="pick('channel', $event)" />
      <DonutChart title="By last problem" :slices="reasonSlices" :selected="query.reason" test-prefix="reason" :unit="['delivery', 'deliveries']" @select="pick('reason', $event)" />
    </section>

    <div class="filters">
      <label class="filter">
        <span class="small muted">Status</span>
        <select v-model="query.status" class="field select" data-testid="monitor-status">
          <option value="">All</option>
          <option v-for="status in STATUSES" :key="status" :value="status">{{ deliveryStatusMeta[status].label }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Channel</span>
        <select v-model="query.channel" class="field select" data-testid="monitor-channel">
          <option value="">All</option>
          <option value="sms">SMS</option>
          <option value="email">Email</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Had a call that was</span>
        <select v-model="query.outcome" class="field select" data-testid="monitor-outcome">
          <option value="">Any</option>
          <option v-for="item in PROBLEMS" :key="item" :value="item">{{ OUTCOMES[item] }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Last problem</span>
        <select v-model="query.reason" class="field select" data-testid="monitor-reason">
          <option value="">Any</option>
          <option v-for="item in REASON_FILTERS" :key="item" :value="item">{{ REASONS[item] }}</option>
          <option value="none">No problem yet</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Changed in</span>
        <select v-model="query.within_hours" class="field select" data-testid="monitor-within">
          <option value="">Any time</option>
          <option v-for="item in WINDOWS" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
      </label>
      <button v-if="isFiltered" class="link small clear" type="button" data-testid="monitor-clear" @click="clearFilters">Clear filters</button>
    </div>

    <ValidationNotice v-if="error" tone="error" title="We could not load the monitor">{{ error }}</ValidationNotice>
    <p v-if="!monitor && !error" class="muted" role="status">Loading the monitor...</p>
    <template v-else-if="monitor">
      <p v-if="!rows.length" class="muted" data-testid="monitor-empty">{{ isFiltered ? "Nothing matches these filters." : "Nothing is waiting and nothing has failed." }}</p>
      <template v-else>
        <p class="small muted line" data-testid="monitor-count">{{ shownOf(rows.length, monitor.total, "delivery", "deliveries") }}</p>
        <div class="table-wrap card">
          <table class="table">
            <thead>
              <tr>
                <th scope="col">Campaign</th>
                <th scope="col">Recipient</th>
                <th scope="col">Channel</th>
                <th scope="col">Status</th>
                <th scope="col" class="num">Calls</th>
                <th scope="col" class="num">Response</th>
                <th scope="col">Note</th>
                <th scope="col">Changed</th>
                <th scope="col"><span class="visually-hidden">History</span></th>
              </tr>
            </thead>
            <tbody>
              <template v-for="row in rows" :key="row.id">
                <tr data-testid="monitor-row">
                  <th scope="row"><a :href="campaignLink(row)" data-testid="monitor-campaign">{{ row.campaign_name }}</a></th>
                  <td>{{ row.first_name || "No first name" }} <span class="muted small mono">#{{ row.user_id }}</span></td>
                  <td>{{ formatChannel(row.channel) }}</td>
                  <td><StatusBadge :status="row.status as DeliveryStatus" /></td>
                  <td class="num mono">{{ row.calls }}</td>
                  <td class="num mono">{{ row.response_code ?? "—" }}</td>
                  <td class="small muted note">{{ REASONS[row.reason] ?? row.reason }}</td>
                  <td class="small">{{ formatDateTime(row.updated_at) }}</td>
                  <td>
                    <button class="link small" type="button" :aria-expanded="openId === row.id" data-testid="monitor-history" @click="toggle(row.campaign_id, row.id)">
                      {{ openId === row.id ? "Hide" : "History" }}
                    </button>
                  </td>
                </tr>
                <tr v-if="openId === row.id" class="history">
                  <td colspan="9">
                    <p v-if="!attempts[row.id]" class="muted small">Loading...</p>
                    <p v-else-if="!attempts[row.id].length" class="muted small">No calls to the provider yet.</p>
                    <ol v-else class="attempts" data-testid="monitor-attempts">
                      <li v-for="(attempt, index) in attempts[row.id]" :key="index" class="small">
                        <span class="mono">{{ attempt.response_code ?? "—" }}</span>
                        <span>{{ OUTCOMES[attempt.outcome] ?? attempt.outcome }}</span>
                        <span class="muted">{{ formatDateTime(attempt.occurred_at) }}</span>
                      </li>
                    </ol>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <div v-if="hasMore">
          <button class="button button--secondary" type="button" data-testid="monitor-more" @click="showMore">Show more</button>
        </div>
      </template>
    </template>
  </div>
</template>

<style scoped>
.monitor {
  display: grid;
  gap: var(--space-6);
  min-width: 0;
}

.intro {
  display: grid;
  gap: var(--space-2);
}

.status-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.refresh {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.refresh .select {
  width: auto;
}

.refresh .button {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.spinning {
  animation: spin 800ms linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .spinning {
    animation: none;
  }
}

.provider {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
}

.dot {
  width: 8px;
  height: 8px;
  background: var(--color-border-input);
  border-radius: 50%;
}

.dot--ok {
  background: var(--color-success);
}

.dot--down {
  background: var(--color-error);
}

.charts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--space-3);
}

.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-3);
}

.filter {
  display: grid;
  gap: var(--space-1);
  min-width: 160px;
}

.select {
  height: 40px;
  padding-block: 0;
}

.clear {
  min-height: 40px;
}

.line {
  margin: 0 0 calc(var(--space-4) * -1);
}

/* Positioned so the table's hidden header text scrolls with it instead of
   widening the page. */
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

.note {
  min-width: 140px;
  white-space: normal;
}

.link {
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.history td {
  background: var(--color-surface-subtle);
}

.attempts {
  display: grid;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-4);
}

.attempts li > span {
  margin-right: var(--space-3);
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>

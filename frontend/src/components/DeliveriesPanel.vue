<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { deliveriesCsvUrl, errorMessage, getDeliveries, getDeliveryCities, getDeliveryAttempts, type Delivery, type DeliveryAttempt, type Progress } from "../api/campaigns";
import { DELIVERY_STATUSES, deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";
import { formatDateTime, shownOf } from "../lib/format";
import { ARRIVALS, CALLS, OUTCOMES, REASONS, applySelection, arrivalOf, describeQuery, isFiltered, noFilter, queryFromSearch, searchWithQuery, type DeliveryQuery } from "../lib/report";
import DeliveryReport from "./DeliveryReport.vue";
import StatusBadge from "./StatusBadge.vue";
import ValidationNotice from "./ValidationNotice.vue";

const PAGE = 100;
// The most the backend returns for one request.
const LARGEST_REQUEST = 500;
const REASON_OPTIONS = ["no_consent", "consent_withdrawn", "server_error", "rejected", "timeout", "lease_expired"];
const PROBLEM_OPTIONS = ["rate_limited", "server_error", "rejected", "timeout"];

// run keeps one send of a repeating campaign; none shows every send.
const props = defineProps<{ campaignId: string; channels: string[]; abTest?: boolean; refreshKey?: number; progress?: Progress | null; run?: number | null }>();

const rows = ref<Delivery[] | null>(null);
// In the address, so a refresh keeps them and a link can share them.
const query = ref<DeliveryQuery>(queryFromSearch(window.location.search));
const cities = ref<string[]>([]);
const tableStart = ref<HTMLElement | null>(null);
const error = ref("");
const total = ref(0);
const hasMore = computed(() => (rows.value?.length ?? 0) < total.value);
const openId = ref("");
const attempts = ref<Record<string, DeliveryAttempt[]>>({});
let latest = 0;

// Asks in requests the backend will answer in full, so a short answer means the list ended.
async function fetchFrom(offset: number, count: number) {
  const found: Delivery[] = [];
  let matching = 0;
  while (found.length < count) {
    const limit = Math.min(count - found.length, LARGEST_REQUEST);
    const page = await getDeliveries(props.campaignId, { ...query.value, run: props.run ?? undefined, limit, offset: offset + found.length });
    found.push(...page.deliveries);
    matching = page.total;
    if (page.deliveries.length < limit) break;
  }
  return { found, matching };
}

// Reads the rows on screen again, because the list changes while a campaign sends.
async function load(count: number) {
  const request = ++latest;
  try {
    const { found, matching } = await fetchFrom(0, count);
    // A slower answer for earlier filters must not replace a newer one.
    if (request !== latest) return;
    rows.value = found;
    total.value = matching;
    error.value = "";
  } catch (failure) {
    if (request === latest) error.value = errorMessage(failure);
  }
}

async function showMore() {
  const request = ++latest;
  const shown = rows.value ?? [];
  try {
    const { found, matching } = await fetchFrom(shown.length, PAGE);
    if (request !== latest) return;
    rows.value = [...shown, ...found];
    total.value = matching;
    error.value = "";
  } catch (failure) {
    if (request === latest) error.value = errorMessage(failure);
  }
}

async function toggle(row: Delivery) {
  if (openId.value === row.id) {
    openId.value = "";
    return;
  }
  openId.value = row.id;
  try {
    attempts.value[row.id] = await getDeliveryAttempts(props.campaignId, row.id);
  } catch (failure) {
    error.value = errorMessage(failure);
  }
}

function changeFilter() {
  openId.value = "";
  rows.value = null;
  total.value = 0;
  load(PAGE);
}

function selectFromChart(selection: Partial<DeliveryQuery>) {
  query.value = applySelection(query.value, selection);
  changeFilter();
  tableStart.value?.scrollIntoView?.({ behavior: "smooth", block: "start" });
}

function clearFilters() {
  query.value = noFilter();
  changeFilter();
}

// Without the list there is simply no city filter; the deliveries still show.
async function loadCities() {
  cities.value = await getDeliveryCities(props.campaignId).catch(() => []);
}

watch(query, (current) => {
  window.history.replaceState({}, "", `${window.location.pathname}${searchWithQuery(window.location.search, current)}`);
}, { deep: true, flush: "sync" });

watch(() => props.refreshKey, () => {
  load(Math.max(rows.value?.length ?? 0, PAGE));
  if (!cities.value.length) loadCities();
});
onMounted(() => {
  load(PAGE);
  loadCities();
});
</script>

<template>
  <section class="deliveries">
    <DeliveryReport
      v-if="progress?.total"
      :campaign-id="campaignId"
      :progress="progress"
      :query="query"
      :run="run"
      :refresh-key="refreshKey"
      @select="selectFromChart"
      @reported="load(Math.max(rows?.length ?? 0, PAGE))"
    />

    <div ref="tableStart" class="filters">
      <label class="filter">
        <span class="small muted">Status</span>
        <select v-model="query.status" class="field select" data-testid="filter-status" @change="changeFilter">
          <option value="">All</option>
          <option v-for="item in DELIVERY_STATUSES" :key="item" :value="item">{{ deliveryStatusMeta[item].label }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Channel</span>
        <select v-model="query.channel" class="field select" data-testid="filter-channel" @change="changeFilter">
          <option value="">All</option>
          <option v-if="channels.includes('sms')" value="sms">SMS</option>
          <option v-if="channels.includes('email')" value="email">Email</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Ended as</span>
        <select v-model="query.reason" class="field select" data-testid="filter-reason" @change="changeFilter">
          <option value="">Any</option>
          <option v-for="item in REASON_OPTIONS" :key="item" :value="item">{{ REASONS[item] }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Had a call that was</span>
        <select v-model="query.outcome" class="field select" data-testid="filter-outcome" @change="changeFilter">
          <option value="">Any</option>
          <option v-for="item in PROBLEM_OPTIONS" :key="item" :value="item">{{ OUTCOMES[item] }}</option>
        </select>
      </label>
      <label v-if="abTest" class="filter">
        <span class="small muted">Version</span>
        <select v-model="query.variant" class="field select" data-testid="filter-variant" @change="changeFilter">
          <option value="">Any</option>
          <option value="A">Version A</option>
          <option value="B">Version B</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Arrival</span>
        <select v-model="query.arrival" class="field select" data-testid="filter-arrival" @change="changeFilter">
          <option value="">Any</option>
          <option value="delivered">{{ ARRIVALS.delivered }}</option>
          <option value="bounced">{{ ARRIVALS.bounced }}</option>
          <option value="waiting">{{ ARRIVALS.waiting }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Opened or read</span>
        <select v-model="query.opened" class="field select" data-testid="filter-opened" @change="changeFilter">
          <option value="">Any</option>
          <option value="yes">Yes</option>
          <option value="no">Not yet</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Provider calls</span>
        <select v-model="query.calls" class="field select" data-testid="filter-calls" @change="changeFilter">
          <option value="">Any</option>
          <option v-for="(label, value) in CALLS" :key="value" :value="value">{{ label }}</option>
        </select>
      </label>
      <label v-if="cities.length > 1" class="filter">
        <span class="small muted">City</span>
        <select v-model="query.city" class="field select" data-testid="filter-city" @change="changeFilter">
          <option value="">Any</option>
          <option v-for="city in cities" :key="city" :value="city">{{ city }}</option>
        </select>
      </label>
    </div>

    <div v-if="rows?.length" class="export">
      <p class="small muted count" data-testid="delivery-count">{{ shownOf(rows.length, total, "delivery", "deliveries") }}</p>
      <a class="button button--secondary" :href="deliveriesCsvUrl(campaignId, { ...query, run: run ?? undefined })" download data-testid="export-csv">
        {{ isFiltered(query) ? "Export these as CSV" : "Export all as CSV" }}
      </a>
    </div>

    <p v-if="isFiltered(query)" class="active small" data-testid="active-filter">
      <span>Showing: {{ describeQuery(query) }}</span>
      <button class="link small" type="button" data-testid="clear-filters" @click="clearFilters">Clear filters</button>
    </p>

    <ValidationNotice v-if="error" tone="error" title="We could not load the deliveries">{{ error }}</ValidationNotice>
    <p v-if="!rows && !error" class="muted" role="status">Loading deliveries...</p>
    <p v-else-if="rows && !rows.length" class="muted" data-testid="deliveries-empty">No deliveries match these filters.</p>

    <div v-else-if="rows" class="table-wrap card">
      <table class="table">
        <thead>
          <tr>
            <th scope="col">Recipient</th>
            <th scope="col">Channel</th>
            <th v-if="abTest" scope="col">Version</th>
            <th scope="col">Status</th>
            <th scope="col" class="num">Calls</th>
            <th scope="col" class="num">Response</th>
            <th scope="col">Arrival</th>
            <th scope="col">Opened</th>
            <th scope="col">Note</th>
            <th scope="col"><span class="visually-hidden">History</span></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in rows" :key="row.id">
            <tr data-testid="delivery-row">
              <th scope="row">{{ row.first_name || "No first name" }} <span class="muted small mono">#{{ row.user_id }}</span></th>
              <td>{{ row.channel === "sms" ? "SMS" : "Email" }}</td>
              <td v-if="abTest" data-testid="delivery-variant">{{ row.variant || "—" }}</td>
              <td><StatusBadge :status="row.status as DeliveryStatus" /></td>
              <td class="num mono" data-testid="delivery-calls">{{ row.calls ?? row.attempts }}</td>
              <td class="num mono">{{ row.response_code ?? "—" }}</td>
              <td class="small" data-testid="delivery-arrival">{{ arrivalOf(row)?.label ?? "—" }}</td>
              <td class="small" data-testid="delivery-opened">{{ row.opened_at ? formatDateTime(row.opened_at) : "—" }}</td>
              <td class="small muted note">{{ REASONS[row.reason] ?? "" }}</td>
              <td>
                <button class="link small" type="button" :disabled="row.status === 'SKIPPED'" :aria-expanded="openId === row.id" data-testid="toggle-attempts" @click="toggle(row)">
                  {{ openId === row.id ? "Hide" : "History" }}
                </button>
              </td>
            </tr>
            <tr v-if="openId === row.id" class="history">
              <td :colspan="abTest ? 10 : 9">
                <p v-if="!attempts[row.id]" class="muted small">Loading...</p>
                <p v-else-if="!attempts[row.id].length" class="muted small">No calls to the provider yet.</p>
                <ol v-else class="attempts" data-testid="attempt-list">
                  <li v-for="(attempt, index) in attempts[row.id]" :key="index" class="small">
                    <span class="mono">{{ attempt.response_code ?? "—" }}</span>
                    <span>{{ OUTCOMES[attempt.outcome] ?? attempt.outcome }}</span>
                    <span class="muted">{{ formatDateTime(attempt.occurred_at) }}</span>
                  </li>
                  <li v-if="arrivalOf(row)?.at" class="small" data-testid="attempt-arrival">
                    <span class="mono">—</span>
                    <span>{{ arrivalOf(row)?.label }} (simulated report)</span>
                    <span class="muted">{{ formatDateTime(arrivalOf(row)?.at ?? "") }}</span>
                  </li>
                </ol>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>

    <div v-if="hasMore">
      <button class="button button--secondary" type="button" data-testid="load-more" @click="showMore">Show more</button>
    </div>
  </section>
</template>

<style scoped>
.deliveries {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.export {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.count {
  margin: 0;
}

.export .button {
  text-decoration: none;
}

.active {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-2) var(--space-3);
  background: var(--color-primary-soft);
  border-radius: 8px;
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
  white-space: normal;
  min-width: 140px;
}

.link {
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.link:disabled {
  color: var(--color-text-muted);
  cursor: not-allowed;
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

<script setup lang="ts">
import { onMounted, ref, watch } from "vue";

import { errorMessage, getDeliveries, getDeliveryAttempts, type Delivery, type DeliveryAttempt } from "../api/campaigns";
import { DELIVERY_STATUSES, deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";
import { formatDateTime } from "../lib/format";
import StatusBadge from "./StatusBadge.vue";
import ValidationNotice from "./ValidationNotice.vue";

const PAGE = 100;
const REASONS: Record<string, string> = {
  no_consent: "No consent for this channel",
  consent_withdrawn: "Consent withdrawn before sending",
  server_error: "Provider error",
  rejected: "Rejected by the provider",
  timeout: "No response from the provider",
  rate_limited: "Rate limited by the provider",
  rate_limiter_unavailable: "Waiting for the rate limiter",
};
const OUTCOMES: Record<string, string> = {
  accepted: "Accepted",
  rate_limited: "Rate limited",
  server_error: "Provider error",
  rejected: "Rejected",
  timeout: "No response",
};

const props = defineProps<{ campaignId: string; refreshKey?: number }>();

const rows = ref<Delivery[] | null>(null);
const status = ref("");
const channel = ref("");
const error = ref("");
const hasMore = ref(false);
const openId = ref("");
const attempts = ref<Record<string, DeliveryAttempt[]>>({});
let latest = 0;

async function load(limit: number) {
  const request = ++latest;
  try {
    const page = await getDeliveries(props.campaignId, { status: status.value, channel: channel.value, limit });
    // A slower answer for earlier filters must not replace a newer one.
    if (request !== latest) return;
    rows.value = page;
    hasMore.value = page.length === limit;
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
  hasMore.value = false;
  load(PAGE);
}

watch(() => props.refreshKey, () => load(Math.max(rows.value?.length ?? 0, PAGE)));
onMounted(() => load(PAGE));
</script>

<template>
  <section class="deliveries">
    <div class="filters">
      <label class="filter">
        <span class="small muted">Status</span>
        <select v-model="status" class="field select" data-testid="filter-status" @change="changeFilter">
          <option value="">All</option>
          <option v-for="item in DELIVERY_STATUSES" :key="item" :value="item">{{ deliveryStatusMeta[item].label }}</option>
        </select>
      </label>
      <label class="filter">
        <span class="small muted">Channel</span>
        <select v-model="channel" class="field select" data-testid="filter-channel" @change="changeFilter">
          <option value="">All</option>
          <option value="sms">SMS</option>
          <option value="email">Email</option>
        </select>
      </label>
    </div>

    <ValidationNotice v-if="error" tone="error" title="We could not load the deliveries">{{ error }}</ValidationNotice>
    <p v-if="!rows && !error" class="muted" role="status">Loading deliveries...</p>
    <p v-else-if="rows && !rows.length" class="muted" data-testid="deliveries-empty">No deliveries match these filters.</p>

    <div v-else-if="rows" class="table-wrap card">
      <table class="table">
        <thead>
          <tr>
            <th scope="col">Recipient</th>
            <th scope="col">Channel</th>
            <th scope="col">Status</th>
            <th scope="col" class="num">Attempts</th>
            <th scope="col" class="num">Response</th>
            <th scope="col">Note</th>
            <th scope="col"><span class="visually-hidden">History</span></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in rows" :key="row.id">
            <tr data-testid="delivery-row">
              <th scope="row">{{ row.first_name || "No first name" }} <span class="muted small mono">#{{ row.user_id }}</span></th>
              <td>{{ row.channel === "sms" ? "SMS" : "Email" }}</td>
              <td><StatusBadge :status="row.status as DeliveryStatus" /></td>
              <td class="num mono">{{ row.attempts }}</td>
              <td class="num mono">{{ row.response_code ?? "—" }}</td>
              <td class="small muted note">{{ REASONS[row.reason] ?? "" }}</td>
              <td>
                <button class="link small" type="button" :disabled="row.status === 'SKIPPED'" :aria-expanded="openId === row.id" data-testid="toggle-attempts" @click="toggle(row)">
                  {{ openId === row.id ? "Hide" : "History" }}
                </button>
              </td>
            </tr>
            <tr v-if="openId === row.id" class="history">
              <td colspan="7">
                <p v-if="!attempts[row.id]" class="muted small">Loading...</p>
                <p v-else-if="!attempts[row.id].length" class="muted small">No calls to the provider yet.</p>
                <ol v-else class="attempts" data-testid="attempt-list">
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
      <button class="button button--secondary" type="button" data-testid="load-more" @click="load((rows?.length ?? 0) + PAGE)">Show more</button>
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

.filter {
  display: grid;
  gap: var(--space-1);
  min-width: 160px;
}

.select {
  height: 40px;
  padding-block: 0;
}

.table-wrap {
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

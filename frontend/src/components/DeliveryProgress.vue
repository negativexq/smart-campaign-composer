<script setup lang="ts">
import { computed } from "vue";

import type { Progress } from "../api/campaigns";
import { deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";
import MetricCard from "./MetricCard.vue";

const props = defineProps<{ progress: Progress; live: boolean }>();

const percent = computed(() => (props.progress.total ? Math.round((props.progress.processed / props.progress.total) * 100) : 0));
const channels = [
  { id: "sms", label: "SMS" },
  { id: "email", label: "Email" },
];
const columns: DeliveryStatus[] = ["SENT", "FAILED", "UNKNOWN", "SKIPPED"];
const waiting = (id: string) => {
  const counts = props.progress.by_channel[id] ?? {};
  return (counts.QUEUED ?? 0) + (counts.SENDING ?? 0) + (counts.RETRYING ?? 0);
};
const responses = computed(() => Object.entries(props.progress.responses).sort(([a], [b]) => a.localeCompare(b)));
const count = (status: DeliveryStatus) => props.progress.by_status[status] ?? 0;
</script>

<template>
  <section class="progress" aria-label="Delivery progress">
    <div v-if="live" class="bar-block">
      <div class="bar-head">
        <span data-testid="progress-count"><strong>{{ progress.processed.toLocaleString("en-US") }}</strong> of {{ progress.total.toLocaleString("en-US") }} processed</span>
        <span class="mono small">{{ percent }}%</span>
      </div>
      <div class="bar" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100">
        <span :style="{ width: `${percent}%` }"></span>
      </div>
    </div>

    <div class="metrics">
      <MetricCard label="Accepted" :value="count('SENT')" hint="By the mock provider" />
      <MetricCard label="Failed" :value="count('FAILED')" />
      <MetricCard label="Outcome unknown" :value="count('UNKNOWN')" />
      <MetricCard label="Skipped" :value="count('SKIPPED')" hint="No consent" />
    </div>

    <div class="table-wrap card">
      <table class="table" data-testid="channel-table">
        <thead>
          <tr>
            <th scope="col">Channel</th>
            <th v-for="status in columns" :key="status" scope="col" class="num">{{ deliveryStatusMeta[status].label }}</th>
            <th v-if="live" scope="col" class="num">Waiting</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="channel in channels" :key="channel.id">
            <th scope="row">{{ channel.label }}</th>
            <td v-for="status in columns" :key="status" class="num mono">{{ progress.by_channel[channel.id]?.[status] ?? 0 }}</td>
            <td v-if="live" class="num mono">{{ waiting(channel.id) }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <p class="muted small" data-testid="provider-summary">
      <span class="mono">{{ progress.attempts.toLocaleString("en-US") }}</span> attempts to the provider.
      <template v-if="responses.length">
        Responses:
        <span v-for="([code, total], index) in responses" :key="code"><span class="mono">{{ code }}</span> × {{ total }}{{ index < responses.length - 1 ? ", " : "." }}</span>
      </template>
    </p>
    <p v-if="progress.responses['429']" class="muted small">Calls answered with 429 are retried and do not use up an attempt.</p>
    <p class="muted small">{{ deliveryStatusMeta.SENT.description }}</p>
  </section>
</template>

<style scoped>
.progress,
.bar-block {
  display: grid;
  gap: var(--space-3);
}

.bar-block {
  gap: var(--space-2);
}

.bar-head {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
}

.bar {
  height: 8px;
  overflow: hidden;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.bar span {
  display: block;
  height: 100%;
  background: var(--color-primary);
  transition: width 0.3s ease;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
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

.table tbody tr:last-child th,
.table tbody tr:last-child td {
  border-bottom: 0;
}

.table .num {
  text-align: right;
}
</style>

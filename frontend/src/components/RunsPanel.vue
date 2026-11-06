<script setup lang="ts">
import { computed } from "vue";

import type { RunSummary } from "../api/campaigns";
import { formatDateTime } from "../lib/format";
import MetricCard from "./MetricCard.vue";

const props = defineProps<{ runs: RunSummary[]; nextRunAt: string | null; selected: number | null }>();
const emit = defineEmits<{ open: [number] }>();

const count = (value: number) => value.toLocaleString("en-US");

// Every send added up. Skipped people were left out before any message, so they are not sent.
const totals = computed(() =>
  props.runs.reduce((sum, run) => ({ sent: sum.sent + run.sent, failed: sum.failed + run.failed, skipped: sum.skipped + run.skipped }), { sent: 0, failed: 0, skipped: 0 }),
);
const nextNumber = computed(() => (props.runs[0]?.number ?? 0) + 1);
</script>

<template>
  <div class="runs">
    <div class="metrics">
      <MetricCard label="Runs" :value="runs.length" />
      <MetricCard label="Sent, every run" :value="totals.sent" />
      <MetricCard label="Failed" :value="totals.failed" />
      <MetricCard label="Left out" :value="totals.skipped" hint="No consent for the channel" />
    </div>

    <div class="wrap card">
      <table class="table">
        <thead>
          <tr>
            <th scope="col">Run</th>
            <th scope="col">Started</th>
            <th scope="col">Status</th>
            <th scope="col" class="numeric">Sent</th>
            <th scope="col" class="numeric optional">Failed</th>
            <th scope="col" class="numeric optional">Left out</th>
            <th scope="col"><span class="visually-hidden">Open</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="nextRunAt" class="upcoming" data-testid="run-upcoming">
            <td class="mono">#{{ nextNumber }}</td>
            <td>{{ formatDateTime(nextRunAt) }}</td>
            <td><span class="badge small">Upcoming</span></td>
            <td class="numeric muted">—</td>
            <td class="numeric muted optional">—</td>
            <td class="numeric muted optional">—</td>
            <td></td>
          </tr>
          <tr v-for="run in runs" :key="run.number" :class="{ selected: run.number === selected }" data-testid="run-row">
            <td class="mono">#{{ run.number }}</td>
            <td>{{ formatDateTime(run.started_at) }}</td>
            <td>
              <span v-if="run.finished_at" class="badge badge--success small">Finished</span>
              <span v-else class="badge badge--primary small">Sending, {{ count(run.processed) }} of {{ count(run.total) }}</span>
            </td>
            <td class="numeric mono">{{ count(run.sent) }}</td>
            <td class="numeric mono optional" :class="{ error: run.failed }">{{ count(run.failed) }}</td>
            <td class="numeric mono optional">{{ count(run.skipped) }}</td>
            <td class="numeric">
              <button class="link" type="button" :data-testid="`open-run-${run.number}`" @click="emit('open', run.number)">Open</button>
            </td>
          </tr>
          <tr v-if="!runs.length && !nextRunAt">
            <td colspan="7" class="muted">This campaign has not sent yet.</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="small muted">Open a run to see its overview and deliveries on their own.</p>
  </div>
</template>

<style scoped>
.runs {
  display: grid;
  gap: var(--space-4);
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
}

.wrap {
  overflow-x: auto;
}

.table {
  width: 100%;
  border-collapse: collapse;
}

th {
  color: var(--color-text-muted);
  font-size: 12px;
  font-weight: 500;
  text-align: left;
}

th,
td {
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  white-space: nowrap;
}

tbody tr:last-child td {
  border-bottom: 0;
}

.upcoming td {
  color: var(--color-text-muted);
}

.selected td {
  background: var(--color-primary-soft);
}

.numeric {
  text-align: right;
}

.error {
  color: var(--color-error);
}

.badge {
  display: inline-block;
  padding: 2px var(--space-2);
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
  font-weight: 500;
}

.badge--success {
  color: var(--color-success);
}

.badge--primary {
  color: var(--color-primary);
}

.link {
  padding: 0;
  color: var(--color-primary);
  background: none;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.link:hover {
  text-decoration: underline;
}

@media (max-width: 760px) {
  .optional {
    display: none;
  }
}
</style>

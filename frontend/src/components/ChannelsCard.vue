<script setup lang="ts">
import { computed } from "vue";

import type { ChannelUse } from "../api/campaigns";
import { formatChannel, formatDateTime } from "../lib/format";
import { systemHealth } from "../lib/systemHealth";

defineProps<{ channels: ChannelUse[] }>();

// Both channels go through one provider, so they cannot be up and down separately.
const reach = computed(() => {
  const check = systemHealth.value?.checks.provider;
  if (!check) return { label: "Not checked", tone: "unknown" };
  return check === "ok" ? { label: "Reachable", tone: "ok" } : { label: "Not reachable", tone: "down" };
});
</script>

<template>
  <div class="channels card" data-testid="channels">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Channel</th>
          <th scope="col">Provider</th>
          <th scope="col">Connection</th>
          <th scope="col">Last accepted</th>
          <th scope="col" class="num">Calls, last hour</th>
          <th scope="col" class="num">Not accepted</th>
          <th scope="col" class="num">Busiest second</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in channels" :key="row.channel" :data-testid="`channel-${row.channel}`">
          <th scope="row">{{ formatChannel(row.channel) }}</th>
          <td>{{ row.provider }}</td>
          <td><span class="dot" :class="`dot--${reach.tone}`" aria-hidden="true"></span> {{ reach.label }}</td>
          <td>{{ row.last_accepted_at ? formatDateTime(row.last_accepted_at) : "Nothing yet" }}</td>
          <td class="num mono">{{ row.calls_last_hour.toLocaleString("en-US") }}</td>
          <td class="num mono">{{ row.not_accepted_last_hour.toLocaleString("en-US") }}</td>
          <td class="num mono" :data-testid="`busiest-${row.channel}`">{{ row.busiest_second_last_hour.toLocaleString("en-US") }}</td>
        </tr>
      </tbody>
    </table>
    <p class="muted small note">Both channels use the mock provider, so they go up and down together. Sending is limited per channel per second, and the busiest second shows how close the last hour came. Accepted means the provider took the message, not that a person got it.</p>
  </div>
</template>

<style scoped>
.channels {
  margin-top: var(--space-3);
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
  padding: var(--space-3) var(--space-4);
}

.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: var(--space-1);
  border-radius: 50%;
  background: var(--color-text-muted);
}

.dot--ok {
  background: var(--color-success);
}

.dot--down {
  background: var(--color-error);
}
</style>

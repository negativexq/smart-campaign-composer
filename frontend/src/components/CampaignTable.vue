<script setup lang="ts">
import type { CampaignSummary } from "../api/campaigns";
import { formatChannel, formatDateTime } from "../lib/format";
import CampaignStatusBadge from "./CampaignStatusBadge.vue";

defineProps<{ campaigns: CampaignSummary[] }>();

function progress(campaign: CampaignSummary) {
  if (!campaign.deliveries_total) return "—";
  return `${Math.floor((campaign.deliveries_processed / campaign.deliveries_total) * 100)}%`;
}

function channels(campaign: CampaignSummary) {
  return campaign.channels.length ? campaign.channels.map(formatChannel).join(" + ") : "—";
}
</script>

<template>
  <div class="wrap card">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Campaign</th>
          <th scope="col">Status</th>
          <th scope="col" class="optional">Channels</th>
          <th scope="col" class="optional">Created</th>
          <th scope="col" class="numeric">Progress</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="campaign in campaigns" :key="campaign.id" data-testid="campaign-row">
          <td class="name">
            <a :href="`/campaigns/${campaign.id}`" :title="campaign.brief">{{ campaign.brief }}</a>
          </td>
          <td><CampaignStatusBadge :status="campaign.status" /></td>
          <td class="optional">{{ channels(campaign) }}</td>
          <td class="optional muted">{{ formatDateTime(campaign.created_at) }}</td>
          <td class="numeric mono">{{ progress(campaign) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
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

.name {
  width: 100%;
  max-width: 0;
  min-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.name a {
  color: var(--color-text);
  font-weight: 500;
  text-decoration: none;
}

.name a:hover {
  color: var(--color-primary);
  text-decoration: underline;
}

.numeric {
  text-align: right;
}

@media (max-width: 760px) {
  .optional {
    display: none;
  }
}
</style>

<script setup lang="ts">
import type { CampaignSummary, RunSummary } from "../api/campaigns";
import { formatChannel, formatDateTime } from "../lib/format";
import CampaignStatusBadge from "./CampaignStatusBadge.vue";

defineProps<{ campaigns: CampaignSummary[]; tag?: string }>();
// Picking a tag on a row filters the list by it.
const emit = defineEmits<{ tag: [string] }>();

const count = (value: number) => value.toLocaleString("en-US");

// What kind of campaign it is, in the words of its rule, and the channels it uses.
function kindOf(campaign: CampaignSummary) {
  const channels = campaign.channels.map(formatChannel).join(", ");
  const kind = campaign.schedule?.repeats ? campaign.schedule.description : campaign.schedule ? "One time, at a set time" : "One time";
  return channels ? `${kind} · ${channels}` : kind;
}

function lastResult(run: RunSummary) {
  if (!run.finished_at) return run.total ? `${Math.floor((run.processed / run.total) * 100)}% done` : "Starting";
  return `${count(run.sent)} sent`;
}

function lastDetail(campaign: CampaignSummary, run: RunSummary) {
  const parts = [];
  if (campaign.schedule?.repeats || run.number > 1) parts.push(`Run #${run.number}`);
  parts.push(run.finished_at ? formatDateTime(run.started_at) : `${count(run.processed)} of ${count(run.total)}`);
  if (run.failed) parts.push(`${count(run.failed)} failed`);
  return parts.join(" · ");
}
</script>

<template>
  <div class="wrap card">
    <table class="table">
      <thead>
        <tr>
          <th scope="col">Campaign</th>
          <th scope="col">Status</th>
          <th scope="col" class="optional">Next send</th>
          <th scope="col">Last send</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="campaign in campaigns" :key="campaign.id" data-testid="campaign-row">
          <td class="name">
            <a :href="`/campaigns/${campaign.id}`" :title="campaign.brief">{{ campaign.name }}</a>
            <span class="sub small muted" data-testid="campaign-kind">{{ kindOf(campaign) }}</span>
            <span v-if="campaign.tags?.length" class="row-tags">
              <button
                v-for="name in campaign.tags"
                :key="name"
                type="button"
                class="row-tag"
                :class="{ 'row-tag--on': name === tag }"
                :aria-pressed="name === tag"
                data-testid="row-tag"
                @click="emit('tag', name === tag ? '' : name)"
              >
                {{ name }}
              </button>
            </span>
          </td>
          <td><CampaignStatusBadge :status="campaign.status" :schedule="campaign.schedule ?? null" :last-run="campaign.last_run ?? null" /></td>
          <td class="optional" data-testid="campaign-next">
            <template v-if="campaign.schedule?.next_run_at">{{ formatDateTime(campaign.schedule.next_run_at) }}</template>
            <span v-else class="muted">—</span>
          </td>
          <td data-testid="campaign-last">
            <template v-if="campaign.last_run">
              {{ lastResult(campaign.last_run) }}
              <span class="sub small muted">{{ lastDetail(campaign, campaign.last_run) }}</span>
            </template>
            <span v-else class="muted">Not sent yet</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.row-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-1);
}

.row-tag {
  height: 22px;
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: 999px;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.row-tag:hover {
  color: var(--color-text);
}

.row-tag--on {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  border-color: transparent;
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
  vertical-align: top;
  white-space: nowrap;
}

tbody tr:last-child td {
  border-bottom: 0;
}

.name {
  width: 100%;
  max-width: 0;
  min-width: 200px;
}

.name a,
.sub {
  display: block;
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

.sub {
  margin-top: 2px;
}

@media (max-width: 760px) {
  .optional {
    display: none;
  }
}
</style>

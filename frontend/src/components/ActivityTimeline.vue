<script setup lang="ts">
import type { ActivityEntry } from "../api/campaigns";
import { describeActivity } from "../lib/activity";
import { formatDateTime } from "../lib/format";

defineProps<{ entries: ActivityEntry[]; showCampaign?: boolean }>();
</script>

<template>
  <ol class="timeline">
    <li v-for="entry in entries" :key="`${entry.campaign_id}-${entry.occurred_at}-${entry.type}`" class="entry" data-testid="activity-entry">
      <div class="body">
        <strong class="title">{{ describeActivity(entry).title }}</strong>
        <span v-if="describeActivity(entry).detail" class="muted small">{{ describeActivity(entry).detail }}</span>
        <a v-if="showCampaign" class="campaign small" :href="`/campaigns/${entry.campaign_id}`" >{{ entry.campaign_name }}</a>
      </div>
      <time class="muted small" :datetime="entry.occurred_at">{{ formatDateTime(entry.occurred_at) }}</time>
    </li>
  </ol>
</template>

<style scoped>
.timeline {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}

.entry {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-1) var(--space-4);
  padding: var(--space-3) 0;
  border-bottom: 1px solid var(--color-border);
}

.entry:last-child {
  border-bottom: 0;
}

.body {
  display: grid;
  flex: 1;
  gap: 2px;
  min-width: 0;
}

.title {
  font-weight: 500;
}

.campaign {
  overflow: hidden;
  color: var(--color-text-muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, listCampaigns, type CampaignGroup, type CampaignSummary } from "../api/campaigns";
import CampaignTable from "../components/CampaignTable.vue";
import ValidationNotice from "../components/ValidationNotice.vue";

const SEARCH_DELAY_MS = 250;

const filters: { group: CampaignGroup; label: string }[] = [
  { group: "", label: "All" },
  { group: "drafts", label: "Drafts" },
  { group: "active", label: "Active" },
  { group: "completed", label: "Completed" },
  { group: "failed", label: "Failed" },
];

const group = ref<CampaignGroup>("");
const query = ref("");
const channel = ref("");
const createdWithinDays = ref("");
const sort = ref("");
const campaigns = ref<CampaignSummary[] | null>(null);
const error = ref("");
const isFiltered = computed(() => group.value !== "" || query.value.trim() !== "" || channel.value !== "" || createdWithinDays.value !== "");

let latestRequest = 0;
let searchTimer: ReturnType<typeof setTimeout> | undefined;

async function load() {
  const request = ++latestRequest;
  error.value = "";
  try {
    const result = await listCampaigns({ group: group.value, query: query.value, channel: channel.value, createdWithinDays: createdWithinDays.value, sort: sort.value });
    // A slower earlier response must not replace the current filter's results.
    if (request === latestRequest) campaigns.value = result;
  } catch (failure) {
    if (request === latestRequest) error.value = errorMessage(failure);
  }
}

watch([group, channel, createdWithinDays, sort], load);
watch(query, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(load, SEARCH_DELAY_MS);
});
onMounted(load);
onBeforeUnmount(() => clearTimeout(searchTimer));
</script>

<template>
  <div class="campaigns">
    <div class="controls">
      <div class="filters" role="group" aria-label="Filter by status">
        <button
          v-for="filter in filters"
          :key="filter.group"
          type="button"
          class="filter"
          :class="{ 'filter--active': group === filter.group }"
          :aria-pressed="group === filter.group"
          @click="group = filter.group"
        >
          {{ filter.label }}
        </button>
      </div>
      <label class="visually-hidden" for="campaign-search">Search campaigns</label>
      <input id="campaign-search" v-model="query" class="field search" type="search" placeholder="Search names and briefs" />
      <div class="selects">
        <label class="visually-hidden" for="campaign-channel">Channel</label>
        <select id="campaign-channel" v-model="channel" class="field select">
          <option value="">All channels</option>
          <option value="sms">Uses SMS</option>
          <option value="email">Uses email</option>
        </select>
        <label class="visually-hidden" for="campaign-age">Created</label>
        <select id="campaign-age" v-model="createdWithinDays" class="field select">
          <option value="">Any time</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
          <option value="90">Last 90 days</option>
        </select>
        <label class="visually-hidden" for="campaign-sort">Sort by</label>
        <select id="campaign-sort" v-model="sort" class="field select">
          <option value="">Newest first</option>
          <option value="oldest">Oldest first</option>
          <option value="name">Name</option>
        </select>
      </div>
    </div>

    <ValidationNotice v-if="error" tone="error" title="We could not load your campaigns">{{ error }}</ValidationNotice>
    <p v-else-if="!campaigns" class="muted" role="status">Loading campaigns...</p>
    <template v-else>
      <CampaignTable v-if="campaigns.length" :campaigns="campaigns" />
      <p v-else-if="isFiltered" class="muted" data-testid="no-results">No campaigns match these filters.</p>
      <div v-else class="empty card" data-testid="no-campaigns">
        <p class="muted">No campaigns yet.</p>
        <div><a class="button button--primary link-button" href="/campaigns/new">Create your first campaign</a></div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.campaigns {
  display: grid;
  gap: var(--space-4);
}

.controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.filter {
  min-height: 32px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.filter:hover {
  background: var(--color-surface-subtle);
}

.filter--active {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 500;
}

.search {
  width: 260px;
  max-width: 100%;
}

.selects {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  min-width: 0;
}

.select {
  width: auto;
  max-width: 100%;
  height: 40px;
  padding-block: 0;
}

.empty {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-6);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}
</style>

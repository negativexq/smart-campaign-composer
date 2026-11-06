<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, getTags, listCampaigns, type CampaignGroup, type CampaignSummary, type TagUse, type UpcomingSend } from "../api/campaigns";
import CampaignTable from "../components/CampaignTable.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { formatDateTime, shownOf } from "../lib/format";

const SEARCH_DELAY_MS = 250;

// The overview's groups, then narrower ones inside them, shown only when they hold
// something, so the row stays short.
const filters: { group: CampaignGroup; label: string; narrow?: boolean }[] = [
  { group: "", label: "All" },
  { group: "drafts", label: "Drafts" },
  { group: "active", label: "Live" },
  { group: "completed", label: "Completed" },
  { group: "failed", label: "Needs attention" },
  { group: "review", label: "Needs approval", narrow: true },
  { group: "repeating", label: "Repeating", narrow: true },
  { group: "sending", label: "Sending now", narrow: true },
  { group: "paused", label: "Paused", narrow: true },
  { group: "archived", label: "Archived", narrow: true },
];

// In the address, so a link can open a filter and a refresh keeps it.
const address = new URLSearchParams(window.location.search);
const named = address.get("group") as CampaignGroup | null;
const group = ref<CampaignGroup>(filters.some((filter) => filter.group === named) ? (named as CampaignGroup) : "");
const query = ref("");
const channel = ref("");
const sort = ref("");
const tag = ref(address.get("tag") ?? "");
const tags = ref<TagUse[]>([]);
const campaigns = ref<CampaignSummary[] | null>(null);
const total = ref(0);
const groups = ref<Partial<Record<string, number>>>({});
const upcoming = ref<UpcomingSend[]>([]);
const error = ref("");
const isFiltered = computed(() => group.value !== "" || query.value.trim() !== "" || channel.value !== "" || tag.value !== "");
// A tag opened from a link stays choosable even when only archived campaigns carry it.
const tagOptions = computed(() => (tag.value && !tags.value.some((use) => use.tag === tag.value) ? [{ tag: tag.value, campaigns: 0 }, ...tags.value] : tags.value));

let latestRequest = 0;
let searchTimer: ReturnType<typeof setTimeout> | undefined;

const currentFilter = () => ({ group: group.value, query: query.value, channel: channel.value, sort: sort.value, tag: tag.value });
const countOf = (key: CampaignGroup) => groups.value[key === "" ? "all" : key];
const shownFilters = computed(() => filters.filter((filter) => !filter.narrow || filter.group === group.value || (countOf(filter.group) ?? 0) > 0));
const broad = computed(() => shownFilters.value.filter((filter) => !filter.narrow));
const narrow = computed(() => shownFilters.value.filter((filter) => filter.narrow));

async function load() {
  const request = ++latestRequest;
  error.value = "";
  try {
    const result = await listCampaigns(currentFilter());
    // A slower earlier response must not replace the current filter's results.
    if (request !== latestRequest) return;
    campaigns.value = result.campaigns;
    total.value = result.total;
    groups.value = result.groups ?? {};
    upcoming.value = result.upcoming ?? [];
  } catch (failure) {
    if (request === latestRequest) error.value = errorMessage(failure);
  }
}

async function showMore() {
  const request = ++latestRequest;
  const shown = campaigns.value ?? [];
  error.value = "";
  try {
    const result = await listCampaigns(currentFilter(), shown.length);
    if (request !== latestRequest) return;
    // A campaign created meanwhile moves the rest down, so the next page can repeat one.
    const known = new Set(shown.map((campaign) => campaign.id));
    campaigns.value = [...shown, ...result.campaigns.filter((campaign) => !known.has(campaign.id))];
    total.value = result.total;
  } catch (failure) {
    if (request === latestRequest) error.value = errorMessage(failure);
  }
}

function writeAddress() {
  const params = new URLSearchParams();
  if (group.value) params.set("group", group.value);
  if (tag.value) params.set("tag", tag.value);
  window.history.replaceState({}, "", `${window.location.pathname}${params.size ? `?${params}` : ""}`);
}

watch([group, tag], writeAddress);
watch([group, channel, sort, tag], load);
watch(query, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(load, SEARCH_DELAY_MS);
});
onMounted(() => {
  load();
  getTags().then((uses) => (tags.value = uses), () => {});
});
onBeforeUnmount(() => clearTimeout(searchTimer));
</script>

<template>
  <div class="campaigns">
    <nav class="groups" aria-label="Filter by status">
      <button
        v-for="filter in broad"
        :key="filter.group"
        type="button"
        class="group"
        :class="{ 'group--active': group === filter.group }"
        :aria-pressed="group === filter.group"
        :data-testid="`group-${filter.group || 'all'}`"
        @click="group = filter.group"
      >
        {{ filter.label }}<span v-if="countOf(filter.group) !== undefined" class="count-pill">{{ countOf(filter.group) }}</span>
      </button>
      <span v-if="narrow.length" class="divider" aria-hidden="true"></span>
      <button
        v-for="filter in narrow"
        :key="filter.group"
        type="button"
        class="group group--narrow"
        :class="{ 'group--active': group === filter.group }"
        :aria-pressed="group === filter.group"
        :data-testid="`group-${filter.group}`"
        @click="group = filter.group"
      >
        {{ filter.label }}<span v-if="countOf(filter.group) !== undefined" class="count-pill">{{ countOf(filter.group) }}</span>
      </button>
    </nav>

    <div class="controls">
      <label class="visually-hidden" for="campaign-search">Search campaigns</label>
      <input id="campaign-search" v-model="query" class="field search" type="search" placeholder="Search names, briefs and tags" />
      <label class="visually-hidden" for="campaign-channel">Channel</label>
      <select id="campaign-channel" v-model="channel" class="field select">
        <option value="">All channels</option>
        <option value="sms">Uses SMS</option>
        <option value="email">Uses email</option>
      </select>
      <template v-if="tagOptions.length">
        <label class="visually-hidden" for="campaign-tag">Tag</label>
        <select id="campaign-tag" v-model="tag" class="field select" data-testid="tag-filter">
          <option value="">All tags</option>
          <option v-for="use in tagOptions" :key="use.tag" :value="use.tag">{{ use.tag }}</option>
        </select>
      </template>
      <label class="visually-hidden" for="campaign-sort">Sort by</label>
      <select id="campaign-sort" v-model="sort" class="field select">
        <option value="">Next send</option>
        <option value="last_sent">Last sent</option>
        <option value="newest">Newest first</option>
        <option value="oldest">Oldest first</option>
        <option value="name">Name</option>
      </select>
    </div>

    <div v-if="upcoming.length" class="upcoming small" data-testid="upcoming">
      <span class="muted">Coming up</span>
      <a v-for="send in upcoming" :key="send.campaign_id" :href="`/campaigns/${send.campaign_id}`">{{ formatDateTime(send.at) }} · {{ send.name }}</a>
    </div>

    <ValidationNotice v-if="error" tone="error" title="We could not load your campaigns">{{ error }}</ValidationNotice>
    <p v-else-if="!campaigns" class="muted" role="status">Loading campaigns...</p>
    <template v-else>
      <template v-if="campaigns.length">
        <p class="small muted count" data-testid="campaign-count">{{ shownOf(campaigns.length, total, "campaign", "campaigns") }}</p>
        <CampaignTable :campaigns="campaigns" :tag="tag" @tag="tag = $event" />
        <div v-if="campaigns.length < total">
          <button class="button button--secondary" type="button" data-testid="more-campaigns" @click="showMore">Show more</button>
        </div>
      </template>
      <p v-else-if="group === 'archived' && !query.trim() && !channel && !tag" class="muted" data-testid="no-archived">No archived campaigns. Archive one from its page to take it out of this list and keep its report.</p>
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
  min-width: 0;
}

.groups {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.group {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 32px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.group:hover {
  background: var(--color-surface-subtle);
}

.group--narrow {
  font-size: 13px;
}

.group--active {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 500;
}

.count-pill {
  color: var(--color-text-muted);
  font-size: 12px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}

.group--active .count-pill {
  color: var(--color-primary);
}

.divider {
  width: 1px;
  height: 20px;
  margin: 0 var(--space-2);
  background: var(--color-border);
}

.controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.search {
  flex: 1;
  min-width: 200px;
}

.select {
  width: auto;
  max-width: 100%;
  height: 40px;
  padding-block: 0;
}

.upcoming {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
  padding: var(--space-2) var(--space-3);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.upcoming a {
  color: var(--color-text);
  text-decoration: none;
}

.upcoming a:hover {
  color: var(--color-primary);
  text-decoration: underline;
}

.count {
  margin: 0;
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

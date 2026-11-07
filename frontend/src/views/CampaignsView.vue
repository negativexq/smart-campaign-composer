<script setup lang="ts">
import { CalendarClock, CalendarDays, List, Repeat } from "lucide-vue-next";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, getTags, listCampaigns, type CampaignGroup, type CampaignSummary, type TagUse, type UpcomingSend } from "../api/campaigns";
import CampaignCalendar from "../components/CampaignCalendar.vue";
import CampaignTable from "../components/CampaignTable.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { CALENDAR_VIEWS, dayKey, parseAnchor, type CalendarView } from "../lib/calendar";
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
// The calendar shows the same campaigns placed in time, so the filters carry over.
const mode = ref<"list" | "calendar">(address.get("view") === "calendar" ? "calendar" : "list");
const namedView = address.get("calendar") as CalendarView | null;
const calendarView = ref<CalendarView>(CALENDAR_VIEWS.some((option) => option.view === namedView) ? (namedView as CalendarView) : "month");
const anchor = ref(parseAnchor(address.get("date")));
// The calendar asks for its own range, after typing pauses as the list does.
const calendarQuery = ref("");
const calendarFilter = computed(() => ({ group: group.value, query: calendarQuery.value, channel: channel.value, tag: tag.value }));
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
  if (mode.value === "calendar") {
    params.set("view", "calendar");
    params.set("calendar", calendarView.value);
    params.set("date", dayKey(anchor.value));
  }
  window.history.replaceState({}, "", `${window.location.pathname}${params.size ? `?${params}` : ""}`);
}

watch([group, tag, mode, calendarView, anchor], writeAddress);
watch([group, channel, sort, tag], load);
watch(query, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    calendarQuery.value = query.value;
    load();
  }, SEARCH_DELAY_MS);
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
      <template v-if="mode === 'list'">
        <label class="visually-hidden" for="campaign-sort">Sort by</label>
        <select id="campaign-sort" v-model="sort" class="field select">
          <option value="">Next send</option>
          <option value="last_sent">Last sent</option>
          <option value="newest">Newest first</option>
          <option value="oldest">Oldest first</option>
          <option value="name">Name</option>
        </select>
      </template>
      <div class="modes" role="group" aria-label="Show campaigns as">
        <button type="button" class="mode" :class="{ 'mode--active': mode === 'list' }" :aria-pressed="mode === 'list'" data-testid="mode-list" @click="mode = 'list'">
          <List :size="16" aria-hidden="true" />List
        </button>
        <button type="button" class="mode" :class="{ 'mode--active': mode === 'calendar' }" :aria-pressed="mode === 'calendar'" data-testid="mode-calendar" @click="mode = 'calendar'">
          <CalendarDays :size="16" aria-hidden="true" />Calendar
        </button>
      </div>
    </div>

    <CampaignCalendar v-if="mode === 'calendar'" v-model:view="calendarView" v-model:anchor="anchor" :filter="calendarFilter" />

    <template v-else>
    <section v-if="upcoming.length" class="upcoming small" aria-labelledby="upcoming-label" data-testid="upcoming">
      <span id="upcoming-label" class="upcoming-label"><CalendarClock :size="16" aria-hidden="true" />Next sends</span>
      <a v-for="send in upcoming" :key="send.campaign_id" class="upcoming-send" :href="`/campaigns/${send.campaign_id}`">
        <time class="upcoming-at" :datetime="send.at">{{ formatDateTime(send.at) }}</time>
        <span class="upcoming-name">{{ send.name }}</span>
        <Repeat v-if="send.repeats" :size="13" class="upcoming-repeat" aria-label="Repeats" />
      </a>
    </section>

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

.modes {
  display: inline-flex;
  margin-left: auto;
  padding: 2px;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.mode {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 36px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: 6px;
  font: inherit;
  cursor: pointer;
}

.mode--active {
  color: var(--color-text);
  background: var(--color-surface);
  box-shadow: 0 0 0 1px var(--color-border);
  font-weight: 500;
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
  align-items: center;
  gap: var(--space-2) var(--space-4);
  padding: var(--space-2) var(--space-3);
  background: var(--color-primary-soft);
  border-left: 3px solid var(--color-primary);
  border-radius: var(--radius-sm);
}

.upcoming-label {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-primary);
  font-weight: 600;
}

.upcoming-send {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  color: var(--color-text);
  text-decoration: none;
}

.upcoming-at {
  color: var(--color-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.upcoming-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.upcoming-repeat {
  flex: none;
  color: var(--color-text-muted);
}

.upcoming-send:hover .upcoming-name {
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

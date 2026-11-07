<script setup lang="ts">
import { AlertTriangle, CheckCircle2, ChevronLeft, ChevronRight, Clock, PencilLine, Send } from "lucide-vue-next";
import { computed, ref, watch } from "vue";

import { errorMessage, getCalendar, type CalendarEntry, type CampaignCalendar, type CampaignGroup, type CampaignSummary } from "../api/campaigns";
import { CALENDAR_VIEWS, ENTRY_LABELS, dayKey, daysOf, entriesByDay, formatTime, hasFailures, rangeOf, shift, startOfDay, titleOf, type CalendarView } from "../lib/calendar";
import CampaignPanel from "./CampaignPanel.vue";
import ChannelIcons from "./ChannelIcons.vue";
import ValidationNotice from "./ValidationNotice.vue";

const props = defineProps<{
  filter: { group: CampaignGroup; query: string; channel: string; tag: string };
  view: CalendarView;
  anchor: Date;
}>();
const emit = defineEmits<{
  "update:view": [CalendarView];
  "update:anchor": [Date];
}>();

// A month cell shows this many entries and counts the rest.
const MONTH_CELL_ENTRIES = 3;

const calendar = ref<CampaignCalendar | null>(null);
const error = ref("");
const selected = ref<CalendarEntry | null>(null);
let latestRequest = 0;

const days = computed(() => daysOf(props.view, props.anchor));
const byDay = computed(() => entriesByDay(calendar.value?.entries ?? []));
const campaigns = computed(() => new Map((calendar.value?.campaigns ?? []).map((campaign) => [campaign.id, campaign])));
const title = computed(() => titleOf(props.view, props.anchor));
const todayKey = dayKey(new Date());
const weekdays = computed(() => daysOf("week", props.anchor).map((day) => day.toLocaleDateString("en-GB", { weekday: "short" })));
const selectedCampaign = computed<CampaignSummary | null>(() => (selected.value ? (campaigns.value.get(selected.value.campaign_id) ?? null) : null));

async function load() {
  const request = ++latestRequest;
  error.value = "";
  const { from, to } = rangeOf(props.view, props.anchor);
  try {
    const result = await getCalendar(props.filter, from, to);
    if (request !== latestRequest) return;
    calendar.value = result;
  } catch (failure) {
    if (request === latestRequest) error.value = errorMessage(failure);
  }
}

watch(() => [props.view, props.anchor.getTime(), props.filter.group, props.filter.query, props.filter.channel, props.filter.tag], load, { immediate: true });

const entriesOn = (day: Date) => byDay.value.get(dayKey(day)) ?? [];
const nameOf = (entry: CalendarEntry) => campaigns.value.get(entry.campaign_id)?.name ?? "Campaign";
const channelsOf = (entry: CalendarEntry) => campaigns.value.get(entry.campaign_id)?.channels ?? [];
const inMonth = (day: Date) => props.view !== "month" || day.getMonth() === props.anchor.getMonth();

function stateOf(entry: CalendarEntry) {
  if (entry.kind === "sent" && hasFailures(entry)) return "Completed with failures";
  return ENTRY_LABELS[entry.kind];
}

function describe(entry: CalendarEntry) {
  return `${stateOf(entry)}, ${formatTime(entry.at)}, ${nameOf(entry)}`;
}

function openDay(day: Date) {
  emit("update:anchor", day);
  emit("update:view", "day");
}

function dayHeading(day: Date) {
  return day.toLocaleDateString("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "short",
  });
}
</script>

<template>
  <section class="calendar" aria-label="Campaign calendar" data-testid="calendar">
    <div class="toolbar">
      <div class="nav">
        <button type="button" class="button button--secondary" data-testid="calendar-today" @click="emit('update:anchor', startOfDay(new Date()))">Today</button>
        <button type="button" class="icon-button" aria-label="Previous" data-testid="calendar-previous" @click="emit('update:anchor', shift(view, anchor, -1))">
          <ChevronLeft :size="18" aria-hidden="true" />
        </button>
        <button type="button" class="icon-button" aria-label="Next" data-testid="calendar-next" @click="emit('update:anchor', shift(view, anchor, 1))">
          <ChevronRight :size="18" aria-hidden="true" />
        </button>
        <h2 class="title" aria-live="polite" data-testid="calendar-title">
          {{ title }}
        </h2>
      </div>
      <div class="segmented" role="group" aria-label="Calendar view">
        <button
          v-for="option in CALENDAR_VIEWS"
          :key="option.view"
          type="button"
          class="segment"
          :class="{ 'segment--active': view === option.view }"
          :aria-pressed="view === option.view"
          :data-testid="`calendar-view-${option.view}`"
          @click="emit('update:view', option.view)"
        >
          {{ option.label }}
        </button>
      </div>
    </div>

    <ul class="legend small" aria-label="What the calendar shows">
      <li class="legend-item entry--draft"><PencilLine :size="14" aria-hidden="true" />Draft, on the day it last changed</li>
      <li class="legend-item entry--scheduled"><Clock :size="14" aria-hidden="true" />Scheduled</li>
      <li class="legend-item entry--sending"><Send :size="14" aria-hidden="true" />Sending</li>
      <li class="legend-item entry--sent"><CheckCircle2 :size="14" aria-hidden="true" />Completed</li>
      <li class="legend-item entry--failed"><AlertTriangle :size="14" aria-hidden="true" />Completed with failures</li>
    </ul>

    <ValidationNotice v-if="error" tone="error" title="We could not load the calendar">{{ error }}</ValidationNotice>
    <p v-if="calendar?.truncated" class="small muted" data-testid="calendar-truncated">
      Some sends are left out because this range holds too many. Narrow the filters or pick a shorter view.
    </p>

    <div v-if="view === 'month'" class="month card" :class="{ 'is-loading': !calendar }">
      <div v-for="name in weekdays" :key="name" class="weekday small muted" aria-hidden="true">
        {{ name }}
      </div>
      <div
        v-for="day in days"
        :key="dayKey(day)"
        class="cell"
        :class="{
          'cell--outside': !inMonth(day),
          'cell--today': dayKey(day) === todayKey,
        }"
        data-testid="calendar-day"
      >
        <button type="button" class="day-number" :aria-label="`Show ${dayHeading(day)}`" @click="openDay(day)">
          {{ day.getDate() }}
        </button>
        <ul class="entries">
          <li v-for="entry in entriesOn(day).slice(0, MONTH_CELL_ENTRIES)" :key="`${entry.campaign_id}-${entry.kind}-${entry.at}`">
            <button
              type="button"
              class="entry"
              :class="[
                `entry--${entry.kind}`,
                {
                  'entry--failed': hasFailures(entry),
                  'entry--selected': selected === entry,
                },
              ]"
              :aria-label="describe(entry)"
              data-testid="calendar-entry"
              @click="selected = entry"
            >
              <AlertTriangle v-if="hasFailures(entry)" :size="13" class="state" aria-hidden="true" />
              <CheckCircle2 v-else-if="entry.kind === 'sent'" :size="13" class="state" aria-hidden="true" />
              <Send v-else-if="entry.kind === 'sending'" :size="13" class="state" aria-hidden="true" />
              <Clock v-else-if="entry.kind === 'scheduled'" :size="13" class="state" aria-hidden="true" />
              <PencilLine v-else :size="13" class="state" aria-hidden="true" />
              <span class="time">{{ formatTime(entry.at) }}</span>
              <span class="name">{{ nameOf(entry) }}</span>
            </button>
          </li>
        </ul>
        <button v-if="entriesOn(day).length > MONTH_CELL_ENTRIES" type="button" class="more small" data-testid="calendar-more" @click="openDay(day)">
          {{ entriesOn(day).length - MONTH_CELL_ENTRIES }} more
        </button>
      </div>
    </div>

    <div v-else class="columns card" :class="[`columns--${view}`, { 'is-loading': !calendar }]">
      <div v-for="day in days" :key="dayKey(day)" class="column" :class="{ 'column--today': dayKey(day) === todayKey }" data-testid="calendar-day">
        <h3 v-if="view === 'week'" class="column-heading small">
          <button type="button" class="column-day" @click="openDay(day)">
            {{ dayHeading(day) }}
          </button>
        </h3>
        <ul v-if="entriesOn(day).length" class="entries">
          <li v-for="entry in entriesOn(day)" :key="`${entry.campaign_id}-${entry.kind}-${entry.at}`">
            <button
              type="button"
              class="entry entry--tall"
              :class="[
                `entry--${entry.kind}`,
                {
                  'entry--failed': hasFailures(entry),
                  'entry--selected': selected === entry,
                },
              ]"
              :aria-label="describe(entry)"
              data-testid="calendar-entry"
              @click="selected = entry"
            >
              <span class="entry-top">
                <AlertTriangle v-if="hasFailures(entry)" :size="14" class="state" aria-hidden="true" />
                <CheckCircle2 v-else-if="entry.kind === 'sent'" :size="14" class="state" aria-hidden="true" />
                <Send v-else-if="entry.kind === 'sending'" :size="14" class="state" aria-hidden="true" />
                <Clock v-else-if="entry.kind === 'scheduled'" :size="14" class="state" aria-hidden="true" />
                <PencilLine v-else :size="14" class="state" aria-hidden="true" />
                <span class="time">{{ formatTime(entry.at) }}</span>
                <span class="kind small">{{ stateOf(entry) }}</span>
              </span>
              <span class="name">{{ nameOf(entry) }}</span>
              <ChannelIcons :channels="channelsOf(entry)" :labels="view === 'day'" />
            </button>
          </li>
        </ul>
        <p v-else-if="view === 'day' && calendar" class="muted empty" data-testid="calendar-empty">Nothing is planned or sent on this day.</p>
      </div>
    </div>

    <CampaignPanel v-if="selected && selectedCampaign" :entry="selected" :campaign="selectedCampaign" @close="selected = null" />
  </section>
</template>

<style scoped>
.calendar {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.nav {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.title {
  margin: 0 0 0 var(--space-2);
  font-size: 16px;
  font-weight: 600;
  white-space: nowrap;
}

.icon-button {
  display: inline-grid;
  place-items: center;
  width: 40px;
  height: 40px;
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.icon-button:hover {
  background: var(--color-surface-subtle);
}

.segmented {
  display: inline-flex;
  padding: 2px;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.segment {
  min-height: 36px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: 6px;
  font: inherit;
  cursor: pointer;
}

.segment--active {
  color: var(--color-text);
  background: var(--color-surface);
  box-shadow: 0 0 0 1px var(--color-border);
  font-weight: 500;
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-text-muted);
}

.legend-item svg {
  color: var(--entry-color);
}

/* Each kind of entry has a color, an icon and a word; the color is never alone. */
.entry--draft {
  --entry-color: var(--color-text-muted);
}

.entry--scheduled,
.entry--sending {
  --entry-color: var(--color-primary);
}

.entry--sent {
  --entry-color: var(--color-success);
}

.entry--failed {
  --entry-color: var(--color-warning);
}

.month {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  overflow: hidden;
}

.weekday {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--color-border);
  font-weight: 500;
}

.cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  min-height: 116px;
  padding: var(--space-1);
  overflow: hidden;
  border-right: 1px solid var(--color-border);
  border-bottom: 1px solid var(--color-border);
}

.cell:nth-child(7n) {
  border-right: 0;
}

.cell:nth-last-child(-n + 7) {
  border-bottom: 0;
}

.cell--outside {
  background: var(--color-canvas);
}

.cell--outside .day-number {
  color: var(--color-text-muted);
}

.day-number {
  align-self: flex-start;
  min-width: 26px;
  height: 26px;
  padding: 0 var(--space-1);
  color: var(--color-text);
  background: transparent;
  border: 0;
  border-radius: 999px;
  font: inherit;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
}

.day-number:hover {
  background: var(--color-surface-subtle);
}

.cell--today .day-number {
  color: var(--color-on-primary);
  background: var(--color-primary);
  font-weight: 600;
}

.entries > li {
  min-width: 0;
}

.entries {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
  min-width: 0;
}

.entry {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  width: 100%;
  min-width: 0;
  min-height: 24px;
  padding: 2px var(--space-1);
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--entry-color);
  border-radius: 6px;
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
  transition: background var(--transition-fast);
}

.entry.entry--draft {
  border-left-style: dashed;
}

.entry:hover {
  background: var(--color-surface-subtle);
}

.entry--selected {
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
  border-left-color: var(--entry-color);
}

.state {
  flex: none;
  color: var(--entry-color);
}

.time {
  flex: none;
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}

.name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.more {
  align-self: flex-start;
  padding: 0 var(--space-1);
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.more:hover {
  text-decoration: underline;
}

.columns {
  display: grid;
  overflow: hidden;
}

.columns--week {
  grid-template-columns: repeat(7, minmax(0, 1fr));
}

.column {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  min-height: 360px;
  padding: var(--space-2);
  border-right: 1px solid var(--color-border);
}

.column:last-child {
  border-right: 0;
}

.columns--day .column {
  min-height: 240px;
  padding: var(--space-3);
}

.column-heading {
  margin: 0;
  font-weight: 500;
}

.column-day {
  padding: 2px var(--space-1);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.column-day:hover {
  color: var(--color-text);
  background: var(--color-surface-subtle);
}

.column--today .column-day {
  color: var(--color-primary);
  font-weight: 600;
}

.entry--tall {
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: var(--space-2);
  font-size: 13px;
}

.entry-top {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  max-width: 100%;
}

.kind {
  overflow: hidden;
  color: var(--color-text-muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.columns--week .kind {
  display: none;
}

.entry--tall .name {
  max-width: 100%;
  font-weight: 500;
}

.columns--day .entries {
  gap: var(--space-2);
}

.empty {
  margin: 0;
}

.is-loading {
  opacity: 0.6;
}

@media (max-width: 900px) {
  /* Seven narrow columns cannot hold names, so a week becomes a list of days. */
  .columns--week {
    grid-template-columns: 1fr;
  }

  .columns--week .column {
    min-height: 0;
    border-right: 0;
    border-bottom: 1px solid var(--color-border);
  }

  .columns--week .column:last-child {
    border-bottom: 0;
  }

  .cell {
    min-height: 72px;
  }

  .cell .time,
  .cell .name {
    display: none;
  }

  .cell .entries {
    display: flex;
    flex-wrap: wrap;
  }

  .cell .entry {
    width: auto;
    padding: 2px;
    border-left-width: 1px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .entry {
    transition: none;
  }
}
</style>

<script setup lang="ts">
import { computed, useId } from "vue";

import type { ScheduleReach } from "../api/campaigns";
import { WEEKDAYS, suggestedRestDays, type ScheduleForm, type ScheduleMode } from "../lib/schedule";

// sentBefore: the campaign already sent, so even a one-time send chooses whom it
// reaches again. scheduleOnly leaves out sending now.
const props = defineProps<{ modelValue: ScheduleForm; sentBefore?: boolean; scheduleOnly?: boolean }>();
const emit = defineEmits<{ "update:modelValue": [value: ScheduleForm] }>();

const id = useId();
const repeats = computed(() => props.modelValue.mode === "interval" || props.modelValue.mode === "weekly");
const choosesReach = computed(() => repeats.value || (props.sentBefore && props.modelValue.mode === "once"));
const allOptions: { mode: ScheduleMode; label: string; hint: string }[] = [
  { mode: "now", label: "Send now", hint: "Starts as soon as you approve." },
  { mode: "once", label: "At a set time", hint: "Sends once, on the date you choose." },
  { mode: "interval", label: "At regular intervals", hint: "Sends again every few hours or days." },
  { mode: "weekly", label: "On chosen days", hint: "Sends on the same days each week." },
];

const options = computed(() => allOptions.filter((option) => !props.scheduleOnly || option.mode !== "now"));

const reaches = computed<{ reach: ScheduleReach; label: string }[]>(() => [
  { reach: "new", label: "Only people who haven't received it yet" },
  { reach: "after", label: "People who haven't received it in the last" },
  { reach: "everyone", label: repeats.value ? "Everyone who matches, every time" : "Everyone, including people who received it before" },
]);

// Choosing a wait starts from one interval, which the person can then change.
function chooseReach(reach: ScheduleReach) {
  const next = { ...props.modelValue, reach };
  if (reach === "after" && props.modelValue.reach !== "after") next.reachAfterDays = suggestedRestDays(props.modelValue);
  emit("update:modelValue", next);
}

const reachNote = computed(() => {
  if (!repeats.value) {
    switch (props.modelValue.reach) {
      case "after":
        return `Someone who received this campaign gets it again only if ${props.modelValue.reachAfterDays === 1 ? "a day has" : `${props.modelValue.reachAfterDays} days have`} passed since their last one.`;
      case "everyone":
        return "Everyone this send finds gets it, including people earlier sends reached. Only someone whose message is still being sent is left out.";
      default:
        return "Nobody who already received this campaign gets it again, so this send may reach few people or none.";
    }
  }
  switch (props.modelValue.reach) {
    case "after":
      return `Each send finds who matches at that time. Someone reached before gets it again once ${props.modelValue.reachAfterDays === 1 ? "a day has" : `${props.modelValue.reachAfterDays} days have`} passed since their last one.`;
    case "everyone":
      return "Each send finds who matches at that time and messages all of them, including people earlier sends reached. Only someone whose message is still being sent is left out.";
    default:
      return "Each send finds who matches at that time, so the number can change. Nobody gets this message twice: only people who have not received it yet are reached.";
  }
});

const timezones = computed(() => {
  const supported = Intl.supportedValuesOf?.("timeZone") ?? [];
  return supported.includes(props.modelValue.timezone) ? supported : [props.modelValue.timezone, ...supported];
});

function change<K extends keyof ScheduleForm>(key: K, value: ScheduleForm[K]) {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
}

function toggleDay(day: string) {
  const days = props.modelValue.days;
  change("days", days.includes(day) ? days.filter((other) => other !== day) : [...days, day]);
}

function inputValue(event: Event) {
  return (event.target as HTMLInputElement).value;
}
</script>

<template>
  <fieldset class="picker">
    <legend class="group-title">When to send</legend>

    <div class="options">
      <label v-for="option in options" :key="option.mode" class="option" :class="{ 'option--active': modelValue.mode === option.mode }">
        <input type="radio" :name="id" :value="option.mode" :checked="modelValue.mode === option.mode" @change="change('mode', option.mode)" />
        <span>
          <span class="option-label">{{ option.label }}</span>
          <span class="muted small hint">{{ option.hint }}</span>
        </span>
      </label>
    </div>

    <!-- Above the details, so it stays put when switching between the repeating modes. -->
    <fieldset v-if="choosesReach" class="reach" data-testid="schedule-reach">
      <legend class="small muted">{{ repeats ? "Later sends reach" : "This send reaches" }}</legend>
      <label v-for="option in reaches" :key="option.reach" class="reach-option">
        <input
          type="radio"
          :name="`${id}-reach`"
          :value="option.reach"
          :checked="modelValue.reach === option.reach"
          :data-testid="`reach-${option.reach}`"
          @change="chooseReach(option.reach)"
        />
        <span>{{ option.label }}</span>
        <template v-if="option.reach === 'after'">
          <input
            class="field rest"
            type="number"
            min="1"
            max="365"
            step="1"
            aria-label="Days to wait before reaching someone again"
            data-testid="reach-after-days"
            :disabled="modelValue.reach !== 'after'"
            :value="modelValue.reachAfterDays"
            @input="change('reachAfterDays', Number(inputValue($event)))"
          />
          <span>days</span>
        </template>
      </label>
      <p v-if="modelValue.reach === 'everyone'" class="small warning" data-testid="reach-everyone-warning">
        {{ repeats ? "People who match again get this message on every send." : "People who already received this message get it again." }} Make sure that is what they would want.
      </p>
    </fieldset>

    <p v-if="choosesReach" class="muted small" data-testid="once-per-person">{{ reachNote }}</p>

    <div v-if="modelValue.mode === 'once'" class="details">
      <label class="control">
        <span class="small muted">Date and time</span>
        <input class="field" type="datetime-local" data-testid="schedule-at" :value="modelValue.at" @input="change('at', inputValue($event))" />
      </label>
    </div>

    <div v-else-if="modelValue.mode === 'interval'" class="details">
      <div class="control">
        <span class="small muted">Send every</span>
        <div class="inline">
          <input
            class="field every"
            type="number"
            min="1"
            step="1"
            aria-label="Interval"
            data-testid="schedule-every"
            :value="modelValue.every"
            @input="change('every', Number(inputValue($event)))"
          />
          <select class="field select" aria-label="Interval unit" :value="modelValue.unit" @change="change('unit', inputValue($event) as ScheduleForm['unit'])">
            <option value="hours">hours</option>
            <option value="days">days</option>
          </select>
        </div>
      </div>
      <label class="control">
        <span class="small muted">First send (optional)</span>
        <input class="field" type="datetime-local" :value="modelValue.startsAt" @input="change('startsAt', inputValue($event))" />
        <span class="muted small">If left empty, the first send is one interval from now.</span>
      </label>
    </div>

    <div v-else-if="modelValue.mode === 'weekly'" class="details">
      <div class="control">
        <span class="small muted">Days</span>
        <div class="days" role="group" aria-label="Days of the week">
          <button
            v-for="day in WEEKDAYS"
            :key="day.id"
            type="button"
            class="day"
            :class="{ 'day--active': modelValue.days.includes(day.id) }"
            :aria-pressed="modelValue.days.includes(day.id)"
            :data-testid="`day-${day.id}`"
            @click="toggleDay(day.id)"
          >
            {{ day.label }}
          </button>
        </div>
      </div>
      <div class="inline">
        <label class="control">
          <span class="small muted">Time</span>
          <input class="field" type="time" data-testid="schedule-time" :value="modelValue.time" @input="change('time', inputValue($event))" />
        </label>
        <label class="control zone">
          <span class="small muted">Time zone</span>
          <select class="field select" :value="modelValue.timezone" @change="change('timezone', inputValue($event))">
            <option v-for="zone in timezones" :key="zone" :value="zone">{{ zone }}</option>
          </select>
        </label>
      </div>
    </div>

    <label v-if="repeats" class="control details-end">
      <span class="small muted">Stop after (optional)</span>
      <input class="field" type="datetime-local" :value="modelValue.endsAt" @input="change('endsAt', inputValue($event))" />
    </label>
    <p v-if="!repeats && !sentBefore" class="muted small" data-testid="approved-people">Goes to the people you approve now. Anyone who withdraws consent before the send is left out.</p>
  </fieldset>
</template>

<style scoped>
.picker {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.group-title {
  margin-bottom: var(--space-2);
  padding: 0;
  font-weight: 600;
}

.options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: var(--space-2);
}

.option {
  display: flex;
  gap: var(--space-2);
  align-items: flex-start;
  padding: var(--space-3);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  cursor: pointer;
}

.option--active {
  border-color: var(--color-primary);
}

.option input {
  margin-top: 3px;
}

.option-label,
.hint {
  display: block;
}

.option-label {
  font-weight: 500;
}

.details {
  display: grid;
  gap: var(--space-3);
}

.control {
  display: grid;
  gap: var(--space-1);
  min-width: 0;
}

.inline {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.every {
  width: 96px;
}

.select {
  height: 40px;
  padding-block: 0;
}

.zone {
  flex: 1;
  min-width: 200px;
}

.days {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.day {
  min-width: 48px;
  min-height: 40px;
  padding: 0 var(--space-2);
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.reach {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.reach legend {
  margin-bottom: var(--space-1);
  padding: 0;
}

.reach-option {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
  cursor: pointer;
}

.rest {
  width: 80px;
}

.warning {
  margin: 0;
  color: var(--color-warning);
}

.day--active {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-surface-subtle);
  font-weight: 500;
}
</style>

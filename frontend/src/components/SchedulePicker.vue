<script setup lang="ts">
import { computed, useId } from "vue";

import { WEEKDAYS, type ScheduleForm, type ScheduleMode } from "../lib/schedule";

const props = defineProps<{ modelValue: ScheduleForm }>();
const emit = defineEmits<{ "update:modelValue": [value: ScheduleForm] }>();

const id = useId();
const options: { mode: ScheduleMode; label: string; hint: string }[] = [
  { mode: "now", label: "Send now", hint: "Starts as soon as you approve." },
  { mode: "once", label: "At a set time", hint: "Sends once, on the date you choose." },
  { mode: "interval", label: "At regular intervals", hint: "Sends again every few hours or days." },
  { mode: "weekly", label: "On chosen days", hint: "Sends on the same days each week." },
];

const timezones = computed(() => {
  const supported = Intl.supportedValuesOf?.("timeZone") ?? [];
  return supported.includes(props.modelValue.timezone) ? supported : [props.modelValue.timezone, ...supported];
});
const repeats = computed(() => props.modelValue.mode === "interval" || props.modelValue.mode === "weekly");

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

    <p v-if="repeats" class="muted small" data-testid="once-per-person">
      Nobody gets this message twice. Each send counts the audience again and reaches only people who have not received it yet.
    </p>
    <p v-else-if="modelValue.mode === 'once'" class="muted small">The audience is counted again at that time, so the number can differ from the one above.</p>
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
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
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

.day--active {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-surface-subtle);
  font-weight: 500;
}
</style>

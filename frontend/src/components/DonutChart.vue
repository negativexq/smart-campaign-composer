<script setup lang="ts">
import { computed, ref, type Component } from "vue";

export type DonutSlice = { key: string; label: string; count: number; color: string; icon?: Component; selectable?: boolean };

const props = defineProps<{ title: string; slices: DonutSlice[]; selected?: string; testPrefix: string; unit: [string, string] }>();
const emit = defineEmits<{ select: [key: string] }>();

const hovered = ref("");
const count = (value: number) => value.toLocaleString("en-GB");
const total = computed(() => props.slices.reduce((sum, slice) => sum + slice.count, 0));
// Never rounds a few away to nothing, or the last few up to everything.
function percent(value: number) {
  if (!total.value) return "";
  const rounded = Math.round((value / total.value) * 100);
  if (value && rounded === 0) return "<1%";
  if (value !== total.value && rounded === 100) return ">99%";
  return `${rounded}%`;
}

// Lengths are out of 100, so every slice animates between the same units, including
// to and from nothing. A slice keeps its place when its share changes.
const GAP = 1;
// A tiny share still has to be seen and pointed at, so it is drawn at least this long and
// the larger slices give up the difference. The legend always has the real numbers.
const SMALLEST_SHARE = 3.5;
const arcs = computed(() => {
  const showing = props.slices.filter((slice) => slice.count).length;
  const real = (count: number) => (total.value ? (count / total.value) * 100 : 0);
  const isSmall = (count: number) => showing > 1 && count > 0 && real(count) < SMALLEST_SHARE;
  const small = props.slices.filter((slice) => isSmall(slice.count));
  const smallReal = small.reduce((sum, slice) => sum + real(slice.count), 0);
  const scale = small.length ? (100 - small.length * SMALLEST_SHARE) / (100 - smallReal) : 1;
  let start = 0;
  return props.slices.map((slice) => {
    const share = isSmall(slice.count) ? SMALLEST_SHARE : real(slice.count) * scale;
    const arc = { ...slice, length: Math.max(share - (showing > 1 ? GAP : 0), 0), start };
    start += share;
    return arc;
  });
});

const readout = computed(() => props.slices.find((slice) => slice.key === hovered.value));
const canSelect = (slice: DonutSlice) => slice.selectable !== false;
const dimmed = (key: string) => Boolean(props.selected) && props.selected !== key;
</script>

<template>
  <figure class="donut card" :data-testid="`${testPrefix}-chart`">
    <figcaption class="title">{{ title }}</figcaption>
    <div class="plot">
      <div class="ring">
        <svg viewBox="0 0 120 120" aria-hidden="true">
          <circle class="base" cx="60" cy="60" r="48" />
          <circle
            v-for="arc in arcs"
            :key="arc.key"
            class="arc"
            :class="{ 'arc--dim': dimmed(arc.key), 'arc--button': canSelect(arc) }"
            cx="60"
            cy="60"
            r="48"
            pathLength="100"
            :stroke="arc.color"
            :stroke-dasharray="`${arc.length} ${100 - arc.length}`"
            :stroke-dashoffset="-arc.start"
            :data-testid="`${testPrefix}-arc-${arc.key}`"
            @mouseenter="hovered = arc.key"
            @mouseleave="hovered = ''"
            @click="canSelect(arc) && emit('select', arc.key)"
          />
        </svg>
        <div class="center">
          <span class="value mono">{{ count(readout ? readout.count : total) }}</span>
          <span class="small muted caption">{{ readout ? readout.label : total === 1 ? unit[0] : unit[1] }}</span>
        </div>
      </div>
      <ul class="legend">
        <li v-for="slice in slices" :key="slice.key">
          <button
            type="button"
            class="item small"
            :class="{ 'item--on': selected === slice.key, 'item--dim': dimmed(slice.key) }"
            :disabled="!canSelect(slice)"
            :aria-pressed="canSelect(slice) ? selected === slice.key : undefined"
            :data-testid="`${testPrefix}-${slice.key}`"
            @mouseenter="hovered = slice.key"
            @mouseleave="hovered = ''"
            @focus="hovered = slice.key"
            @blur="hovered = ''"
            @click="emit('select', slice.key)"
          >
            <span class="swatch" :style="{ background: slice.color }" aria-hidden="true"></span>
            <component :is="slice.icon" v-if="slice.icon" :size="13" aria-hidden="true" />
            <span class="label">{{ slice.label }}</span>
            <span class="numbers"><span class="mono">{{ count(slice.count) }}</span> <span class="muted">{{ percent(slice.count) }}</span></span>
          </button>
        </li>
      </ul>
    </div>
  </figure>
</template>

<style scoped>
.donut {
  display: grid;
  align-content: start;
  gap: var(--space-3);
  min-width: 0;
  margin: 0;
  padding: var(--space-4);
}

.title {
  font-weight: 600;
}

.plot {
  display: grid;
  gap: var(--space-3);
  justify-items: center;
}

.ring {
  position: relative;
  width: 148px;
  height: 148px;
}

.ring svg {
  display: block;
  width: 100%;
  height: 100%;
  transform: rotate(-90deg);
}

circle {
  fill: none;
  stroke-width: 16;
}

.base {
  stroke: var(--color-surface-subtle);
}

/* The filter's effect: slices slide to their new share instead of jumping. */
.arc {
  transition: stroke-dasharray 420ms ease, stroke-dashoffset 420ms ease, opacity 200ms ease;
}

.arc--dim {
  opacity: 0.3;
}

.arc--button {
  cursor: pointer;
}

.center {
  position: absolute;
  inset: 0;
  display: grid;
  align-content: center;
  justify-items: center;
  padding: 0 28px;
  text-align: center;
  pointer-events: none;
}

.value {
  font-size: 20px;
  font-weight: 600;
}

.caption {
  line-height: 1.2;
}

.legend {
  display: grid;
  gap: 2px;
  width: 100%;
  margin: 0;
  padding: 0;
  list-style: none;
}

.item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 28px;
  padding: 2px var(--space-2);
  color: var(--color-text);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: opacity 200ms ease;
}

.item:hover:not(:disabled) {
  background: var(--color-surface-subtle);
}

.item:disabled {
  cursor: default;
}

.item--on {
  font-weight: 500;
  background: var(--color-primary-soft);
}

.item--dim {
  opacity: 0.6;
}

.swatch {
  flex: none;
  width: 10px;
  height: 10px;
  border-radius: 3px;
}

.label {
  flex: 1;
  min-width: 0;
}

.numbers {
  flex: none;
  white-space: nowrap;
}

@media (prefers-reduced-motion: reduce) {
  .arc,
  .item {
    transition: none;
  }
}
</style>

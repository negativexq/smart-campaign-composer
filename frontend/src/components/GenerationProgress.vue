<script setup lang="ts">
import { CircleCheck, Clock, LoaderCircle } from "lucide-vue-next";
import { computed } from "vue";

type Row = { language: string; name: string; state: "waiting" | "writing" | "done" };

const props = defineProps<{ languages: Row[]; variant?: string; writes: string }>();

const finished = computed(() => props.languages.filter((row) => row.state === "done").length);
const title = computed(() => (props.variant ? `Writing version ${props.variant}` : "Writing the messages"));
const words = computed(() => ({ waiting: "Up next", writing: `Writing ${props.writes}`, done: "Written and checked" }));
const icons = { waiting: Clock, writing: LoaderCircle, done: CircleCheck };

// A test is written one version after the other, so a finished one stays ticked.
const versions = computed(() => {
  if (!props.variant) return [];
  const current = finished.value === props.languages.length ? ("done" as const) : ("writing" as const);
  return props.variant === "A"
    ? [{ name: "A", state: current }, { name: "B", state: "waiting" as const }]
    : [{ name: "A", state: "done" as const }, { name: "B", state: current }];
});
</script>

<template>
  <section class="progress card" role="status" aria-live="polite" data-testid="generation-progress">
    <div class="head">
      <h3>{{ title }}</h3>
      <span class="muted small" data-testid="generation-count">{{ finished }} of {{ languages.length }} {{ languages.length === 1 ? "language" : "languages" }}</span>
    </div>
    <ol v-if="versions.length" class="versions" data-testid="generation-versions">
      <li v-for="version in versions" :key="version.name" class="row" :class="`row--${version.state}`" :data-testid="`generation-version-${version.name}`" :data-state="version.state">
        <component :is="icons[version.state]" class="icon" :class="{ 'icon--spin': version.state === 'writing' }" :size="16" aria-hidden="true" />
        <span class="name">Version {{ version.name }}</span>
        <span class="visually-hidden">{{ { waiting: "up next", writing: "being written", done: "finished" }[version.state] }}</span>
      </li>
    </ol>
    <ul class="rows">
      <li v-for="row in languages" :key="row.language" class="row" :class="`row--${row.state}`" :data-testid="`generation-${row.language}`">
        <component :is="icons[row.state]" class="icon" :class="{ 'icon--spin': row.state === 'writing' }" :size="16" aria-hidden="true" />
        <span class="name">{{ row.name }}</span>
        <span class="muted small state">{{ words[row.state] }}</span>
      </li>
    </ul>
    <p class="muted small">Nothing is saved until every language has passed its checks.</p>
  </section>
</template>

<style scoped>
.progress {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.head h3,
.progress p {
  margin: 0;
}

.versions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-6);
  margin: 0;
  padding: 0 0 var(--space-3);
  border-bottom: 1px solid var(--color-border);
  list-style: none;
}

.rows {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.icon {
  flex: none;
  color: var(--color-text-muted, currentColor);
}

.row--writing .icon {
  color: var(--color-primary);
}

.row--done .icon {
  color: var(--color-success, var(--color-primary));
}

.row--waiting .name {
  color: var(--color-text-muted, inherit);
}

.name {
  font-weight: 500;
}

.state {
  margin-left: auto;
}

.icon--spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .icon--spin {
    animation: none;
  }
}
</style>

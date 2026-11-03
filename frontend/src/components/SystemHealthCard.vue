<script setup lang="ts">
import { computed } from "vue";

import { SYSTEM_COMPONENTS, systemHealth } from "../lib/systemHealth";

// A component the API was not told about has nothing to report, so it is left out.
const rows = computed(() => SYSTEM_COMPONENTS.filter((component) => systemHealth.value?.checks[component.id]));
</script>

<template>
  <ul v-if="rows.length" class="health card" data-testid="system-health">
    <li v-for="row in rows" :key="row.id" class="row">
      <span class="dot" :class="systemHealth?.checks[row.id] === 'ok' ? 'dot--ok' : 'dot--down'" aria-hidden="true"></span>
      <span class="name">{{ row.label }}</span>
      <span class="small" :class="{ muted: systemHealth?.checks[row.id] === 'ok' }" :data-testid="`health-${row.id}`">
        {{ systemHealth?.checks[row.id] === "ok" ? "Up" : `Down. ${row.effect}` }}
      </span>
    </li>
  </ul>
  <p v-else class="muted" data-testid="system-health-unknown">We cannot reach the system right now.</p>
</template>

<style scoped>
.health {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.row:last-child {
  border-bottom: 0;
}

.name {
  flex: 0 0 140px;
  font-weight: 500;
}

.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.dot--ok {
  background: var(--color-success);
}

.dot--down {
  background: var(--color-error);
}
</style>

<script setup lang="ts">
import { computed } from "vue";

import { deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";
import StatusBadge from "./StatusBadge.vue";

const props = defineProps<{
  events: { id: string; status: DeliveryStatus; title: string; at: string; detail?: string }[];
}>();

const hasAccepted = computed(() => props.events.some((event) => event.status === "SENT"));
</script>

<template>
  <section class="timeline card" aria-label="Delivery activity">
    <p v-if="events.length === 0" class="empty muted">No delivery activity yet. Events appear here once sending starts.</p>

    <ul v-else class="events">
      <li v-for="event in events" :key="event.id" class="event">
        <StatusBadge :status="event.status" />
        <div class="body">
          <span>{{ event.title }}</span>
          <span v-if="event.detail" class="muted small">{{ event.detail }}</span>
        </div>
        <time class="muted small mono">{{ event.at }}</time>
      </li>
    </ul>

    <p v-if="hasAccepted" class="note muted small">{{ deliveryStatusMeta.SENT.description }}</p>
  </section>
</template>

<style scoped>
.empty {
  padding: var(--space-6);
  text-align: center;
}

.events {
  margin: 0;
  padding: 0;
  list-style: none;
}

.event {
  display: grid;
  grid-template-columns: 136px minmax(0, 1fr) auto;
  gap: var(--space-3);
  align-items: baseline;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.event:last-child {
  border-bottom: 0;
}

.body {
  display: grid;
}

.note {
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border);
}

@media (max-width: 560px) {
  .event {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .body {
    grid-column: 1 / -1;
    grid-row: 2;
  }
}
</style>

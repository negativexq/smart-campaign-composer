<script setup lang="ts">
import OriginTag, { type ContentOrigin } from "./OriginTag.vue";

defineProps<{
  label: string;
  value: string;
  origin: ContentOrigin;
  assumed?: boolean;
}>();
</script>

<template>
  <div class="rule card">
    <div class="condition">
      <span class="muted">{{ label }}</span>
      <strong class="value">{{ value }}</strong>
    </div>
    <div class="meta">
      <span v-if="assumed" class="assumption small">Assumption, please check</span>
      <OriginTag :origin="origin" />
    </div>
  </div>
</template>

<style scoped>
.rule {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
  padding: var(--space-3) var(--space-4);
}

.condition {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.value {
  font-weight: 500;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.assumption {
  color: var(--color-warning);
  font-weight: 500;
}
</style>

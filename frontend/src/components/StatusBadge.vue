<script setup lang="ts">
import { computed } from "vue";

import { deliveryStatusMeta, type DeliveryStatus } from "../lib/deliveryStatus";

const props = defineProps<{ status: DeliveryStatus }>();

const meta = computed(() => deliveryStatusMeta[props.status]);
</script>

<template>
  <span class="badge small" :class="`badge--${meta.tone}`" :title="meta.description">
    <component :is="meta.icon" :size="14" aria-hidden="true" />
    {{ meta.label }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  font-weight: 500;
  white-space: nowrap;
}

.badge--neutral {
  color: var(--color-text-muted);
}

.badge--primary {
  color: var(--color-primary);
}

.badge--success {
  color: var(--color-success);
}

.badge--warning {
  color: var(--color-warning);
}

.badge--error {
  color: var(--color-error);
}
</style>

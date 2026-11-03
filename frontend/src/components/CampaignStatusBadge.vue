<script setup lang="ts">
import { computed } from "vue";

import { campaignStatusMeta } from "../lib/campaignStatus";

const props = defineProps<{ status: string }>();

const meta = computed(() => campaignStatusMeta(props.status));
</script>

<template>
  <span class="badge small" :class="`badge--${meta.tone}`" data-testid="status-badge">{{ meta.label }}</span>
</template>

<style scoped>
.badge {
  display: inline-block;
  padding: 2px var(--space-2);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
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

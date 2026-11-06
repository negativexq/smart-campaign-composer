<script setup lang="ts">
import { computed } from "vue";

import { campaignStatusMeta, rowStatusMeta } from "../lib/campaignStatus";

// schedule and lastRun, when given, let a repeating campaign say it waits for its next run.
const props = defineProps<{ status: string; schedule?: { repeats: boolean; next_run_at: string | null } | null; lastRun?: { number: number } | null }>();

const meta = computed(() => (props.schedule ? rowStatusMeta({ status: props.status, schedule: props.schedule, last_run: props.lastRun }) : campaignStatusMeta(props.status)));
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

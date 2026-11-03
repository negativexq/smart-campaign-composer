<script setup lang="ts">
import { computed } from "vue";

import type { AiConfig } from "../api/campaigns";

const props = defineProps<{ config: AiConfig }>();

const isMock = computed(() => props.config.llm_provider === "mock");
const label = computed(() => (isMock.value ? "Mock AI" : `AI: ${props.config.llm_model}`));
const description = computed(() =>
  isMock.value
    ? "Audiences come from a local keyword matcher, not a real model. Set LLM_PROVIDER to use one."
    : `Audiences come from ${props.config.llm_provider}.`,
);
</script>

<template>
  <span class="badge small" :class="{ 'badge--mock': isMock }" :title="description" data-testid="ai-mode">{{ label }}</span>
</template>

<style scoped>
.badge {
  padding: 2px var(--space-2);
  color: var(--color-text-muted);
  white-space: nowrap;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
  font-weight: 500;
}

.badge--mock {
  color: var(--color-warning);
  background: transparent;
  box-shadow: inset 0 0 0 1px currentcolor;
}
</style>

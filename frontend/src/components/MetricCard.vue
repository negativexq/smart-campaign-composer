<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  label: string;
  value: number;
  hint?: string;
  prominent?: boolean;
  href?: string;
}>();

const formatted = computed(() => props.value.toLocaleString("en-US"));
</script>

<template>
  <component :is="href ? 'a' : 'div'" class="metric card" :class="{ 'metric--prominent': prominent, 'metric--link': href }" :href="href">
    <div class="value">{{ formatted }}</div>
    <div class="label">{{ label }}</div>
    <div v-if="hint" class="muted small">{{ hint }}</div>
  </component>
</template>

<style scoped>
.metric {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-4);
}

.metric--link {
  color: inherit;
  text-decoration: none;
}

.metric--link:hover {
  border-color: var(--color-primary);
}

.value {
  font-size: 20px;
  font-weight: 600;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.metric--prominent .value {
  font-size: 40px;
  letter-spacing: -0.02em;
}

.label {
  font-weight: 500;
}
</style>

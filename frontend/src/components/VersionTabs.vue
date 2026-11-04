<script setup lang="ts">
defineProps<{
  modelValue: string;
  label: string;
  options: { id: string; name: string; hint?: string; seen?: boolean }[];
  testPrefix: string;
}>();
defineEmits<{ "update:modelValue": [value: string] }>();
</script>

<template>
  <div class="tabs" role="tablist" :aria-label="label" :data-testid="`${testPrefix}-tabs`">
    <button
      v-for="option in options"
      :key="option.id"
      class="tab"
      :class="{ 'tab--active': option.id === modelValue }"
      type="button"
      role="tab"
      :aria-selected="option.id === modelValue"
      :data-testid="`${testPrefix}-${option.id}`"
      @click="$emit('update:modelValue', option.id)"
    >
      {{ option.name }}
      <span v-if="option.hint" class="muted small">{{ option.hint }}</span>
      <span v-if="option.seen === false" class="unseen small" :data-testid="`${testPrefix}-unread-${option.id}`">Not read</span>
    </button>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  border-bottom: 1px solid var(--color-border);
}

.tab {
  display: inline-flex;
  align-items: baseline;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  color: var(--color-text);
  font: inherit;
  background: none;
  border: 0;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
}

.tab:hover:not(.tab--active) {
  background: var(--color-surface-subtle);
}

.tab--active {
  color: var(--color-on-primary);
  font-weight: 600;
  background: var(--color-primary);
  border-bottom-color: var(--color-primary);
  border-radius: var(--radius-md) var(--radius-md) 0 0;
}

.tab--active .muted,
.tab--active .unseen {
  color: inherit;
}

.unseen {
  color: var(--color-primary);
}
</style>

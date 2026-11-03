<script setup lang="ts">
import { Check } from "lucide-vue-next";
import { computed } from "vue";

const props = defineProps<{
  steps: { id: string; label: string }[];
  currentId: string;
}>();

const currentIndex = computed(() => props.steps.findIndex((step) => step.id === props.currentId));
</script>

<template>
  <nav aria-label="Campaign steps">
    <ol class="stepper">
      <li
        v-for="(step, index) in steps"
        :key="step.id"
        class="step"
        :class="{ 'step--current': index === currentIndex, 'step--done': index < currentIndex }"
        :aria-current="index === currentIndex ? 'step' : undefined"
      >
        <span class="marker mono" aria-hidden="true">
          <Check v-if="index < currentIndex" :size="14" />
          <template v-else>{{ String(index + 1).padStart(2, "0") }}</template>
        </span>
        <span>{{ step.label }}</span>
        <span v-if="index < currentIndex" class="visually-hidden">Completed</span>
      </li>
    </ol>
  </nav>
</template>

<style scoped>
.stepper {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-6);
  margin: 0;
  padding: 0;
  list-style: none;
}

.step {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
}

.marker {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  font-size: 12px;
}

.step--done {
  color: var(--color-text);
}

.step--current {
  color: var(--color-primary);
  font-weight: 600;
}
</style>

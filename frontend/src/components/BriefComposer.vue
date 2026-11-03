<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{
  modelValue: string;
  actionLabel: string;
  suggestions?: string[];
  unavailableReason?: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
  submit: [];
}>();

const isEmpty = computed(() => props.modelValue.trim() === "");
const blockedReason = computed(() =>
  isEmpty.value ? "Write a brief to continue." : (props.unavailableReason ?? ""),
);

function submit() {
  if (!blockedReason.value) emit("submit");
}
</script>

<template>
  <form class="composer" @submit.prevent="submit">
    <label class="label" for="brief">Campaign brief</label>
    <textarea
      id="brief"
      class="field"
      rows="7"
      placeholder="Describe who you want to reach, what you want to say and the tone you want."
      aria-describedby="brief-hint"
      :value="modelValue"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>

    <div class="actions">
      <button class="button button--primary" type="submit" :disabled="Boolean(blockedReason)">
        {{ actionLabel }}
      </button>
      <span id="brief-hint" class="muted small" data-testid="brief-hint">{{ blockedReason }}</span>
    </div>

    <div v-if="suggestions?.length" class="suggestions">
      <h2 class="suggestions-title">Start from an example</h2>
      <button
        v-for="suggestion in suggestions"
        :key="suggestion"
        type="button"
        class="suggestion card"
        data-testid="brief-suggestion"
        @click="emit('update:modelValue', suggestion)"
      >
        {{ suggestion }}
      </button>
    </div>
  </form>
</template>

<style scoped>
.composer {
  display: grid;
  gap: var(--space-2);
}

.label {
  font-weight: 500;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.suggestions {
  display: grid;
  gap: var(--space-2);
  margin-top: var(--space-8);
}

.suggestions-title {
  font-size: 13px;
  font-weight: 500;
  letter-spacing: 0;
  color: var(--color-text-muted);
}

.suggestion {
  padding: var(--space-3) var(--space-4);
  color: var(--color-text);
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color var(--transition-fast);
}

.suggestion:hover {
  border-color: var(--color-border-input);
}
</style>

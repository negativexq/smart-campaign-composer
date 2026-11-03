<script setup lang="ts">
const props = defineProps<{ modelValue: string[]; disabled?: boolean }>();
const emit = defineEmits<{ "update:modelValue": [value: string[]] }>();

const options = [
  { id: "sms", label: "SMS", hint: "One short text, up to 160 characters" },
  { id: "email", label: "Email", hint: "A subject line and a message" },
];

function toggle(id: string, checked: boolean) {
  // Kept in the product's order, whichever was ticked first.
  emit("update:modelValue", options.map((option) => option.id).filter((option) => (option === id ? checked : props.modelValue.includes(option))));
}
</script>

<template>
  <fieldset class="picker" :disabled="disabled" data-testid="channel-picker">
    <legend class="legend">Channels</legend>
    <div class="options">
      <label v-for="option in options" :key="option.id" class="option" :class="{ 'option--on': modelValue.includes(option.id) }">
        <input
          type="checkbox"
          :value="option.id"
          :checked="modelValue.includes(option.id)"
          :data-testid="`channel-${option.id}`"
          @change="toggle(option.id, ($event.target as HTMLInputElement).checked)"
        />
        <span class="text">
          <span class="name">{{ option.label }}</span>
          <span class="muted small">{{ option.hint }}</span>
        </span>
      </label>
    </div>
  </fieldset>
</template>

<style scoped>
.picker {
  min-width: 0;
  margin: var(--space-2) 0 0;
  padding: 0;
  border: 0;
}

.legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-weight: 500;
}

.options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.option {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.option--on {
  border-color: var(--color-primary);
}

.picker:disabled .option {
  cursor: not-allowed;
  opacity: 0.6;
}

.option input {
  margin: 3px 0 0;
  accent-color: var(--color-primary);
}

.text {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.name {
  font-weight: 500;
}

@media (max-width: 520px) {
  .options {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

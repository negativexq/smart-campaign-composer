<script setup lang="ts">
import { computed, ref, useId } from "vue";

import { countCharacters, renderMessage } from "../lib/message";

const props = defineProps<{
  message: string;
  recipients: { id: number; label: string; first_name: string }[];
}>();

const id = useId();
const selectedId = ref(String(props.recipients[0]?.id ?? ""));
const selected = computed(() => props.recipients.find((recipient) => String(recipient.id) === selectedId.value));
const rendered = computed(() => renderMessage(props.message, { first_name: selected.value?.first_name ?? "" }));
</script>

<template>
  <section class="preview" aria-label="SMS preview">
    <div class="control">
      <label class="small muted" :for="id">Preview as</label>
      <select :id="id" v-model="selectedId" class="field select">
        <option v-for="recipient in recipients" :key="recipient.id" :value="String(recipient.id)">
          {{ recipient.label }}
        </option>
      </select>
    </div>

    <div class="phone">
      <p class="bubble" data-testid="phone-message">{{ rendered }}</p>
    </div>

    <p class="small muted" data-testid="phone-length">
      <span class="mono">{{ countCharacters(rendered) }}</span> characters for this recipient
    </p>
  </section>
</template>

<style scoped>
.preview {
  display: grid;
  gap: var(--space-3);
}

.control {
  display: grid;
  gap: var(--space-1);
}

.select {
  height: 40px;
  padding-block: 0;
}

.phone {
  min-height: 220px;
  padding: var(--space-6) var(--space-4);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
}

.bubble {
  max-width: 88%;
  padding: var(--space-2) var(--space-3);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  border-bottom-left-radius: var(--radius-sm);
  overflow-wrap: anywhere;
}
</style>

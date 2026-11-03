<script setup lang="ts">
import { computed, ref, useId } from "vue";

import { renderMessage } from "../lib/message";

const props = defineProps<{
  subject: string;
  body: string;
  recipients: { id: number; label: string; first_name: string }[];
}>();

const id = useId();
const selectedId = ref(String(props.recipients[0]?.id ?? ""));
const values = computed(() => {
  const selected = props.recipients.find((recipient) => String(recipient.id) === selectedId.value);
  return { first_name: selected?.first_name ?? "" };
});
</script>

<template>
  <section class="preview" aria-label="Email preview">
    <div class="control">
      <label class="small muted" :for="id">Preview as</label>
      <select :id="id" v-model="selectedId" class="field select">
        <option v-for="recipient in recipients" :key="recipient.id" :value="String(recipient.id)">
          {{ recipient.label }}
        </option>
      </select>
    </div>

    <div class="mail card">
      <p class="subject" data-testid="email-subject">{{ renderMessage(subject, values) }}</p>
      <p class="body" data-testid="email-body">{{ renderMessage(body, values) }}</p>
    </div>
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

.mail {
  display: grid;
  gap: var(--space-3);
  min-height: 160px;
  align-content: start;
  padding: var(--space-4);
}

.subject {
  padding-bottom: var(--space-3);
  font-weight: 600;
  border-bottom: 1px solid var(--color-border);
  overflow-wrap: anywhere;
}

.body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>

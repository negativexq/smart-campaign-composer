<script setup lang="ts">
import { computed, ref, useId, watch } from "vue";

import { countCharacters, renderMessage, SAMPLE_SHORT_LINK, smsSegments } from "../lib/message";

const props = defineProps<{
  message: string;
  recipients: { id: number; label: string; first_name: string }[];
  language?: string;
  code?: string;
  // Whether the SMS's {{link}} has a link behind it; the preview shows a sample short link.
  link?: string;
}>();

const id = useId();
const selectedId = ref(String(props.recipients[0]?.id ?? ""));
// The people change with the language shown and can arrive after the first render.
watch(
  () => props.recipients,
  (people) => {
    if (!people.some((person) => String(person.id) === selectedId.value)) selectedId.value = String(people[0]?.id ?? "");
  },
);
const selected = computed(() => props.recipients.find((recipient) => String(recipient.id) === selectedId.value));
const rendered = computed(() => renderMessage(props.message, { first_name: selected.value?.first_name ?? "", discount_code: props.code, link: props.link ? SAMPLE_SHORT_LINK : undefined }, props.language));
const parts = computed(() => smsSegments(rendered.value));
</script>

<template>
  <section class="preview" aria-label="SMS preview">
    <div v-if="recipients.length" class="control">
      <label class="small muted" :for="id">Preview as</label>
      <select :id="id" v-model="selectedId" class="field select">
        <option v-for="recipient in recipients" :key="recipient.id" :value="String(recipient.id)">
          {{ recipient.label }}
        </option>
      </select>
    </div>

    <div class="phone" data-testid="phone-frame">
      <div class="screen">
        <div class="notch" aria-hidden="true"></div>
        <p class="sender small">Your brand</p>
        <div class="thread">
          <p v-show="rendered.trim()" class="bubble" data-testid="phone-message">{{ rendered }}</p>
        </div>
      </div>
    </div>

    <p class="small muted" data-testid="phone-length">
      <span class="mono">{{ countCharacters(rendered) }}</span> characters for this recipient
    </p>
    <p class="small muted" data-testid="phone-segments">
      <span class="mono">{{ parts.segments }}</span> {{ parts.segments === 1 ? "segment" : "segments" }}, {{ parts.encoding }}.
      <template v-if="parts.segments > 1">Each segment is billed as one SMS.</template>
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
  justify-self: center;
  width: min(100%, 280px);
  padding: 10px;
  background: var(--color-text);
  border-radius: 36px;
}

.screen {
  display: flex;
  flex-direction: column;
  min-height: 380px;
  overflow: hidden;
  background: var(--color-surface);
  border-radius: 27px;
}

.notch {
  align-self: center;
  width: 84px;
  height: 18px;
  background: var(--color-text);
  border-radius: 0 0 12px 12px;
}

.sender {
  margin: 0;
  padding: var(--space-2) var(--space-3);
  font-weight: 500;
  text-align: center;
  border-bottom: 1px solid var(--color-border);
}

.thread {
  flex: 1;
  padding: var(--space-3);
  background: var(--color-surface-subtle);
}

.bubble {
  max-width: 88%;
  margin: 0;
  padding: var(--space-2) var(--space-3);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  border-bottom-left-radius: var(--radius-sm);
  overflow-wrap: anywhere;
}
</style>

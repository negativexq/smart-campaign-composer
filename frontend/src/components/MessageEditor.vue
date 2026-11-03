<script setup lang="ts">
import { CheckCircle2, CircleAlert, LoaderCircle, PencilLine } from "lucide-vue-next";
import { computed, useId } from "vue";

import { countCharacters, findUnknownVariables } from "../lib/message";
import OriginTag, { type ContentOrigin } from "./OriginTag.vue";

const props = defineProps<{
  label: string;
  modelValue: string;
  limit?: number;
  rows?: number;
  // "validated" must only come from the backend. Streaming text is always a draft.
  state: "streaming" | "draft" | "validated" | "invalid";
  origin: ContentOrigin;
}>();

const emit = defineEmits<{ "update:modelValue": [value: string] }>();

const id = useId();
const length = computed(() => countCharacters(props.modelValue));
const overBy = computed(() => (props.limit ? length.value - props.limit : 0));
const unknownVariables = computed(() => findUnknownVariables(props.modelValue));
const streaming = computed(() => props.state === "streaming");
const hasLocalProblem = computed(() => overBy.value > 0 || unknownVariables.value.length > 0);

const counter = computed(() => {
  if (!props.limit) return String(length.value);
  const base = `${length.value} / ${props.limit}`;
  return overBy.value > 0 ? `${base}, ${overBy.value} over` : base;
});

const display = computed(() => {
  if (streaming.value) return { icon: LoaderCircle, text: "Draft, still generating", tone: "ai" };
  if (props.state === "invalid" || hasLocalProblem.value) return { icon: CircleAlert, text: "Needs changes", tone: "error" };
  if (props.state === "validated") return { icon: CheckCircle2, text: "Validated", tone: "success" };
  return { icon: PencilLine, text: "Draft", tone: "muted" };
});

function insertFirstName() {
  emit("update:modelValue", `${props.modelValue}{{first_name}}`);
}
</script>

<template>
  <div class="editor card">
    <div class="header">
      <label class="title" :for="id">{{ label }}</label>
      <OriginTag :origin="origin" />
    </div>

    <textarea
      :id="id"
      class="field"
      :rows="rows ?? 4"
      :readonly="streaming"
      :value="modelValue"
      :aria-invalid="hasLocalProblem ? 'true' : undefined"
      :aria-describedby="`${id}-problem`"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>

    <p v-if="unknownVariables.length" :id="`${id}-problem`" class="problem small">
      Unsupported {{ unknownVariables.length === 1 ? "variable" : "variables" }}:
      <span class="mono">{{ unknownVariables.join(", ") }}</span>
    </p>

    <div class="footer">
      <button
        type="button"
        class="insert small mono"
        data-testid="insert-variable"
        :disabled="streaming"
        @click="insertFirstName"
      >
        + first_name
      </button>
      <span class="state small" :class="`state--${display.tone}`" data-testid="message-state">
        <component :is="display.icon" :size="14" aria-hidden="true" />
        {{ display.text }}
      </span>
      <span class="count small mono" :class="{ 'count--over': overBy > 0 }" data-testid="char-count">{{ counter }}</span>
    </div>
  </div>
</template>

<style scoped>
.editor {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
}

.header,
.footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}

.header {
  justify-content: space-between;
}

.title {
  font-size: 16px;
  font-weight: 600;
}

.problem {
  color: var(--color-error);
}

.insert {
  padding: 2px var(--space-2);
  color: var(--color-text);
  background: var(--color-surface-subtle);
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.insert:disabled {
  color: var(--color-text-muted);
  cursor: not-allowed;
}

.state {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  margin-left: auto;
}

.state--ai {
  color: var(--color-ai);
}

.state--error {
  color: var(--color-error);
}

.state--success {
  color: var(--color-success);
}

.state--muted,
.count {
  color: var(--color-text-muted);
}

.count--over {
  color: var(--color-error);
  font-weight: 500;
}
</style>

<script setup lang="ts">
import { CheckCircle2, CircleAlert, LoaderCircle, PencilLine } from "lucide-vue-next";
import { computed, useId } from "vue";

import { countCharacters, findUnknownVariables, longestRenderedLength } from "../lib/message";
import OriginTag, { type ContentOrigin } from "./OriginTag.vue";

const props = defineProps<{
  label: string;
  modelValue: string;
  limit?: number;
  rows?: number;
  // "validated" must only come from the backend. Streaming text is always a draft.
  state: "streaming" | "draft" | "validated" | "invalid";
  origin: ContentOrigin;
  // Longest {{first_name}} in the audience. When given, the count is what the
  // longest recipient would get rather than the template as written.
  longestName?: number;
  // Hides the shortcut for adding a variable, as in a link, where one has no place.
  noVariables?: boolean;
  problem?: string;
  // One field among others in a step that shows state and origin once for all of them.
  plain?: boolean;
}>();

const emit = defineEmits<{ "update:modelValue": [value: string] }>();

const id = useId();
const countsRendered = computed(() => props.longestName !== undefined);
const length = computed(() =>
  countsRendered.value ? longestRenderedLength(props.modelValue, props.longestName ?? 0) : countCharacters(props.modelValue),
);
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
  <div class="editor" :class="{ card: !plain, 'editor--plain': plain }">
    <div class="header">
      <label class="title" :for="id">{{ label }}</label>
      <OriginTag v-if="!plain" :origin="origin" />
      <span v-else class="count small mono" :class="{ 'count--over': overBy > 0 }" data-testid="char-count" :title="countsRendered ? 'Length for the longest name in this audience' : undefined">{{ counter }}</span>
    </div>

    <textarea
      :id="id"
      class="field"
      :rows="rows ?? 4"
      :readonly="streaming"
      :value="modelValue"
      :aria-invalid="hasLocalProblem ? 'true' : undefined"
      :aria-describedby="unknownVariables.length ? `${id}-problem` : undefined"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    ></textarea>

    <p v-if="unknownVariables.length" :id="`${id}-problem`" class="problem small">
      Unsupported {{ unknownVariables.length === 1 ? "variable" : "variables" }}:
      <span class="mono">{{ unknownVariables.join(", ") }}</span>
    </p>

    <p v-if="problem" class="problem small" data-testid="message-problem">{{ problem }}</p>

    <div v-if="!plain || !noVariables" class="footer">
      <button
        v-if="!noVariables"
        type="button"
        class="insert small mono"
        data-testid="insert-variable"
        :disabled="streaming"
        @click="insertFirstName"
      >
        + first_name
      </button>
      <span v-if="!plain" class="state small" :class="`state--${display.tone}`" data-testid="message-state">
        <component :is="display.icon" :size="14" aria-hidden="true" />
        {{ display.text }}
      </span>
      <span v-if="!plain" class="count small mono" :class="{ 'count--over': overBy > 0 }" data-testid="char-count" :title="countsRendered ? 'Length for the longest name in this audience' : undefined">{{ counter }}</span>
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

.editor--plain {
  padding: 0;
  gap: var(--space-2);
}

.editor--plain .title {
  font-size: 14px;
  font-weight: 500;
}

.count--over {
  color: var(--color-error);
  font-weight: 500;
}
</style>

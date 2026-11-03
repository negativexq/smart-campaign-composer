<script setup lang="ts">
import { computed } from "vue";

import MetricCard from "./MetricCard.vue";
import ValidationNotice from "./ValidationNotice.vue";

const props = defineProps<{
  smsCount: number;
  emailCount: number;
  conditions: string[];
  blockers?: string[];
  submitting?: boolean;
}>();

const emit = defineEmits<{ approve: [] }>();

const total = computed(() => props.smsCount + props.emailCount);
const allBlockers = computed(() => [
  ...(total.value === 0 ? ["No one can be reached with these conditions and consents."] : []),
  ...(props.blockers ?? []),
]);
const disabled = computed(() => allBlockers.value.length > 0 || Boolean(props.submitting));
const buttonLabel = computed(() => {
  if (props.submitting) return "Approving…";
  return `Approve & send ${total.value.toLocaleString("en-US")} ${total.value === 1 ? "message" : "messages"}`;
});
</script>

<template>
  <section class="summary card" aria-labelledby="approval-title">
    <h2 id="approval-title">Review and approve</h2>

    <div>
      <h3 class="group-title">Who receives it</h3>
      <ul class="conditions">
        <li v-for="condition in conditions" :key="condition">{{ condition }}</li>
      </ul>
    </div>

    <div class="counts">
      <MetricCard label="SMS" :value="smsCount" />
      <MetricCard label="Email" :value="emailCount" />
    </div>

    <p class="muted small">Consent is checked again before each message, so the final count can be lower.</p>

    <ValidationNotice v-if="allBlockers.length" tone="error" title="This campaign cannot be approved yet">
      <span v-for="blocker in allBlockers" :key="blocker" class="blocker">{{ blocker }}</span>
    </ValidationNotice>

    <button class="button button--primary" type="button" :disabled="disabled" @click="!disabled && emit('approve')">
      {{ buttonLabel }}
    </button>
  </section>
</template>

<style scoped>
.summary {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-6);
}

.group-title {
  margin-bottom: var(--space-2);
}

.conditions {
  margin: 0;
  padding-left: var(--space-4);
}

.counts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.blocker {
  display: block;
}
</style>

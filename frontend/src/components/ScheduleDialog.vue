<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

import { errorMessage, scheduleCampaign, type Campaign } from "../api/campaigns";
import { buildSchedule, emptyScheduleForm, scheduleProblem, type ScheduleForm } from "../lib/schedule";
import SchedulePicker from "./SchedulePicker.vue";

// Schedules a campaign that already sent. Each send is its own run, and the rule
// says whom earlier sends leave out.
const props = defineProps<{ campaignId: string; campaignName: string }>();
const emit = defineEmits<{ close: []; scheduled: [campaign: Campaign] }>();

const form = ref<ScheduleForm>({ ...emptyScheduleForm(), mode: "once" });
const saving = ref(false);
const failure = ref("");
const dialog = ref<HTMLElement | null>(null);
const problem = computed(() => scheduleProblem(form.value, true));

function onKey(event: KeyboardEvent) {
  if (event.key === "Escape" && !saving.value) emit("close");
}

onMounted(async () => {
  document.addEventListener("keydown", onKey);
  await nextTick();
  dialog.value?.querySelector<HTMLElement>("input, button")?.focus();
});

onBeforeUnmount(() => document.removeEventListener("keydown", onKey));

async function save() {
  const spec = buildSchedule(form.value, true);
  if (!spec) return;
  saving.value = true;
  failure.value = "";
  try {
    emit("scheduled", await scheduleCampaign(props.campaignId, spec));
  } catch (error) {
    failure.value = errorMessage(error);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="backdrop" @click.self="!saving && emit('close')">
    <div ref="dialog" class="dialog" role="dialog" aria-modal="true" aria-labelledby="schedule-title" data-testid="schedule-dialog">
      <h2 id="schedule-title" class="title">Schedule “{{ campaignName }}” again</h2>
      <p class="muted small">The same approved messages go out again on the schedule you choose. Each send gets its own report.</p>

      <SchedulePicker v-model="form" sent-before schedule-only />

      <p v-if="failure" class="error small" role="alert">{{ failure }}</p>

      <div class="actions">
        <span v-if="problem" class="muted small" data-testid="schedule-again-problem">{{ problem }}</span>
        <button class="button button--secondary" type="button" :disabled="saving" @click="emit('close')">Cancel</button>
        <button class="button button--primary" type="button" data-testid="schedule-again-confirm" :disabled="saving || Boolean(problem)" @click="save">
          {{ saving ? "Scheduling..." : "Schedule" }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: var(--space-4);
  background: rgb(0 0 0 / 40%);
}

.dialog {
  display: grid;
  gap: var(--space-3);
  width: min(640px, 100%);
  max-height: calc(100vh - 2 * var(--space-4));
  overflow-y: auto;
  padding: var(--space-5, 24px);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
}

.title {
  margin: 0;
  font-size: 1.125rem;
}

.dialog > p {
  margin: 0;
}

.error {
  color: var(--color-error);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

.actions .muted {
  margin-right: auto;
}
</style>

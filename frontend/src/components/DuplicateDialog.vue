<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { duplicateCampaign, errorMessage, getDuplicatePreview, type Campaign, type DuplicatePreview } from "../api/campaigns";
import { formatDateTime } from "../lib/format";

const props = defineProps<{ campaignId: string; campaignName: string }>();
const emit = defineEmits<{ close: []; created: [copy: Campaign] }>();

const DEFAULT_REST_DAYS = 10;

const first = ref<DuplicatePreview | null>(null);
const current = ref<DuplicatePreview | null>(null);
const counting = ref(true);
const creating = ref(false);
const problem = ref("");
const leaveOut = ref(false);
const days = ref(DEFAULT_REST_DAYS);
const dialog = ref<HTMLElement | null>(null);
let request = 0;
let timer: ReturnType<typeof setTimeout> | undefined;

const keepsRule = computed(() => first.value?.source_rule != null);
const daysValid = computed(() => Number.isInteger(days.value) && days.value >= 1 && days.value <= 365);
const rule = computed(() => (leaveOut.value && daysValid.value ? days.value : null));
const count = (value: number) => value.toLocaleString("en-US");
const reachable = computed(() => (current.value ? current.value.audience.sms + current.value.audience.email : 0));
const sendsNothing = computed(() => current.value != null && reachable.value === 0);

// Counts the copy's audience with the rule as it stands. A later count replaces an
// earlier one still on its way.
async function recount() {
  const mine = ++request;
  counting.value = true;
  problem.value = "";
  try {
    const preview = await getDuplicatePreview(props.campaignId, rule.value);
    if (mine !== request) return;
    current.value = preview;
    if (!first.value) first.value = preview;
  } catch (failure) {
    if (mine === request) problem.value = errorMessage(failure);
  } finally {
    if (mine === request) counting.value = false;
  }
}

// A rule long enough to cover the last send, so ticking the box leaves those people out.
function suggestedDays(lastSentAt: string | null): number {
  if (!lastSentAt) return DEFAULT_REST_DAYS;
  const since = Math.ceil((Date.now() - new Date(lastSentAt).getTime()) / 86_400_000);
  return Math.min(365, Math.max(DEFAULT_REST_DAYS, since + 1));
}

onMounted(async () => {
  document.addEventListener("keydown", onKey);
  await recount();
  if (first.value?.source_rule != null) {
    leaveOut.value = true;
    days.value = first.value.source_rule;
  } else {
    days.value = suggestedDays(first.value?.last_sent_at ?? null);
  }
  await nextTick();
  dialog.value?.querySelector<HTMLElement>("input, button")?.focus();
});

onBeforeUnmount(() => {
  document.removeEventListener("keydown", onKey);
  clearTimeout(timer);
});

watch([leaveOut, days], () => {
  if (!first.value) return;
  clearTimeout(timer);
  if (leaveOut.value && !daysValid.value) return;
  counting.value = true;
  timer = setTimeout(recount, 300);
});

function onKey(event: KeyboardEvent) {
  if (event.key === "Escape" && !creating.value) emit("close");
}

async function create() {
  creating.value = true;
  problem.value = "";
  try {
    emit("created", await duplicateCampaign(props.campaignId, rule.value));
  } catch (failure) {
    problem.value = errorMessage(failure);
  } finally {
    creating.value = false;
  }
}
</script>

<template>
  <div class="backdrop" @click.self="!creating && emit('close')">
    <div ref="dialog" class="dialog" role="dialog" aria-modal="true" aria-labelledby="duplicate-title" data-testid="duplicate-dialog">
      <h2 id="duplicate-title" class="title">Duplicate “{{ campaignName }}”</h2>
      <p class="muted small">
        The copy is a new campaign with its own approval and report. Its audience is counted again now, and nothing is sent until it is approved.
      </p>

      <p v-if="!first && counting" class="muted" role="status" data-testid="duplicate-counting">Counting the copy's audience...</p>

      <template v-else-if="current && first">
        <section class="numbers" :class="{ 'numbers--stale': counting }" aria-live="polite" data-testid="duplicate-audience">
          <p class="audience">
            <strong>{{ count(current.audience.total) }}</strong> people match the copy's conditions. Of them, it can message
            <strong>{{ count(current.audience.sms) }}</strong> by SMS and <strong>{{ count(current.audience.email) }}</strong> by email.
          </p>
          <p v-if="reachable === 0" class="warning" data-testid="duplicate-nobody">
            Nobody who matches can be messaged<template v-if="current.audience.total > 0">: none of them have agreed to SMS or email</template>, so the copy would send nothing.
          </p>
          <p v-if="current.reached_before > 0" class="warning" data-testid="duplicate-reached">
            {{ count(current.reached_before) }} of them already received this campaign<template v-if="current.last_sent_at">, last on {{ formatDateTime(current.last_sent_at) }}</template>.
            Approving the copy as it is sends them the same message again.
          </p>
          <p v-else-if="first.reached_before > 0 && reachable > 0" class="ok" data-testid="duplicate-clear">Nobody this campaign reached is in the copy's audience.</p>
          <p v-if="counting" class="muted small" role="status" data-testid="duplicate-recounting">Counting again with the new rule...</p>
        </section>

        <fieldset class="rule" data-testid="duplicate-rule">
          <legend class="small muted">Recently messaged people</legend>
          <p v-if="keepsRule" class="small">This campaign leaves out people we messaged recently, and so does the copy. You can change the number of days.</p>
          <p v-else-if="leaveOut" class="small" data-testid="duplicate-rule-added">The copy will leave out people any campaign messaged in the last {{ daysValid ? days : "N" }} days. This campaign did not.</p>
          <p v-else class="small">This campaign does not leave out people we messaged recently, so neither would the copy.</p>
          <label class="choice">
            <input v-model="leaveOut" type="checkbox" :disabled="keepsRule" data-testid="duplicate-leave-out" />
            <span>Leave out people we messaged in the last</span>
            <input
              v-model.number="days"
              class="field days"
              type="number"
              min="1"
              max="365"
              step="1"
              aria-label="Days"
              data-testid="duplicate-days"
              :disabled="!leaveOut"
            />
            <span>days</span>
          </label>
          <p v-if="leaveOut && !daysValid" class="error small">Choose from 1 to 365 days.</p>
        </fieldset>
      </template>

      <p v-if="problem" class="error small" role="alert">{{ problem }}</p>

      <div class="actions">
        <button class="button button--secondary" type="button" :disabled="creating" @click="emit('close')">Cancel</button>
        <!-- A copy that can message nobody is still allowed, since it can be changed
             before approval, but the button says so instead of inviting a click. -->
        <button
          class="button"
          :class="sendsNothing ? 'button--secondary' : 'button--primary'"
          type="button"
          data-testid="duplicate-confirm"
          :disabled="creating || counting || !current || (leaveOut && !daysValid)"
          @click="create"
        >
          {{ creating ? "Copying..." : sendsNothing ? "Create copy anyway" : "Create copy" }}
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
  width: min(520px, 100%);
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

.numbers {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
  transition: opacity 0.15s;
}

.numbers--stale {
  opacity: 0.55;
}

.numbers p,
.rule p {
  margin: 0;
}

.warning {
  color: var(--color-warning);
}

.ok {
  color: var(--color-success);
}

.error {
  color: var(--color-error);
}

.rule {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.rule legend {
  margin-bottom: var(--space-1);
  padding: 0;
}

.choice {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
}

.days {
  width: 80px;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
</style>

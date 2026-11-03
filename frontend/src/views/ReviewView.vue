<script setup lang="ts">
import { CheckCircle2, CircleAlert } from "lucide-vue-next";
import { computed, onMounted, ref, watch } from "vue";

import {
  approveCampaign,
  dispatchCampaign,
  errorMessage,
  getCampaign,
  scheduleCampaign,
  type Campaign,
} from "../api/campaigns";
import EmailPreview from "../components/EmailPreview.vue";
import MetricCard from "../components/MetricCard.vue";
import SchedulePicker from "../components/SchedulePicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { navigate } from "../lib/navigation";
import { buildSchedule, emptyScheduleForm, scheduleProblem } from "../lib/schedule";

const props = defineProps<{ campaignId: string }>();
const emit = defineEmits<{ status: [status: string] }>();

const campaign = ref<Campaign | null>(null);
const loadError = ref("");
const actionError = ref("");
const submitting = ref(false);
const form = ref(emptyScheduleForm());

const reviewable = computed(() => ["READY_FOR_REVIEW", "APPROVED"].includes(campaign.value?.status ?? ""));
const messageCount = computed(() => (campaign.value ? campaign.value.audience.sms + campaign.value.audience.email : 0));
const problem = computed(() => scheduleProblem(form.value));
type Check = { label: string; detail: string; ok: boolean };
const checks = computed<Check[]>(() => {
  const current = campaign.value;
  if (!current) return [];
  const count = messageCount.value.toLocaleString("en-US");
  const list: Check[] = [
    { label: "Audience", detail: `${current.audience.total.toLocaleString("en-US")} people match, and ${count} messages go to those who consented.`, ok: messageCount.value > 0 },
    { label: "Limits", detail: current.content ? "Every message fits its limit for the longest name in this audience." : "The messages are not written yet.", ok: Boolean(current.content) },
    {
      label: "Offer",
      detail: current.content_warnings.length ? current.content_warnings.map((warning) => warning.message).join(" ") : "Any discount matches the brief.",
      ok: !current.content_warnings.length,
    },
  ];
  if (current.channels.includes("email") && current.content) {
    list.push({
      label: "Email link",
      detail: current.content.email_cta_url ? `The button opens ${current.content.email_cta_url}` : "This email has no button.",
      ok: true,
    });
    list.push({ label: "Unsubscribe", detail: "Each email carries its own unsubscribe link and a plain text version.", ok: true });
  }
  list.push({ label: "When", detail: problem.value || (form.value.mode === "now" ? "Sends as soon as you approve." : "Sends on the schedule below."), ok: !problem.value });
  return list;
});
const blocked = computed(() => Boolean(campaign.value?.blockers.length) || messageCount.value === 0);
const actionLabel = computed(() => {
  if (submitting.value) return "Working...";
  if (form.value.mode !== "now") return "Approve and schedule";
  return `Approve and send ${messageCount.value.toLocaleString("en-US")} ${messageCount.value === 1 ? "message" : "messages"}`;
});

// Approving twice is harmless, so trying again after a failed second step is safe.
async function confirm() {
  submitting.value = true;
  actionError.value = "";
  try {
    await approveCampaign(props.campaignId);
    const spec = buildSchedule(form.value);
    if (spec) await scheduleCampaign(props.campaignId, spec);
    else await dispatchCampaign(props.campaignId);
    navigate(`/campaigns/${props.campaignId}`);
  } catch (failure) {
    actionError.value = errorMessage(failure);
  } finally {
    submitting.value = false;
  }
}

watch(() => campaign.value?.status, (status) => status && emit("status", status));

onMounted(async () => {
  try {
    campaign.value = await getCampaign(props.campaignId);
  } catch (failure) {
    loadError.value = errorMessage(failure);
  }
});
</script>

<template>
  <ValidationNotice v-if="loadError" tone="error" title="We could not open this campaign">
    {{ loadError }} <a href="/campaigns">Back to campaigns</a>
  </ValidationNotice>
  <p v-else-if="!campaign" class="muted" role="status">Loading campaign...</p>

  <ValidationNotice v-else-if="!reviewable" tone="info" title="This campaign is not waiting for review">
    <a :href="`/campaigns/${campaignId}`">Open the campaign</a>
  </ValidationNotice>

  <div v-else class="review">
    <header class="intro">
      <h1>Approve what goes out.</h1>
      <p class="muted">After approval the audience rules and messages can no longer change.</p>
    </header>

    <ValidationNotice v-for="blocker in campaign.blockers" :key="blocker" tone="error" title="This blocks approval">{{ blocker }}</ValidationNotice>
    <ValidationNotice v-for="warning in campaign.content_warnings" :key="warning.field + warning.reason" tone="warning" title="Differs from the brief">
      {{ warning.message }}
    </ValidationNotice>

    <section class="section">
      <h2>Who receives it</h2>
      <ul class="conditions card" data-testid="review-conditions">
        <li v-for="condition in campaign.conditions" :key="condition.field">{{ condition.label }} <strong>{{ condition.value }}</strong></li>
      </ul>
      <div class="metrics">
        <MetricCard v-if="campaign.channels.includes('sms')" label="SMS" :value="campaign.audience.sms" hint="People who consented" />
        <MetricCard v-if="campaign.channels.includes('email')" label="Email" :value="campaign.audience.email" hint="People who consented" />
      </div>
      <p class="muted small">Consent is checked again just before each message, so the final count can be lower.</p>
    </section>

    <section v-if="campaign.content" class="section">
      <h2>What they read</h2>
      <article v-if="campaign.channels.includes('sms')" class="message card" data-testid="review-sms">
        <h3 class="muted small">SMS</h3>
        <p class="text">{{ campaign.content.sms }}</p>
      </article>
      <article v-if="campaign.channels.includes('email')" class="message card" data-testid="review-email">
        <h3 class="muted small">Email</h3>
        <EmailPreview :message="campaign.content" :recipients="[]" />
      </article>
    </section>

    <SchedulePicker v-model="form" />

    <section class="section" aria-labelledby="checks-title">
      <h2 id="checks-title">Before it goes out</h2>
      <ul class="checks card" data-testid="review-checks">
        <li v-for="check in checks" :key="check.label" class="check" :data-state="check.ok ? 'ok' : 'attention'">
          <component :is="check.ok ? CheckCircle2 : CircleAlert" :size="16" class="check-icon" :class="{ 'check-icon--attention': !check.ok }" aria-hidden="true" />
          <span class="check-text"><strong>{{ check.label }}</strong> {{ check.detail }}</span>
          <span class="visually-hidden">{{ check.ok ? "Passed" : "Needs attention" }}</span>
        </li>
      </ul>
    </section>

    <ValidationNotice v-if="actionError" tone="error" title="This did not go through">{{ actionError }}</ValidationNotice>

    <div class="row">
      <a class="small" :href="`/campaigns/${campaignId}/content`">Back to content</a>
      <button class="button button--primary" type="button" data-testid="approve" :disabled="submitting || blocked || Boolean(problem)" @click="confirm">
        {{ actionLabel }}
      </button>
      <span v-if="problem" class="muted small" data-testid="schedule-problem">{{ problem }}</span>
      <a class="exit small" :href="`/campaigns/${campaignId}`">Save and exit</a>
    </div>
  </div>
</template>

<style scoped>
.review {
  display: grid;
  gap: var(--space-6);
  max-width: 760px;
}

.intro,
.section {
  display: grid;
  gap: var(--space-3);
}

.conditions {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-4) var(--space-4) var(--space-4) var(--space-8);
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
}

.checks {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-4);
  list-style: none;
}

.check {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
}

.check-text {
  min-width: 0;
  overflow-wrap: anywhere;
}

.check-text strong {
  font-weight: 500;
}

.check-icon {
  flex: none;
  margin-top: 3px;
  color: var(--color-success);
}

.check-icon--attention {
  color: var(--color-warning);
}

.message {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-4);
}

.text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.exit {
  margin-left: auto;
}
</style>

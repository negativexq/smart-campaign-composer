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
  type MessageVersion,
} from "../api/campaigns";
import EmailPreview from "../components/EmailPreview.vue";
import MetricCard from "../components/MetricCard.vue";
import SchedulePicker from "../components/SchedulePicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { channelLabel, lastChannel, lastMessagePath } from "../lib/campaignSteps";
import { languageVersions, missingLanguages, emptySide, missingSecondVersion, languageName, variantCount, variantPeople, variantShare } from "../lib/languages";
import { uniqueWarnings, warningTitle } from "../lib/message";
import { navigate } from "../lib/navigation";
import { buildSchedule, emptyScheduleForm, scheduleProblem } from "../lib/schedule";

const props = defineProps<{ campaignId: string }>();
const emit = defineEmits<{ status: [status: string]; channels: [channels: string[]] }>();

const campaign = ref<Campaign | null>(null);
const loadError = ref("");
const actionError = ref("");
const submitting = ref(false);
const form = ref(emptyScheduleForm());

const reviewable = computed(() => ["READY_FOR_REVIEW", "APPROVED"].includes(campaign.value?.status ?? ""));
const messageCount = computed(() => (campaign.value ? campaign.value.audience.sms + campaign.value.audience.email : 0));
const problem = computed(() => scheduleProblem(form.value));
// Every version is on the page, so nobody approves a language they did not see.
const versions = computed(() => {
  const current = campaign.value;
  if (!current?.content) return [];
  const known = languageVersions(current);
  return known.length ? known : [{ language: "", name: "", sms: current.audience.sms, email: current.audience.email, version: current.content }];
});
// A test puts its second version on the page the same way.
const secondVersions = computed(() => {
  const current = campaign.value;
  const second = current?.content?.variant_b;
  if (!current || !second) return [];
  const known = languageVersions(current, "B");
  return known.length ? known : [{ language: "", name: "", sms: current.audience.sms, email: current.audience.email, version: second as MessageVersion }];
});
const groups = computed(() =>
  secondVersions.value.length
    ? [
        { variant: "A", label: "Version A", entries: versions.value },
        { variant: "B", label: "Version B", entries: secondVersions.value },
      ]
    : [{ variant: "", label: "", entries: versions.value }],
);
const noSecondVersion = computed(() => (campaign.value ? missingSecondVersion(campaign.value) : false));
// What one version sends to one language's readers.
function peopleIn(variant: string, entry: { language: string; sms: number; email: number }) {
  const current = campaign.value;
  if (!variant || !current) return { sms: entry.sms, email: entry.email };
  return {
    sms: variantCount(current, variant, { language: entry.language, channel: "sms" }),
    email: variantCount(current, variant, { language: entry.language, channel: "email" }),
  };
}
const missing = computed(() => (campaign.value ? missingLanguages(campaign.value).map(languageName) : []));
type Check = { label: string; detail: string; ok: boolean };
const checks = computed<Check[]>(() => {
  const current = campaign.value;
  if (!current) return [];
  const count = messageCount.value.toLocaleString("en-US");
  const list: Check[] = [
    { label: "Audience", detail: `${current.audience.total.toLocaleString("en-US")} people match, and ${count} messages go to those who consented.`, ok: messageCount.value > 0 },
    { label: "Limits", detail: current.content ? "Every message fits its limit for the longest name in this audience." : "The messages are not written yet.", ok: Boolean(current.content) },
  ];
  // A campaign without a discount has nothing to check here.
  const hasDiscount = Boolean(current.offer_percent) || /%\s*\d|\d\s*%/.test(current.brief);
  // What the model left undone is its own notice, whatever it was about.
  const differs = current.content_warnings.filter((warning) => warning.reason !== "not_applied");
  if (differs.length || hasDiscount) {
    list.push({
      label: "Discount",
      detail: differs.length
        ? uniqueWarnings(differs).map((warning) => warning.message).join(" ")
        : current.offer_percent
          ? `Every message carries the ${current.offer_percent}% discount you entered.`
          : "Every message carries the discount from the brief.",
      ok: !differs.length,
    });
  }
  if (current.content && (versions.value.length > 1 || missing.value.length)) {
    list.push({
      label: "Languages",
      detail: missing.value.length
        ? `Part of this audience reads ${missing.value.join(" and ")} and has no messages yet.`
        : `Each person gets ${versions.value.map((entry) => entry.name).join(" or ")}, with the same offer.`,
      ok: !missing.value.length,
    });
  }
  if (current.ab_test && current.content) {
    const empty = emptySide(current);
    list.push({
      label: "Test",
      detail: noSecondVersion.value
        ? "The second version is not written yet."
        : empty
          ? `Nobody in this audience lands on version ${empty}, so there is nothing to compare.`
          : `Each person stays on one version: ${variantPeople(current, "A")} get A and ${variantPeople(current, "B")} get B, with the same offer. The report compares them and names no winner.`,
      ok: !noSecondVersion.value && !empty,
    });
  }
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
const blocked = computed(() => Boolean(campaign.value?.blockers.length) || messageCount.value === 0 || missing.value.length > 0 || noSecondVersion.value);
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
watch(() => campaign.value?.channels, (channels) => channels && emit("channels", channels));

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
    <ValidationNotice v-for="warning in uniqueWarnings(campaign.content_warnings)" :key="warning.field + warning.reason" tone="warning" :title="warningTitle(warning)">
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
      <div v-for="group in groups" :key="group.variant" class="group" :data-testid="group.variant ? `review-variant-${group.variant}` : undefined">
        <h3 v-if="group.label" class="group-head">
          {{ group.label }}
          <span class="muted small" :data-testid="`people-${group.variant}`">{{ variantShare(campaign, group.variant) }}% · {{ variantPeople(campaign, group.variant) }}</span>
        </h3>
        <div v-for="entry in group.entries" :key="entry.language" class="version" :data-testid="entry.language ? `review-${entry.language}` : undefined">
          <h3 v-if="group.entries.length > 1" class="version-head">
            {{ entry.name }}
            <span class="muted small">{{ (peopleIn(group.variant, entry).sms + peopleIn(group.variant, entry).email).toLocaleString("en-US") }} messages</span>
          </h3>
          <article v-if="campaign.channels.includes('sms') && (entry.sms || group.entries.length === 1)" class="message card" data-testid="review-sms">
            <h3 class="muted small">SMS</h3>
            <p class="text">{{ entry.version.sms }}</p>
          </article>
          <article v-if="campaign.channels.includes('email') && (entry.email || group.entries.length === 1)" class="message card" data-testid="review-email">
            <h3 class="muted small">Email</h3>
            <EmailPreview :message="entry.version" :recipients="[]" :language="entry.language" />
          </article>
        </div>
      </div>
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
      <a class="small" :href="lastMessagePath(campaignId, campaign.channels)">Back to {{ channelLabel(lastChannel(campaign.channels)) }}</a>
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

.group,
.version {
  display: grid;
  gap: var(--space-2);
}

.group {
  gap: var(--space-3);
}

.group-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
  padding-bottom: var(--space-1);
  border-bottom: 1px solid var(--color-border);
}

.version-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
  font-size: inherit;
  font-weight: 500;
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

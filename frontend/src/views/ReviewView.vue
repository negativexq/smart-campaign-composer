<script setup lang="ts">
import { CheckCircle2, CircleAlert } from "lucide-vue-next";
import { computed, onMounted, ref, watch } from "vue";

import {
  approveCampaign,
  dispatchCampaign,
  errorMessage,
  getCampaign,
  getSampleRecipients,
  scheduleCampaign,
  type Campaign,
  type MessageVersion,
  type SampleRecipient,
} from "../api/campaigns";
import PhonePreview from "../components/PhonePreview.vue";
import VersionTabs from "../components/VersionTabs.vue";
import EmailPreview from "../components/EmailPreview.vue";
import MetricCard from "../components/MetricCard.vue";
import SchedulePicker from "../components/SchedulePicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { channelLabel, lastChannel, lastMessagePath } from "../lib/campaignSteps";
import { listNames, recipientsFor, languageVersions, missingLanguages, emptySide, missingSecondVersion, languageName, variantCount, variantPeople, variantShare } from "../lib/languages";
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
const recipients = ref<SampleRecipient[]>([]);

const reviewable = computed(() => ["READY_FOR_REVIEW", "APPROVED"].includes(campaign.value?.status ?? ""));
const messageCount = computed(() => (campaign.value ? campaign.value.audience.sms + campaign.value.audience.email : 0));
const problem = computed(() => scheduleProblem(form.value));
// One version shows at a time, and approval waits until each has been opened, so
// nobody approves a language they did not see.
const versions = computed(() => {
  const current = campaign.value;
  if (!current?.content) return [];
  const known = languageVersions(current);
  return known.length ? known : [{ language: "", name: "", sms: current.audience.sms, email: current.audience.email, also: [] as string[], version: current.content }];
});
// A test has its second version to read the same way.
const secondVersions = computed(() => {
  const current = campaign.value;
  const second = current?.content?.variant_b;
  if (!current || !second) return [];
  const known = languageVersions(current, "B");
  return known.length ? known : [{ language: "", name: "", sms: current.audience.sms, email: current.audience.email, also: [] as string[], version: second as MessageVersion }];
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
// Those whose language was left out stay in the audience and read English.
const fallbackNote = computed(() => {
  const also = versions.value.flatMap((entry) => ("also" in entry ? entry.also : []));
  return also.length ? ` ${listNames(also.map(languageName))} readers get English.` : "";
});
const selectedVariant = ref("");
const selectedLanguage = ref("");
const seen = ref(new Set<string>());
const shownGroup = computed(() => groups.value.find((group) => group.variant === selectedVariant.value) ?? groups.value[0]);
const shownEntry = computed(() => shownGroup.value.entries.find((entry) => entry.language === selectedLanguage.value) ?? shownGroup.value.entries[0]);
const shownRecipients = computed(() => (shownEntry.value ? recipientsFor(recipients.value, shownEntry.value.language, shownEntry.value.also) : []));
const versionKey = (variant: string, language: string) => `${variant}:${language}`;
function sideSeen(group: { variant: string; entries: { language: string }[] }) {
  return group.entries.every((entry) => seen.value.has(versionKey(group.variant, entry.language)));
}
const languageTabs = computed(() =>
  shownGroup.value.entries.map((entry) => ({
    id: entry.language,
    name: entry.name,
    seen: seen.value.has(versionKey(shownGroup.value.variant, entry.language)),
  })),
);
const unread = computed(() => groups.value.flatMap((group) => group.entries.filter((entry) => !seen.value.has(versionKey(group.variant, entry.language))).map((entry) => [group.label, entry.name].filter(Boolean).join(" "))));
watch(
  () => (shownEntry.value ? versionKey(shownGroup.value.variant, shownEntry.value.language) : ""),
  (key) => {
    if (key) seen.value = new Set(seen.value).add(key);
  },
  { immediate: true },
);
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
  // What the model left undone and a missing button are their own notices, not about discounts.
  const codeReasons = ["missing_code", "code_changed"];
  const linkReasons = ["missing_link", "link_changed"];
  const differs = current.content_warnings.filter(
    (warning) => warning.reason !== "not_applied" && warning.reason !== "no_button" && !codeReasons.includes(warning.reason) && !linkReasons.includes(warning.reason),
  );
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
  // Shown only for a campaign with a code, or messages written for one it no longer has.
  const codeProblems = current.content_warnings.filter((warning) => codeReasons.includes(warning.reason));
  if (current.discount_code || codeProblems.length) {
    list.push({
      label: "Discount code",
      detail: codeProblems.length
        ? uniqueWarnings(codeProblems).map((warning) => warning.message).join(" ")
        : `Every SMS carries ${current.discount_code}, and every email shows it in a box below the text.`,
      ok: !codeProblems.length,
    });
  }
  if (current.content && (versions.value.length > 1 || missing.value.length)) {
    list.push({
      label: "Languages",
      detail: missing.value.length
        ? `${listNames(missing.value)} has no messages yet.`
        : `Each person gets ${listNames(versions.value.map((entry) => entry.name), "or")}, with the same offer.${fallbackNote.value}`,
      ok: !missing.value.length,
    });
  }
  if (current.content && unread.value.length) {
    list.push({ label: "Read", detail: `Open ${listNames(unread.value)} before approving.`, ok: false });
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
  // Shown only for a brief with a link, or messages written for one it no longer has.
  const linkProblems = current.content_warnings.filter((warning) => linkReasons.includes(warning.reason));
  if (current.link || linkProblems.length) {
    const carriers = [current.channels.includes("sms") && "every SMS carries it as its own short link", current.channels.includes("email") && "every email's button opens it"].filter(Boolean);
    list.push({
      label: "Link",
      detail: linkProblems.length ? uniqueWarnings(linkProblems).map((warning) => warning.message).join(" ") : `${current.link}: ${carriers.join(", and ")}.`,
      ok: !linkProblems.length,
    });
  }
  if (current.channels.includes("email") && current.content) {
    // With a link in the brief, the Link row says where the button goes.
    if (!current.link) {
      list.push({
        label: "Email link",
        detail: current.content.email_cta_url ? `The button opens ${current.content.email_cta_url}` : "This email has no button.",
        ok: true,
      });
    }
    list.push({ label: "Unsubscribe", detail: "Each email carries its own unsubscribe link and a plain text version.", ok: true });
  }
  list.push({ label: "When", detail: problem.value || (form.value.mode === "now" ? "Sends as soon as you approve." : "Sends on the schedule below."), ok: !problem.value });
  return list;
});
const blocked = computed(() => unread.value.length > 0 || Boolean(campaign.value?.blockers.length) || messageCount.value === 0 || missing.value.length > 0 || noSecondVersion.value);
const actionLabel = computed(() => {
  if (submitting.value) return "Working...";
  if (form.value.mode !== "now") return "Approve and schedule";
  return `Approve and send ${messageCount.value.toLocaleString("en-US")} ${messageCount.value === 1 ? "message" : "messages"}`;
});

// Approving twice is harmless, so trying again after a failed second step is safe.
// The approval names what this page showed, so a change made since is refused.
async function confirm() {
  submitting.value = true;
  actionError.value = "";
  try {
    await approveCampaign(props.campaignId, campaign.value?.review_revision ?? "");
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
  // Without samples the previews use the fallback greeting, and approval still works.
  getSampleRecipients(props.campaignId).then((people) => (recipients.value = people), () => {});
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
      <p class="small" data-testid="review-total">
        <strong>{{ campaign.audience.total.toLocaleString("en-US") }}</strong> {{ campaign.audience.total === 1 ? "person matches" : "people match" }} these
        conditions. Of them, these can be messaged:
      </p>
      <div class="metrics">
        <MetricCard v-if="campaign.channels.includes('sms')" label="SMS" :value="campaign.audience.sms" hint="People who consented" />
        <MetricCard v-if="campaign.channels.includes('email')" label="Email" :value="campaign.audience.email" hint="People who consented" />
      </div>
      <p class="muted small">Consent is checked again just before each message, so the final count can be lower.</p>
    </section>

    <section v-if="campaign.content" class="section">
      <h2>What they read</h2>
      <VersionTabs v-if="groups.length > 1" :model-value="shownGroup.variant" label="Message version" @update:model-value="selectedVariant = $event" :options="groups.map((group) => ({ id: group.variant, name: group.label, seen: sideSeen(group) }))" test-prefix="review-version" />
      <VersionTabs v-if="shownGroup.entries.length > 1" :model-value="shownEntry?.language ?? ''" label="Message language" @update:model-value="selectedLanguage = $event" :options="languageTabs" test-prefix="review-language" />
      <div v-if="shownEntry" class="group" :data-testid="shownGroup.variant ? `review-variant-${shownGroup.variant}` : undefined">
        <p v-if="shownGroup.label" class="muted small" :data-testid="`people-${shownGroup.variant}`">{{ variantShare(campaign, shownGroup.variant) }}% · {{ variantPeople(campaign, shownGroup.variant) }}</p>
        <div class="version" :data-testid="shownEntry.language ? `review-${shownEntry.language}` : undefined">
          <p v-if="shownGroup.entries.length > 1" class="muted small">{{ (peopleIn(shownGroup.variant, shownEntry).sms + peopleIn(shownGroup.variant, shownEntry).email).toLocaleString("en-US") }} messages</p>
          <p v-if="'also' in shownEntry && shownEntry.also.length" class="muted small" :data-testid="`review-also-${shownEntry.language}`">Also read by those who read {{ listNames(shownEntry.also.map(languageName)) }}.</p>
          <article v-if="campaign.channels.includes('sms') && (shownEntry.sms || shownGroup.entries.length === 1)" class="message card" data-testid="review-sms">
            <h3 class="muted small">SMS</h3>
            <PhonePreview :message="shownEntry.version.sms" :recipients="shownRecipients" :language="shownEntry.language" :code="shownEntry.version.discount_code" :link="shownEntry.version.link" />
          </article>
          <article v-if="campaign.channels.includes('email') && (shownEntry.email || shownGroup.entries.length === 1)" class="message card" data-testid="review-email">
            <h3 class="muted small">Email</h3>
            <EmailPreview :message="shownEntry.version" :recipients="shownRecipients" :language="shownEntry.language" />
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

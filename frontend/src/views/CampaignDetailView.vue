<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

import {
  cancelSchedule,
  dispatchCampaign,
  errorMessage,
  getActivity,
  getCampaign,
  getProgress,
  resumeCampaign,
  getSampleRecipients,
  watchCampaign,
  type ActivityEntry,
  type Campaign,
  type SampleRecipient,
  type MessageField,
  type MessageVersion,
  type LiveState,
  type Progress,
} from "../api/campaigns";
import ActivityTimeline from "../components/ActivityTimeline.vue";
import AudienceReport from "../components/AudienceReport.vue";
import CampaignName from "../components/CampaignName.vue";
import CampaignStatusBadge from "../components/CampaignStatusBadge.vue";
import DeliveriesPanel from "../components/DeliveriesPanel.vue";
import DuplicateDialog from "../components/DuplicateDialog.vue";
import EmailPreview from "../components/EmailPreview.vue";
import DeliveryProgress from "../components/DeliveryProgress.vue";
import MetricCard from "../components/MetricCard.vue";
import OriginTag from "../components/OriginTag.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { firstMessagePath } from "../lib/campaignSteps";
import { isDraftStatus } from "../lib/campaignStatus";
import { formatDateTime } from "../lib/format";
import { languageName, languageVersions, recipientsFor } from "../lib/languages";
import PhonePreview from "../components/PhonePreview.vue";
import VersionTabs from "../components/VersionTabs.vue";
import { navigate } from "../lib/navigation";
import { sendingBlockers } from "../lib/systemHealth";
import { campaignDiscount, reachHint, uniqueWarnings, warningTitle } from "../lib/message";

const props = defineProps<{ campaignId: string }>();

type Tab = "overview" | "audience" | "content" | "deliveries" | "activity";

const campaign = ref<Campaign | null>(null);
const activity = ref<ActivityEntry[] | null>(null);
const error = ref("");
const activityError = ref("");
// In the address, so a refresh stays on the same tab. What this campaign has no tab for falls back once it has loaded.
const tab = ref<Tab>((new URLSearchParams(window.location.search).get("tab") as Tab | null) ?? "overview");
const recipients = ref<SampleRecipient[]>([]);
const progress = ref<Progress | null>(null);
const refreshKey = ref(0);
const acting = ref(false);
const duplicating = ref(false);
const actionError = ref("");
const disconnected = ref(false);
let stopWatching: (() => void) | null = null;

const tabs = computed(() => {
  const list: { id: Tab; label: string }[] = [
    { id: "overview", label: "Overview" },
    { id: "audience", label: "Audience" },
  ];
  if (campaign.value?.content) list.push({ id: "content", label: "Content" });
  if (progress.value?.total) list.push({ id: "deliveries", label: "Deliveries" });
  list.push({ id: "activity", label: "Activity" });
  return list;
});

function selectTab(id: Tab) {
  // The address also holds the open tab's own state, such as delivery filters.
  if (id === tab.value) return;
  tab.value = id;
  const path = window.location.pathname;
  window.history.replaceState({}, "", id === "overview" ? path : `${path}?tab=${id}`);
}

const isLive = computed(() => campaign.value?.status === "DISPATCHING");
const isDraft = computed(() => (campaign.value ? isDraftStatus(campaign.value.status) : false));
const editHref = computed(() => (campaign.value?.content ? firstMessagePath(props.campaignId, campaign.value.channels) : `/campaigns/${props.campaignId}/edit`));

function originOf(field: MessageField, version: MessageVersion) {
  return version.edited.includes(field) ? "user" : "ai";
}

// Every language someone reads, or the one version an older campaign has.
const versions = computed(() => {
  const current = campaign.value;
  if (!current?.content) return [];
  const test = Boolean(current.content.variant_b);
  return (test ? ["A", "B"] : ["A"]).flatMap((variant) => {
    const known = languageVersions(current, variant);
    const root = (variant === "B" ? current.content?.variant_b : current.content) as MessageVersion;
    const list = known.length > 1 ? known : [{ language: root.language ?? "", name: "", also: [] as string[], version: root }];
    return list.map((entry) => ({ ...entry, variant, name: [test && `Version ${variant}`, entry.name].filter(Boolean).join(" · ") }));
  });
});

const selectedVariant = ref("A");
const selectedLanguage = ref("");
const variantOptions = computed(() => [...new Set(versions.value.map((entry) => entry.variant))].map((variant) => ({ id: variant, name: `Version ${variant}` })));
const languageOptions = computed(() =>
  versions.value
    .filter((entry) => entry.variant === selectedVariant.value && entry.language)
    .map((entry) => ({ id: entry.language, name: languageName(entry.language) })),
);
const shownVersion = computed(() => {
  const side = versions.value.filter((entry) => entry.variant === selectedVariant.value);
  return side.find((entry) => entry.language === selectedLanguage.value) ?? side[0];
});
const shownRecipients = computed(() => {
  const entry = shownVersion.value;
  return entry ? recipientsFor(recipients.value, entry.language, entry.also) : [];
});

async function refresh() {
  const [updated, counts, entries] = await Promise.all([getCampaign(props.campaignId), getProgress(props.campaignId), getActivity(props.campaignId)]);
  campaign.value = updated;
  progress.value = counts;
  activity.value = entries;
  refreshKey.value++;
}

async function onLiveUpdate(state: LiveState) {
  const changed = state.status !== campaign.value?.status;
  progress.value = state.progress;
  refreshKey.value++;
  if (!changed) return;
  try {
    await refresh();
  } catch {
    // The status is still right, and the next change reads the rest again.
    if (campaign.value) campaign.value = { ...campaign.value, status: state.status };
  }
}

function watchIfChanging() {
  if (stopWatching || !["APPROVED", "SCHEDULED", "DISPATCHING", "PAUSED"].includes(campaign.value?.status ?? "")) return;
  stopWatching = watchCampaign(props.campaignId, {
    onUpdate: onLiveUpdate,
    onConnection: (connected) => (disconnected.value = !connected),
    onEnd: () => {
      stopWatching = null;
      disconnected.value = false;
    },
  });
}

async function act(action: () => Promise<unknown>) {
  acting.value = true;
  actionError.value = "";
  try {
    await action();
    await refresh();
    watchIfChanging();
  } catch (failure) {
    actionError.value = errorMessage(failure);
  } finally {
    acting.value = false;
  }
}

// The copy opens where its next step is: its messages are checked, or its audience found.
function onDuplicated(copy: Campaign) {
  duplicating.value = false;
  navigate(`/campaigns/${copy.id}/${copy.content ? "review" : "edit"}`);
}

function onRenamed(updated: Campaign) {
  campaign.value = updated;
  getActivity(props.campaignId).then((entries) => (activity.value = entries), () => {});
}

onMounted(async () => {
  // Without samples the previews use the fallback greeting.
  getSampleRecipients(props.campaignId).then((people) => (recipients.value = people), () => {});
  try {
    campaign.value = await getCampaign(props.campaignId);
  } catch (failure) {
    error.value = errorMessage(failure);
    return;
  }
  try {
    activity.value = await getActivity(props.campaignId);
  } catch (failure) {
    activityError.value = errorMessage(failure);
  }
  try {
    progress.value = await getProgress(props.campaignId);
  } catch {
    // The rest of the page is still useful without the counts.
  }
  if (!tabs.value.some((item) => item.id === tab.value)) selectTab("overview");
  watchIfChanging();
});

onBeforeUnmount(() => stopWatching?.());
</script>

<template>
  <ValidationNotice v-if="error" tone="error" title="We could not open this campaign">
    {{ error }} <a href="/campaigns">Back to campaigns</a>
  </ValidationNotice>
  <p v-else-if="!campaign" class="muted" role="status">Loading campaign...</p>

  <div v-else class="detail">
    <nav class="small muted" aria-label="Breadcrumb"><a href="/campaigns">Campaigns</a> / Campaign detail</nav>

    <header class="head">
      <div class="title">
        <CampaignName as="h1" :campaign="campaign" hide-origin @renamed="onRenamed" />
        <CampaignStatusBadge :status="campaign.status" />
      </div>
      <button class="button button--secondary" type="button" data-testid="duplicate" @click="duplicating = true">Duplicate</button>
      <DuplicateDialog v-if="duplicating" :campaign-id="campaignId" :campaign-name="campaign.name" @close="duplicating = false" @created="onDuplicated" />
      <a
        v-if="isDraft"
        class="button button--primary link-button"
        :href="editHref"
        data-testid="continue-editing"
      >
        Continue editing
      </a>
      <button v-else-if="campaign.status === 'PAUSED'" class="button button--primary" type="button" data-testid="resume" :disabled="acting" @click="act(() => resumeCampaign(campaignId))">Resume sending</button>
      <div v-else-if="campaign.status === 'APPROVED'" class="actions">
        <a class="button button--secondary link-button" :href="`/campaigns/${campaignId}/review`" data-testid="choose-time">Choose a time</a>
        <button class="button button--primary" type="button" data-testid="send-now" :disabled="acting" @click="act(() => dispatchCampaign(campaignId))">Send now</button>
      </div>
    </header>

    <div class="tabs" role="tablist" aria-label="Campaign sections">
      <button
        v-for="item in tabs"
        :id="`tab-${item.id}`"
        :key="item.id"
        type="button"
        role="tab"
        class="tab"
        :class="{ 'tab--active': tab === item.id }"
        :aria-selected="tab === item.id"
        :aria-controls="`panel-${item.id}`"
        @click="selectTab(item.id)"
      >
        {{ item.label }}
      </button>
    </div>

    <ValidationNotice v-if="actionError" tone="error" title="This did not go through">{{ actionError }}</ValidationNotice>
    <ValidationNotice v-if="disconnected" tone="warning" title="Live updates stopped" data-testid="live-disconnected">
      These numbers may be out of date. We keep trying to reconnect, and sending does not depend on this page.
    </ValidationNotice>
    <ValidationNotice v-if="campaign.status === 'PAUSED'" tone="warning" title="Sending stopped" data-testid="paused">
      The provider gave no way forward for a long time, so this campaign stopped trying. Nothing is lost and nobody gets a message twice. Resume when the provider is back.
    </ValidationNotice>
    <ValidationNotice v-if="isLive && sendingBlockers.length" tone="warning" title="Sending is held up" data-testid="sending-blocked">
      <span v-for="blocker in sendingBlockers" :key="blocker.id" class="blocker"><strong>{{ blocker.label }} is down.</strong> {{ blocker.effect }}</span>
      Nothing is lost, and sending continues when it is back.
    </ValidationNotice>

    <section v-if="tab === 'overview'" id="panel-overview" class="panel" role="tabpanel" aria-labelledby="tab-overview">
      <section class="brief card" aria-label="Campaign brief">
        <span class="muted small">Brief</span>
        <p data-testid="brief-text">{{ campaign.brief }}</p>
      </section>
      <section v-if="campaign.schedule" class="schedule card" data-testid="schedule-card">
        <div class="schedule-body">
          <span class="muted small">Schedule</span>
          <p data-testid="schedule-description">{{ campaign.schedule.description }}</p>
          <p v-if="campaign.schedule.next_run_at" class="muted small" data-testid="next-run">Next send {{ formatDateTime(campaign.schedule.next_run_at) }}</p>
          <p v-else class="muted small">No more sends are planned.</p>
          <p v-if="campaign.schedule.created_by" class="muted small" data-testid="schedule-by">Set up by {{ campaign.schedule.created_by }}</p>
          <p v-if="campaign.schedule.runs" class="muted small">Sent {{ campaign.schedule.runs }} {{ campaign.schedule.runs === 1 ? "time" : "times" }} so far. Nobody receives this message twice.</p>
        </div>
        <button v-if="campaign.schedule.next_run_at" class="button button--secondary" type="button" data-testid="cancel-schedule" :disabled="acting" @click="act(() => cancelSchedule(campaignId))">
          Cancel schedule
        </button>
      </section>
      <DeliveryProgress v-if="progress?.total" :progress="progress" :live="isLive" />
      <template v-if="campaign.proposal">
        <div class="metrics">
          <MetricCard label="Matching customers" :value="campaign.audience.total" />
          <MetricCard v-if="campaign.channels.includes('sms')" label="Reachable by SMS" :value="campaign.audience.sms" :hint="reachHint(campaign.audience.total, campaign.audience.sms, 'SMS')" />
          <MetricCard v-if="campaign.channels.includes('email')" label="Reachable by email" :value="campaign.audience.email" :hint="reachHint(campaign.audience.total, campaign.audience.email, 'email')" />
        </div>
      </template>
      <p v-else class="muted">This campaign has no audience yet.</p>
      <ValidationNotice v-for="blocker in campaign.blockers" :key="blocker" tone="warning" title="This blocks approval">
        {{ blocker }}
      </ValidationNotice>
      <dl class="facts card">
        <div><dt class="muted small">Created by</dt><dd data-testid="created-by">{{ campaign.created_by ?? "Not recorded" }}</dd></div>
        <div><dt class="muted small">Created</dt><dd>{{ formatDateTime(campaign.created_at) }}</dd></div>
        <div><dt class="muted small">Last updated</dt><dd>{{ formatDateTime(campaign.updated_at) }}</dd></div>
        <div v-if="campaign.tone" data-testid="campaign-tone"><dt class="muted small">Tone</dt><dd>{{ campaign.tone[0].toUpperCase() + campaign.tone.slice(1) }}</dd></div>
        <div v-if="campaign.offer_percent" data-testid="campaign-offer"><dt class="muted small">Discount</dt><dd>{{ campaign.offer_percent }}%</dd></div>
        <div v-if="campaign.approved_at" data-testid="approved-version"><dt class="muted small">Approved</dt><dd>Version {{ campaign.version }}, {{ formatDateTime(campaign.approved_at) }}</dd></div>
      </dl>
    </section>

    <section v-else-if="tab === 'audience'" id="panel-audience" class="panel" role="tabpanel" aria-labelledby="tab-audience">
      <AudienceReport v-if="campaign.proposal" :campaign="campaign" :progress="progress" />
      <p v-else class="muted">No audience conditions yet.</p>
    </section>

    <section v-else-if="tab === 'content' && campaign.content" id="panel-content" class="panel" role="tabpanel" aria-labelledby="tab-content">
      <p v-if="campaign.approved_at" class="muted small" data-testid="approved-note">Version {{ campaign.version }}, approved {{ formatDateTime(campaign.approved_at) }}. This is exactly what is sent, and it can no longer change.</p>
      <ValidationNotice v-for="warning in uniqueWarnings(campaign.content_warnings)" :key="warning.field + warning.reason" tone="warning" :title="warningTitle(warning)">
        {{ warning.message }}
      </ValidationNotice>
      <p v-if="campaignDiscount(campaign)" class="muted small" data-testid="content-discount">Discount in these messages: <strong>{{ campaignDiscount(campaign) }}</strong></p>
      <VersionTabs v-if="variantOptions.length > 1" v-model="selectedVariant" label="Message version" :options="variantOptions" test-prefix="content-variant" />
      <VersionTabs v-if="languageOptions.length > 1" :model-value="shownVersion?.language ?? ''" label="Message language" @update:model-value="selectedLanguage = $event" :options="languageOptions" test-prefix="content-language" />
      <template v-if="shownVersion">
        <article v-if="campaign.channels.includes('sms')" class="message card" data-testid="content-sms">
          <div class="message-head"><h2>SMS</h2><OriginTag :origin="originOf('sms', shownVersion.version)" /></div>
          <PhonePreview :message="shownVersion.version.sms" :recipients="shownRecipients" :language="shownVersion.language" />
        </article>
        <article v-if="campaign.channels.includes('email')" class="message card" data-testid="content-email">
          <div class="message-head"><h2>Email</h2><OriginTag :origin="originOf('email_body', shownVersion.version)" /></div>
          <EmailPreview :message="shownVersion.version" :recipients="shownRecipients" :language="shownVersion.language" />
        </article>
      </template>
    </section>

    <section v-else-if="tab === 'deliveries'" id="panel-deliveries" class="panel" role="tabpanel" aria-labelledby="tab-deliveries">
      <DeliveriesPanel :campaign-id="campaignId" :channels="campaign.channels" :ab-test="campaign.ab_test" :refresh-key="refreshKey" :progress="progress" />
    </section>

    <section v-else id="panel-activity" class="panel" role="tabpanel" aria-labelledby="tab-activity">
      <ValidationNotice v-if="activityError" tone="error" title="We could not load the activity">{{ activityError }}</ValidationNotice>
      <p v-else-if="!activity" class="muted" role="status">Loading activity...</p>
      <div v-else class="activity card"><ActivityTimeline :entries="activity" /></div>
    </section>
  </div>
</template>

<style scoped>
.detail {
  display: grid;
  gap: var(--space-6);
  max-width: 960px;
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}

.title {
  display: grid;
  flex: 1;
  gap: var(--space-2);
  justify-items: start;
  min-width: 240px;
}

.blocker {
  display: block;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.schedule {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-4);
}

.schedule-body {
  display: grid;
  gap: var(--space-1);
}

.brief {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-4);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}

.tabs {
  display: flex;
  gap: var(--space-4);
  overflow-x: auto;
  border-bottom: 1px solid var(--color-border);
}

.tab {
  min-height: 40px;
  padding: 0 var(--space-1);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-bottom: 2px solid transparent;
  font: inherit;
  cursor: pointer;
}

.tab--active {
  color: var(--color-primary);
  border-bottom-color: var(--color-primary);
  font-weight: 500;
}

.panel {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
}

.facts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-4) var(--space-8);
  margin: 0;
  padding: var(--space-4);
}

.facts dd {
  margin: 0;
}

.notes {
  margin: 0;
  padding-left: var(--space-4);
}

.message {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-4);
}

.message-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.message-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.activity {
  padding: var(--space-1) var(--space-4);
}
</style>

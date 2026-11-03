<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { errorMessage, getActivity, getCampaign, type ActivityEntry, type Campaign, type MessageField } from "../api/campaigns";
import ActivityTimeline from "../components/ActivityTimeline.vue";
import AudienceRule from "../components/AudienceRule.vue";
import CampaignName from "../components/CampaignName.vue";
import CampaignStatusBadge from "../components/CampaignStatusBadge.vue";
import MetricCard from "../components/MetricCard.vue";
import OriginTag from "../components/OriginTag.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { isDraftStatus } from "../lib/campaignStatus";
import { formatDateTime } from "../lib/format";

const props = defineProps<{ campaignId: string }>();

type Tab = "overview" | "audience" | "content" | "activity";

const campaign = ref<Campaign | null>(null);
const activity = ref<ActivityEntry[] | null>(null);
const error = ref("");
const activityError = ref("");
const tab = ref<Tab>("overview");

// Deliveries joins these tabs when that stage has real data.
const tabs = computed(() => {
  const list: { id: Tab; label: string }[] = [
    { id: "overview", label: "Overview" },
    { id: "audience", label: "Audience" },
  ];
  if (campaign.value?.content) list.push({ id: "content", label: "Content" });
  list.push({ id: "activity", label: "Activity" });
  return list;
});

const isDraft = computed(() => (campaign.value ? isDraftStatus(campaign.value.status) : false));
const editHref = computed(() => `/campaigns/${props.campaignId}/${campaign.value?.content ? "content" : "edit"}`);
const assumptions = computed(() => campaign.value?.proposal?.assumptions ?? []);

function isAssumed(field: string) {
  return assumptions.value.some((assumption) => assumption.field === field);
}

function originOf(field: MessageField) {
  return campaign.value?.content?.edited.includes(field) ? "user" : "ai";
}

function onRenamed(updated: Campaign) {
  campaign.value = updated;
  getActivity(props.campaignId).then((entries) => (activity.value = entries), () => {});
}

onMounted(async () => {
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
});
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
        <CampaignName as="h1" :campaign="campaign" @renamed="onRenamed" />
        <CampaignStatusBadge :status="campaign.status" />
      </div>
      <a
        v-if="isDraft"
        class="button button--primary link-button"
        :href="editHref"
        data-testid="continue-editing"
      >
        Continue editing
      </a>
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
        @click="tab = item.id"
      >
        {{ item.label }}
      </button>
    </div>

    <section v-if="tab === 'overview'" id="panel-overview" class="panel" role="tabpanel" aria-labelledby="tab-overview">
      <section class="brief card" aria-label="Campaign brief">
        <span class="muted small">Brief</span>
        <p data-testid="brief-text">{{ campaign.brief }}</p>
      </section>
      <template v-if="campaign.proposal">
        <div class="metrics">
          <MetricCard label="Matching customers" :value="campaign.audience.total" />
          <MetricCard label="Reachable by SMS" :value="campaign.audience.sms" hint="Consented to SMS" />
          <MetricCard label="Reachable by email" :value="campaign.audience.email" hint="Consented to email" />
        </div>
      </template>
      <p v-else class="muted">This campaign has no audience yet.</p>
      <ValidationNotice v-for="blocker in campaign.blockers" :key="blocker" tone="warning" title="This blocks approval">
        {{ blocker }}
      </ValidationNotice>
      <dl class="facts card">
        <div><dt class="muted small">Created</dt><dd>{{ formatDateTime(campaign.created_at) }}</dd></div>
        <div><dt class="muted small">Last updated</dt><dd>{{ formatDateTime(campaign.updated_at) }}</dd></div>
      </dl>
    </section>

    <section v-else-if="tab === 'audience'" id="panel-audience" class="panel" role="tabpanel" aria-labelledby="tab-audience">
      <p v-if="!campaign.conditions.length" class="muted">No audience conditions yet.</p>
      <AudienceRule
        v-for="condition in campaign.conditions"
        :key="condition.field"
        :label="condition.label"
        :value="condition.value"
        origin="ai"
        :assumed="isAssumed(condition.field)"
      />
      <ul v-if="assumptions.length" class="notes muted small">
        <li v-for="assumption in assumptions" :key="assumption.field">{{ assumption.note }}</li>
      </ul>
      <p v-if="campaign.audience_as_of" class="muted small">Counted on {{ formatDateTime(campaign.audience_as_of) }}.</p>
    </section>

    <section v-else-if="tab === 'content' && campaign.content" id="panel-content" class="panel" role="tabpanel" aria-labelledby="tab-content">
      <ValidationNotice v-for="warning in campaign.content_warnings" :key="warning.field + warning.reason" tone="warning" title="Differs from the brief">
        {{ warning.message }}
      </ValidationNotice>
      <article class="message card" data-testid="content-sms">
        <div class="message-head"><h2>SMS</h2><OriginTag :origin="originOf('sms')" /></div>
        <p class="message-text">{{ campaign.content.sms }}</p>
      </article>
      <article class="message card" data-testid="content-email">
        <div class="message-head"><h2>Email</h2><OriginTag :origin="originOf('email_body')" /></div>
        <p class="message-subject">{{ campaign.content.email_subject }}</p>
        <p class="message-text">{{ campaign.content.email_body }}</p>
      </article>
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

.message-subject {
  font-weight: 500;
}

.message-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.activity {
  padding: var(--space-1) var(--space-4);
}
</style>

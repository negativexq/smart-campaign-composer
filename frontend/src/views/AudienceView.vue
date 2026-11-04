<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { errorMessage, getCampaign, proposeSegment, setExcludedLanguages, type Campaign } from "../api/campaigns";
import AudienceFunnel from "../components/AudienceFunnel.vue";
import AudienceLoading from "../components/AudienceLoading.vue";
import AudienceSteps from "../components/AudienceSteps.vue";
import BriefReading from "../components/BriefReading.vue";
import SegmentRules from "../components/SegmentRules.vue";
import CampaignName from "../components/CampaignName.vue";
import LanguagePicker from "../components/LanguagePicker.vue";
import MetricCard from "../components/MetricCard.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { briefSegments } from "../lib/audience";
import { reachHint } from "../lib/message";
import { channelLabel, firstChannel, firstMessagePath } from "../lib/campaignSteps";
import { takeAudienceRequest } from "../lib/pendingAudience";
import { useAudienceBreakdown } from "../lib/useAudienceBreakdown";

const props = defineProps<{ campaignId: string }>();
const emit = defineEmits<{ status: [status: string]; channels: [channels: string[]] }>();

const campaign = ref<Campaign | null>(null);
const loadError = ref("");
const actionError = ref("");
const busyMessage = ref("");
const refinement = ref("");
const savingLanguages = ref(false);
const briefUpdatedBy = ref("");
const editingBrief = ref(false);
const editedBrief = ref("");
// The results build up in order only right after a search, not when a draft is reopened.
const staged = ref(false);

const { breakdown, failed: breakdownFailed } = useAudienceBreakdown(() => campaign.value);
const hasProposal = computed(() => Boolean(campaign.value?.proposal));
const parts = computed(() => campaign.value?.proposal?.brief_parts ?? []);
const segments = computed(() => briefSegments(campaign.value?.brief ?? "", parts.value));
const blocked = computed(() => Boolean(campaign.value?.blockers.length));
const waitingForCounts = computed(() => staged.value && !breakdown.value && !breakdownFailed.value);
const conditionCount = computed(() => campaign.value?.conditions.length ?? 0);
const funnelStart = computed(() => conditionCount.value * 300 + 200);
// The rules, then each bar, then the last bar finishing.
const restStart = computed(() => funnelStart.value + conditionCount.value * 700 + 1100);
const asOf = computed(() =>
  campaign.value?.audience_as_of
    ? new Date(campaign.value.audience_as_of).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" })
    : "",
);

async function propose(input: { brief?: string; refinement?: string }, message: string) {
  busyMessage.value = message;
  actionError.value = "";
  briefUpdatedBy.value = "";
  try {
    campaign.value = await proposeSegment(props.campaignId, input);
    staged.value = true;
    return true;
  } catch (failure) {
    actionError.value = errorMessage(failure);
    return false;
  } finally {
    busyMessage.value = "";
  }
}

async function refine() {
  const asked = refinement.value;
  const before = campaign.value?.brief;
  if (!(await propose({ refinement: asked }, "Updating the segment rule..."))) return;
  refinement.value = "";
  if (campaign.value?.brief !== before) briefUpdatedBy.value = asked;
}

async function chooseLanguages(excluded: string[]) {
  savingLanguages.value = true;
  actionError.value = "";
  try {
    campaign.value = await setExcludedLanguages(props.campaignId, excluded);
  } catch (failure) {
    actionError.value = errorMessage(failure);
  } finally {
    savingLanguages.value = false;
  }
}

function startEditingBrief() {
  editedBrief.value = campaign.value?.brief ?? "";
  editingBrief.value = true;
}

async function saveBrief() {
  if (await propose({ brief: editedBrief.value }, "Working out the segment rule...")) editingBrief.value = false;
}

watch(() => campaign.value?.status, (status) => status && emit("status", status));
watch(() => campaign.value?.channels, (channels) => channels && emit("channels", channels));

onMounted(async () => {
  try {
    campaign.value = await getCampaign(props.campaignId);
  } catch (failure) {
    loadError.value = errorMessage(failure);
    return;
  }
  if (takeAudienceRequest(props.campaignId)) await propose({}, "Working out the segment rule...");
});
</script>

<template>
  <ValidationNotice v-if="loadError" tone="error" title="We could not open this campaign">
    {{ loadError }} <a href="/">Start a new campaign</a>
  </ValidationNotice>
  <p v-else-if="!campaign" class="muted" role="status">Loading campaign...</p>

  <div v-else class="audience">
    <header class="intro">
      <h1>Check who will receive this.</h1>
      <p class="muted">Read the conditions first. The number only tells you how many people they match.</p>
    </header>

    <CampaignName as="h2" :campaign="campaign" @renamed="campaign = $event" />

    <section class="brief card" aria-label="Campaign brief">
      <template v-if="editingBrief && !busyMessage">
        <label class="small muted" for="edit-brief">Campaign brief</label>
        <textarea id="edit-brief" v-model="editedBrief" class="field" rows="4"></textarea>
        <div class="row">
          <button class="button button--primary" type="button" :disabled="Boolean(busyMessage) || !editedBrief.trim()" @click="saveBrief">
            Find audience again
          </button>
          <button class="button button--secondary" type="button" :disabled="Boolean(busyMessage)" @click="editingBrief = false">Cancel</button>
        </div>
      </template>
      <template v-else>
        <p v-if="editingBrief" data-testid="brief-text">{{ editedBrief }}</p>
        <p v-else data-testid="brief-text"><template v-for="(segment, index) in segments" :key="index"><mark v-if="segment.tone" class="part" :class="`part--${segment.tone}`">{{ segment.text }}</mark><template v-else>{{ segment.text }}</template></template></p>
        <p v-if="!editingBrief && parts.length" class="legend small muted" data-testid="brief-legend">
          <span><i class="dot dot--used"></i>In the rule</span>
          <span><i class="dot dot--missing"></i>Not in the rule</span>
          <span><i class="dot dot--other"></i>Not for the audience</span>
        </p>
        <p v-if="briefUpdatedBy" class="small muted" data-testid="brief-updated">Updated from your request: "{{ briefUpdatedBy }}"</p>
        <button class="button button--secondary" type="button" :disabled="Boolean(busyMessage)" @click="startEditingBrief">Edit brief</button>
      </template>
    </section>

    <ValidationNotice v-if="actionError" tone="error" :title="hasProposal ? 'The audience was not changed' : 'We could not find an audience'">{{ actionError }}</ValidationNotice>
    <AudienceLoading v-if="busyMessage || waitingForCounts" :message="busyMessage || 'Counting the audience...'" />

    <template v-else-if="hasProposal">
      <SegmentRules :campaign="campaign" :staged="staged" />
      <AudienceFunnel :campaign="campaign" :breakdown="breakdown" :failed="breakdownFailed" :staged="staged" :start-after="funnelStart" show-sources />

      <div class="rest" :class="{ 'rest--staged': staged }" :style="{ '--start': `${restStart}ms` }" data-testid="audience-rest">

      <ValidationNotice v-for="blocker in campaign.blockers" :key="blocker" tone="error" title="This blocks approval">
        {{ blocker }}
      </ValidationNotice>

      <ValidationNotice v-for="note in campaign.proposal?.not_applied ?? []" :key="note" tone="warning" title="Not done">
        {{ note }}
      </ValidationNotice>
      <ValidationNotice v-for="words in campaign.proposal?.unsure ?? []" :key="words" tone="warning" title="Check how this was read" data-testid="unsure">
        "{{ words }}". We could not confirm this part of the reading, so the rule may not mean what the brief says. You can still continue.
      </ValidationNotice>
      <ValidationNotice v-if="!parts.length && campaign.proposal?.not_audience?.length" tone="warning" title="Not used for the audience">
        {{ campaign.proposal.not_audience.join(", ") }}. If any of these should decide who receives this, refine the audience.
      </ValidationNotice>

      <BriefReading :campaign="campaign" />

      <section class="section">
        <h2>Audience size</h2>
        <div class="metrics">
          <MetricCard class="metrics-total" label="Matching customers" :value="campaign.audience.total" prominent />
          <MetricCard v-if="campaign.channels.includes('sms')" label="Reachable by SMS" :value="campaign.audience.sms" :hint="reachHint(campaign.audience.total, campaign.audience.sms, 'SMS')" />
          <MetricCard v-if="campaign.channels.includes('email')" label="Reachable by email" :value="campaign.audience.email" :hint="reachHint(campaign.audience.total, campaign.audience.email, 'email')" />
        </div>
        <p v-if="asOf" class="muted small" data-testid="as-of">Counted on {{ asOf }}. Customers without consent are never messaged.</p>
      </section>

      <LanguagePicker :campaign="campaign" :disabled="savingLanguages || Boolean(busyMessage)" @update="chooseLanguages" />

      <details class="card calculation" data-testid="calculation-details">
        <summary class="calculation-toggle">Show calculation details</summary>
        <AudienceSteps :campaign="campaign" :breakdown="breakdown" :failed="false" />
      </details>

      <form class="section" @submit.prevent="refine">
        <label for="refinement"><h2>Refine with AI</h2></label>
        <p v-if="campaign.content" class="muted small" data-testid="clears-content">Changing the audience clears the messages written for it.</p>
        <div class="row">
          <input
            id="refinement"
            v-model="refinement"
            class="field"
            placeholder="For example: make it 14 days, or remove the age condition"
          />
          <button class="button button--secondary" type="submit" :disabled="Boolean(busyMessage) || !refinement.trim()">Update</button>
        </div>
      </form>

      <div class="row">
        <a v-if="!blocked" class="button button--primary link-button" :href="firstMessagePath(campaignId, campaign.channels)" data-testid="continue-content">
          Continue to {{ channelLabel(firstChannel(campaign.channels)) }}
        </a>
        <template v-else>
          <button class="button button--primary" type="button" disabled>Continue to {{ channelLabel(firstChannel(campaign.channels)) }}</button>
          <span class="muted small" data-testid="continue-hint">Resolve the issues above to continue.</span>
        </template>
        <a class="exit small" :href="`/campaigns/${campaignId}`">Save and exit</a>
      </div>
      </div>
    </template>

    <section v-else class="section">
      <p class="muted">This campaign has no audience yet.</p>
      <div>
        <button class="button button--primary" type="button" :disabled="Boolean(busyMessage)" @click="propose({}, 'Finding your audience...')">
          Find audience
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.part {
  padding: 1px 3px;
  border-radius: 4px;
  color: inherit;
}

.part--used {
  background: color-mix(in srgb, var(--color-success) 12%, transparent);
}

.part--missing {
  border-bottom: 2px solid var(--color-error);
  background: color-mix(in srgb, var(--color-error) 8%, transparent);
}

.part--other {
  background: var(--color-surface-subtle);
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
}

.dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  margin-right: var(--space-1);
  border-radius: 3px;
}

.dot--used {
  background: color-mix(in srgb, var(--color-success) 30%, transparent);
}

.dot--missing {
  background: var(--color-error);
}

.dot--other {
  background: var(--color-border);
}

.rest {
  display: grid;
  gap: var(--space-6);
  min-width: 0;
}

.rest--staged {
  animation: rest-in 320ms ease-out both;
  animation-delay: var(--start);
}

@keyframes rest-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .rest--staged {
    animation: none;
  }
}

.calculation {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
}

.calculation-toggle {
  font-weight: 500;
  cursor: pointer;
}

.calculation[open] .calculation-toggle {
  margin-bottom: var(--space-3);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}

.exit {
  margin-left: auto;
}

.audience {
  display: grid;
  gap: var(--space-6);
  max-width: 720px;
}

.intro,
.section,
.brief {
  display: grid;
  gap: var(--space-3);
}

.brief {
  padding: var(--space-4);
  justify-items: start;
}

.brief textarea,
.brief p {
  width: 100%;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.row .field {
  flex: 1;
  min-width: 220px;
}

.notes {
  margin: 0;
  padding-left: var(--space-4);
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
}

.metrics-total {
  grid-column: 1 / -1;
}
</style>

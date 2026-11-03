<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { ApiError, getCampaign, proposeSegment, type Campaign } from "../api/campaigns";
import AudienceRule from "../components/AudienceRule.vue";
import MetricCard from "../components/MetricCard.vue";
import ValidationNotice from "../components/ValidationNotice.vue";

const props = defineProps<{ campaignId: string }>();

const campaign = ref<Campaign | null>(null);
const loadError = ref("");
const actionError = ref("");
const busyMessage = ref("");
const refinement = ref("");
const editingBrief = ref(false);
const editedBrief = ref("");

const assumptions = computed(() => campaign.value?.proposal?.assumptions ?? []);
const hasProposal = computed(() => Boolean(campaign.value?.proposal));
const continueHint = computed(() =>
  campaign.value?.blockers.length ? "Resolve the issues above to continue." : "Content generation is not available yet.",
);
const asOf = computed(() =>
  campaign.value?.audience_as_of
    ? new Date(campaign.value.audience_as_of).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" })
    : "",
);

function messageOf(failure: unknown) {
  return failure instanceof ApiError ? failure.message : "Something went wrong. Try again.";
}

function isAssumed(field: string) {
  return assumptions.value.some((assumption) => assumption.field === field);
}

async function propose(input: { brief?: string; refinement?: string }, message: string) {
  busyMessage.value = message;
  actionError.value = "";
  try {
    campaign.value = await proposeSegment(props.campaignId, input);
    return true;
  } catch (failure) {
    actionError.value = messageOf(failure);
    return false;
  } finally {
    busyMessage.value = "";
  }
}

async function refine() {
  if (await propose({ refinement: refinement.value }, "Updating the audience...")) refinement.value = "";
}

function startEditingBrief() {
  editedBrief.value = campaign.value?.brief ?? "";
  editingBrief.value = true;
}

async function saveBrief() {
  if (await propose({ brief: editedBrief.value }, "Finding your audience...")) editingBrief.value = false;
}

onMounted(async () => {
  try {
    campaign.value = await getCampaign(props.campaignId);
  } catch (failure) {
    loadError.value = messageOf(failure);
  }
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

    <section class="brief card" aria-label="Campaign brief">
      <template v-if="editingBrief">
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
        <p data-testid="brief-text">{{ campaign.brief }}</p>
        <button class="button button--secondary" type="button" :disabled="Boolean(busyMessage)" @click="startEditingBrief">Edit brief</button>
      </template>
    </section>

    <ValidationNotice v-if="actionError" tone="error" title="The audience was not changed">{{ actionError }}</ValidationNotice>
    <p v-if="busyMessage" class="muted" role="status">{{ busyMessage }}</p>

    <template v-if="hasProposal">
      <section class="section">
        <h2>Conditions</h2>
        <p v-if="!campaign.conditions.length" class="muted">No condition could be applied from this brief.</p>
        <AudienceRule
          v-for="condition in campaign.conditions"
          :key="condition.field"
          :label="condition.label"
          :value="condition.value"
          origin="ai"
          :assumed="isAssumed(condition.field)"
        />
        <ul v-if="assumptions.length" class="notes muted small" data-testid="assumption-notes">
          <li v-for="assumption in assumptions" :key="assumption.field">{{ assumption.note }}</li>
        </ul>
      </section>

      <ValidationNotice v-for="blocker in campaign.blockers" :key="blocker" tone="warning" title="This blocks approval">
        {{ blocker }}
      </ValidationNotice>

      <section class="section">
        <h2>Audience size</h2>
        <div class="metrics">
          <MetricCard class="metrics-total" label="Matching customers" :value="campaign.audience.total" prominent />
          <MetricCard label="Reachable by SMS" :value="campaign.audience.sms" hint="Consented to SMS" />
          <MetricCard label="Reachable by email" :value="campaign.audience.email" hint="Consented to email" />
        </div>
        <p v-if="asOf" class="muted small" data-testid="as-of">Counted on {{ asOf }}. Customers without consent are never messaged.</p>
      </section>

      <form class="section" @submit.prevent="refine">
        <label for="refinement"><h2>Refine with AI</h2></label>
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
        <button class="button button--primary" type="button" disabled>Continue to content</button>
        <span class="muted small" data-testid="continue-hint">{{ continueHint }}</span>
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
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.metrics-total {
  grid-column: 1 / -1;
}
</style>

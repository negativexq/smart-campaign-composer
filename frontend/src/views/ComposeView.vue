<script setup lang="ts">
import { computed, ref } from "vue";

import { createCampaign, errorMessage } from "../api/campaigns";
import BriefComposer from "../components/BriefComposer.vue";
import ChannelPicker from "../components/ChannelPicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { navigate } from "../lib/navigation";
import { requestAudience } from "../lib/pendingAudience";

const brief = ref("");
const channels = ref(["sms", "email"]);
const tone = ref("");
const hasOffer = ref(false);
const offer = ref<number | "">("");
const quietDays = ref("");
const busy = ref(false);
const error = ref("");

const suggestions = [
  "Send a 30% discount reminder to users in Istanbul who added to their cart in the last 30 days and did not buy. Keep it friendly and short.",
  "Tell Ankara customers who bought in the last 60 days about free shipping this weekend.",
];

const unavailableReason = computed(() => {
  if (busy.value) return "Finding your audience...";
  if (!channels.value.length) return "Choose at least one channel.";
  if (hasOffer.value && (offer.value === "" || !Number.isInteger(offer.value) || offer.value < 1 || offer.value > 100)) return "Enter a discount from 1 to 100.";
  return undefined;
});

// Says what is set while the options are closed, so nothing is applied unseen.
const optionsSummary = computed(() => {
  const chosen = [
    tone.value && tone.value[0].toUpperCase() + tone.value.slice(1),
    hasOffer.value && offer.value !== "" && `${offer.value}% discount`,
    quietDays.value && `Leaves out people messaged in the last ${quietDays.value} days`,
  ].filter(Boolean);
  return chosen.length ? chosen.join(" · ") : "Optional";
});

async function findAudience() {
  busy.value = true;
  error.value = "";
  try {
    const id = await createCampaign(brief.value, channels.value, {
      ...(tone.value ? { tone: tone.value } : {}),
      ...(hasOffer.value && offer.value !== "" ? { offer_percent: offer.value } : {}),
      ...(quietDays.value ? { not_messaged_within_days: Number(quietDays.value) } : {}),
    });
    requestAudience(id);
    navigate(`/campaigns/${id}/edit`);
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="compose">
    <header class="intro">
      <h1>Bring your next campaign to life.</h1>
      <p class="muted">From an idea to a ready-to-send campaign, with control at every step.</p>
    </header>

    <ValidationNotice v-if="error" class="error" tone="error" title="We could not save this campaign">{{ error }}</ValidationNotice>

    <BriefComposer
      v-model="brief"
      action-label="Find audience"
      :suggestions="suggestions"
      :unavailable-reason="unavailableReason"
      @submit="findAudience"
    >
      <ChannelPicker v-model="channels" :disabled="busy" />
      <details class="more" data-testid="campaign-options">
        <summary class="more-toggle">
          <span>Options</span>
          <span class="muted small" data-testid="options-summary">{{ optionsSummary }}</span>
        </summary>
        <div class="option-groups">
          <fieldset class="options" :disabled="busy" data-testid="message-options">
            <legend class="legend">The message</legend>
            <div class="option">
              <label class="small muted" for="campaign-tone">Tone</label>
              <select id="campaign-tone" v-model="tone" class="field control">
                <option value="">As the brief says</option>
                <option value="friendly">Friendly</option>
                <option value="formal">Formal</option>
                <option value="urgent">Urgent</option>
              </select>
            </div>
            <label class="check small" for="campaign-has-offer">
              <input id="campaign-has-offer" v-model="hasOffer" type="checkbox" />
              Add a discount
            </label>
            <div v-if="hasOffer" class="option">
              <label class="small muted" for="campaign-offer">Discount (%)</label>
              <input id="campaign-offer" v-model.number="offer" class="field control" type="number" min="1" max="100" step="1" />
            </div>
          </fieldset>
          <fieldset class="options" :disabled="busy" data-testid="audience-options">
            <legend class="legend">Who to leave out</legend>
            <div class="option">
              <label class="small muted" for="campaign-quiet">People messaged in the last</label>
              <select id="campaign-quiet" v-model="quietDays" class="field control">
                <option value="">Leave nobody out</option>
                <option value="3">3 days</option>
                <option value="7">7 days</option>
                <option value="14">14 days</option>
                <option value="30">30 days</option>
              </select>
            </div>
          </fieldset>
        </div>
      </details>
    </BriefComposer>
  </div>
</template>

<style scoped>
.more {
  margin-top: var(--space-2);
}

.more-toggle {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  padding: var(--space-1) 0;
  font-weight: 500;
  cursor: pointer;
}

.more-toggle::before {
  content: "›";
  display: inline-block;
  transition: transform 120ms ease;
}

.more[open] .more-toggle::before {
  transform: rotate(90deg);
}

.option-groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  align-items: start;
  gap: var(--space-4);
  margin-top: var(--space-2);
}

.options {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.check {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 24px;
  cursor: pointer;
}

.legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-weight: 500;
}

.option {
  display: grid;
  align-content: end;
  gap: var(--space-1);
  min-width: 0;
}

.control {
  width: 100%;
  height: 40px;
  padding-block: 0;
}

.compose {
  max-width: 720px;
}

.intro {
  display: grid;
  gap: var(--space-2);
  margin-bottom: var(--space-8);
}

.error {
  margin-bottom: var(--space-4);
}
</style>

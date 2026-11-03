<script setup lang="ts">
import { computed, ref } from "vue";

import { createCampaign, errorMessage, proposeSegment } from "../api/campaigns";
import BriefComposer from "../components/BriefComposer.vue";
import ChannelPicker from "../components/ChannelPicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { navigate } from "../lib/navigation";

const brief = ref("");
const channels = ref(["sms", "email"]);
const tone = ref("");
const offer = ref<number | "">("");
const quietDays = ref("");
const busy = ref(false);
const error = ref("");
// Kept across a failed attempt, so trying again does not create a second campaign.
const campaignId = ref("");

const suggestions = [
  "Send a 30% discount reminder to users in Istanbul who added to their cart in the last 30 days and did not buy. Keep it friendly and short.",
  "Tell Ankara customers who bought in the last 60 days about free shipping this weekend.",
];

const unavailableReason = computed(() => {
  if (busy.value) return "Finding your audience...";
  if (!channels.value.length) return "Choose at least one channel.";
  if (offer.value !== "" && (!Number.isInteger(offer.value) || offer.value < 1 || offer.value > 100)) return "Enter a discount from 1 to 100.";
  return undefined;
});

async function findAudience() {
  busy.value = true;
  error.value = "";
  try {
    if (campaignId.value) {
      await proposeSegment(campaignId.value, { brief: brief.value });
    } else {
      campaignId.value = await createCampaign(brief.value, channels.value, {
        ...(tone.value ? { tone: tone.value } : {}),
        ...(offer.value !== "" ? { offer_percent: offer.value } : {}),
        ...(quietDays.value ? { not_messaged_within_days: Number(quietDays.value) } : {}),
      });
      await proposeSegment(campaignId.value);
    }
    navigate(`/campaigns/${campaignId.value}/edit`);
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

    <ValidationNotice v-if="error" class="error" tone="error" title="We could not find an audience">{{ error }}</ValidationNotice>

    <BriefComposer
      v-model="brief"
      action-label="Find audience"
      :suggestions="suggestions"
      :unavailable-reason="unavailableReason"
      @submit="findAudience"
    >
      <ChannelPicker v-model="channels" :disabled="busy || Boolean(campaignId)" />
      <fieldset class="options" :disabled="busy || Boolean(campaignId)" data-testid="campaign-options">
        <legend class="legend">Options <span class="muted small">(optional)</span></legend>
        <div class="option-grid">
          <div class="option">
            <label class="small muted" for="campaign-tone">Tone</label>
            <select id="campaign-tone" v-model="tone" class="field control">
              <option value="">As the brief says</option>
              <option value="friendly">Friendly</option>
              <option value="formal">Formal</option>
              <option value="urgent">Urgent</option>
            </select>
          </div>
          <div class="option">
            <label class="small muted" for="campaign-offer">Discount (%)</label>
            <input id="campaign-offer" v-model.number="offer" class="field control" type="number" min="1" max="100" step="1" placeholder="As the brief says" />
          </div>
          <div class="option">
            <label class="small muted" for="campaign-quiet">Skip people messaged in the last</label>
            <select id="campaign-quiet" v-model="quietDays" class="field control">
              <option value="">Skip nobody</option>
              <option value="3">3 days</option>
              <option value="7">7 days</option>
              <option value="14">14 days</option>
              <option value="30">30 days</option>
            </select>
          </div>
        </div>
      </fieldset>
    </BriefComposer>
  </div>
</template>

<style scoped>
.options {
  min-width: 0;
  margin: var(--space-2) 0 0;
  padding: 0;
  border: 0;
}

.legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-weight: 500;
}

.option-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: var(--space-2);
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

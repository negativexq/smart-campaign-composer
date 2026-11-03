<script setup lang="ts">
import { computed, ref } from "vue";

import { ApiError, createCampaign, proposeSegment } from "../api/campaigns";
import BriefComposer from "../components/BriefComposer.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { navigate } from "../lib/navigation";

const brief = ref("");
const busy = ref(false);
const error = ref("");
// Kept across a failed attempt, so trying again does not create a second campaign.
let campaignId = "";

const suggestions = [
  "Send a 30% discount reminder to users in Istanbul who added to their cart in the last 30 days and did not buy. Keep it friendly and short.",
  "Tell Ankara customers who bought in the last 60 days about free shipping this weekend.",
];

const busyReason = computed(() => (busy.value ? "Finding your audience..." : undefined));

async function findAudience() {
  busy.value = true;
  error.value = "";
  try {
    if (campaignId) {
      await proposeSegment(campaignId, { brief: brief.value });
    } else {
      campaignId = await createCampaign(brief.value);
      await proposeSegment(campaignId);
    }
    navigate(`/campaigns/${campaignId}`);
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : "Something went wrong. Try again.";
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
      :unavailable-reason="busyReason"
      @submit="findAudience"
    />
  </div>
</template>

<style scoped>
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

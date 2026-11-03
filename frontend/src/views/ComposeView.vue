<script setup lang="ts">
import { ref } from "vue";

import BriefComposer from "../components/BriefComposer.vue";

const brief = ref("");

const suggestions = [
  "Send a 30% discount reminder to users in Istanbul who added to their cart in the last 30 days and did not buy. Keep it friendly and short.",
  "Tell Ankara customers who bought in the last 60 days about free shipping this weekend.",
];
</script>

<template>
  <div class="compose">
    <header class="intro">
      <h1>Bring your next campaign to life.</h1>
      <p class="muted">From an idea to a ready-to-send campaign, with control at every step.</p>
    </header>

    <BriefComposer
      v-model="brief"
      action-label="Find audience"
      :suggestions="suggestions"
      unavailable-reason="Audience generation is not available yet."
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
</style>

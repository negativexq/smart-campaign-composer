<script setup lang="ts">
import { onMounted, ref } from "vue";

import { errorMessage, getChannels, type ChannelUse } from "../api/campaigns";
import ChannelsCard from "../components/ChannelsCard.vue";
import SystemHealthCard from "../components/SystemHealthCard.vue";
import ValidationNotice from "../components/ValidationNotice.vue";

const channels = ref<ChannelUse[] | null>(null);
const error = ref("");

onMounted(async () => {
  try {
    channels.value = await getChannels();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
</script>

<template>
  <div class="channels-page">
    <header class="intro">
      <h1>See how each channel is doing.</h1>
      <p class="muted">Both channels send through the mock provider, so no real person gets a message. There is nothing to connect or change here.</p>
    </header>
    <ValidationNotice v-if="error" tone="error" title="We could not load the channels">{{ error }}</ValidationNotice>
    <p v-else-if="!channels" class="muted" role="status">Loading channels...</p>
    <ChannelsCard v-else :channels="channels" />
    <section class="section">
      <h2>System</h2>
      <SystemHealthCard />
    </section>
  </div>
</template>

<style scoped>
.channels-page,
.intro,
.section {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.channels-page {
  gap: var(--space-6);
}
</style>

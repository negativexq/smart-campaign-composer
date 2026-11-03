<script setup lang="ts">
import { onMounted, ref } from "vue";

import { fetchSystemStatus, type SystemStatus } from "./api/system";
import AppShell from "./components/AppShell.vue";
import CampaignStepper from "./components/CampaignStepper.vue";
import ComposeView from "./views/ComposeView.vue";
import DesignView from "./views/DesignView.vue";

const path = window.location.pathname;
const isDesign = path === "/design";
const status = ref<SystemStatus | "checking">("checking");

const steps = [
  { id: "brief", label: "Brief" },
  { id: "audience", label: "Audience" },
  { id: "content", label: "Content" },
  { id: "review", label: "Review" },
];

onMounted(async () => {
  status.value = await fetchSystemStatus();
});
</script>

<template>
  <AppShell v-if="isDesign" active-path="/design" title="Design system" :system-status="status">
    <DesignView />
  </AppShell>

  <AppShell v-else active-path="/" title="New campaign" campaign-status="Draft" :system-status="status">
    <template #topbar>
      <CampaignStepper :steps="steps" current-id="brief" />
    </template>

    <ComposeView />

    <template #panel>
      <section class="next card">
        <h3>What happens next</h3>
        <ol class="next-list">
          <li>AI suggests an audience from your brief.</li>
          <li>AI drafts your SMS and email.</li>
          <li>You edit and check everything.</li>
        </ol>
        <p class="muted small">Nothing is sent until you approve it.</p>
      </section>
    </template>
  </AppShell>
</template>

<style scoped>
.next {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
}

.next-list {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding-left: var(--space-4);
}
</style>

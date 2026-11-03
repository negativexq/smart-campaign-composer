<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { fetchAiConfig, type AiConfig } from "./api/campaigns";
import { fetchSystemStatus, type SystemStatus } from "./api/system";
import AiModeBadge from "./components/AiModeBadge.vue";
import AppShell from "./components/AppShell.vue";
import CampaignStepper from "./components/CampaignStepper.vue";
import { currentPath } from "./lib/navigation";
import AudienceView from "./views/AudienceView.vue";
import ComposeView from "./views/ComposeView.vue";
import DesignView from "./views/DesignView.vue";

const isDesign = computed(() => currentPath.value === "/design");
const campaignId = computed(() => currentPath.value.match(/^\/campaigns\/([^/]+)$/)?.[1] ?? "");
const status = ref<SystemStatus | "checking">("checking");
const aiConfig = ref<AiConfig | null>(null);

const steps = [
  { id: "brief", label: "Brief" },
  { id: "audience", label: "Audience" },
  { id: "content", label: "Content" },
  { id: "review", label: "Review" },
];

onMounted(async () => {
  [status.value, aiConfig.value] = await Promise.all([fetchSystemStatus(), fetchAiConfig()]);
});
</script>

<template>
  <AppShell v-if="isDesign" active-path="/design" title="Design system" :system-status="status">
    <DesignView />
  </AppShell>

  <AppShell v-else active-path="/" title="New campaign" campaign-status="Draft" :system-status="status">
    <template #topbar>
      <div class="topbar-actions">
        <CampaignStepper :steps="steps" :current-id="campaignId ? 'audience' : 'brief'" />
        <AiModeBadge v-if="aiConfig" :config="aiConfig" />
      </div>
    </template>

    <AudienceView v-if="campaignId" :key="campaignId" :campaign-id="campaignId" />
    <ComposeView v-else />

    <template #panel>
      <section v-if="campaignId" class="next card">
        <h3>How this audience was built</h3>
        <ul class="next-list">
          <li>AI turned your brief into the conditions shown here.</li>
          <li>Anything marked as an assumption was not in your brief.</li>
          <li>The system checks each condition and counts the matches itself.</li>
        </ul>
        <p class="muted small">Nothing is sent until you approve it.</p>
      </section>
      <section v-else class="next card">
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
.topbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-4);
}

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

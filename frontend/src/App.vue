<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { fetchAiConfig, type AiConfig } from "./api/campaigns";
import { fetchSession } from "./api/session";
import AiModeBadge from "./components/AiModeBadge.vue";
import AppShell from "./components/AppShell.vue";
import CampaignStepper from "./components/CampaignStepper.vue";
import { campaignStatusMeta } from "./lib/campaignStatus";
import { currentPath } from "./lib/navigation";
import { currentUser } from "./lib/session";
import { refreshSystemHealth, systemHealth } from "./lib/systemHealth";
import AudienceView from "./views/AudienceView.vue";
import CampaignDetailView from "./views/CampaignDetailView.vue";
import CampaignsView from "./views/CampaignsView.vue";
import ComposeView from "./views/ComposeView.vue";
import ContentView from "./views/ContentView.vue";
import DesignView from "./views/DesignView.vue";
import OverviewView from "./views/OverviewView.vue";
import ReviewView from "./views/ReviewView.vue";
import SignInView from "./views/SignInView.vue";

const route = computed(() => {
  const path = currentPath.value;
  if (path === "/") return { name: "overview", id: "" };
  if (path === "/design") return { name: "design", id: "" };
  if (path === "/campaigns") return { name: "campaigns", id: "" };
  if (path === "/campaigns/new") return { name: "new", id: "" };
  const edit = path.match(/^\/campaigns\/([^/]+)\/edit$/);
  if (edit) return { name: "edit", id: edit[1] };
  const content = path.match(/^\/campaigns\/([^/]+)\/content$/);
  if (content) return { name: "content", id: content[1] };
  const review = path.match(/^\/campaigns\/([^/]+)\/review$/);
  if (review) return { name: "review", id: review[1] };
  const detail = path.match(/^\/campaigns\/([^/]+)$/);
  if (detail) return { name: "detail", id: detail[1] };
  return { name: "not-found", id: "" };
});
const HEALTH_INTERVAL = 15000;
const status = computed(() => systemHealth.value?.status ?? "checking");
let healthTimer: ReturnType<typeof setInterval> | undefined;
const aiConfig = ref<AiConfig | null>(null);

const wizardStatus = ref("DRAFT");
watch(() => route.value.id, () => (wizardStatus.value = "DRAFT"));

const steps = [
  { id: "brief", label: "Brief" },
  { id: "audience", label: "Audience" },
  { id: "content", label: "Content" },
  { id: "review", label: "Review" },
];

onMounted(async () => {
  healthTimer = setInterval(refreshSystemHealth, HEALTH_INTERVAL);
  try {
    currentUser.value = await fetchSession();
  } catch {
    // The API is out of reach. Signing in says so when someone tries.
    currentUser.value = null;
  }
  [, aiConfig.value] = await Promise.all([refreshSystemHealth(), fetchAiConfig()]);
});

onBeforeUnmount(() => clearInterval(healthTimer));
</script>

<template>
  <p v-if="currentUser === undefined" class="muted" role="status">Loading...</p>
  <SignInView v-else-if="currentUser === null" />

  <AppShell v-else-if="route.name === 'design'" active-path="/design" title="Design system" :system-status="status">
    <DesignView />
  </AppShell>

  <AppShell v-else-if="route.name === 'overview'" active-path="/" title="Overview" :system-status="status">
    <template #topbar><AiModeBadge v-if="aiConfig" :config="aiConfig" /></template>
    <OverviewView />
  </AppShell>

  <AppShell v-else-if="route.name === 'campaigns'" active-path="/campaigns" title="Campaigns" :system-status="status">
    <template #topbar><AiModeBadge v-if="aiConfig" :config="aiConfig" /></template>
    <CampaignsView />
  </AppShell>

  <AppShell v-else-if="route.name === 'detail'" active-path="/campaigns" title="Campaign" :system-status="status">
    <template #topbar><AiModeBadge v-if="aiConfig" :config="aiConfig" /></template>
    <CampaignDetailView :key="route.id" :campaign-id="route.id" />
  </AppShell>

  <AppShell
    v-else-if="route.name === 'content' || route.name === 'review'"
    active-path="/campaigns"
    title="Campaign setup"
    :campaign-status="campaignStatusMeta(wizardStatus).label"
    :system-status="status"
  >
    <template #topbar>
      <div class="topbar-actions">
        <CampaignStepper :steps="steps" :current-id="route.name" />
        <AiModeBadge v-if="aiConfig" :config="aiConfig" />
      </div>
    </template>
    <ContentView v-if="route.name === 'content'" :key="route.id" :campaign-id="route.id" @status="wizardStatus = $event" />
    <ReviewView v-else :key="route.id" :campaign-id="route.id" @status="wizardStatus = $event" />
  </AppShell>

  <AppShell
    v-else-if="route.name === 'new' || route.name === 'edit'"
    active-path="/campaigns"
    :title="route.name === 'new' ? 'New campaign' : 'Campaign setup'"
    :campaign-status="campaignStatusMeta(wizardStatus).label"
    :system-status="status"
  >
    <template #topbar>
      <div class="topbar-actions">
        <CampaignStepper :steps="steps" :current-id="route.name === 'edit' ? 'audience' : 'brief'" />
        <AiModeBadge v-if="aiConfig" :config="aiConfig" />
      </div>
    </template>

    <AudienceView v-if="route.name === 'edit'" :key="route.id" :campaign-id="route.id" @status="wizardStatus = $event" />
    <ComposeView v-else />

    <template #panel>
      <section v-if="route.name === 'edit'" class="next card">
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

  <AppShell v-else active-path="" title="Page not found" :system-status="status">
    <h1>This page does not exist.</h1>
    <p class="muted"><a href="/">Back to overview</a></p>
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

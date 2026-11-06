<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { fetchAiConfig, type AiConfig } from "./api/campaigns";
import { fetchSession } from "./api/session";
import AiModeBadge from "./components/AiModeBadge.vue";
import AppShell from "./components/AppShell.vue";
import CampaignStepper from "./components/CampaignStepper.vue";
import { wizardSteps } from "./lib/campaignSteps";
import { campaignStatusMeta } from "./lib/campaignStatus";
import { currentPath } from "./lib/navigation";
import { currentUser } from "./lib/session";
import { refreshSystemHealth, systemHealth } from "./lib/systemHealth";
import { everyWhileVisible } from "./lib/visibility";
import AnalyticsView from "./views/AnalyticsView.vue";
import AudiencesView from "./views/AudiencesView.vue";
import AudienceView from "./views/AudienceView.vue";
import CampaignDetailView from "./views/CampaignDetailView.vue";
import CampaignsView from "./views/CampaignsView.vue";
import ChannelsView from "./views/ChannelsView.vue";
import HelpView from "./views/HelpView.vue";
import SettingsView from "./views/SettingsView.vue";
import ComposeView from "./views/ComposeView.vue";
import ContentView from "./views/ContentView.vue";
import DeliveryMonitorView from "./views/DeliveryMonitorView.vue";
import DesignView from "./views/DesignView.vue";
import OverviewView from "./views/OverviewView.vue";
import ReviewView from "./views/ReviewView.vue";
import SignInView from "./views/SignInView.vue";
import AssetsView from "./views/AssetsView.vue";
import BrandKitView from "./views/BrandKitView.vue";
import TemplateTabs from "./components/TemplateTabs.vue";
import TemplateBuilderView from "./views/TemplateBuilderView.vue";
import TemplatesView from "./views/TemplatesView.vue";

const route = computed(() => {
  const path = currentPath.value;
  if (path === "/") return { name: "overview", id: "" };
  if (path === "/design") return { name: "design", id: "" };
  if (path === "/settings") return { name: "settings", id: "" };
  if (path === "/help") return { name: "help", id: "" };
  if (path === "/audiences") return { name: "audiences", id: "" };
  if (path === "/analytics") return { name: "analytics", id: "" };
  if (path === "/channels") return { name: "channels", id: "" };
  if (path === "/delivery-monitor") return { name: "monitor", id: "" };
  if (path === "/templates" || path === "/templates/images" || path === "/templates/brand") return { name: "templates", id: "" };
  if (path === "/templates/new") return { name: "template-builder", id: "" };
  const design = path.match(/^\/templates\/([^/]+)\/edit$/);
  if (design) return { name: "template-builder", id: design[1] };
  if (path === "/campaigns") return { name: "campaigns", id: "" };
  if (path === "/campaigns/new") return { name: "new", id: "" };
  const edit = path.match(/^\/campaigns\/([^/]+)\/edit$/);
  if (edit) return { name: "edit", id: edit[1] };
  const message = path.match(/^\/campaigns\/([^/]+)\/(sms|email)$/);
  if (message) return { name: message[2], id: message[1] };
  const review = path.match(/^\/campaigns\/([^/]+)\/review$/);
  if (review) return { name: "review", id: review[1] };
  const detail = path.match(/^\/campaigns\/([^/]+)$/);
  if (detail) return { name: "detail", id: detail[1] };
  return { name: "not-found", id: "" };
});
const HEALTH_INTERVAL = 15000;
const status = computed(() => systemHealth.value?.status ?? "checking");
let stopHealth = () => {};
const aiConfig = ref<AiConfig | null>(null);

const wizardStatus = ref("DRAFT");
const wizardChannels = ref(["sms", "email"]);
const wizardSavedAudience = ref(false);
watch(() => route.value.id, () => {
  wizardStatus.value = "DRAFT";
  wizardChannels.value = ["sms", "email"];
  wizardSavedAudience.value = false;
});

const steps = computed(() => wizardSteps(wizardChannels.value));

onMounted(async () => {
  stopHealth = everyWhileVisible(refreshSystemHealth, HEALTH_INTERVAL);
  try {
    currentUser.value = await fetchSession();
  } catch {
    // The API is out of reach. Signing in says so when someone tries.
    currentUser.value = null;
  }
  [, aiConfig.value] = await Promise.all([refreshSystemHealth(), fetchAiConfig()]);
});

onBeforeUnmount(() => stopHealth());
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

  <AppShell v-else-if="route.name === 'settings'" active-path="/settings" title="Settings" :system-status="status">
    <SettingsView />
  </AppShell>

  <AppShell v-else-if="route.name === 'help'" active-path="/help" title="Help & documentation" :system-status="status">
    <HelpView />
  </AppShell>

  <AppShell v-else-if="route.name === 'audiences'" active-path="/audiences" title="Audiences" :system-status="status">
    <AudiencesView />
  </AppShell>

  <AppShell v-else-if="route.name === 'analytics'" active-path="/analytics" title="Analytics" :system-status="status">
    <AnalyticsView />
  </AppShell>

  <AppShell v-else-if="route.name === 'channels'" active-path="/channels" title="Channels" :system-status="status">
    <ChannelsView />
  </AppShell>

  <AppShell v-else-if="route.name === 'monitor'" active-path="/delivery-monitor" title="Delivery monitor" :system-status="status">
    <DeliveryMonitorView />
  </AppShell>

  <AppShell v-else-if="route.name === 'templates'" active-path="/templates" title="Templates" :system-status="status">
    <TemplateTabs :active="currentPath" />
    <AssetsView v-if="currentPath === '/templates/images'" />
    <BrandKitView v-else-if="currentPath === '/templates/brand'" />
    <TemplatesView v-else />
  </AppShell>

  <AppShell v-else-if="route.name === 'template-builder'" active-path="/templates" :title="route.id ? 'Edit template' : 'New template'" :system-status="status">
    <TemplateBuilderView :key="route.id" :design-id="route.id || undefined" />
  </AppShell>

  <AppShell v-else-if="route.name === 'detail'" active-path="/campaigns" title="Campaign" :system-status="status">
    <template #topbar><AiModeBadge v-if="aiConfig" :config="aiConfig" /></template>
    <CampaignDetailView :key="route.id" :campaign-id="route.id" />
  </AppShell>

  <AppShell
    v-else-if="route.name === 'sms' || route.name === 'email' || route.name === 'review'"
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
    <ContentView
      v-if="route.name === 'sms' || route.name === 'email'"
      :key="`${route.id} ${route.name}`"
      :campaign-id="route.id"
      :channel="route.name"
      @status="wizardStatus = $event"
      @channels="wizardChannels = $event"
    />
    <ReviewView v-else :key="route.id" :campaign-id="route.id" @status="wizardStatus = $event" @channels="wizardChannels = $event" />
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

    <AudienceView v-if="route.name === 'edit'" :key="route.id" :campaign-id="route.id" @status="wizardStatus = $event" @channels="wizardChannels = $event" @saved-audience="wizardSavedAudience = $event" />
    <ComposeView v-else @saved-audience="wizardSavedAudience = $event" />

    <template #panel>
      <section v-if="route.name === 'edit'" class="next card">
        <h3>How this audience was built</h3>
        <ul v-if="wizardSavedAudience" class="next-list">
          <li>These conditions come from your saved audience. AI did not read them from the brief.</li>
          <li>The system counted the matches again just now.</li>
          <li>Refining with AI replaces them on this campaign only.</li>
        </ul>
        <ul v-else class="next-list">
          <li>AI turned your brief into the conditions shown here.</li>
          <li>Anything marked as an assumption was not in your brief.</li>
          <li>The system checks each condition and counts the matches itself.</li>
        </ul>
        <p class="muted small">Nothing is sent until you approve it.</p>
      </section>
      <section v-else class="next card">
        <h3>What happens next</h3>
        <ol class="next-list">
          <li>{{ wizardSavedAudience ? "You check your saved audience, counted again." : "AI suggests an audience from your brief." }}</li>
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

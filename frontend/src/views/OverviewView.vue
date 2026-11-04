<script setup lang="ts">
import { onMounted, ref } from "vue";

import { errorMessage, getOverview, type Overview } from "../api/campaigns";
import ActivityTimeline from "../components/ActivityTimeline.vue";
import CampaignTable from "../components/CampaignTable.vue";
import ChannelsCard from "../components/ChannelsCard.vue";
import MetricCard from "../components/MetricCard.vue";
import SystemHealthCard from "../components/SystemHealthCard.vue";
import ValidationNotice from "../components/ValidationNotice.vue";

const overview = ref<Overview | null>(null);
const error = ref("");

onMounted(async () => {
  try {
    overview.value = await getOverview();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
</script>

<template>
  <ValidationNotice v-if="error" tone="error" title="We could not load your workspace">{{ error }}</ValidationNotice>
  <p v-else-if="!overview" class="muted" role="status">Loading your workspace...</p>

  <div v-else class="overview">
    <header class="intro">
      <h1>Your campaign workspace at a glance.</h1>
    </header>

    <section v-if="overview.counts.total === 0" class="empty card" data-testid="empty-workspace">
      <h2>No campaigns yet</h2>
      <p class="muted">Describe who you want to reach, and you will check the audience and content before anything is sent.</p>
      <div><a class="button button--primary link-button" href="/campaigns/new">Create your first campaign</a></div>
    </section>

    <template v-else>
      <section class="metrics" aria-label="Campaign totals">
        <MetricCard label="Total campaigns" :value="overview.counts.total" />
        <MetricCard label="Active" :value="overview.counts.active" hint="Approved, scheduled or sending" />
        <MetricCard label="Completed" :value="overview.counts.completed" hint="Finished without failures" />
        <MetricCard label="Drafts" :value="overview.counts.drafts" hint="Not approved yet" />
        <MetricCard label="Needs attention" :value="overview.counts.failed" hint="Failed or partly failed" />
      </section>

      <section class="section">
        <div class="section-head">
          <h2>Recent campaigns</h2>
          <a class="small" href="/campaigns">View all</a>
        </div>
        <CampaignTable :campaigns="overview.recent_campaigns" />
      </section>

      <section class="section">
        <h2>Recent activity</h2>
        <div class="activity card">
          <ActivityTimeline :entries="overview.recent_activity" show-campaign />
        </div>
      </section>
    </template>

    <section class="section">
      <h2>System</h2>
      <SystemHealthCard />
      <ChannelsCard v-if="overview.channels?.length" :channels="overview.channels" />
    </section>
  </div>
</template>

<style scoped>
.overview {
  display: grid;
  gap: var(--space-8);
}

.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
}

.section {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

.activity {
  padding: var(--space-1) var(--space-4);
}

.empty {
  display: grid;
  gap: var(--space-3);
  max-width: 560px;
  padding: var(--space-6);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}
</style>

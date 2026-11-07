<script setup lang="ts">
import { X } from "lucide-vue-next";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import type { CalendarEntry, CampaignSummary } from "../api/campaigns";
import { ENTRY_LABELS, hasFailures } from "../lib/calendar";
import { kindOf, runDetail, runResult } from "../lib/campaignRow";
import { formatDateTime } from "../lib/format";
import CampaignStatusBadge from "./CampaignStatusBadge.vue";
import ChannelIcons from "./ChannelIcons.vue";

const props = defineProps<{
  entry: CalendarEntry;
  campaign: CampaignSummary;
}>();
const emit = defineEmits<{ close: [] }>();

const heading = ref<HTMLElement | null>(null);
// Focus goes back where it came from when the panel closes.
const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;

const count = (value: number) => value.toLocaleString("en-US");

// What the picked entry is, in one sentence.
const moment = computed(() => {
  const { entry } = props;
  const when = formatDateTime(entry.at);
  if (entry.kind === "draft") return `Draft, last changed ${when}`;
  if (entry.kind === "scheduled") return `Scheduled to send ${when}`;
  const run = entry.run;
  if (!run) return `${ENTRY_LABELS[entry.kind]} ${when}`;
  if (entry.kind === "sending") return `Run #${run.number} is sending: ${count(run.processed)} of ${count(run.total)} done`;
  return `Run #${run.number} started ${formatDateTime(run.started_at)}`;
});

const runCounts = computed(() => {
  const run = props.entry.run;
  if (!run || !run.finished_at) return [];
  return [
    { label: "Accepted", value: run.sent },
    { label: "Failed", value: run.failed },
    { label: "Skipped", value: run.skipped },
    { label: "Outcome unknown", value: run.unknown },
  ].filter((item) => item.value || item.label === "Accepted");
});

function closeOnEscape(event: KeyboardEvent) {
  if (event.key === "Escape") emit("close");
}

onMounted(() => {
  document.addEventListener("keydown", closeOnEscape);
  heading.value?.focus({ preventScroll: true });
});
// Picking another entry while open moves focus to the panel again.
watch(
  () => props.entry,
  () => nextTick(() => heading.value?.focus({ preventScroll: true })),
);
onBeforeUnmount(() => {
  document.removeEventListener("keydown", closeOnEscape);
  opener?.focus({ preventScroll: true });
});
</script>

<template>
  <aside class="panel" role="complementary" aria-labelledby="campaign-panel-title" data-testid="campaign-panel">
    <header class="panel-header">
      <h2 id="campaign-panel-title" ref="heading" class="panel-title" tabindex="-1">
        {{ campaign.name }}
      </h2>
      <button type="button" class="close" aria-label="Close details" data-testid="panel-close" @click="emit('close')">
        <X :size="18" aria-hidden="true" />
      </button>
    </header>

    <div class="panel-body">
      <div class="badges">
        <CampaignStatusBadge :status="campaign.status" :schedule="campaign.schedule ?? null" :last-run="campaign.last_run ?? null" />
        <ChannelIcons :channels="campaign.channels" labels />
      </div>

      <p class="moment" :class="{ 'moment--warning': hasFailures(entry) }" data-testid="panel-moment">
        {{ moment }}
      </p>

      <dl v-if="runCounts.length" class="counts">
        <div v-for="item in runCounts" :key="item.label" class="count">
          <dt class="small muted">{{ item.label }}</dt>
          <dd>{{ count(item.value) }}</dd>
        </div>
      </dl>

      <dl class="facts">
        <div>
          <dt class="small muted">Sends</dt>
          <dd>{{ kindOf(campaign) }}</dd>
        </div>
        <div>
          <dt class="small muted">Next send</dt>
          <dd>
            {{ campaign.schedule?.next_run_at ? formatDateTime(campaign.schedule.next_run_at) : "None planned" }}
          </dd>
        </div>
        <div>
          <dt class="small muted">Last send</dt>
          <dd v-if="campaign.last_run">
            {{ runResult(campaign.last_run) }}
            <span class="small muted">{{ runDetail(campaign, campaign.last_run) }}</span>
          </dd>
          <dd v-else>Not sent yet</dd>
        </div>
      </dl>

      <section class="messages" aria-labelledby="panel-messages">
        <h3 id="panel-messages" class="section-title">Messages</h3>
        <template v-if="campaign.preview && (campaign.preview.sms || campaign.preview.email_subject)">
          <div v-if="campaign.channels.includes('sms') && campaign.preview.sms" class="message" data-testid="panel-sms">
            <span class="message-label small muted">SMS</span>
            <p class="message-text">{{ campaign.preview.sms }}</p>
          </div>
          <div v-if="campaign.channels.includes('email') && campaign.preview.email_subject" class="message" data-testid="panel-subject">
            <span class="message-label small muted">Email subject</span>
            <p class="message-text">{{ campaign.preview.email_subject }}</p>
          </div>
        </template>
        <p v-else class="small muted">No messages written yet.</p>
      </section>

      <div v-if="campaign.tags?.length" class="tags">
        <span v-for="name in campaign.tags" :key="name" class="tag small">{{ name }}</span>
      </div>
    </div>

    <footer class="panel-footer">
      <a class="button button--primary open" :href="`/campaigns/${campaign.id}`" data-testid="panel-open">Open campaign</a>
    </footer>
  </aside>
</template>

<style scoped>
.panel {
  position: fixed;
  top: var(--topbar-height);
  right: 0;
  bottom: 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  width: min(var(--panel-width), 100vw);
  background: var(--color-surface);
  border-left: 1px solid var(--color-border);
  box-shadow: var(--shadow-overlay);
  animation: slide-in var(--transition-standard);
}

@keyframes slide-in {
  from {
    transform: translateX(16px);
    opacity: 0;
  }
}

.panel-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.panel-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.panel-title:focus {
  outline: none;
}

.panel-title:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.close {
  display: inline-grid;
  flex: none;
  place-items: center;
  width: 32px;
  height: 32px;
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.close:hover {
  color: var(--color-text);
  background: var(--color-surface-subtle);
}

.panel-body {
  display: grid;
  flex: 1;
  align-content: start;
  gap: var(--space-4);
  padding: var(--space-4);
  overflow-y: auto;
}

.badges {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.moment {
  margin: 0;
  font-weight: 500;
}

.moment--warning {
  color: var(--color-warning);
}

.counts {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-2);
  margin: 0;
}

.count {
  padding: var(--space-2) var(--space-3);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.count dd {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.facts {
  display: grid;
  gap: var(--space-3);
  margin: 0;
}

.facts dd {
  display: grid;
  margin: 2px 0 0;
}

.section-title {
  margin: 0 0 var(--space-2);
  font-size: 14px;
  font-weight: 600;
}

.messages {
  display: grid;
  gap: var(--space-2);
}

.message {
  padding: var(--space-3);
  background: var(--color-canvas);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
}

.message-text {
  margin: var(--space-1) 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.tag {
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: 999px;
}

.panel-footer {
  padding: var(--space-4);
  border-top: 1px solid var(--color-border);
}

.open {
  display: flex;
  justify-content: center;
  text-decoration: none;
}

@media (prefers-reduced-motion: reduce) {
  .panel {
    animation: none;
  }
}
</style>

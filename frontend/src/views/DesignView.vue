<script setup lang="ts">
import { ref } from "vue";

import ApprovalSummary from "../components/ApprovalSummary.vue";
import AudienceRule from "../components/AudienceRule.vue";
import CampaignStepper from "../components/CampaignStepper.vue";
import DeliveryTimeline from "../components/DeliveryTimeline.vue";
import MessageEditor from "../components/MessageEditor.vue";
import MetricCard from "../components/MetricCard.vue";
import PhonePreview from "../components/PhonePreview.vue";
import StatusBadge from "../components/StatusBadge.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { DELIVERY_STATUSES } from "../lib/deliveryStatus";

const sms = ref("Hi {{first_name}}, your cart is waiting. Enjoy 30% off today!");
const tooLong = ref("Hi {{first_name}}, " + "your cart is waiting and this message keeps going. ".repeat(4));
const subject = ref("{{first_name}}, your 30% off is waiting");

const steps = [
  { id: "brief", label: "Brief" },
  { id: "audience", label: "Audience" },
  { id: "content", label: "Content" },
  { id: "review", label: "Review" },
];

const recipients = [
  { id: 1, label: "Ayşe", first_name: "Ayşe" },
  { id: 2, label: "Long name: Maximiliano Alexander", first_name: "Maximiliano Alexander" },
  { id: 3, label: "No first name on file", first_name: "" },
];

const events = [
  { id: "1", status: "SENT" as const, title: "SMS to user 1,204", at: "12:04:31" },
  { id: "2", status: "RETRYING" as const, title: "Email to user 882", detail: "Provider returned 429. Next attempt in 4 seconds.", at: "12:04:30" },
  { id: "3", status: "SKIPPED" as const, title: "SMS to user 319", detail: "Consent was withdrawn before sending.", at: "12:04:28" },
  { id: "4", status: "UNKNOWN" as const, title: "Email to user 77", detail: "All attempts timed out.", at: "12:04:12" },
];
</script>

<template>
  <div class="design">
    <header class="intro">
      <h1>Signal design system</h1>
      <p class="muted">Calm, precise, human-controlled.</p>
    </header>

    <ValidationNotice tone="info" title="Sample data">
      Every value on this page is made up to show how the components behave. Nothing here comes from a campaign.
    </ValidationNotice>

    <section class="section">
      <h2>Campaign steps</h2>
      <CampaignStepper :steps="steps" current-id="content" />
    </section>

    <section class="section">
      <h2>Audience</h2>
      <div class="stack">
        <AudienceRule label="City is" value="İstanbul" origin="ai" />
        <AudienceRule label="Added to cart in the last" value="30 days" origin="ai" assumed />
        <AudienceRule label="Purchased after adding to cart" value="No" origin="user" />
      </div>
      <div class="audience-metrics">
        <MetricCard label="Matching customers" :value="1284" prominent />
        <MetricCard label="Reachable by SMS" :value="902" />
        <MetricCard label="Reachable by email" :value="1036" />
      </div>
      <ValidationNotice tone="warning" title="One condition could not be used">
        The brief mentions age, but there is no age data. Remove it from the brief or accept the audience without it.
      </ValidationNotice>
    </section>

    <section class="section">
      <h2>Content</h2>
      <div class="content-grid">
        <div class="stack">
          <MessageEditor v-model="sms" label="SMS" :limit="160" state="validated" origin="ai" />
          <MessageEditor v-model="subject" label="Email subject" :limit="80" :rows="2" state="streaming" origin="ai" />
          <MessageEditor v-model="tooLong" label="SMS over the limit" :limit="160" state="draft" origin="user" />
        </div>
        <PhonePreview :message="sms" :recipients="recipients" />
      </div>
    </section>

    <section class="section">
      <h2>Notices</h2>
      <div class="stack">
        <ValidationNotice tone="success" title="Content validated">The backend confirmed all channel rules.</ValidationNotice>
        <ValidationNotice tone="error" title="SMS is 12 characters too long">Shorten the message to continue.</ValidationNotice>
      </div>
    </section>

    <section class="section">
      <h2>Approval</h2>
      <div class="approval-grid">
        <ApprovalSummary :sms-count="902" :email-count="1036" :conditions="['City is İstanbul', 'Added to cart in the last 30 days']" />
        <ApprovalSummary :sms-count="0" :email-count="0" :conditions="['City is Trabzon']" />
      </div>
    </section>

    <section class="section">
      <h2>Monitor</h2>
      <div class="metric-grid">
        <MetricCard label="Accepted" :value="1420" hint="By the mock provider" />
        <MetricCard label="In progress" :value="380" hint="Queued, sending or retrying" />
        <MetricCard label="Needs attention" :value="27" hint="Failed or outcome unknown" />
        <MetricCard label="Skipped" :value="111" hint="Consent withdrawn" />
      </div>
      <div class="badges">
        <StatusBadge v-for="status in DELIVERY_STATUSES" :key="status" :status="status" />
      </div>
      <DeliveryTimeline :events="events" />
    </section>
  </div>
</template>

<style scoped>
.design {
  display: grid;
  gap: var(--space-8);
  max-width: 1040px;
}

.intro {
  display: grid;
  gap: var(--space-2);
}

.section,
.stack {
  display: grid;
  gap: var(--space-3);
}

.section {
  gap: var(--space-4);
}

.audience-metrics {
  display: grid;
  grid-template-columns: 2fr 1fr 1fr;
  gap: var(--space-3);
}

.content-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) var(--panel-width);
  gap: var(--space-6);
  align-items: start;
}

.approval-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
  align-items: start;
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.badges {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
}

@media (max-width: 1000px) {
  .content-grid,
  .approval-grid,
  .audience-metrics {
    grid-template-columns: minmax(0, 1fr);
  }

  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>

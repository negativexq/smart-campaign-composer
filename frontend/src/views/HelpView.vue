<script setup lang="ts">
import StatusBadge from "../components/StatusBadge.vue";
import { DELIVERY_STATUSES, deliveryStatusMeta } from "../lib/deliveryStatus";

const REPOSITORY = "https://github.com/negativexq/smart-campaign-composer";

const steps = [
  { title: "Write a brief", text: "Say who you want to reach and what to tell them, or start from a saved audience or an email design." },
  { title: "Check the audience", text: "AI turns the brief into conditions, and the backend checks them and counts the matches. Refine them until they say what you meant." },
  { title: "Check the messages", text: "AI writes the SMS and email for each language the audience reads. Edit anything you want." },
  { title: "Approve and send", text: "Nothing goes out until you approve it. Send now, or schedule it to run once or repeat." },
  { title: "Follow it", text: "The campaign page shows each message as it goes, and its report shows the results." },
];
const pages = [
  { href: "/audiences", name: "Audiences", text: "Rules you saved to use again, with their last count." },
  { href: "/templates", name: "Templates", text: "Make your own email from blocks or start from a ready design, and keep your images and brand kit." },
  { href: "/analytics", name: "Analytics", text: "Totals across campaigns, and which ones failed most." },
  { href: "/delivery-monitor", name: "Delivery monitor", text: "Every message waiting, retrying or stuck, across all campaigns." },
  { href: "/channels", name: "Channels", text: "What each channel did in the last hour, and system health." },
  { href: "/settings", name: "Settings", text: "How this workspace is set up." },
];
const questions = [
  { question: "Does accepted mean someone got the message?", answer: "No. It means the mock provider took it. Bounces and opens in a report are simulated, and no real person gets anything here." },
  { question: "Can someone get the same message twice?", answer: "A campaign has one delivery per person and channel, and every retry reuses the same key, so the provider can refuse a repeat." },
  { question: "Why did a campaign stop by itself?", answer: "If nothing finishes for 10 minutes, it pauses with its messages intact. Open it to resume once the problem is gone." },
  { question: "Why is someone in the audience but not sent a message?", answer: "They have not agreed to that channel, have unsubscribed, or were excluded by an option. These show as skipped." },
];
const documents = [
  { href: `${REPOSITORY}#readme`, name: "README", text: "How to run it, how sending behaves and how to test it." },
  { href: `${REPOSITORY}/blob/main/DECISIONS.md`, name: "Decisions", text: "The trade-offs behind the design." },
];
</script>

<template>
  <div class="help">
    <header class="intro">
      <h1>Learn how a campaign gets from a brief to a send.</h1>
    </header>

    <section class="section">
      <h2>The steps</h2>
      <ol class="card steps">
        <li v-for="step in steps" :key="step.title"><strong>{{ step.title }}</strong> <span class="muted">{{ step.text }}</span></li>
      </ol>
    </section>

    <section class="section">
      <h2>What each delivery status means</h2>
      <div class="card rows" data-testid="help-statuses">
        <div v-for="status in DELIVERY_STATUSES" :key="status" class="row">
          <span class="badge"><StatusBadge :status="status" /></span>
          <span class="muted">{{ deliveryStatusMeta[status].description }}</span>
        </div>
      </div>
    </section>

    <section class="section">
      <h2>Common questions</h2>
      <div class="card rows">
        <details v-for="item in questions" :key="item.question" class="row question">
          <summary>{{ item.question }}</summary>
          <p class="muted">{{ item.answer }}</p>
        </details>
      </div>
    </section>

    <section class="section">
      <h2>Where things are</h2>
      <div class="card rows">
        <div v-for="page in pages" :key="page.href" class="row">
          <a :href="page.href">{{ page.name }}</a>
          <span class="muted">{{ page.text }}</span>
        </div>
      </div>
    </section>

    <section class="section">
      <h2>Documentation</h2>
      <div class="card rows" data-testid="help-documents">
        <div v-for="document in documents" :key="document.href" class="row">
          <a :href="document.href" target="_blank" rel="noopener">{{ document.name }}</a>
          <span class="muted">{{ document.text }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.help,
.section {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.help {
  gap: var(--space-6);
  max-width: 760px;
}

h2,
p {
  margin: 0;
}

.steps {
  display: grid;
  gap: var(--space-3);
  margin: 0;
  padding: var(--space-4) var(--space-4) var(--space-4) var(--space-8);
}

.row {
  display: grid;
  grid-template-columns: 160px minmax(0, 1fr);
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.row:last-child {
  border-bottom: 0;
}

.question {
  display: block;
}

.question summary {
  cursor: pointer;
}

.question p {
  margin-top: var(--space-2);
}

@media (max-width: 560px) {
  .row {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-1);
  }
}
</style>

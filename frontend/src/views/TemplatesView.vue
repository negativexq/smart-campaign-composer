<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { errorMessage, getEmailTemplates, previewEmail, type EmailTemplateUse } from "../api/campaigns";
import ValidationNotice from "../components/ValidationNotice.vue";
import { EMAIL_TEMPLATES } from "../lib/emailTemplates";

// Only to show the design. Campaigns get their own words from the model.
const SAMPLE = {
  email_subject: "Your cart is still waiting",
  email_preheader: "Sample text",
  email_headline: "Still thinking it over?",
  email_body: "Hi {{first_name}},\n\nThis is sample text. Your campaign's own message goes here, written for its audience.",
  email_cta_label: "Back to your cart",
  email_cta_url: "https://example.com",
};

const uses = ref<EmailTemplateUse[] | null>(null);
const previews = ref<Record<string, string>>({});
const error = ref("");

const cards = computed(() =>
  EMAIL_TEMPLATES.map((template) => ({ ...template, use: uses.value?.find((item) => item.id === template.id) })),
);

onMounted(async () => {
  try {
    uses.value = await getEmailTemplates();
  } catch (failure) {
    error.value = errorMessage(failure);
    return;
  }
  // Rendered by the backend, so a card shows what people would actually receive.
  await Promise.all(
    EMAIL_TEMPLATES.map(async (template) => {
      const email = await previewEmail({ ...SAMPLE, email_template: template.id }, "Ayşe").catch(() => null);
      if (email) previews.value[template.id] = email.html;
    }),
  );
});
</script>

<template>
  <div class="templates">
    <header class="intro">
      <h1>Choose how your email looks.</h1>
      <p class="muted">Start a campaign from a design. The AI writes the words, and you check them before anything is sent.</p>
    </header>

    <ValidationNotice v-if="error" tone="error" title="We could not load the templates">{{ error }}</ValidationNotice>
    <p v-else-if="!uses" class="muted" role="status">Loading templates...</p>

    <template v-else>
      <section class="grid" aria-label="Email templates">
        <article v-for="card in cards" :key="card.id" class="template card" :data-testid="`template-${card.id}`">
          <div class="preview" aria-hidden="true">
            <iframe v-if="previews[card.id]" class="frame" title="" tabindex="-1" sandbox="" :srcdoc="previews[card.id]" data-testid="template-frame"></iframe>
          </div>
          <div class="body">
            <div class="head">
              <h2>{{ card.name }}</h2>
              <span class="channel small">Email</span>
            </div>
            <p class="muted small">{{ card.hint }}</p>
            <p class="muted small" data-testid="template-use">
              {{ card.use?.campaigns === 1 ? "Used by 1 campaign" : `Used by ${card.use?.campaigns ?? 0} campaigns` }}
              · version <span class="mono">{{ card.use?.version }}</span>
            </p>
            <a class="button button--secondary use" :href="`/campaigns/new?template=${card.id}`" data-testid="use-template">Use template</a>
          </div>
        </article>
      </section>
      <p class="muted small" data-testid="sms-note">SMS has no design to choose: the AI writes each text for its campaign, and you edit it before approval.</p>
    </template>
  </div>
</template>

<style scoped>
.templates {
  display: grid;
  gap: var(--space-6);
}

.intro {
  display: grid;
  gap: var(--space-2);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: var(--space-4);
}

.template {
  display: grid;
  grid-template-rows: auto 1fr;
  min-width: 0;
  overflow: hidden;
}

.preview {
  position: relative;
  height: 280px;
  overflow: hidden;
  background: var(--color-surface-subtle);
  border-bottom: 1px solid var(--color-border);
}

/* A whole email at reading size, drawn small. It cannot be scrolled or clicked. */
.frame {
  position: absolute;
  top: 0;
  left: 0;
  width: 640px;
  height: 700px;
  border: 0;
  pointer-events: none;
  transform: scale(0.45);
  transform-origin: top left;
}

.body {
  display: grid;
  align-content: start;
  gap: var(--space-2);
  padding: var(--space-4);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

h2,
.body p {
  margin: 0;
}

h2 {
  font-size: inherit;
  font-weight: 600;
}

.channel {
  padding: 2px var(--space-2);
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.use {
  justify-content: center;
  margin-top: var(--space-2);
  text-decoration: none;
}
</style>

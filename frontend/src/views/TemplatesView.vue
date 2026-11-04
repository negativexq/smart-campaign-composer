<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { deleteEmailDesign, errorMessage, getEmailDesigns, getEmailTemplates, previewEmail, type EmailTemplateUse, type SavedEmailDesign } from "../api/campaigns";
import ValidationNotice from "../components/ValidationNotice.vue";
import { customTemplateName, EMAIL_TEMPLATES, SAMPLE_EMAIL } from "../lib/emailTemplates";

const uses = ref<EmailTemplateUse[] | null>(null);
const designs = ref<SavedEmailDesign[]>([]);
const previews = ref<Record<string, string>>({});
const error = ref("");
const deleteError = ref("");
const confirming = ref("");
const deleting = ref(false);

const cards = computed(() =>
  EMAIL_TEMPLATES.map((template) => ({ ...template, use: uses.value?.find((item) => item.id === template.id) })),
);
const usedBy = (count = 0) => (count === 1 ? "Used by 1 campaign" : `Used by ${count} campaigns`);

// Rendered by the backend, so a card shows what people would actually receive.
async function drawPreview(id: string, fields: Parameters<typeof previewEmail>[0]) {
  const email = await previewEmail({ ...SAMPLE_EMAIL, ...fields }, "Ayşe").catch(() => null);
  if (email) previews.value[id] = email.html;
}

async function remove(id: string) {
  deleting.value = true;
  deleteError.value = "";
  try {
    await deleteEmailDesign(id);
    designs.value = designs.value.filter((design) => design.id !== id);
    confirming.value = "";
  } catch (failure) {
    deleteError.value = errorMessage(failure);
  } finally {
    deleting.value = false;
  }
}

onMounted(async () => {
  try {
    [uses.value, designs.value] = await Promise.all([getEmailTemplates(), getEmailDesigns()]);
  } catch (failure) {
    error.value = errorMessage(failure);
    return;
  }
  await Promise.all([
    ...EMAIL_TEMPLATES.map((template) => drawPreview(template.id, { email_template: template.id })),
    ...designs.value.map((design) => drawPreview(customTemplateName(design.id), { email_template: customTemplateName(design.id), email_design: design })),
  ]);
});
</script>

<template>
  <div class="templates">
    <header class="intro">
      <div class="intro-text">
        <h1>Choose how your email looks.</h1>
        <p class="muted">Design the layout yourself or start from a ready one. The AI writes the words for each campaign, and you check them before anything is sent.</p>
      </div>
      <a class="button button--primary link-button" href="/templates/new" data-testid="create-template">Create template</a>
    </header>

    <ValidationNotice v-if="error" tone="error" title="We could not load the templates">{{ error }}</ValidationNotice>
    <p v-else-if="!uses" class="muted" role="status">Loading templates...</p>

    <template v-else>
      <section class="section">
        <h2>Your templates</h2>
        <ValidationNotice v-if="deleteError" tone="error" title="The template was not deleted">{{ deleteError }}</ValidationNotice>
        <div v-if="!designs.length" class="empty card" data-testid="no-designs">
          <p class="muted">You have not made a template yet. Add your logo and images, arrange the blocks, and leave the words to each campaign.</p>
        </div>
        <div v-else class="grid" aria-label="Your email templates">
          <article v-for="design in designs" :key="design.id" class="template card" :data-testid="`design-${design.id}`">
            <div class="preview" aria-hidden="true">
              <iframe v-if="previews[customTemplateName(design.id)]" class="frame" title="" tabindex="-1" sandbox="allow-same-origin" :srcdoc="previews[customTemplateName(design.id)]" data-testid="template-frame"></iframe>
            </div>
            <div class="body">
              <h3>{{ design.name }}</h3>
              <p class="muted small" data-testid="template-use">{{ usedBy(design.campaigns) }} · version <span class="mono">{{ design.version }}</span></p>
              <a class="button button--secondary use" :href="`/campaigns/new?template=${customTemplateName(design.id)}`" data-testid="use-template">Use template</a>
              <div v-if="confirming === design.id" class="confirm">
                <p class="small">Delete this template? Campaigns already using it keep their copy.</p>
                <div class="row">
                  <button class="plain small danger" type="button" :disabled="deleting" data-testid="confirm-delete-design" @click="remove(design.id)">Delete</button>
                  <button class="plain small" type="button" :disabled="deleting" @click="confirming = ''">Keep it</button>
                </div>
              </div>
              <div v-else class="row">
                <a class="small" :href="`/templates/${design.id}/edit`" data-testid="edit-design">Edit</a>
                <button class="plain small" type="button" data-testid="delete-design" @click="confirming = design.id">Delete</button>
              </div>
            </div>
          </article>
        </div>
      </section>

      <section class="section">
        <h2>Ready to use</h2>
        <div class="grid" aria-label="Ready-made email templates">
          <article v-for="card in cards" :key="card.id" class="template card" :data-testid="`template-${card.id}`">
            <div class="preview" aria-hidden="true">
              <iframe v-if="previews[card.id]" class="frame" title="" tabindex="-1" sandbox="allow-same-origin" :srcdoc="previews[card.id]" data-testid="template-frame"></iframe>
            </div>
            <div class="body">
              <h3>{{ card.name }}</h3>
              <p class="muted small">{{ card.hint }}</p>
              <p class="muted small" data-testid="template-use">{{ usedBy(card.use?.campaigns) }} · version <span class="mono">{{ card.use?.version }}</span></p>
              <a class="button button--secondary use" :href="`/campaigns/new?template=${card.id}`" data-testid="use-template">Use template</a>
            </div>
          </article>
        </div>
      </section>
      <p class="muted small" data-testid="sms-note">Templates are for email. An SMS is plain text: the AI writes it for each campaign, and you edit it before approval.</p>
    </template>
  </div>
</template>

<style scoped>
.templates {
  display: grid;
  gap: var(--space-6);
}

.intro {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.intro-text,
.section {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  flex: 1 1 320px;
}

.section {
  gap: var(--space-3);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}

.empty {
  padding: var(--space-6);
}

.empty p {
  margin: 0;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.confirm {
  display: grid;
  gap: var(--space-2);
}

.plain {
  padding: 0;
  color: var(--color-text-muted);
  background: none;
  border: 0;
  font: inherit;
  text-decoration: underline;
  cursor: pointer;
}

.danger {
  color: var(--color-error);
  font-weight: 500;
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

h2,
h3,
.body p {
  margin: 0;
}

h3 {
  font-size: inherit;
  font-weight: 600;
}

.use {
  justify-content: center;
  margin-top: var(--space-2);
  text-decoration: none;
}
</style>

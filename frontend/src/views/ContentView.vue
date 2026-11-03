<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";

import {
  ApiError,
  errorMessage,
  generateContent,
  getCampaign,
  getSampleRecipients,
  saveContent,
  type Campaign,
  type ContentIssue,
  type MessageField,
  type SampleRecipient,
} from "../api/campaigns";
import EmailPreview from "../components/EmailPreview.vue";
import MessageEditor from "../components/MessageEditor.vue";
import PhonePreview from "../components/PhonePreview.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { isDraftStatus } from "../lib/campaignStatus";

const props = defineProps<{ campaignId: string }>();
const emit = defineEmits<{ status: [status: string] }>();

const campaign = ref<Campaign | null>(null);
const recipients = ref<SampleRecipient[]>([]);
const loadError = ref("");
const generationError = ref("");
const saveError = ref("");
const stopped = ref(false);
const generating = ref(false);
const saving = ref(false);
const issues = ref<ContentIssue[]>([]);
const instruction = ref("");
const messages = reactive({ sms: "", email_subject: "", email_body: "" });
let controller: AbortController | null = null;

const stored = computed(() => campaign.value?.content);
const dirty = computed(() =>
  stored.value ? (Object.keys(messages) as MessageField[]).some((field) => messages[field] !== stored.value?.[field]) : false,
);
const canGenerate = computed(() => !generating.value && !saving.value);

function fill(content: Campaign["content"]) {
  messages.sms = content?.sms ?? "";
  messages.email_subject = content?.email_subject ?? "";
  messages.email_body = content?.email_body ?? "";
}

function issueFor(field: MessageField) {
  return issues.value.find((issue) => issue.field === field)?.message;
}

function stateOf(field: MessageField) {
  if (generating.value) return "streaming";
  if (issueFor(field)) return "invalid";
  // Only what the backend accepted counts as validated.
  return stored.value && messages[field] === stored.value[field] ? "validated" : "draft";
}

function originOf(field: MessageField) {
  if (generating.value) return "ai";
  const changed = stored.value ? messages[field] !== stored.value[field] : false;
  return changed || stored.value?.edited.includes(field) ? "user" : "ai";
}

async function generate(input: { instruction?: string } = {}) {
  controller = new AbortController();
  const previous = stored.value;
  generating.value = true;
  generationError.value = "";
  saveError.value = "";
  stopped.value = false;
  issues.value = [];
  fill(undefined);
  try {
    campaign.value = await generateContent(
      props.campaignId,
      input,
      {
        onDelta: (field, text) => {
          if (field in messages) messages[field] += text;
        },
        onReset: () => fill(undefined),
      },
      controller.signal,
    );
    fill(campaign.value.content);
    instruction.value = "";
  } catch (failure) {
    // Nothing unvalidated stays on screen.
    fill(previous);
    if (controller?.signal.aborted) stopped.value = true;
    else generationError.value = errorMessage(failure);
  } finally {
    generating.value = false;
    controller = null;
  }
}

function stop() {
  controller?.abort();
}

async function save() {
  saving.value = true;
  saveError.value = "";
  issues.value = [];
  try {
    campaign.value = await saveContent(props.campaignId, { ...messages });
    fill(campaign.value.content);
  } catch (failure) {
    if (failure instanceof ApiError) issues.value = failure.issues;
    saveError.value = errorMessage(failure);
  } finally {
    saving.value = false;
  }
}

watch(() => campaign.value?.status, (status) => status && emit("status", status));
watch(generating, (active) => {
  if (active) emit("status", "GENERATING");
  else if (campaign.value) emit("status", campaign.value.status);
});

onMounted(async () => {
  try {
    [campaign.value, recipients.value] = await Promise.all([
      getCampaign(props.campaignId),
      getSampleRecipients(props.campaignId).catch(() => []),
    ]);
  } catch (failure) {
    loadError.value = errorMessage(failure);
    return;
  }
  const current = campaign.value;
  if (!current?.proposal || current.blockers.length || !(isDraftStatus(current.status) || current.status === "GENERATING")) return;
  if (current.content) fill(current.content);
  // A person who left after a failure decides for themselves when to try again.
  else if (current.status === "GENERATION_FAILED") generationError.value = "The last attempt to write these messages failed.";
  else await generate();
});

onBeforeUnmount(() => controller?.abort());
</script>

<template>
  <ValidationNotice v-if="loadError" tone="error" title="We could not open this campaign">
    {{ loadError }} <a href="/campaigns">Back to campaigns</a>
  </ValidationNotice>
  <p v-else-if="!campaign" class="muted" role="status">Loading campaign...</p>

  <ValidationNotice v-else-if="!isDraftStatus(campaign.status) && campaign.status !== 'GENERATING'" tone="info" title="These messages are approved">
    Approved messages can no longer change. <a :href="`/campaigns/${campaignId}`">Open the campaign</a>
  </ValidationNotice>

  <ValidationNotice v-else-if="!campaign.proposal || campaign.blockers.length" tone="warning" title="The audience comes first">
    Messages are written for a checked audience. <a :href="`/campaigns/${campaignId}/edit`">Go to the audience</a>
  </ValidationNotice>

  <div v-else class="content">
    <header class="intro">
      <h1>Check what they will read.</h1>
      <p class="muted" data-testid="audience-line">
        Written for {{ campaign.audience.total.toLocaleString("en-US") }} matching customers. Counts show the length for the longest name in this audience.
      </p>
    </header>

    <ValidationNotice v-if="generationError" tone="error" title="The messages were not written">
      {{ generationError }}
      <template v-if="stored"> Your current messages are unchanged.</template>
    </ValidationNotice>
    <ValidationNotice v-if="stopped" tone="info" title="Stopped">
      {{ stored ? "Your current messages are unchanged." : "No messages were saved." }}
    </ValidationNotice>
    <ValidationNotice v-if="saveError" tone="error" title="Your changes were not saved">{{ saveError }}</ValidationNotice>
    <template v-if="!generating && !dirty">
      <ValidationNotice v-for="warning in campaign.content_warnings" :key="warning.field + warning.reason" tone="warning" title="Differs from the brief">
        {{ warning.message }}
      </ValidationNotice>
    </template>

    <p v-if="generating" class="muted" role="status" data-testid="generating">Writing your messages. They are checked once they are complete.</p>

    <div class="grid">
      <div class="editors">
        <MessageEditor
          v-if="campaign.channels.includes('sms')"
          v-model="messages.sms"
          label="SMS"
          :limit="160"
          :rows="4"
          :state="stateOf('sms')"
          :origin="originOf('sms')"
          :longest-name="campaign.content_limits.sms_name_length"
          :problem="issueFor('sms')"
        />
        <MessageEditor
          v-if="campaign.channels.includes('email')"
          v-model="messages.email_subject"
          label="Email subject"
          :limit="80"
          :rows="2"
          :state="stateOf('email_subject')"
          :origin="originOf('email_subject')"
          :longest-name="campaign.content_limits.email_name_length"
          :problem="issueFor('email_subject')"
        />
        <MessageEditor
          v-if="campaign.channels.includes('email')"
          v-model="messages.email_body"
          label="Email body"
          :limit="5000"
          :rows="8"
          :state="stateOf('email_body')"
          :origin="originOf('email_body')"
          :longest-name="campaign.content_limits.email_name_length"
          :problem="issueFor('email_body')"
        />

        <div v-if="generating" class="row">
          <button class="button button--secondary" type="button" data-testid="stop" @click="stop">Stop</button>
        </div>
        <template v-else>
          <div v-if="stored" class="row">
            <button class="button button--secondary" type="button" :disabled="!dirty || saving" data-testid="save" @click="save">Save changes</button>
            <span v-if="dirty" class="muted small" data-testid="unsaved">Unsaved changes</span>
          </div>

          <form class="regenerate" @submit.prevent="generate({ instruction })">
            <label class="small muted" for="instruction">{{ stored ? "Ask AI for a new version" : "Instruction for AI (optional)" }}</label>
            <div class="row">
              <input id="instruction" v-model="instruction" class="field" maxlength="500" placeholder="For example: shorter, and more formal" />
              <button class="button button--secondary" type="submit" :disabled="!canGenerate" data-testid="regenerate">
                {{ stored ? "Regenerate" : generationError ? "Try again" : "Write messages" }}
              </button>
            </div>
            <p v-if="stored" class="muted small">This replaces the messages, including your edits.</p>
          </form>
        </template>

        <div class="row actions">
          <a class="small" :href="`/campaigns/${campaignId}/edit`">Back to audience</a>
          <a v-if="stored && !dirty && !generating" class="button button--primary link-button" :href="`/campaigns/${campaignId}/review`" data-testid="continue-review">
            Continue to review
          </a>
          <template v-else>
            <button class="button button--primary" type="button" disabled>Continue to review</button>
            <span v-if="dirty" class="muted small" data-testid="continue-hint">Save your changes to continue.</span>
          </template>
          <a class="exit small" :href="`/campaigns/${campaignId}`">Save and exit</a>
        </div>
      </div>

      <aside v-if="recipients.length" class="previews" aria-label="Message previews">
        <template v-if="campaign.channels.includes('sms')">
          <h2>SMS</h2>
          <PhonePreview :message="messages.sms" :recipients="recipients" />
        </template>
        <template v-if="campaign.channels.includes('email')">
          <h2>Email</h2>
          <EmailPreview :subject="messages.email_subject" :body="messages.email_body" :recipients="recipients" />
        </template>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.link-button {
  display: inline-flex;
  text-decoration: none;
}

.content {
  display: grid;
  gap: var(--space-6);
}

.intro {
  display: grid;
  gap: var(--space-2);
  max-width: 720px;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) var(--panel-width, 320px);
  gap: var(--space-6);
  align-items: start;
}

.editors,
.previews,
.regenerate {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.regenerate {
  gap: var(--space-2);
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.row .field {
  flex: 1;
  min-width: 200px;
}

.exit {
  margin-left: auto;
}

@media (max-width: 960px) {
  .grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>

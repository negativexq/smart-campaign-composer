<script setup lang="ts">
import { CheckCircle2, CircleAlert, LoaderCircle, PencilLine } from "lucide-vue-next";
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
  type Messages,
  type SampleRecipient,
  getEmailDesigns,
  type EmailDesign,
} from "../api/campaigns";
import EmailPreview from "../components/EmailPreview.vue";
import EmailTemplatePicker from "../components/EmailTemplatePicker.vue";
import GenerationProgress from "../components/GenerationProgress.vue";
import MessageEditor from "../components/MessageEditor.vue";
import OriginTag from "../components/OriginTag.vue";
import PhonePreview from "../components/PhonePreview.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { channelLabel, firstMessagePath } from "../lib/campaignSteps";
import { isDraftStatus } from "../lib/campaignStatus";
import { audienceLanguages, recipientsFor, listNames, languageName, longestNameFor, missingLanguages, missingSecondVersion, variantCount, variantPeople, variantShare, versionFor } from "../lib/languages";
import { customTemplateName, EMAIL_TEMPLATES, isCustomTemplate } from "../lib/emailTemplates";
import { navigate } from "../lib/navigation";

const EMAIL_FIELDS: MessageField[] = ["email_subject", "email_preheader", "email_headline", "email_body", "email_cta_label", "email_cta_url"];

const props = defineProps<{ campaignId: string; channel: "sms" | "email" }>();
const emit = defineEmits<{ status: [status: string]; channels: [channels: string[]] }>();

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
const messages = reactive<Messages>({ sms: "", email_subject: "", email_preheader: "", email_headline: "", email_body: "", email_cta_label: "", email_cta_url: "", email_template: "" });
const selectedLanguage = ref("");
const writingLanguage = ref("");
// Every language this run writes, what is being written now and what has finished.
const planned = ref<string[]>([]);
const writingNow = ref<string[]>([]);
const finished = ref<string[]>([]);
// One channel when only it is being written again, otherwise all the campaign uses.
const wholeCampaign = ref(false);
const writes = computed(() =>
  wholeCampaign.value ? (campaign.value?.channels ?? []).map(channelLabel).join(" and ") : channelLabel(props.channel),
);
const progress = computed(() =>
  planned.value.map((language) => ({
    language,
    name: languageName(language),
    state: finished.value.includes(language) ? ("done" as const) : writingNow.value.includes(language) ? ("writing" as const) : ("waiting" as const),
  })),
);
const selectedVariant = ref("A");
const writingVariant = ref("");
let controller: AbortController | null = null;

const stored = computed(() => (campaign.value ? versionFor(campaign.value, selectedLanguage.value, selectedVariant.value) : undefined));
const isTest = computed(() => Boolean(campaign.value?.content?.variant_b));
const noSecondVersion = computed(() => (campaign.value ? missingSecondVersion(campaign.value) : false));
const languageTabs = computed(() => {
  const current = campaign.value;
  if (!current?.content?.language) return [];
  return audienceLanguages(current, props.channel)
    .filter((audience) => versionFor(current, audience.language, selectedVariant.value))
    .map((audience) => ({
      language: audience.language,
      name: languageName(audience.language),
      // Under a version, the people of that version.
      people: isTest.value
        ? variantCount(current, selectedVariant.value, { language: audience.language, channel: props.channel })
        : props.channel === "sms"
          ? audience.sms
          : audience.email,
    }));
});
const missing = computed(() => (campaign.value ? missingLanguages(campaign.value, props.channel).map(languageName) : []));
const shownRecipients = computed(() => recipientsFor(recipients.value, selectedLanguage.value));
const dirty = computed(() =>
  stored.value ? (Object.keys(messages) as MessageField[]).some((field) => messages[field] !== (stored.value?.[field] ?? "")) : false,
);
const canGenerate = computed(() => !generating.value && !saving.value);
const name = computed(() => channelLabel(props.channel));
const fields = computed<MessageField[]>(() => (props.channel === "sms" ? ["sms"] : EMAIL_FIELDS));
const nameLength = computed(() => (campaign.value ? longestNameFor(campaign.value, selectedLanguage.value, props.channel) : undefined));
const reachable = computed(() => (props.channel === "sms" ? campaign.value?.audience.sms : campaign.value?.audience.email) ?? 0);
const warnings = computed(() => campaign.value?.content_warnings.filter((warning) => fields.value.includes(warning.field as MessageField)) ?? []);
const otherChannel = computed(() => campaign.value?.channels.find((channel) => channel !== props.channel));
const previous = computed(() =>
  props.channel === "email" && otherChannel.value
    ? { href: `/campaigns/${props.campaignId}/sms`, label: "Back to SMS" }
    : { href: `/campaigns/${props.campaignId}/edit`, label: "Back to audience" },
);
const next = computed(() =>
  props.channel === "sms" && otherChannel.value
    ? { href: `/campaigns/${props.campaignId}/email`, label: "Continue to email" }
    : { href: `/campaigns/${props.campaignId}/review`, label: "Continue to review" },
);
// A campaign keeps its own copy, so that copy can be chosen even after the original is deleted.
const designLibrary = ref<EmailDesign[]>([]);
const designChoices = computed(() => {
  const own = campaign.value?.email_design;
  return own && !designLibrary.value.some((design) => design.id === own.id) ? [...designLibrary.value, own] : designLibrary.value;
});
const previewMessage = computed(() => {
  const chosen = template.value;
  if (!isCustomTemplate(chosen)) return messages;
  const own = campaign.value?.email_design;
  const design = own && customTemplateName(own.id) === chosen ? own : designLibrary.value.find((item) => customTemplateName(item.id) === chosen);
  return { ...messages, email_design: design ?? null };
});
const template = computed({
  get: () => messages.email_template || campaign.value?.email_template || EMAIL_TEMPLATES[0].id,
  set: (id: string) => (messages.email_template = id),
});
const buttonHasProblem = computed(() => Boolean(issueFor("email_cta_label") || issueFor("email_cta_url")));

const writingText = computed(() => {
  const languages = writingLanguage.value ? writingLanguage.value.split(",").map(languageName) : [];
  const language = listNames(languages);
  if (languages.length > 1) return writingVariant.value ? `Writing version ${writingVariant.value} in ${language}` : `Writing the ${language} versions`;
  if (writingVariant.value) return language ? `Writing version ${writingVariant.value} in ${language}` : `Writing version ${writingVariant.value}`;
  return language ? `Writing the ${language} version` : "Writing";
});

// The one state of this message. "Validated" only ever comes from the backend.
const state = computed(() => {
  if (generating.value) return { icon: LoaderCircle, text: writingText.value, tone: "ai" };
  if (fields.value.some((field) => issueFor(field))) return { icon: CircleAlert, text: "Needs changes", tone: "error" };
  if (dirty.value) return { icon: PencilLine, text: "Unsaved changes", tone: "muted" };
  if (stored.value) return { icon: CheckCircle2, text: "Validated", tone: "success" };
  return { icon: PencilLine, text: "Not written yet", tone: "muted" };
});
const origin = computed(() => {
  if (generating.value) return "ai";
  return fields.value.some((field) => stored.value?.edited.includes(field) || (stored.value && messages[field] !== stored.value[field])) ? "user" : "ai";
});

function fill(content: Partial<Messages> | undefined) {
  for (const field of Object.keys(messages) as MessageField[]) messages[field] = content?.[field] ?? "";
}

// Opens the language most of this channel's people read.
function showFirstLanguage() {
  const current = campaign.value;
  selectedVariant.value = "A";
  selectedLanguage.value = languageTabs.value[0]?.language ?? current?.content?.language ?? "";
  fill(stored.value);
}

// Moving to another language keeps what was typed here. Text the backend refuses stays.
async function selectLanguage(language: string) {
  if (language === selectedLanguage.value || !(await save())) return;
  selectedLanguage.value = language;
  issues.value = [];
  fill(stored.value);
}

// The other version opens in the same language, so the two read side by side in turn.
async function selectVariant(variant: string) {
  if (variant === selectedVariant.value || !(await save())) return;
  selectedVariant.value = variant;
  issues.value = [];
  if (!stored.value) selectedLanguage.value = languageTabs.value[0]?.language ?? "";
  fill(stored.value);
}

// What a generation is about to replace. The other channel stays until it is done.
function clearOwnFields() {
  for (const field of fields.value) messages[field] = "";
}

function issueFor(field: MessageField) {
  return issues.value.find((issue) => issue.field === field)?.message;
}

function editorState(field: MessageField) {
  if (generating.value) return "streaming";
  return issueFor(field) ? "invalid" : "draft";
}

async function generate(input: { instruction?: string; missing_languages?: boolean } = {}) {
  controller = new AbortController();
  // The first version written is the one that shows as it comes.
  selectedVariant.value = "A";
  selectedLanguage.value = campaign.value?.content?.language ?? "";
  const before = stored.value;
  // A template picked just now is not saved yet, and new text should not undo it.
  const pickedTemplate = messages.email_template;
  generating.value = true;
  generationError.value = "";
  saveError.value = "";
  stopped.value = false;
  writingLanguage.value = "";
  writingVariant.value = "";
  wholeCampaign.value = !before || Boolean(input.missing_languages);
  planned.value = [];
  writingNow.value = [];
  finished.value = [];
  issues.value = [];
  fill(before);
  // Adding a language leaves what is on screen as it is.
  if (!input.missing_languages) clearOwnFields();
  try {
    campaign.value = await generateContent(
      props.campaignId,
      { ...input, channel: props.channel },
      {
        onDelta: (field, text) => {
          if (field in messages) messages[field] += text;
        },
        onReset: clearOwnFields,
        onLanguage: (language) => {
          writingLanguage.value = language;
          writingNow.value = language.split(",");
        },
        onPlan: (languages) => {
          planned.value = languages;
          writingNow.value = languages.slice(0, 1);
          finished.value = [];
        },
        onLanguageDone: (language) => finished.value.push(language),
        onVariant: (variant) => {
          writingVariant.value = variant;
          writingLanguage.value = "";
        },
      },
      controller.signal,
    );
    showFirstLanguage();
    messages.email_template = pickedTemplate || messages.email_template;
    instruction.value = "";
  } catch (failure) {
    // Nothing unvalidated stays on screen.
    showFirstLanguage();
    messages.email_template = pickedTemplate;
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
  if (!dirty.value) return true;
  saving.value = true;
  saveError.value = "";
  issues.value = [];
  try {
    campaign.value = await saveContent(props.campaignId, {
      ...messages,
      ...(selectedLanguage.value ? { language: selectedLanguage.value } : {}),
      ...(selectedVariant.value === "B" ? { variant: "B" } : {}),
    });
    fill(stored.value);
    return true;
  } catch (failure) {
    if (failure instanceof ApiError) issues.value = failure.issues;
    saveError.value = errorMessage(failure);
    return false;
  } finally {
    saving.value = false;
  }
}

// Moving on keeps what was typed. Text the backend refuses stays here to be fixed.
async function leaveTo(href: string) {
  if (await save()) navigate(href);
}

watch(() => campaign.value?.status, (status) => status && emit("status", status));
watch(() => campaign.value?.channels, (channels) => channels && emit("channels", channels));
watch(generating, (active) => {
  if (active) emit("status", "GENERATING");
  else if (campaign.value) emit("status", campaign.value.status);
});

onMounted(() => {
  // Only the extra choices depend on this, so the page still works without them.
  getEmailDesigns().then((designs) => (designLibrary.value = designs)).catch(() => {});
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
  if (current.content) showFirstLanguage();
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

  <ValidationNotice v-else-if="!campaign.channels.includes(channel)" tone="info" :title="`This campaign does not send ${name}`">
    <a :href="firstMessagePath(campaignId, campaign.channels)">Go to its messages</a>
  </ValidationNotice>

  <div v-else class="content">
    <header class="intro">
      <h1>Check the {{ name }}.</h1>
      <p class="muted" data-testid="audience-line">Goes to {{ reachable.toLocaleString("en-US") }} customers who agreed to {{ name }}.</p>
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
      <ValidationNotice v-for="warning in warnings" :key="warning.field + warning.reason" tone="warning" :title="warning.reason === 'not_applied' ? 'Not done' : 'Differs from the brief'">
        {{ warning.message }}
      </ValidationNotice>
    </template>

    <GenerationProgress v-if="generating && progress.length" :languages="progress" :variant="writingVariant" :writes="writes" />

    <ValidationNotice v-if="!generating && missing.length" tone="warning" title="A language has no messages">
      {{ listNames(missing) }} has no messages yet, and what is already written stays as it is.
      <button class="button button--secondary missing-write" type="button" data-testid="write-missing" @click="generate({ missing_languages: true })">Write {{ listNames(missing) }}</button>
    </ValidationNotice>

    <ValidationNotice v-if="!generating && noSecondVersion" tone="warning" title="This test has one version">
      It compares two versions, and the second is not written yet. Regenerate to write it.
    </ValidationNotice>

    <div v-if="isTest" class="languages" role="tablist" aria-label="Message version" data-testid="variant-tabs">
      <button
        v-for="variant in ['A', 'B']"
        :key="variant"
        class="language"
        :class="{ 'language--active': variant === selectedVariant }"
        type="button"
        role="tab"
        :aria-selected="variant === selectedVariant"
        :disabled="generating || saving"
        :data-testid="`variant-${variant}`"
        @click="selectVariant(variant)"
      >
        Version {{ variant }} <span class="muted small">{{ variantShare(campaign, variant) }}% · {{ variantPeople(campaign, variant, { channel }) }}</span>
      </button>
    </div>

    <div v-if="languageTabs.length > 1" class="languages" role="tablist" aria-label="Message language" data-testid="language-tabs">
      <button
        v-for="tab in languageTabs"
        :key="tab.language"
        class="language"
        :class="{ 'language--active': tab.language === selectedLanguage }"
        type="button"
        role="tab"
        :aria-selected="tab.language === selectedLanguage"
        :disabled="generating || saving"
        :data-testid="`language-${tab.language}`"
        @click="selectLanguage(tab.language)"
      >
        {{ tab.name }} <span class="muted small">{{ tab.people.toLocaleString("en-US") }} {{ tab.people === 1 ? "person" : "people" }}</span>
      </button>
    </div>

    <div class="grid" :class="`grid--${channel}`">
      <div class="editors">
        <section class="message card" :aria-label="name">
          <div class="message-head">
            <span class="state small" :class="`state--${state.tone}`" data-testid="message-state" :role="generating ? 'status' : undefined">
              <component :is="state.icon" :size="14" aria-hidden="true" />
              {{ state.text }}
            </span>
            <OriginTag v-if="stored || generating" :origin="origin" />
          </div>

          <MessageEditor
            v-if="channel === 'sms'"
            v-model="messages.sms"
            plain
            label="Message"
            :limit="160"
            :rows="5"
            :state="editorState('sms')"
            origin="ai"
            :longest-name="nameLength"
            :problem="issueFor('sms')"
          />
          <template v-else>
            <EmailTemplatePicker v-model="template" :designs="designChoices" :disabled="generating" />
            <MessageEditor v-model="messages.email_subject" plain no-variables label="Subject" :limit="80" :rows="1" :state="editorState('email_subject')" origin="ai" :longest-name="nameLength" :problem="issueFor('email_subject')" />
            <MessageEditor v-model="messages.email_preheader" plain no-variables label="Preview text" :limit="120" :rows="2" :state="editorState('email_preheader')" origin="ai" :longest-name="nameLength" :problem="issueFor('email_preheader')" />
            <MessageEditor v-model="messages.email_headline" plain no-variables label="Headline" :limit="80" :rows="1" :state="editorState('email_headline')" origin="ai" :longest-name="nameLength" :problem="issueFor('email_headline')" />
            <MessageEditor v-model="messages.email_body" plain label="Body" :limit="5000" :rows="8" :state="editorState('email_body')" origin="ai" :longest-name="nameLength" :problem="issueFor('email_body')" />
            <details class="more" :open="buttonHasProblem || undefined" data-testid="email-button">
              <summary class="more-toggle">
                <span>Button</span>
                <span class="muted small">{{ messages.email_cta_label || "None" }}</span>
              </summary>
              <div class="more-fields">
                <MessageEditor v-model="messages.email_cta_label" plain no-variables label="Label" :limit="30" :rows="1" :state="editorState('email_cta_label')" origin="ai" :longest-name="nameLength" :problem="issueFor('email_cta_label')" />
                <MessageEditor v-model="messages.email_cta_url" plain label="Link" :rows="1" no-variables :state="editorState('email_cta_url')" origin="ai" :problem="issueFor('email_cta_url')" />
              </div>
            </details>
          </template>

          <div v-if="generating" class="row">
            <button class="button button--secondary" type="button" data-testid="stop" @click="stop">Stop</button>
          </div>
        </section>

        <form v-if="!generating" class="regenerate" @submit.prevent="generate({ instruction })">
          <label class="small muted" for="instruction">{{ stored ? `Ask AI for a new ${name}` : "Instruction for AI (optional)" }}</label>
          <div class="row">
            <input id="instruction" v-model="instruction" class="field" maxlength="500" placeholder="For example: shorter, and more formal" />
            <button class="button button--secondary" type="submit" :disabled="!canGenerate" data-testid="regenerate">
              {{ stored ? "Regenerate" : generationError ? "Try again" : "Write messages" }}
            </button>
          </div>
          <p v-if="stored" class="muted small" data-testid="regenerate-scope">
            Replaces {{ isTest ? `both versions of this ${name}` : `this ${name}` }}, including your edits.<template v-if="otherChannel"> The {{ channelLabel(otherChannel) }} stays as it is.</template>
          </p>
        </form>

        <div class="row actions">
          <button class="link small" type="button" :disabled="saving || generating" data-testid="back" @click="leaveTo(previous.href)">{{ previous.label }}</button>
          <button class="button button--primary" type="button" :disabled="!stored || saving || generating" data-testid="continue" @click="leaveTo(next.href)">{{ next.label }}</button>
          <button class="link small exit" type="button" :disabled="saving || generating" data-testid="exit" @click="leaveTo(`/campaigns/${campaignId}`)">Save and exit</button>
        </div>
      </div>

      <aside v-if="recipients.length" class="previews" aria-label="Message preview">
        <PhonePreview v-if="channel === 'sms'" :key="selectedVariant + selectedLanguage" :message="messages.sms" :recipients="shownRecipients" :language="selectedLanguage" />
        <EmailPreview v-else :key="selectedVariant + selectedLanguage" :message="previewMessage" :recipients="shownRecipients" :language="selectedLanguage" />
      </aside>
    </div>
  </div>
</template>

<style scoped>
.link {
  padding: 0;
  color: var(--color-primary);
  background: none;
  border: 0;
  cursor: pointer;
}

.link:disabled {
  color: var(--color-text-muted);
  cursor: not-allowed;
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

.missing-write {
  display: block;
  margin-top: var(--space-2);
}

.languages {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  border-bottom: 1px solid var(--color-border);
}

.language {
  padding: var(--space-2) var(--space-3);
  color: var(--color-text);
  font: inherit;
  background: none;
  border: 0;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
}

.language--active {
  color: var(--color-on-primary);
  font-weight: 600;
  background: var(--color-primary);
  border-bottom-color: var(--color-primary);
  border-radius: var(--radius-md) var(--radius-md) 0 0;
}

.language--active .muted {
  color: inherit;
}

.language:disabled {
  cursor: not-allowed;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) var(--panel-width, 320px);
  gap: var(--space-6);
  align-items: start;
}

.grid--email {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
}

.message {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-4);
  min-width: 0;
}

.message-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-3);
}

.state {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.state--ai {
  color: var(--color-ai);
}

.state--error {
  color: var(--color-error);
}

.state--success {
  color: var(--color-success);
}

.state--muted {
  color: var(--color-text-muted);
}

.more-toggle {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  font-weight: 500;
  cursor: pointer;
}

.more-toggle::before {
  content: "›";
  display: inline-block;
  transition: transform 120ms ease;
}

.more[open] .more-toggle::before {
  transform: rotate(90deg);
}

.more-fields {
  display: grid;
  gap: var(--space-4);
  margin-top: var(--space-3);
}

.editors,
.previews,
.regenerate {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.previews {
  position: sticky;
  top: var(--space-4);
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
  .grid,
  .grid--email {
    grid-template-columns: minmax(0, 1fr);
  }

  .previews {
    position: static;
  }
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { createCampaign, errorMessage, getAudiences, getEmailDesigns, type SavedAudience } from "../api/campaigns";
import BriefComposer from "../components/BriefComposer.vue";
import ChannelPicker from "../components/ChannelPicker.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { ruleText } from "../lib/audience";
import { defaultChannels } from "../lib/preferences";
import { EMAIL_TEMPLATES, isCustomTemplate } from "../lib/emailTemplates";
import { BRIEF_EXAMPLES as suggestions } from "../lib/briefExamples";
import { briefCodes, briefLinks, validCode } from "../lib/message";
import { navigate } from "../lib/navigation";
import { requestAudience } from "../lib/pendingAudience";

const brief = ref("");
const channels = ref(defaultChannels());
const tone = ref("");
const hasOffer = ref(false);
const abTest = ref(false);
const firstShare = ref<number | "">(50);
const offer = ref<number | "">("");
const code = ref("");
const typedCode = computed(() => code.value.trim().toUpperCase());
const codesInBrief = computed(() => briefCodes(brief.value));
const linksInBrief = computed(() => briefLinks(brief.value));
const quietDays = ref("");
// Set by "Use template". It needs a channel to apply to, so email must stay chosen.
const namedTemplate = new URLSearchParams(window.location.search).get("template") ?? "";
const template = ref<{ id: string; name: string } | null>(EMAIL_TEMPLATES.find((item) => item.id === namedTemplate) ?? null);
const busy = ref(false);
const error = ref("");

const savedAudience = ref<SavedAudience | null>(null);
const emit = defineEmits<{ savedAudience: [used: boolean] }>();
watch(savedAudience, (saved) => emit("savedAudience", Boolean(saved)));

// "Use in a new campaign" brings the rule itself, so the brief only says what to tell them.
onMounted(async () => {
  if (!isCustomTemplate(namedTemplate)) return;
  try {
    const design = (await getEmailDesigns()).find((item) => `custom:${item.id}` === namedTemplate);
    if (design) template.value = { id: namedTemplate, name: design.name };
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
onMounted(async () => {
  const id = new URLSearchParams(window.location.search).get("audience");
  if (!id) return;
  try {
    savedAudience.value = (await getAudiences()).find((item) => item.id === id) ?? null;
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});

const validShare = computed(() => Number.isInteger(firstShare.value) && Number(firstShare.value) >= 1 && Number(firstShare.value) <= 99);

const unavailableReason = computed(() => {
  if (busy.value) return "Finding your audience...";
  if (!channels.value.length) return "Choose at least one channel.";
  if (hasOffer.value && (offer.value === "" || !Number.isInteger(offer.value) || offer.value < 1 || offer.value > 100)) return "Enter a discount from 1 to 100.";
  if (typedCode.value && !validCode(typedCode.value)) return "Enter a discount code of 3 to 30 capital letters, digits or hyphens.";
  if (codesInBrief.value.length > 1) return `The brief names more than one code: ${codesInBrief.value.join(", ")}. Keep one.`;
  if (typedCode.value && codesInBrief.value.length === 1 && codesInBrief.value[0] !== typedCode.value) return `The brief names the code ${codesInBrief.value[0]}. Use the same code here, or leave this empty.`;
  if (linksInBrief.value.length > 1) return "The brief has more than one link. Every SMS carries one short link, so keep the one people should open.";
  if (abTest.value && !validShare.value) return "Enter a share for version A from 1 to 99.";
  return undefined;
});

// Says what is set while the options are closed, so nothing is applied unseen.
const optionsSummary = computed(() => {
  const chosen = [
    tone.value && tone.value[0].toUpperCase() + tone.value.slice(1),
    hasOffer.value && offer.value !== "" && `${offer.value}% discount`,
    typedCode.value && `Code ${typedCode.value}`,
    quietDays.value && `Leaves out people messaged in the last ${quietDays.value} days`,
  ].filter(Boolean);
  return chosen.length ? chosen.join(" · ") : "Optional";
});

async function findAudience() {
  busy.value = true;
  error.value = "";
  try {
    const id = await createCampaign(brief.value, channels.value, {
      ...(tone.value ? { tone: tone.value } : {}),
      ...(hasOffer.value && offer.value !== "" ? { offer_percent: offer.value } : {}),
      ...(typedCode.value ? { discount_code: typedCode.value } : {}),
      ...(quietDays.value ? { not_messaged_within_days: Number(quietDays.value) } : {}),
      ...(abTest.value ? { ab_test: true, variant_a_percent: Number(firstShare.value) } : {}),
      ...(template.value && channels.value.includes("email") ? { email_template: template.value.id } : {}),
      ...(savedAudience.value ? { saved_audience_id: savedAudience.value.id } : {}),
    });
    requestAudience(id);
    navigate(`/campaigns/${id}/edit`);
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="compose">
    <header class="intro">
      <h1>Bring your next campaign to life.</h1>
      <p class="muted">From an idea to a ready-to-send campaign, with control at every step.</p>
    </header>

    <ValidationNotice v-if="error" class="error" tone="error" title="We could not save this campaign">{{ error }}</ValidationNotice>

    <BriefComposer
      v-model="brief"
      :action-label="savedAudience ? 'Continue' : 'Find audience'"
      :label="savedAudience ? 'What should the message say?' : undefined"
      :placeholder="savedAudience ? 'What you want to tell them and the tone you want.' : undefined"
      :suggestions="savedAudience ? [] : suggestions"
      :unavailable-reason="unavailableReason"
      @submit="findAudience"
    >
      <ChannelPicker v-model="channels" :disabled="busy" />
      <fieldset class="ab" :class="{ 'ab--on': abTest }" :disabled="busy" data-testid="ab-option">
        <label class="ab-choice" for="campaign-ab-test">
          <input id="campaign-ab-test" v-model="abTest" type="checkbox" data-testid="ab-test" />
          <span class="ab-text">
            <span class="ab-name">A/B test: try two versions of the message</span>
            <span class="muted small">Each person gets one of two wordings, and the report shows how each did.</span>
          </span>
        </label>
        <div v-if="abTest" class="option ab-share">
          <label class="small muted" for="campaign-first-share">People who get version A (%)</label>
          <input id="campaign-first-share" v-model.number="firstShare" class="field control" type="number" min="1" max="99" step="1" data-testid="ab-share" />
          <p class="muted small" data-testid="ab-test-note">
            <template v-if="validShare">The other {{ 100 - Number(firstShare) }}% get version B. </template>The offer stays the same; only the wording differs.
          </p>
        </div>
      </fieldset>
      <p v-if="savedAudience" class="small muted chosen-template" data-testid="chosen-audience">
        Audience: {{ savedAudience.name }} ({{ savedAudience.conditions.map(ruleText).join(", ") }}).
        <button class="link-button small" type="button" data-testid="remove-audience" @click="savedAudience = null">Remove</button>
      </p>
      <p v-if="template && channels.includes('email')" class="small muted chosen-template" data-testid="chosen-template">
        Email design: {{ template.name }}.
        <button class="link-button small" type="button" data-testid="drop-template" :disabled="busy" @click="template = null">Remove</button>
      </p>
      <details class="more" data-testid="campaign-options">
        <summary class="more-toggle">
          <span>Options</span>
          <span class="muted small" data-testid="options-summary">{{ optionsSummary }}</span>
        </summary>
        <div class="option-groups">
          <fieldset class="options" :disabled="busy" data-testid="message-options">
            <legend class="legend">The message</legend>
            <div class="option">
              <label class="small muted" for="campaign-tone">Tone</label>
              <select id="campaign-tone" v-model="tone" class="field control">
                <option value="">As the brief says</option>
                <option value="friendly">Friendly</option>
                <option value="formal">Formal</option>
                <option value="urgent">Urgent</option>
              </select>
            </div>
            <label class="check small" for="campaign-has-offer">
              <input id="campaign-has-offer" v-model="hasOffer" type="checkbox" />
              Add a discount
            </label>
            <div v-if="hasOffer" class="option">
              <label class="small muted" for="campaign-offer">Discount (%)</label>
              <input id="campaign-offer" v-model.number="offer" class="field control" type="number" min="1" max="100" step="1" />
            </div>
            <div class="option">
              <label class="small muted" for="campaign-code">Discount code</label>
              <input
                id="campaign-code"
                v-model="code"
                class="field control mono"
                type="text"
                maxlength="30"
                autocapitalize="characters"
                spellcheck="false"
                :placeholder="codesInBrief.length === 1 ? codesInBrief[0] : 'SPRING15'"
                data-testid="discount-code"
              />
              <p v-if="!typedCode && codesInBrief.length === 1" class="small muted" data-testid="code-from-brief">From the brief: <span class="mono">{{ codesInBrief[0] }}</span>. Every message will carry it.</p>
              <p v-else-if="typedCode" class="small muted">Every SMS and email will carry it exactly as written.</p>
            </div>
          </fieldset>
          <fieldset class="options" :disabled="busy" data-testid="audience-options">
            <legend class="legend">Who to leave out</legend>
            <div class="option">
              <label class="small muted" for="campaign-quiet">People messaged in the last</label>
              <select id="campaign-quiet" v-model="quietDays" class="field control">
                <option value="">Leave nobody out</option>
                <option value="3">3 days</option>
                <option value="7">7 days</option>
                <option value="14">14 days</option>
                <option value="30">30 days</option>
              </select>
            </div>
          </fieldset>
        </div>
      </details>
    </BriefComposer>
  </div>
</template>

<style scoped>
.ab {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  margin: var(--space-2) 0 0;
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

.ab--on {
  border-color: var(--color-primary);
}

.ab:disabled {
  opacity: 0.6;
}

.ab-choice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  cursor: pointer;
}

.ab:disabled .ab-choice {
  cursor: not-allowed;
}

.ab-choice input {
  margin: 3px 0 0;
  accent-color: var(--color-primary);
}

.ab-text {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.ab-name {
  font-weight: 500;
}

.ab-share .control {
  max-width: 120px;
}

.ab-share p {
  margin: 0;
}

.more {
  margin-top: var(--space-2);
}

.more-toggle {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  padding: var(--space-1) 0;
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

.option-groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  align-items: start;
  gap: var(--space-4);
  margin-top: var(--space-2);
}

.options {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.check {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 24px;
  cursor: pointer;
}

.legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-weight: 500;
}

.option {
  display: grid;
  align-content: end;
  gap: var(--space-1);
  min-width: 0;
}

.control {
  width: 100%;
  height: 40px;
  padding-block: 0;
}

.compose {
  max-width: 720px;
}

.intro {
  display: grid;
  gap: var(--space-2);
  margin-bottom: var(--space-8);
}

.error {
  margin-bottom: var(--space-4);
}

.chosen-template {
  margin: 0;
}

.link-button {
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}
</style>
